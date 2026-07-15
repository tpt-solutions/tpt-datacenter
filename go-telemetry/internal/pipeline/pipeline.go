// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package pipeline wires collectors to the time-series writer through a
// bounded, concurrent ingest bus with a worker pool, per-worker batching, and
// dead-letter handling for failed writes.
package pipeline

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/collector"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/metrics"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/writer"
)

// Config tunes the ingestion pipeline.
type Config struct {
	// Collectors are the telemetry sources.
	Collectors []collector.Collector
	// Sink is the time-series writer (QuestDB ILP or dev log).
	Sink writer.TimeSeriesWriter
	// Registry records counters (optional but recommended).
	Registry *metrics.Registry
	// DeadLetters captures points that fail to write (optional).
	DeadLetters *metrics.DeadLetterSink

	// IngestBuffer is the bounded capacity of the ingest channel. When full,
	// sources block on send — this is the backpressure mechanism.
	IngestBuffer int
	// Workers is the number of concurrent write workers.
	Workers int
	// BatchSize is the number of points accumulated before a flush.
	BatchSize int
	// BatchTimeout bounds how long a partial batch may wait before flushing.
	BatchTimeout time.Duration
	// PollInterval is how often each collector is polled.
	PollInterval time.Duration
}

// Pipeline is the concurrent ingestion engine.
type Pipeline struct {
	cfg     Config
	ingest  chan point.DataPoint
	cancel  context.CancelFunc
	sources sync.WaitGroup
	workers sync.WaitGroup
}

// New validates the config and constructs a Pipeline.
func New(cfg Config) *Pipeline {
	if cfg.IngestBuffer <= 0 {
		cfg.IngestBuffer = 65536
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 4096
	}
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = 200 * time.Millisecond
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.Registry == nil {
		cfg.Registry = metrics.NewRegistry()
	}
	return &Pipeline{cfg: cfg, ingest: make(chan point.DataPoint, cfg.IngestBuffer)}
}

// Run starts all sources and workers and blocks until ctx is cancelled, then
// drains the bus, closes the sink, and returns. It is safe to call Stop from
// another goroutine to trigger shutdown.
func (p *Pipeline) Run(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)

	for _, c := range p.cfg.Collectors {
		p.sources.Add(1)
		go p.source(ctx, c)
	}
	for i := 0; i < p.cfg.Workers; i++ {
		p.workers.Add(1)
		go p.worker(ctx)
	}

	<-ctx.Done()     // wait for cancellation (or Stop)
	p.sources.Wait() // sources stop producing
	close(p.ingest)  // unblock workers; they drain remaining points
	p.workers.Wait() // then exit
	if err := p.cfg.Sink.Close(); err != nil {
		log.Printf("[pipeline] sink close error: %v", err)
	}
	return nil
}

// Stop cancels the pipeline context, triggering graceful shutdown.
func (p *Pipeline) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

// source polls one collector on a fixed interval and forwards points.
func (p *Pipeline) source(ctx context.Context, c collector.Collector) {
	defer p.sources.Done()
	defer log.Printf("[pipeline] source %s stopped", c.Name())

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pts, err := c.Poll(ctx)
			if err != nil {
				p.cfg.Registry.IncPollErrors(1)
				log.Printf("[pipeline] source %s poll error: %v", c.Name(), err)
				continue
			}
			p.cfg.Registry.IncSource(c.Name(), int64(len(pts)))
			for _, pt := range pts {
				select {
				case p.ingest <- pt:
					p.cfg.Registry.IncIngested(1)
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// worker drains the ingest bus, batches points, and flushes to the sink.
func (p *Pipeline) worker(ctx context.Context) {
	defer p.workers.Done()
	defer log.Printf("[pipeline] worker stopped")

	batch := make([]point.DataPoint, 0, p.cfg.BatchSize)
	timer := time.NewTimer(p.cfg.BatchTimeout)
	timer.Stop() // armed lazily per loop iteration
	flush := func() {
		if len(batch) == 0 {
			return
		}
		p.flush(batch)
		batch = batch[:0]
	}
	for {
		timer.Reset(p.cfg.BatchTimeout)
		select {
		case pt, ok := <-p.ingest:
			if !ok {
				flush()
				return
			}
			batch = append(batch, pt)
			if len(batch) >= p.cfg.BatchSize {
				flush()
			}
		case <-timer.C:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}

func (p *Pipeline) flush(batch []point.DataPoint) {
	if err := p.cfg.Sink.WriteBatch(context.Background(), batch); err != nil {
		p.cfg.Registry.IncWriteErrors(1)
		log.Printf("[pipeline] write error: %v", err)
		if p.cfg.DeadLetters != nil {
			for _, pt := range batch {
				p.cfg.DeadLetters.Put(metrics.DeadLetter{
					Point:     pt,
					Error:     err.Error(),
					Attempted: time.Now(),
				})
			}
		}
		return
	}
	p.cfg.Registry.IncBatches(1)
	p.cfg.Registry.IncWritten(int64(len(batch)))
}
