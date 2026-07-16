// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package orchestration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/TPT-Solutions/tpt-datacenter/api/internal/orchestration"
)

// failSink is a HalCommandSink that always errors — used to simulate edge
// comms loss / agent crash in chaos testing.
type failSink struct {
	mu   sync.Mutex
	fail bool
}

func (f *failSink) Apply(_ context.Context, _ string, _ orchestration.Command, _ any) (*orchestration.ActuatorState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, context.DeadlineExceeded
	}
	return &orchestration.ActuatorState{Device: "d", Mode: "auto"}, nil
}

// TestE2E_SimulatorStack drives a command through the orchestrator envelope and
// confirms the downstream sink receives the (clamped) value — simulating the
// edge -> orchestrator -> sim-sink path with no real hardware.
func TestE2E_SimulatorStack(t *testing.T) {
	sink := orchestration.NewSimSink()
	orc := orchestration.New(sink, orchestration.DefaultPolicy())

	resp, err := orc.Submit(context.Background(), orchestration.SubmitRequest{
		Device: "rack-01", Command: orchestration.CmdValve, Value: 123.0,
		Operator: "e2e", Source: "operator", Reason: "sim test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted || !resp.Clamped {
		t.Fatalf("expected accepted+clamped, got %+v", resp)
	}
	st := sink.State("rack-01")
	if st == nil || st.Valve != 100.0 {
		t.Fatalf("sim sink did not record clamped valve=100, got %+v", st)
	}
	if len(orc.Audit(1)) != 1 {
		t.Fatal("expected one audit entry")
	}
}

// TestChaos_SinkFailure verifies that when the edge sink is unreachable the
// orchestrator fails safe: the command is rejected (never silently dropped) and
// recorded in the audit log as not-accepted.
func TestChaos_SinkFailure(t *testing.T) {
	fs := &failSink{fail: true}
	orc := orchestration.New(fs, orchestration.DefaultPolicy())

	_, err := orc.Submit(context.Background(), orchestration.SubmitRequest{
		Device: "rack-01", Command: orchestration.CmdFan, Value: 50.0, Source: "operator",
	})
	if err == nil {
		t.Fatal("expected error when sink fails")
	}
	// Audit must still capture the rejected attempt for forensics.
	entries := orc.Audit(10)
	if len(entries) != 1 || entries[0].Accepted {
		t.Fatalf("expected one not-accepted audit entry, got %+v", entries)
	}
}

// TestHTTP_SubmitThroughServer exercises the orchestrator over its real HTTP
// handler (auth + clamp + audit), simulating how the dashboard / API gateway
// would call it.
func TestHTTP_SubmitThroughServer(t *testing.T) {
	orc := orchestration.New(orchestration.NewSimSink(), orchestration.DefaultPolicy())
	srv := orchestration.NewServer(orchestration.ServerConfig{
		Addr: "127.0.0.1:0", Orc: orc, AuthToken: "secret",
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// No token -> 401.
	r1, _ := http.Post(ts.URL+"/orc/submit", "application/json",
		strings.NewReader(`{"device":"rack-01","command":"valve","value":200}`))
	if r1.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", r1.StatusCode)
	}
	r1.Body.Close()

	// With token -> accepted + clamped.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/orc/submit",
		strings.NewReader(`{"device":"rack-01","command":"valve","value":200,"operator":"http"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r2.StatusCode)
	}
}
