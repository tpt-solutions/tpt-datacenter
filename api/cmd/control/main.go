// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command control runs the TPT DataCenter manual override API: a small,
// safety-bounded, audited surface that lets operators drive facility actuators
// (cooling valve, fan, PDU outlet, UPS discharge limit) within the same
// physical envelope the rust-edge supervisors enforce.
//
// In this phase actuator state is held in memory and seeded from the facility
// topology spec. The seam for routing commands to real edge HAL agents is the
// future orchestration service (todo.md Phase 8).
//
//	go run ./cmd/control -addr :8082 -token <secret> -spec deploy/topology/facility.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/api/internal/control"
)

// facilitySpec is the minimal subset of the topology spec we need to seed
// actuator state (device ids + kinds).
type facilitySpec struct {
	Nodes []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"nodes"`
}

func main() {
	var (
		addr      = flag.String("addr", ":8082", "control API listen address")
		token     = flag.String("token", os.Getenv("CONTROL_API_TOKEN"), "bearer token required on all endpoints (empty = no auth, dev only)")
		cors      = flag.String("cors", os.Getenv("CONTROL_CORS_ORIGIN"), "allowed CORS origin ('*' to allow any; empty disables)")
		specPath  = flag.String("spec", "deploy/topology/facility.json", "facility topology spec used to seed devices")
	)
	flag.Parse()

	store := control.NewStore(control.DefaultLimits())
	if err := seed(store, *specPath); err != nil {
		log.Printf("[control] warning: could not seed from %s: %v (continuing with no devices)", *specPath, err)
	}

	srv := control.NewServer(control.ServerConfig{
		Addr:      *addr,
		Store:     store,
		AuthToken: *token,
		CORS:      *cors,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("[control] server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("[control] shutdown error: %v", err)
	}
	log.Printf("[control] stopped")
}

// seed installs an initial auto state for every rack, PDU, UPS and cooling loop
// found in the topology spec.
func seed(store *control.Store, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var spec facilitySpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return err
	}
	count := 0
	for _, n := range spec.Nodes {
		switch n.Kind {
		case "rack", "pdu", "ups", "cooling_loop":
			// Sensible default actuator state for a running facility.
			store.Seed(n.ID, 50.0, 50.0, true, 50.0)
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("no rack/pdu/ups/cooling_loop nodes found in %s", path)
	}
	log.Printf("[control] seeded %d devices from %s", count, path)
	return nil
}
