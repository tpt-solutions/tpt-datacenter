// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package collector

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
)

// SyntheticConfig configures a synthetic source that emits realistic-looking
// telemetry. It mirrors the Rust Simulator backend so the rest of the stack
// can be exercised without physical hardware (used by the load test and the
// dev "simulator mode" path).
type SyntheticConfig struct {
	// Devices is the number of virtual devices to emit from.
	Devices int
	// SensorsPerDevice is the number of sensor channels per device.
	SensorsPerDevice int
	// Metrics to cycle through per sensor channel.
	Metrics []point.Metric
}

// Synthetic implements Collector by generating pseudo-random but plausible
// readings (bounded temperature drift, noisy power, etc.).
type Synthetic struct {
	cfg SyntheticConfig
	rng *rand.Rand
}

// NewSynthetic builds a synthetic collector.
func NewSynthetic(cfg SyntheticConfig) *Synthetic {
	if cfg.Devices <= 0 {
		cfg.Devices = 8
	}
	if cfg.SensorsPerDevice <= 0 {
		cfg.SensorsPerDevice = 4
	}
	if len(cfg.Metrics) == 0 {
		cfg.Metrics = []point.Metric{
			point.MetricTemperature, point.MetricPower, point.MetricAmperage, point.MetricAirflow,
		}
	}
	return &Synthetic{cfg: cfg, rng: rand.New(rand.NewSource(1))}
}

// Name reports the collector name.
func (s *Synthetic) Name() string { return "synthetic" }

// Poll emits one batch of synthetic points.
func (s *Synthetic) Poll(ctx context.Context) ([]point.DataPoint, error) {
	ts := time.Now()
	var out []point.DataPoint
	for d := 0; d < s.cfg.Devices; d++ {
		device := fmt.Sprintf("rack-%02d", d)
		for c := 0; c < s.cfg.SensorsPerDevice; c++ {
			metric := s.cfg.Metrics[c%len(s.cfg.Metrics)]
			sensor := fmt.Sprintf("chan-%d", c)
			var value float64
			switch metric {
			case point.MetricTemperature:
				value = 22 + s.rng.Float64()*18 // 22–40 °C
			case point.MetricPower:
				value = 200 + s.rng.Float64()*800 // 200–1000 W
			case point.MetricAmperage:
				value = 1 + s.rng.Float64()*15 // 1–16 A
			case point.MetricAirflow:
				value = 0.2 + s.rng.Float64()*1.8 // 0.2–2.0 m^3/s
			default:
				value = s.rng.Float64() * 100
			}
			out = append(out, point.New(device, sensor, metric, value, ts))
		}
	}
	return out, nil
}
