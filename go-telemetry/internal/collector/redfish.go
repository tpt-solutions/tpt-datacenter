// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
)

// RedfishConfig configures the Redfish telemetry collector.
type RedfishConfig struct {
	// BaseURL is the BMC base, e.g. "https://192.168.1.10".
	BaseURL string
	// Username / Password for BMC basic auth.
	Username string
	Password string
	// Chassis lists the Chassis member ids to scrape (e.g. "1", "System.Embedded.1").
	Chassis []string
	// Client optionally overrides the HTTP client (used by tests).
	Client *http.Client
}

// Redfish implements Collector for Redfish-enabled BMCs. It scrapes the
// Thermal and Power sub-resources of each configured Chassis member.
type Redfish struct {
	cfg    RedfishConfig
	client *http.Client
}

// NewRedfish builds a Redfish collector.
func NewRedfish(cfg RedfishConfig) *Redfish {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Redfish{cfg: cfg, client: client}
}

// Name reports the collector name.
func (r *Redfish) Name() string { return "redfish" }

// redfishThermal models the subset of the Redfish Thermal resource we read.
type redfishThermal struct {
	ID           string `json:"Id"`
	Temperatures []struct {
		Name           string  `json:"Name"`
		ReadingCelsius float64 `json:"ReadingCelsius"`
	} `json:"Temperatures"`
}

// redfishPower models the subset of the Redfish Power resource we read.
type redfishPower struct {
	ID           string `json:"Id"`
	PowerControl []struct {
		Name               string  `json:"Name"`
		PowerConsumedWatts float64 `json:"PowerConsumedWatts"`
	} `json:"PowerControl"`
}

// Poll fetches the latest thermal and power readings for each chassis.
func (r *Redfish) Poll(ctx context.Context) ([]point.DataPoint, error) {
	ts := time.Now()
	var out []point.DataPoint
	for _, ch := range r.cfg.Chassis {
		var thermal redfishThermal
		if err := r.get(ctx, fmt.Sprintf("/redfish/v1/Chassis/%s/Thermal", ch), &thermal); err != nil {
			return nil, err
		}
		for _, t := range thermal.Temperatures {
			out = append(out, point.New(
				"chassis:"+ch, "thermal:"+t.Name, point.MetricTemperature, t.ReadingCelsius, ts,
			))
		}
		var power redfishPower
		if err := r.get(ctx, fmt.Sprintf("/redfish/v1/Chassis/%s/Power", ch), &power); err != nil {
			return nil, err
		}
		for _, p := range power.PowerControl {
			out = append(out, point.New(
				"chassis:"+ch, "power:"+p.Name, point.MetricPower, p.PowerConsumedWatts, ts,
			))
		}
	}
	return out, nil
}

func (r *Redfish) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.cfg.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if r.cfg.Username != "" {
		req.SetBasicAuth(r.cfg.Username, r.cfg.Password)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("redfish: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("redfish: GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
