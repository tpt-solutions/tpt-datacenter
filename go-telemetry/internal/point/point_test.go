// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package point

import (
	"testing"
	"time"
)

func TestMarshalRoundTrip(t *testing.T) {
	orig := New("rack-01", "chan-0", MetricTemperature, 30.5, time.Unix(1_700_000_000, 123456789))

	b, err := orig.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalBinary(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != orig {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", got, orig)
	}
}

func TestUnitForMetric(t *testing.T) {
	cases := map[Metric]Unit{
		MetricTemperature: UnitCelsius,
		MetricVoltage:     UnitVolt,
		MetricAmperage:    UnitAmpere,
		MetricAirflow:     UnitCubicMetersPerSec,
		MetricPower:       UnitWatt,
		MetricHumidity:    UnitPercent,
		MetricOutletState: UnitBoolean,
	}
	for m, want := range cases {
		if got := UnitForMetric(m); got != want {
			t.Errorf("UnitForMetric(%s) = %s, want %s", m, got, want)
		}
	}
}

func TestMarshalRejectsEmpty(t *testing.T) {
	p := DataPoint{Metric: MetricTemperature}
	if _, err := p.MarshalBinary(); err == nil {
		t.Fatal("expected error for empty device/sensor")
	}
}

func TestJSON(t *testing.T) {
	p := New("d", "s", MetricPower, 42.0, time.Now())
	if _, err := p.MarshalJSON(); err != nil {
		t.Fatalf("json: %v", err)
	}
}
