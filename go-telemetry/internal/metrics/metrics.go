// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package metrics provides lightweight, dependency-free observability for the
// ingestion pipeline: atomic counters, a snapshot for scraping, and a
// dead-letter sink for points that fail to be written.
package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Registry holds pipeline counters. All mutations are lock-free via atomics.
type Registry struct {
	ingested    atomic.Int64
	written     atomic.Int64
	batches     atomic.Int64
	writeErrors atomic.Int64
	deadLetters atomic.Int64
	pollErrors  atomic.Int64
	perSource   sync.Map // map[string]*atomic.Int64
	start       time.Time
}

// NewRegistry creates a ready-to-use registry.
func NewRegistry() *Registry {
	return &Registry{start: time.Now()}
}

// IncIngested records n points entering the pipeline.
func (r *Registry) IncIngested(n int64) { r.ingested.Add(n) }

// IncWritten records n points successfully committed to the time-series DB.
func (r *Registry) IncWritten(n int64) { r.written.Add(n) }

// IncBatches records n batches flushed.
func (r *Registry) IncBatches(n int64) { r.batches.Add(n) }

// IncWriteErrors records n failed batch writes.
func (r *Registry) IncWriteErrors(n int64) { r.writeErrors.Add(n) }

// IncDeadLetters records n points routed to the dead-letter sink.
func (r *Registry) IncDeadLetters(n int64) { r.deadLetters.Add(n) }

// IncPollErrors records n collector poll failures.
func (r *Registry) IncPollErrors(n int64) { r.pollErrors.Add(n) }

// IncSource records points ingested from a named collector.
func (r *Registry) IncSource(name string, n int64) {
	v, _ := r.perSource.LoadOrStore(name, new(atomic.Int64))
	v.(*atomic.Int64).Add(n)
}

// Snapshot is a point-in-time, JSON-friendly view of the counters.
type Snapshot struct {
	UptimeSec   float64          `json:"uptime_sec"`
	Ingested    int64            `json:"ingested"`
	Written     int64            `json:"written"`
	Batches     int64            `json:"batches"`
	WriteErrors int64            `json:"write_errors"`
	DeadLetters int64            `json:"dead_letters"`
	PollErrors  int64            `json:"poll_errors"`
	PerSource   map[string]int64 `json:"per_source"`
	// Rate is ingested points/sec since registry creation.
	Rate float64 `json:"ingest_rate_per_sec"`
}

// Snapshot returns the current counter values.
func (r *Registry) Snapshot() Snapshot {
	uptime := time.Since(r.start).Seconds()
	ing := r.ingested.Load()
	s := Snapshot{
		UptimeSec:   uptime,
		Ingested:    ing,
		Written:     r.written.Load(),
		Batches:     r.batches.Load(),
		WriteErrors: r.writeErrors.Load(),
		DeadLetters: r.deadLetters.Load(),
		PollErrors:  r.pollErrors.Load(),
		PerSource:   map[string]int64{},
	}
	if uptime > 0 {
		s.Rate = float64(ing) / uptime
	}
	r.perSource.Range(func(k, v any) bool {
		s.PerSource[k.(string)] = v.(*atomic.Int64).Load()
		return true
	})
	return s
}
