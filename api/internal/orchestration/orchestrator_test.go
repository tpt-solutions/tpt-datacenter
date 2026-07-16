// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package orchestration

import (
	"context"
	"testing"
)

func TestOrchestrator_ClampsAndAccepts(t *testing.T) {
	orc := New(NewSimSink(), DefaultPolicy())

	// Out-of-range value gets clamped and accepted.
	resp, err := orc.Submit(context.Background(), SubmitRequest{
		Device: "rack-01", Command: CmdValve, Value: 150.0, Operator: "op1", Source: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted {
		t.Fatal("expected accepted")
	}
	if !resp.Clamped {
		t.Fatal("expected clamped")
	}
	if resp.State == nil || resp.State.Valve != 100.0 {
		t.Fatalf("expected valve clamped to 100, got %+v", resp.State)
	}
}

func TestOrchestrator_RejectsUnknownCommand(t *testing.T) {
	orc := New(NewSimSink(), DefaultPolicy())
	resp, err := orc.Submit(context.Background(), SubmitRequest{
		Device: "rack-01", Command: Command("bogus"), Value: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted {
		t.Fatal("expected rejection for unknown command")
	}
}

func TestOrchestrator_TwoPersonRule(t *testing.T) {
	policy := DefaultPolicy()
	policy.RequireTwoPerson = true
	orc := New(NewSimSink(), policy)

	// Extreme setpoint without two-person ack must be rejected.
	resp, _ := orc.Submit(context.Background(), SubmitRequest{
		Device: "rack-01", Command: CmdValve, Value: 100.0, Source: "operator",
	})
	if resp.Accepted {
		t.Fatal("expected two-person rejection at extreme setpoint")
	}

	// AI brain source bypasses the operator two-person rule.
	resp2, _ := orc.Submit(context.Background(), SubmitRequest{
		Device: "rack-01", Command: CmdValve, Value: 100.0, Source: "ai_brain",
	})
	if !resp2.Accepted {
		t.Fatal("expected ai_brain to pass two-person rule")
	}
}

func TestOrchestrator_PolicyOnlyMode(t *testing.T) {
	orc := New(nil, DefaultPolicy())
	resp, _ := orc.Submit(context.Background(), SubmitRequest{
		Device: "rack-01", Command: CmdFan, Value: 50.0,
	})
	if resp.Accepted {
		t.Fatal("expected rejection when no sink configured")
	}
}
