// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package control

import "testing"

func newSeeded() *Store {
	s := NewStore(DefaultLimits())
	s.Seed("rack-01", 50, 50, true, 50)
	return s
}

func TestOverrideClampsToEnvelope(t *testing.T) {
	s := newSeeded()
	st, clamped, err := s.Override("rack-01", CmdValve, 250.0, "op", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !clamped {
		t.Fatal("expected value 250 to be clamped")
	}
	if st.Valve != 100.0 {
		t.Fatalf("valve = %v, want 100", st.Valve)
	}
	if st.Mode != ModeManual {
		t.Fatalf("mode = %v, want manual", st.Mode)
	}
}

func TestOverrideUnknownDevice(t *testing.T) {
	s := newSeeded()
	if _, _, err := s.Override("nope", CmdFan, 10, "op", ""); err != ErrUnknownDevice {
		t.Fatalf("err = %v, want ErrUnknownDevice", err)
	}
}

func TestResetReturnsToAuto(t *testing.T) {
	s := newSeeded()
	if _, _, err := s.Override("rack-01", CmdFan, 80, "op", ""); err != nil {
		t.Fatal(err)
	}
	st, err := s.Reset("rack-01", "op", "done")
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != ModeAuto {
		t.Fatalf("mode = %v, want auto", st.Mode)
	}
}

func TestAuditRecorded(t *testing.T) {
	s := newSeeded()
	if _, _, err := s.Override("rack-01", CmdOutlet, false, "op", "power down"); err != nil {
		t.Fatal(err)
	}
	entries := s.Audit(10)
	if len(entries) != 1 {
		t.Fatalf("audit len = %d, want 1", len(entries))
	}
	if entries[0].Operator != "op" || entries[0].Reason != "power down" {
		t.Fatalf("audit entry missing fields: %+v", entries[0])
	}
}

func TestLatchSafeBlocksOverride(t *testing.T) {
	s := newSeeded()
	if _, err := s.LatchSafe("rack-01", "op", "trip"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Override("rack-01", CmdValve, 10, "op", ""); err != ErrLatchedSafe {
		t.Fatalf("err = %v, want ErrLatchedSafe", err)
	}
}
