// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package point defines the internal message schema for ingested telemetry
// data points and its wire serialization.
//
// The schema is intentionally protobuf-shaped and forwards-compatible: every
// frame carries a versioned magic header so a future protobuf IDL can be
// adopted without breaking the on-the-wire contract. A compact binary framing
// is used on the ingest bus and a JSON form is provided for debugging and for
// the dev/stdout writer.
package point

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"time"
)

// Metric is the physical quantity a reading measures. The names match the Rust
// HAL enum in rust-edge/src/hal/types.rs so the two stacks agree on the wire.
type Metric string

const (
	MetricTemperature   Metric = "temperature"
	MetricVoltage       Metric = "voltage"
	MetricAmperage      Metric = "amperage"
	MetricAirflow       Metric = "airflow"
	MetricPower         Metric = "power"
	MetricHumidity      Metric = "humidity"
	MetricStateOfCharge Metric = "state_of_charge"
	MetricFanSpeed      Metric = "fan_speed"
	MetricValvePosition Metric = "valve_position"
	MetricOutletState   Metric = "outlet_state"
)

// Unit is the unit of measure attached to a value.
type Unit string

const (
	UnitCelsius           Unit = "celsius"
	UnitVolt              Unit = "volt"
	UnitAmpere            Unit = "ampere"
	UnitCubicMetersPerSec Unit = "m3_per_s"
	UnitWatt              Unit = "watt"
	UnitPercent           Unit = "percent"
	UnitBoolean           Unit = "boolean"
	UnitRatio             Unit = "ratio"
)

// UnitForMetric returns the canonical unit for a metric, mirroring
// Metric::unit in the Rust HAL.
func UnitForMetric(m Metric) Unit {
	switch m {
	case MetricTemperature:
		return UnitCelsius
	case MetricVoltage:
		return UnitVolt
	case MetricAmperage:
		return UnitAmpere
	case MetricAirflow:
		return UnitCubicMetersPerSec
	case MetricPower:
		return UnitWatt
	case MetricHumidity, MetricStateOfCharge, MetricFanSpeed, MetricValvePosition:
		return UnitPercent
	case MetricOutletState:
		return UnitBoolean
	default:
		return UnitRatio
	}
}

// DataPoint is a single time-stamped telemetry sample. It is the unit of work
// that flows through the ingestion pipeline from a collector to the
// time-series writer.
type DataPoint struct {
	// Device is the stable id of the producing device (rack, PDU, UPS, loop).
	Device string `json:"device"`
	// Sensor is the channel within the device.
	Sensor string `json:"sensor"`
	// Metric is the measured quantity.
	Metric Metric `json:"metric"`
	// Value is the sample value.
	Value float64 `json:"value"`
	// Unit is the unit of measure (usually derivable from Metric).
	Unit Unit `json:"unit"`
	// Timestamp is when the sample was taken.
	Timestamp time.Time `json:"timestamp"`
}

// New constructs a DataPoint, filling in the canonical unit when empty.
func New(device, sensor string, metric Metric, value float64, ts time.Time) DataPoint {
	u := UnitForMetric(metric)
	return DataPoint{
		Device:    device,
		Sensor:    sensor,
		Metric:    metric,
		Value:     value,
		Unit:      u,
		Timestamp: ts,
	}
}

// binary wire format:
//
//	magic(4) version(1) devLen(2) dev devLen... sensor... metric... unit... value(f64) ts(i64 ns)
const (
	wireMagic   = 0x54505444 // "TPTD"
	wireVersion = 1
)

// MarshalBinary encodes the point into the compact versioned wire format used
// on the ingest bus.
func (p DataPoint) MarshalBinary() ([]byte, error) {
	if p.Device == "" || p.Sensor == "" || p.Metric == "" {
		return nil, errors.New("point: device, sensor and metric are required")
	}
	dev := []byte(p.Device)
	sensor := []byte(p.Sensor)
	metric := []byte(p.Metric)
	unit := []byte(p.Unit)
	// 4 magic + 1 version + 2 devLen + dev + 2 sensorLen + sensor +
	// 2 metricLen + metric + 2 unitLen + unit + 8 value + 8 ts
	buf := make([]byte, 0, 4+1+2+len(dev)+2+len(sensor)+2+len(metric)+2+len(unit)+16)
	var hdr [7]byte
	binary.BigEndian.PutUint32(hdr[0:4], wireMagic)
	hdr[4] = wireVersion
	binary.BigEndian.PutUint16(hdr[5:7], uint16(len(dev)))
	buf = append(buf, hdr[:]...)
	buf = append(buf, dev...)
	buf = putLenPrefixed(buf, sensor)
	buf = putLenPrefixed(buf, metric)
	buf = putLenPrefixed(buf, unit)
	var num [16]byte
	binary.BigEndian.PutUint64(num[0:8], math.Float64bits(p.Value))
	binary.BigEndian.PutUint64(num[8:16], uint64(p.Timestamp.UnixNano()))
	buf = append(buf, num[:]...)
	return buf, nil
}

func putLenPrefixed(b, s []byte) []byte {
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(s)))
	b = append(b, l[:]...)
	return append(b, s...)
}

// UnmarshalBinary decodes a point produced by MarshalBinary.
func UnmarshalBinary(data []byte) (DataPoint, error) {
	var p DataPoint
	if len(data) < 7 {
		return p, errors.New("point: buffer too short")
	}
	if binary.BigEndian.Uint32(data[0:4]) != wireMagic {
		return p, errors.New("point: bad magic")
	}
	if data[4] != wireVersion {
		return p, errors.New("point: unsupported version")
	}
	off := 5
	devLen := int(binary.BigEndian.Uint16(data[off : off+2]))
	off += 2
	if off+devLen > len(data) {
		return p, errors.New("point: truncated device")
	}
	p.Device = string(data[off : off+devLen])
	off += devLen
	read := func() (string, error) {
		if off+2 > len(data) {
			return "", errors.New("point: truncated length")
		}
		l := int(binary.BigEndian.Uint16(data[off : off+2]))
		off += 2
		if off+l > len(data) {
			return "", errors.New("point: truncated field")
		}
		s := string(data[off : off+l])
		off += l
		return s, nil
	}
	var err error
	if p.Sensor, err = read(); err != nil {
		return p, err
	}
	var m, u string
	if m, err = read(); err != nil {
		return p, err
	}
	if u, err = read(); err != nil {
		return p, err
	}
	if off+16 > len(data) {
		return p, errors.New("point: truncated value/timestamp")
	}
	p.Metric = Metric(m)
	p.Unit = Unit(u)
	p.Value = math.Float64frombits(binary.BigEndian.Uint64(data[off : off+8]))
	off += 8
	p.Timestamp = time.Unix(0, int64(binary.BigEndian.Uint64(data[off:off+8])))
	return p, nil
}

// MarshalJSON is provided for debugging and the dev/stdout writer.
func (p DataPoint) MarshalJSON() ([]byte, error) {
	type alias DataPoint
	return json.Marshal(alias(p))
}
