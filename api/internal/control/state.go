// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package control implements the minimal, safety-bounded, audited manual
// override surface for the TPT DataCenter facility edge (todo.md Phase 8 stub).
//
// It is intentionally self-contained (no internal go-telemetry imports) so it
// can be deployed as a small, auditable binary. In this phase the actuator
// state is held in memory and seeded from the facility topology spec; the
// documented seam for routing commands to real edge HAL agents (rust-edge
// supervisors) is left for the full orchestration service.
package control

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"
)

// Control-store errors.
var (
	// ErrUnknownDevice means the override targeted a device not in the store.
	ErrUnknownDevice = errors.New("unknown device")
	// ErrUnknownCommand means the command kind is not an actuator channel.
	ErrUnknownCommand = errors.New("unknown command")
	// ErrLatchedSafe means the device is latched and must be reset first.
	ErrLatchedSafe = errors.New("device is latched in safe state; reset first")
)

// Command is an actuator channel a human operator may override.
type Command string

const (
	// CmdValve is the cooling valve position, 0..100 % open.
	CmdValve Command = "valve"
	// CmdFan is the cooling fan speed, 0..100 % of maximum.
	CmdFan Command = "fan"
	// CmdOutlet is the PDU outlet on/off state.
	CmdOutlet Command = "outlet"
	// CmdDischarge is the UPS maximum discharge rate, 0..100 % of capacity.
	CmdDischarge Command = "discharge_limit"
)

// Mode describes how a device is currently being driven.
type Mode string

const (
	// ModeAuto means the edge supervisor / AI brain is in control.
	ModeAuto Mode = "auto"
	// ModeManual means an operator override is active.
	ModeManual Mode = "manual"
	// ModeSafe means the device is latched into a fail-safe state.
	ModeSafe Mode = "safe"
)

// SafetyLimits is the actuator envelope and alarm thresholds. It mirrors
// rust-edge's control::safety::SafetyLimits so operator overrides are bounded
// by the same physics the edge agents enforce.
type SafetyLimits struct {
	// CritAirTempC is the temperature above which we would trip to FullCooling.
	CritAirTempC float64
	// WarnAirTempC is the temperature at which we surface a warning.
	WarnAirTempC float64
	// MinAirTempC is the temperature below which we trip (over-cool / freeze).
	MinAirTempC float64
	// Valve/fan/discharge are hard [Min,Max] envelopes in percent.
	MinValve, MaxValve     float64
	MinFan, MaxFan         float64
	MinDischarge, MaxDischarge float64
}

// DefaultLimits returns the same defaults rust-edge uses.
func DefaultLimits() SafetyLimits {
	return SafetyLimits{
		CritAirTempC:  45.0,
		WarnAirTempC:  35.0,
		MinAirTempC:   5.0,
		MinValve: 0, MaxValve: 100,
		MinFan: 0, MaxFan: 100,
		MinDischarge: 0, MaxDischarge: 100,
	}
}

// ActuatorState is the current commanded state of one device.
type ActuatorState struct {
	Device         string    `json:"device"`
	Valve          float64   `json:"valve"`
	Fan            float64   `json:"fan"`
	Outlet         bool      `json:"outlet"`
	DischargeLimit float64   `json:"discharge_limit"`
	Mode           Mode      `json:"mode"`
	LatchedSafe    bool      `json:"latched_safe"`
	Reason         string    `json:"reason,omitempty"`
	Operator       string    `json:"operator,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AuditEntry is an immutable record of a control action (or reset).
type AuditEntry struct {
	ID       string    `json:"id"`
	TS       time.Time `json:"ts"`
	Device   string    `json:"device"`
	Command  Command   `json:"command,omitempty"`
	Requested any       `json:"requested"`
	Applied  any       `json:"applied"`
	Clamped  bool      `json:"clamped"`
	Operator string    `json:"operator,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Note     string    `json:"note,omitempty"`
}

// Store holds actuator state + an append-only audit log, guarded by a mutex.
type Store struct {
	mu      sync.RWMutex
	limits  SafetyLimits
	state   map[string]*ActuatorState
	audit   []AuditEntry
	auditCap int
	seq     uint64
}

// NewStore builds an empty store with the default safety limits.
func NewStore(limits SafetyLimits) *Store {
	if limits == (SafetyLimits{}) {
		limits = DefaultLimits()
	}
	return &Store{
		limits:   limits,
		state:    make(map[string]*ActuatorState),
		auditCap: 1000,
	}
}

// Seed installs an initial (auto) state for a device.
func (s *Store) Seed(device string, valve, fan float64, outlet bool, discharge float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state[device]; ok {
		return
	}
	s.state[device] = &ActuatorState{
		Device:         device,
		Valve:          clamp(valve, s.limits.MinValve, s.limits.MaxValve),
		Fan:            clamp(fan, s.limits.MinFan, s.limits.MaxFan),
		Outlet:         outlet,
		DischargeLimit: clamp(discharge, s.limits.MinDischarge, s.limits.MaxDischarge),
		Mode:           ModeAuto,
		UpdatedAt:      time.Now().UTC(),
	}
}

// Devices returns a snapshot of every known device's state.
func (s *Store) Devices() []ActuatorState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ActuatorState, 0, len(s.state))
	for _, st := range s.state {
		out = append(out, *st)
	}
	return out
}

// State returns the state of one device, or nil if unknown.
func (s *Store) State(device string) *ActuatorState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.state[device]
	if !ok {
		return nil
	}
	c := *st
	return &c
}

// Override clamps and applies a manual command, recording an audit entry.
// It returns the resulting state and whether the value was clamped.
func (s *Store) Override(device string, cmd Command, value any, operator, reason string) (*ActuatorState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[device]
	if !ok {
		return nil, false, ErrUnknownDevice
	}
	if st.LatchedSafe {
		return nil, false, ErrLatchedSafe
	}

	var applied any
	clamped := false
	switch cmd {
	case CmdValve:
		v := toFloat(value)
		cv := clamp(v, s.limits.MinValve, s.limits.MaxValve)
		clamped = cv != v
		st.Valve = cv
		applied = cv
	case CmdFan:
		v := toFloat(value)
		cv := clamp(v, s.limits.MinFan, s.limits.MaxFan)
		clamped = cv != v
		st.Fan = cv
		applied = cv
	case CmdDischarge:
		v := toFloat(value)
		cv := clamp(v, s.limits.MinDischarge, s.limits.MaxDischarge)
		clamped = cv != v
		st.DischargeLimit = cv
		applied = cv
	case CmdOutlet:
		b := toBool(value)
		st.Outlet = b
		applied = b
	default:
		return nil, false, ErrUnknownCommand
	}

	st.Mode = ModeManual
	st.Operator = operator
	st.Reason = reason
	st.UpdatedAt = time.Now().UTC()

	s.recordLocked(AuditEntry{
		Device:   device,
		Command:  cmd,
		Requested: value,
		Applied:  applied,
		Clamped:  clamped,
		Operator: operator,
		Reason:   reason,
	})
	c := *st
	return &c, clamped, nil
}

// Reset returns a device to auto mode and clears any latched safe state.
func (s *Store) Reset(device, operator, reason string) (*ActuatorState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[device]
	if !ok {
		return nil, ErrUnknownDevice
	}
	st.Mode = ModeAuto
	st.LatchedSafe = false
	st.Operator = operator
	st.Reason = reason
	st.UpdatedAt = time.Now().UTC()
	s.recordLocked(AuditEntry{
		Device:   device,
		Requested: "reset",
		Applied:  "auto",
		Operator: operator,
		Reason:   reason,
		Note:     "returned to automatic control",
	})
	c := *st
	return &c, nil
}

// LatchSafe forces a device into the latched safe state (e.g. operator-acked
// trip). Used by the future orchestration layer; exposed for completeness.
func (s *Store) LatchSafe(device, operator, reason string) (*ActuatorState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[device]
	if !ok {
		return nil, ErrUnknownDevice
	}
	st.LatchedSafe = true
	st.Mode = ModeSafe
	st.Valve = s.limits.MaxValve
	st.Fan = s.limits.MaxFan
	st.Operator = operator
	st.Reason = reason
	st.UpdatedAt = time.Now().UTC()
	s.recordLocked(AuditEntry{
		Device:   device,
		Requested: "latch_safe",
		Applied:  "safe",
		Operator: operator,
		Reason:   reason,
		Note:     "latched into fail-safe state",
	})
	c := *st
	return &c, nil
}

// Audit returns up to limit most-recent audit entries (oldest first within the
// window; we return newest-first for UI convenience).
func (s *Store) Audit(limit int) []AuditEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.audit) {
		limit = len(s.audit)
	}
	out := make([]AuditEntry, 0, limit)
	for i := len(s.audit) - limit; i < len(s.audit); i++ {
		out = append(out, s.audit[i])
	}
	return out
}

func (s *Store) recordLocked(e AuditEntry) {
	s.seq++
	e.ID = formatSeq(s.seq)
	e.TS = time.Now().UTC()
	s.audit = append(s.audit, e)
	if len(s.audit) > s.auditCap {
		s.audit = s.audit[len(s.audit)-s.auditCap:]
	}
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}

func toBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1" || t == "on"
	case float64:
		return t != 0
	default:
		return false
	}
}

func formatSeq(n uint64) string {
	return "aud-" + strconv.FormatUint(n, 10)
}
