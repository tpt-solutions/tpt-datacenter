// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package collector

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
)

// RegisterPoll describes one contiguous holding-register read mapped to a
// telemetry metric.
type RegisterPoll struct {
	// Address is the starting holding register (Modbus reference - 1).
	Address uint16
	// Count is the number of 16-bit registers to read.
	Count uint16
	// Metric is the measured quantity for every register in the block.
	Metric point.Metric
	// Device / Sensor identify the source; Sensor may include %d for the
	// register offset when Count > 1.
	Device string
	Sensor string
	// Scale multiplies each raw register value (default 1.0).
	Scale float64
}

// ModbusConfig configures the Modbus TCP collector.
type ModbusConfig struct {
	// Address is host:port of the Modbus TCP gateway (port 502 default).
	Address string
	// UnitID is the Modbus slave/unit identifier.
	UnitID byte
	// Polls lists the register blocks to read each cycle.
	Polls []RegisterPoll
	// Dial optionally overrides connection establishment (used by tests).
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// Modbus implements Collector for Modbus TCP gateways (PDUs, cooling loops).
type Modbus struct {
	cfg  ModbusConfig
	mu   sync.Mutex
	conn net.Conn
	txn  uint16
	dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewModbus builds a Modbus TCP collector.
func NewModbus(cfg ModbusConfig) *Modbus {
	dial := cfg.Dial
	if dial == nil {
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, address)
		}
	}
	return &Modbus{cfg: cfg, dial: dial}
}

// Name reports the collector name.
func (m *Modbus) Name() string { return "modbus" }

// functionReadHoldingRegisters is Modbus function code 0x03.
const functionReadHoldingRegisters byte = 0x03

// Poll reads every configured register block and emits one point per register.
func (m *Modbus) Poll(ctx context.Context) ([]point.DataPoint, error) {
	ts := time.Now()
	var out []point.DataPoint
	for _, p := range m.cfg.Polls {
		regs, err := m.readRegisters(ctx, p.Address, p.Count)
		if err != nil {
			return nil, err
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1.0
		}
		for i, raw := range regs {
			sensor := p.Sensor
			if p.Count > 1 {
				sensor = fmt.Sprintf(p.Sensor, i)
			}
			out = append(out, point.New(p.Device, sensor, p.Metric, float64(raw)*scale, ts))
		}
	}
	return out, nil
}

func (m *Modbus) readRegisters(ctx context.Context, addr, count uint16) ([]uint16, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn == nil {
		conn, err := m.dial(ctx, "tcp", m.cfg.Address)
		if err != nil {
			return nil, fmt.Errorf("modbus: dial %s: %w", m.cfg.Address, err)
		}
		m.conn = conn
	}
	m.txn++
	req := makeModbusRequest(m.txn, m.cfg.UnitID, addr, count)
	if err := m.conn.SetDeadline(deadline(ctx)); err != nil {
		return nil, err
	}
	if _, err := m.conn.Write(req); err != nil {
		m.conn = nil
		return nil, fmt.Errorf("modbus: write: %w", err)
	}
	// MBAP(7) + unit(1) + func(1) + byteCount(1) + registers.
	header := make([]byte, 10)
	if err := readFull(m.conn, header); err != nil {
		m.conn = nil
		return nil, fmt.Errorf("modbus: read header: %w", err)
	}
	byteCount := int(header[9])
	body := make([]byte, byteCount)
	if err := readFull(m.conn, body); err != nil {
		m.conn = nil
		return nil, fmt.Errorf("modbus: read body: %w", err)
	}
	if header[8] != functionReadHoldingRegisters {
		return nil, fmt.Errorf("modbus: unexpected function 0x%02x", header[8])
	}
	regs := make([]uint16, byteCount/2)
	for i := 0; i < len(regs); i++ {
		regs[i] = binary.BigEndian.Uint16(body[i*2 : i*2+2])
	}
	return regs, nil
}

// makeModbusRequest builds an MBAP + PDU frame for function 0x03.
//
//	MBAP: txn(2) proto(2)=0 len(2) unit(1)
//	PDU : func(1)=0x03 addr(2) quantity(2)
func makeModbusRequest(txn uint16, unitID byte, addr, count uint16) []byte {
	frame := make([]byte, 12)
	binary.BigEndian.PutUint16(frame[0:2], txn)
	// frame[2:4] protocol id = 0
	binary.BigEndian.PutUint16(frame[4:6], 6) // remaining length after length field
	frame[6] = unitID
	frame[7] = functionReadHoldingRegisters
	binary.BigEndian.PutUint16(frame[8:10], addr)
	binary.BigEndian.PutUint16(frame[10:12], count)
	return frame
}

// deadline returns the absolute deadline for an I/O op honoring ctx.
func deadline(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return time.Now().Add(10 * time.Second)
}

// readFull reads exactly len(buf) bytes, honoring any connection deadline.
func readFull(conn net.Conn, buf []byte) error {
	_, err := io.ReadFull(conn, buf)
	return err
}
