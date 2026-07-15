// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package collector defines the telemetry collector interface and concrete
// collectors for the protocols the physical plant exposes: Redfish, Modbus
// TCP and IPMI. Collectors are poll-based: the pipeline drives Poll on a
// fixed interval and forwards the resulting points into the ingest bus.
package collector

import (
	"context"
	"errors"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
)

// ErrNotImplemented is returned by a collector for a transport capability that
// is wired up but not yet fully implemented (e.g. in-process IPMI RAKP
// sessions). It mirrors the Rust HAL's HalError::NotImplemented stance.
var ErrNotImplemented = errors.New("collector: not implemented")

// Collector is a single telemetry source. Implementations are safe to call
// Poll concurrently from multiple goroutines, though the pipeline drives each
// collector from a single goroutine.
type Collector interface {
	// Name uniquely identifies the collector (used for metrics/backpressure).
	Name() string
	// Poll fetches one batch of samples. It must respect ctx cancellation and
	// return quickly; long polls should select on ctx.Done().
	Poll(ctx context.Context) ([]point.DataPoint, error)
}
