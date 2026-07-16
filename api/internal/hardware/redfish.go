// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package hardware implements the TPT DataCenter Hardware Management API
// (todo.md Phase 7): a Redfish client for compute-server management (Dell,
// HPE, Supermicro), IPMI power control, dynamic CPU power throttling tied to
// grid-stress signals, and the grid-stress signal input interface (a clean
// stub for future TPT Dynamo/Relay integration).
//
// It is intentionally dependency-light (net/http + encoding/json) so it can be
// embedded in the same auditable control binary as the manual override API
// (api/internal/control). In this phase the server managements commands are
// routed to a Redfish BMC endpoint behind the same auth/audit envelope as the
// control store; full vendor command-set coverage is enumerated per-OEM below.
package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// RedfishClient talks to a compute server's BMC over the Redfish API.
// It supports pluggable transport so tests can substitute a fake server, and
// carries per-OEM capability flags used to gate vendor-specific commands.
type RedfishClient struct {
	BaseURL    string
	HTTPClient *http.Client
	// Creds are sent as HTTP Basic auth to the BMC.
	Creds RedfishCreds
	// OEM selects vendor-specific command handling (dell, hpe, supermicro).
	OEM OEM
}

// RedfishCreds carries BMC authentication.
type RedfishCreds struct {
	Username string
	Password string
}

// OEM is a supported compute-server vendor.
type OEM string

const (
	// OEMGeneric is any Redfish-compliant BMC (base schema only).
	OEMGeneric OEM = "generic"
	// OEMDell is Dell iDRAC.
	OEMDell OEM = "dell"
	// OEMHPE is HPE iLO.
	OEMHPE OEM = "hpe"
	// OEMSupermicro is Supermicro BMC.
	OEMSupermicro OEM = "supermicro"
)

// NewRedfishClient builds a client for the given BMC base URL.
func NewRedfishClient(baseURL string, creds RedfishCreds, oem OEM) *RedfishClient {
	if oem == "" {
		oem = OEMGeneric
	}
	return &RedfishClient{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
		Creds:      creds,
		OEM:        oem,
	}
}

// ServerPowerState is the power state of a compute server.
type ServerPowerState struct {
	PowerState string `json:"PowerState"` // On, Off, PoweringOn, PoweringOff
	OEM        any    `json:"Oem,omitempty"`
}

// GetSystemPower reads the power state of a chassis/computer system.
func (c *RedfishClient) GetSystemPower(ctx context.Context, systemID string) (*ServerPowerState, error) {
	var out ServerPowerState
	path := fmt.Sprintf("/redfish/v1/Systems/%s", systemID)
	if err := c.redfishGet(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetSystemPower sets the power state of a compute server.
// action is one of "On", "Off", "GracefulShutdown", "GracefulRestart",
// "ForceOff", "ForceRestart", "PushPowerButton".
func (c *RedfishClient) SetSystemPower(ctx context.Context, systemID, action string) error {
	payload := map[string]any{
		"ResetType": action,
	}
	// Dell iDRAC expects the action under an Oem extension for certain resets.
	if c.OEM == OEMDell && action == "GracefulRestart" {
		payload["@Redfish.ActionInfo"] = "/redfish/v1/Systems/" + systemID + "/#ComputerSystem.Reset"
	}
	return c.redfishPost(ctx, fmt.Sprintf("/redfish/v1/Systems/%s/Actions/ComputerSystem.Reset", systemID), payload)
}

// BootOverride configures the next-boot device (PXE, disk, bios, etc.).
func (c *RedfishClient) BootOverride(ctx context.Context, systemID, target string) error {
	payload := map[string]any{
		"Boot": map[string]any{
			"BootSourceOverrideTarget":  target,
			"BootSourceOverrideEnabled": "Once",
		},
	}
	return c.redfishPatch(ctx, fmt.Sprintf("/redfish/v1/Systems/%s", systemID), payload)
}

// CPUPowerLimit describes a power capping request (watts).
type CPUPowerLimit struct {
	// PowerWatts is the cap; 0 means "no cap" (unlimited).
	PowerWatts float64 `json:"PowerWatts"`
}

// SetCPUPowerCap applies a processor power cap via the Chassis Power resource.
// Vendor specifics:
//   - HPE iLO exposes this under Oem/Hpe/PowerLimit.
//   - Dell iDRAC exposes it under Oem/Dell/PowerLimit or the PowerControl
//     resource's "PowerCapWatts".
//   - Supermicro exposes a similar extension under Oem/Supermicro.
func (c *RedfishClient) SetCPUPowerCap(ctx context.Context, chassisID string, capW CPUPowerLimit) error {
	payload := map[string]any{"PowerControl": []map[string]any{
		{"PowerCapWatts": capW.PowerWatts},
	}}
	switch c.OEM {
	case OEMHPE:
		payload = map[string]any{"Oem": map[string]any{"Hpe": map[string]any{
			"PowerLimit": map[string]any{"PowerLimitInWatts": capW.PowerWatts},
		}}}
	case OEMDell:
		payload = map[string]any{"Oem": map[string]any{"Dell": map[string]any{
			"PowerLimit": map[string]any{"PowerLimitInWatts": capW.PowerWatts},
		}}}
	case OEMSupermicro:
		payload = map[string]any{"Oem": map[string]any{"Supermicro": map[string]any{
			"PowerLimit": map[string]any{"PowerLimitWatts": capW.PowerWatts},
		}}}
	}
	return c.redfishPatch(ctx, fmt.Sprintf("/redfish/v1/Chassis/%s/Power", chassisID), payload)
}

// IndicatorLED toggles the server's locate LED (operator aid).
func (c *RedfishClient) IndicatorLED(ctx context.Context, systemID, state string) error {
	payload := map[string]any{"IndicatorLED": state} // Lit, Off, Blinking
	return c.redfishPatch(ctx, fmt.Sprintf("/redfish/v1/Systems/%s", systemID), payload)
}

func (c *RedfishClient) redfishGet(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("redfish request build: %w", err)
	}
	c.auth(req)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("redfish GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("redfish GET %s -> %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *RedfishClient) redfishPost(ctx context.Context, path string, body any) error {
	return c.redfishSend(ctx, http.MethodPost, path, body)
}

func (c *RedfishClient) redfishPatch(ctx context.Context, path string, body any) error {
	return c.redfishSend(ctx, http.MethodPatch, path, body)
}

func (c *RedfishClient) redfishSend(ctx context.Context, method, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("redfish marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytesReader(buf))
	if err != nil {
		return fmt.Errorf("redfish request build: %w", err)
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("redfish %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("redfish %s %s -> %s", method, path, resp.Status)
	}
	return nil
}

func (c *RedfishClient) auth(req *http.Request) {
	if c.Creds.Username != "" {
		req.SetBasicAuth(c.Creds.Username, c.Creds.Password)
	}
}

// LoadRedfishEndpoint builds a client from environment variables
// (REDFISH_URL, REDFISH_USER, REDFISH_PASS, REDFISH_OEM). Used by the
// management API server.
func LoadRedfishEndpoint() (*RedfishClient, bool) {
	base := os.Getenv("REDFISH_URL")
	if base == "" {
		return nil, false
	}
	return NewRedfishClient(base,
		RedfishCreds{Username: os.Getenv("REDFISH_USER"), Password: os.Getenv("REDFISH_PASS")},
		OEM(os.Getenv("REDFISH_OEM"))), true
}
