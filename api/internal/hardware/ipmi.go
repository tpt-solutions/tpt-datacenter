// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package hardware — see redfish.go. This file implements IPMI power control
// for compute servers (todo.md Phase 7): power on/off/cycle/reset through a
// BMC's IPMI-over-LAN service, using the `ipmitool` binary when available and
// falling back to a no-op stub that records the intended action for audit.

package hardware

import (
	"context"
	"fmt"
	"os/exec"
)

// IPMIClient controls a compute server's BMC over IPMI.
type IPMIClient struct {
	Host     string
	User     string
	Password string
	// Tool is the ipmitool binary path; empty defers to PATH.
	Tool string
}

// NewIPMIClient builds an IPMI power-control client.
func NewIPMIClient(host, user, password string) *IPMIClient {
	return &IPMIClient{Host: host, User: user, Password: password}
}

// PowerAction is an IPMI chassis power operation.
type PowerAction string

const (
	PowerOn      PowerAction = "on"
	PowerOff     PowerAction = "off"
	PowerCycle   PowerAction = "cycle"
	PowerReset   PowerAction = "reset"
	PowerSoftOff PowerAction = "soft"
)

// PowerControl issues a chassis power command to the BMC.
//
// When `ipmitool` is present the command is executed for real; otherwise we
// return a sentinel ErrIPMIToolMissing so the caller can decide whether to
// fall back to the Redfish equivalent (most BMCs expose both). This mirrors
// the go-telemetry collector's stance: real IPMI-over-LAN session handling is
// delegated to a well-audited external tool rather than reimplemented.
func (c *IPMIClient) PowerControl(ctx context.Context, action PowerAction) error {
	tool := c.Tool
	if tool == "" {
		tool = "ipmitool"
	}
	args := []string{
		"-H", c.Host,
		"-U", c.User,
		"-P", c.Password,
		"chassis", "power", string(action),
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var pathErr *exec.Error
		if asExecError(err, &pathErr) && pathErr.Err == exec.ErrNotFound {
			return fmt.Errorf("%w: %s", ErrIPMIToolMissing, tool)
		}
		return fmt.Errorf("ipmi power %s failed: %w (%s)", action, err, string(out))
	}
	return nil
}

// ErrIPMIToolMissing indicates ipmitool is not installed/available.
var ErrIPMIToolMissing = fmt.Errorf("ipmitool not available")

func asExecError(err error, target **exec.Error) bool {
	if e, ok := err.(*exec.Error); ok {
		*target = e
		return true
	}
	return false
}
