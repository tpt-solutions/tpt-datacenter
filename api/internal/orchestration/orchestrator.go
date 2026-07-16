// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package orchestration implements the TPT DataCenter core platform
// orchestration layer (todo.md Phase 8): a central service that routes control
// intents to the responsible edge HAL agents, enforces the safety/policy
// envelope, and records every action to a single audit log.
//
// The southbound seam to the rust-edge supervisors is the HalCommandSink
// interface. In this phase a no-op / simulator-backed sink is provided; a real
// deployment wires the gRPC Orchestrator to the rust-edge supervisors (each of
// which already exposes a From<ai_brain::serve::Command> conversion for
// ControlCommand) without changing the orchestration logic.
package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Command mirrors the actuator channels in the proto / control store.
type Command string

const (
	CmdValve     Command = "valve"
	CmdFan       Command = "fan"
	CmdOutlet    Command = "outlet"
	CmdDischarge Command = "discharge_limit"
)

// ActuatorState is the resolved state of one device after a command.
type ActuatorState struct {
	Device      string    `json:"device"`
	Valve       float64   `json:"valve"`
	Fan         float64   `json:"fan"`
	Outlet      bool      `json:"outlet"`
	Discharge   float64   `json:"discharge_limit"`
	Mode        string    `json:"mode"`
	LatchedSafe bool      `json:"latched_safe"`
	Operator    string    `json:"operator,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AuditEntry is an immutable record of a routed control action.
type AuditEntry struct {
	ID        string    `json:"id"`
	TS        time.Time `json:"ts"`
	Device    string    `json:"device"`
	Command   Command   `json:"command,omitempty"`
	Source    string    `json:"source"`
	Requested any       `json:"requested"`
	Applied   any       `json:"applied"`
	Clamped   bool      `json:"clamped"`
	Accepted  bool      `json:"accepted"`
	Reason    string    `json:"reason,omitempty"`
	Operator  string    `json:"operator,omitempty"`
}

// HalCommandSink is the seam to the rust-edge supervisors / real HAL. The
// orchestrator hands it a normalized command; the sink is responsible for
// delivering it to the target device (simulator or real hardware) and returning
// the resulting actuator state. A nil sink makes the orchestrator reject all
// submits (policy-only mode) which is useful for dry-run/audit deployments.
type HalCommandSink interface {
	Apply(ctx context.Context, device string, cmd Command, value any) (*ActuatorState, error)
}

// Policy is the enforced safety/policy envelope.
type Policy struct {
	CritAirTempC               float64
	WarnAirTempC               float64
	MinAirTempC                float64
	MinValve, MaxValve         float64
	MinFan, MaxFan             float64
	MinDischarge, MaxDischarge float64
	// RequireTwoPerson forces a second operator acknowledgement for manual
	// overrides on safety-critical actuators (valve/fan at extreme settings).
	RequireTwoPerson bool
}

// DefaultPolicy returns the baseline envelope (aligned with control.DefaultLimits).
func DefaultPolicy() Policy {
	return Policy{
		CritAirTempC: 45, WarnAirTempC: 35, MinAirTempC: 5,
		MinValve: 0, MaxValve: 100, MinFan: 0, MaxFan: 100,
		MinDischarge: 0, MaxDischarge: 100,
	}
}

// Orchestrator routes commands, enforces policy, and audits.
type Orchestrator struct {
	mu     sync.RWMutex
	sink   HalCommandSink
	policy Policy
	audit  []AuditEntry
	cap    int
	seq    uint64
}

// New builds an orchestrator. A nil sink puts it in policy-only (reject) mode.
func New(sink HalCommandSink, policy Policy) *Orchestrator {
	if policy == (Policy{}) {
		policy = DefaultPolicy()
	}
	return &Orchestrator{sink: sink, policy: policy, cap: 1000}
}

// SubmitRequest is a routed control intent.
type SubmitRequest struct {
	Device   string
	Command  Command
	Value    any
	Operator string
	Reason   string
	Source   string
}

// SubmitResponse is the result of routing an intent.
type SubmitResponse struct {
	Accepted bool
	Clamped  bool
	Reason   string
	State    *ActuatorState
}

// clampedValue enforces the numeric envelope and returns the clamped value and
// whether clamping occurred.
func (o *Orchestrator) clampedValue(cmd Command, v float64) (float64, bool) {
	var lo, hi float64
	switch cmd {
	case CmdValve:
		lo, hi = o.policy.MinValve, o.policy.MaxValve
	case CmdFan:
		lo, hi = o.policy.MinFan, o.policy.MaxFan
	case CmdDischarge:
		lo, hi = o.policy.MinDischarge, o.policy.MaxDischarge
	default:
		return v, false
	}
	if v < lo {
		return lo, true
	}
	if v > hi {
		return hi, true
	}
	return v, false
}

// Submit routes one control intent through policy enforcement to the sink.
func (o *Orchestrator) Submit(ctx context.Context, req SubmitRequest) (*SubmitResponse, error) {
	if req.Source == "" {
		req.Source = "operator"
	}

	// Validate command kind.
	switch req.Command {
	case CmdValve, CmdFan, CmdDischarge, CmdOutlet:
	default:
		return o.reject(req, fmt.Sprintf("unknown command %q", req.Command))
	}

	// Numeric envelope enforcement (clamp, record).
	var applied any
	clamped := false
	if req.Command != CmdOutlet {
		f, ok := toFloat(req.Value)
		if !ok {
			return o.reject(req, "value is not numeric")
		}
		cv, was := o.clampedValue(req.Command, f)
		clamped = was
		applied = cv
		req.Value = cv
	} else {
		b, ok := toBool(req.Value)
		if !ok {
			return o.reject(req, "outlet value is not boolean")
		}
		applied = b
		req.Value = b
	}

	// Two-person rule for extreme manual overrides.
	if o.policy.RequireTwoPerson && req.Source == "operator" {
		if req.Command == CmdValve || req.Command == CmdFan {
			f, _ := toFloat(applied)
			if f >= o.policy.MaxValve-0.001 || f <= o.policy.MinValve+0.001 {
				return o.reject(req, "two-person acknowledgement required for extreme valve/fan setpoint")
			}
		}
	}

	if o.sink == nil {
		return o.reject(req, "no edge sink configured (policy-only mode)")
	}

	st, err := o.sink.Apply(ctx, req.Device, req.Command, req.Value)
	if err != nil {
		o.record(req, applied, false, false, err.Error())
		return nil, err
	}
	o.record(req, applied, clamped, true, "")
	return &SubmitResponse{Accepted: true, Clamped: clamped, State: st}, nil
}

// Policy returns the enforced envelope.
func (o *Orchestrator) Policy() Policy {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.policy
}

// Audit returns the most-recent limit entries (newest first).
func (o *Orchestrator) Audit(limit int) []AuditEntry {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if limit <= 0 || limit > len(o.audit) {
		limit = len(o.audit)
	}
	out := make([]AuditEntry, 0, limit)
	for i := len(o.audit) - limit; i < len(o.audit); i++ {
		out = append(out, o.audit[i])
	}
	return out
}

func (o *Orchestrator) reject(req SubmitRequest, reason string) (*SubmitResponse, error) {
	o.record(req, req.Value, false, false, reason)
	return &SubmitResponse{Accepted: false, Reason: reason}, nil
}

func (o *Orchestrator) record(req SubmitRequest, applied any, clamped, accepted bool, reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	e := AuditEntry{
		ID:        fmt.Sprintf("orc-%d", o.seq),
		TS:        time.Now().UTC(),
		Device:    req.Device,
		Command:   req.Command,
		Source:    req.Source,
		Requested: req.Value,
		Applied:   applied,
		Clamped:   clamped,
		Accepted:  accepted,
		Reason:    reason,
		Operator:  req.Operator,
	}
	o.audit = append(o.audit, e)
	if len(o.audit) > o.cap {
		o.audit = o.audit[len(o.audit)-o.cap:]
	}
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(t, "%f", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func toBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		return t == "true" || t == "1" || t == "on", true
	case float64:
		return t != 0, true
	}
	return false, false
}
