// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package metrics

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
)

// DeadLetter is a point that could not be written, retained for replay/audit.
type DeadLetter struct {
	Point     point.DataPoint `json:"point"`
	Error     string          `json:"error"`
	Attempted time.Time       `json:"attempted"`
}

// DeadLetterSink buffers failed points. It is bounded; when full, the oldest
// entry is dropped (counted by the registry) so a poison stream can never OOM
// the ingest host.
type DeadLetterSink struct {
	mu    sync.Mutex
	cond  *sync.Cond
	items []DeadLetter
	cap   int
	reg   *Registry
}

// NewDeadLetterSink creates a sink holding at most cap entries.
func NewDeadLetterSink(reg *Registry, capacity int) *DeadLetterSink {
	if capacity <= 0 {
		capacity = 1024
	}
	s := &DeadLetterSink{cap: capacity, reg: reg}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Put enqueues a dead letter, evicting the oldest entry if at capacity.
func (s *DeadLetterSink) Put(d DeadLetter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= s.cap {
		s.items = s.items[1:]
	}
	s.items = append(s.items, d)
	if s.reg != nil {
		s.reg.IncDeadLetters(1)
	}
	s.cond.Signal()
}

// Drain calls fn for each queued dead letter and clears the sink. It blocks
// until at least one item is available or ctx is cancelled.
func (s *DeadLetterSink) Drain(ctx context.Context, fn func(DeadLetter)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if len(s.items) > 0 {
			items := s.items
			s.items = nil
			s.mu.Unlock()
			for _, d := range items {
				fn(d)
			}
			s.mu.Lock()
			continue
		}
		done := make(chan struct{})
		go func() {
			s.mu.Lock()
			s.cond.Wait()
			s.mu.Unlock()
			close(done)
		}()
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			return
		case <-done:
			s.mu.Lock()
			// loop and re-check
		}
	}
}

// Len returns the current number of buffered dead letters.
func (s *DeadLetterSink) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// Trace logs the duration of an operation as a structured span.
func Trace(ctx context.Context, op string, fn func() error) error {
	start := time.Now()
	err := fn()
	log.Printf("[trace] op=%s duration=%s err=%v", op, time.Since(start), err)
	return err
}
