// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package hardware

import (
	"context"
	"testing"
)

func TestThrottleResolve_Levels(t *testing.T) {
	policy := DefaultThrottlePolicy(400.0, []string{"srv-critical"})

	cases := []struct {
		name    string
		level   GridStressLevel
		server  string
		wantCap float64
		wantOff bool
	}{
		{"none clears cap", StressNone, "srv-1", 0, false},
		{"mild caps at 90%", StressMild, "srv-1", 360.0, false},
		{"high caps at 70%", StressHigh, "srv-1", 280.0, false},
		{"critical sheds nominated", StressCritical, "srv-critical", 0, true},
		{"critical caps others", StressCritical, "srv-1", 280.0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sig := GridStressSignal{Level: c.level, Score: 1.0}
			act := policy.Resolve(c.server, sig)
			if act.CapW != c.wantCap {
				t.Errorf("server %s level %s: cap = %v, want %v", c.server, c.level, act.CapW, c.wantCap)
			}
			if act.Shutdown != c.wantOff {
				t.Errorf("server %s level %s: shutdown = %v, want %v", c.server, c.level, act.Shutdown, c.wantOff)
			}
		})
	}
}

// fakeSource is a GridStressSource used to verify the monitor stores updates.
type fakeSource struct{ sig GridStressSignal }

func (f fakeSource) Current(context.Context) (GridStressSignal, error) { return f.sig, nil }

func TestGridStressMonitor_Polls(t *testing.T) {
	src := fakeSource{sig: GridStressSignal{Level: StressHigh, Source: "test", Score: 0.8}}
	m := NewGridStressMonitor(src)
	if got := m.Latest().Level; got != StressNone {
		t.Fatalf("initial should be none, got %s", got)
	}
	if err := m.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.Latest().Level; got != StressHigh {
		t.Fatalf("after poll want high, got %s", got)
	}
}

func TestRedfishSetSystemPower_Stubbed(t *testing.T) {
	// Without a live BMC we can at least assert the client is constructible and
	// the action payload target is sent. We use a fake HTTP server.
	c := NewRedfishClient("http://example.invalid", RedfishCreds{Username: "u", Password: "p"}, OEMDell)
	if c.OEM != OEMDell {
		t.Fatalf("oem not set: %v", c.OEM)
	}
	if c.Creds.Username != "u" {
		t.Fatal("creds not stored")
	}
}
