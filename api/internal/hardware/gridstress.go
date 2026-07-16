// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package hardware — see redfish.go. This file defines the grid-stress signal
// input interface and the dynamic CPU power-throttling logic that reacts to it
// (todo.md Phase 7).
//
// Grid-stress is an external signal (e.g. a demand-response event, carbon
// intensity spike, or grid-frequency excursion) that should prompt the
// facility to shed compute load. In this phase GridStressSource is a clean
// stub interface; a future TPT Dynamo/Relay integration plugs in a concrete
// source without changing any consumer.

package hardware

import (
	"context"
	"sync"
	"time"
)

// GridStressLevel is a coarse classification of grid conditions.
type GridStressLevel string

const (
	// StressNone: normal operation.
	StressNone GridStressLevel = "none"
	// StressMild: prefer efficiency, throttle non-urgent turbo.
	StressMild GridStressLevel = "mild"
	// StressHigh: curtail compute power to a hard cap.
	StressHigh GridStressLevel = "high"
	// StressCritical: emergency shed — power down non-critical servers.
	StressCritical GridStressLevel = "critical"
)

// GridStressSignal is a single observation from an external grid signal.
type GridStressSignal struct {
	Level GridStressLevel `json:"level"`
	// Score is a normalized 0..1 stress magnitude (higher = worse).
	Score float64 `json:"score"`
	// Source identifies the origin (e.g. "dynamo", "relay", "manual").
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
	// Note is free-text context for operators.
	Note string `json:"note,omitempty"`
}

// GridStressSource yields the current grid-stress observation. The default
// stub always reports StressNone; a real integration (TPT Dynamo/Relay)
// implements this interface and is injected at startup.
type GridStressSource interface {
	Current(ctx context.Context) (GridStressSignal, error)
}

// StaticGridStress is a GridStressSource returning a fixed signal. Useful for
// manual override and tests, and as the default stub before a real source is
// wired in.
type StaticGridStress struct {
	Signal GridStressSignal
}

// Current returns the configured static signal.
func (s StaticGridStress) Current(_ context.Context) (GridStressSignal, error) {
	return s.Signal, nil
}

// ThrottlePolicy maps a grid-stress level to a compute power response.
type ThrottlePolicy struct {
	// MaxPowerCapW is the absolute ceiling applied under StressHigh.
	MaxPowerCapW float64
	// MildCapW is the softer ceiling applied under StressMild.
	MildCapW float64
	// CriticalShutdown lists servers powered down under StressCritical.
	CriticalShutdown []string
}

// DefaultThrottlePolicy returns a sensible starting policy: high stress caps
// at 70% of nameplate, mild at 90%, critical sheds the nominated servers.
func DefaultThrottlePolicy(nameplateW float64, critical []string) ThrottlePolicy {
	return ThrottlePolicy{
		MaxPowerCapW:     nameplateW * 0.70,
		MildCapW:         nameplateW * 0.90,
		CriticalShutdown: critical,
	}
}

// ThrottleAction is the resolved response for one server under a signal.
type ThrottleAction struct {
	ServerID string          `json:"server_id"`
	Level    GridStressLevel `json:"level"`
	// CapW is the power cap to apply (0 = no cap / unlimited).
	CapW float64 `json:"cap_w"`
	// Shutdown is true when the server should be powered down (critical).
	Shutdown bool `json:"shutdown"`
}

// Resolve computes the throttle action for a single server given the policy
// and current signal. It is pure (no I/O) so it is trivially testable and can
// be run per-server in parallel by the orchestrator.
func (p ThrottlePolicy) Resolve(serverID string, sig GridStressSignal) ThrottleAction {
	act := ThrottleAction{ServerID: serverID, Level: sig.Level}
	switch sig.Level {
	case StressMild:
		act.CapW = p.MildCapW
	case StressHigh:
		act.CapW = p.MaxPowerCapW
	case StressCritical:
		for _, s := range p.CriticalShutdown {
			if s == serverID {
				act.Shutdown = true
				break
			}
		}
		if !act.Shutdown {
			act.CapW = p.MaxPowerCapW
		}
	default:
		// StressNone: clear any cap (unlimited).
		act.CapW = 0
	}
	return act
}

// GridStressMonitor polls a GridStressSource on an interval and keeps the
// latest signal available to the throttle controller. It is the single
// integration seam: swap the Source to connect a real grid feed.
type GridStressMonitor struct {
	mu     sync.RWMutex
	src    GridStressSource
	latest GridStressSignal
}

// NewGridStressMonitor starts monitoring from the given source.
func NewGridStressMonitor(src GridStressSource) *GridStressMonitor {
	if src == nil {
		src = StaticGridStress{Signal: GridStressSignal{
			Level: StressNone, Source: "stub", ObservedAt: time.Now().UTC(),
		}}
	}
	m := &GridStressMonitor{src: src, latest: GridStressSignal{Level: StressNone, Source: "stub", ObservedAt: time.Now().UTC()}}
	return m
}

// Latest returns the most recent observed signal.
func (m *GridStressMonitor) Latest() GridStressSignal {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latest
}

// PollOnce reads the source once and stores the result.
func (m *GridStressMonitor) PollOnce(ctx context.Context) error {
	sig, err := m.src.Current(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.latest = sig
	m.mu.Unlock()
	return nil
}

// Run polls the source on the interval until ctx is cancelled.
func (m *GridStressMonitor) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = m.PollOnce(ctx)
		}
	}
}
