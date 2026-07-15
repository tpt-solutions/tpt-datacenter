// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command loadtest drives the ingestion pipeline at high volume to measure
// sustained throughput. It pairs the synthetic collector with a null sink so
// the cost of the pipeline (collect, batch, serialize, dispatch) — not the
// storage engine — is what is measured.
//
//	go run ./cmd/loadtest -duration 10s -workers 8 -batch 8192
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/collector"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/metrics"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/pipeline"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/writer"
)

func main() {
	var (
		duration = flag.Duration("duration", 10*time.Second, "how long to generate load")
		workers  = flag.Int("workers", 8, "number of write workers")
		batch    = flag.Int("batch", 8192, "points per batch")
		sources  = flag.Int("sources", 4, "number of synthetic sources")
		buf      = flag.Int("buf", 1<<20, "ingest channel capacity")
		devices  = flag.Int("devices", 64, "virtual devices per source")
	)
	flag.Parse()

	reg := metrics.NewRegistry()
	collectors := make([]collector.Collector, 0, *sources)
	for i := 0; i < *sources; i++ {
		collectors = append(collectors, collector.NewSynthetic(collector.SyntheticConfig{Devices: *devices}))
	}

	pl := pipeline.New(pipeline.Config{
		Collectors:   collectors,
		Sink:         writer.NewNullWriter(),
		Registry:     reg,
		IngestBuffer: *buf,
		Workers:      *workers,
		BatchSize:    *batch,
		BatchTimeout: 50 * time.Millisecond,
		PollInterval: time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	log.Printf("[loadtest] sources=%d workers=%d batch=%d duration=%s", *sources, *workers, *batch, *duration)
	done := make(chan struct{})
	go func() {
		_ = pl.Run(ctx)
		close(done)
	}()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
loop:
	for {
		select {
		case <-done:
			break loop
		case <-tick.C:
			log.Printf("[loadtest] %+v", reg.Snapshot())
		}
	}

	snap := reg.Snapshot()
	log.Printf("[loadtest] DONE ingested=%d written=%d rate=%.0f pts/sec batches=%d",
		snap.Ingested, snap.Written, snap.Rate, snap.Batches)
}
