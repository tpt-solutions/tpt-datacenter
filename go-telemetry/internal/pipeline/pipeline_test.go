// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package pipeline

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/collector"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/metrics"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
)

// countingSink records every point written, used to assert pipeline delivery.
type countingSink struct {
	written atomic.Int64
}

func (c *countingSink) Name() string { return "counting" }
func (c *countingSink) WriteBatch(_ context.Context, pts []point.DataPoint) error {
	c.written.Add(int64(len(pts)))
	return nil
}
func (c *countingSink) Close() error { return nil }

func TestPipelineDeliversAllPoints(t *testing.T) {
	reg := metrics.NewRegistry()
	sink := &countingSink{}

	pl := New(Config{
		Collectors: []collector.Collector{
			collector.NewSynthetic(collector.SyntheticConfig{Devices: 4, SensorsPerDevice: 3}),
		},
		Sink:         sink,
		Registry:     reg,
		IngestBuffer: 1024,
		Workers:      2,
		BatchSize:    64,
		BatchTimeout: 20 * time.Millisecond,
		PollInterval: time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if err := pl.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	ing := reg.Snapshot().Ingested
	wrt := sink.written.Load()
	if wrt != ing {
		t.Fatalf("written %d != ingested %d (points lost)", wrt, ing)
	}
	if ing == 0 {
		t.Fatal("expected points to be ingested")
	}
}

func TestPipelineDeadLettersOnWriteError(t *testing.T) {
	reg := metrics.NewRegistry()
	dl := metrics.NewDeadLetterSink(reg, 64)
	boom := &failingSink{}
	pl := New(Config{
		Collectors: []collector.Collector{
			collector.NewSynthetic(collector.SyntheticConfig{Devices: 2, SensorsPerDevice: 2}),
		},
		Sink:         boom,
		Registry:     reg,
		DeadLetters:  dl,
		IngestBuffer: 1024,
		Workers:      2,
		BatchSize:    16,
		BatchTimeout: 20 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := pl.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if reg.Snapshot().DeadLetters == 0 {
		t.Fatal("expected dead letters after write failure")
	}
}

type failingSink struct{}

func (f *failingSink) Name() string { return "failing" }
func (f *failingSink) WriteBatch(_ context.Context, _ []point.DataPoint) error {
	return context.DeadlineExceeded
}
func (f *failingSink) Close() error { return nil }
