// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command orchestrator runs the TPT DataCenter core platform orchestration
// service (todo.md Phase 8): routes control intents to edge HAL agents,
// enforces the safety/policy envelope, and audits every action. In Simulator
// mode it uses an in-memory sink; for Real mode the sink is swapped for one
// that forwards to rust-edge supervisors.
//
//	go run ./cmd/orchestrator -addr :8084 -token <secret> -two-person
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/api/internal/orchestration"
)

func main() {
	var (
		addr      = flag.String("addr", ":8084", "orchestration API listen address")
		token     = flag.String("token", os.Getenv("ORC_API_TOKEN"), "bearer token required on all endpoints (empty = no auth, dev only)")
		cors      = flag.String("cors", os.Getenv("ORC_CORS_ORIGIN"), "allowed CORS origin ('*' to allow any; empty disables)")
		twoPerson = flag.Bool("two-person", false, "require two-person ack for extreme valve/fan setpoints")
		insecure  = flag.Bool("insecure-no-auth", false, "explicitly allow running without -token on a non-loopback address (dev only)")
	)
	flag.Parse()
	requireAuthOrLoopback(*addr, *token, *insecure)

	policy := orchestration.DefaultPolicy()
	policy.RequireTwoPerson = *twoPerson

	sink := orchestration.NewSimSink()
	orc := orchestration.New(sink, policy)

	srv := orchestration.NewServer(orchestration.ServerConfig{
		Addr: *addr, Orc: orc, AuthToken: *token, CORS: *cors,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("[orchestrator] server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("[orchestrator] shutdown error: %v", err)
	}
	log.Printf("[orchestrator] stopped")
}

// requireAuthOrLoopback refuses to start unauthenticated on a non-loopback
// address: this API routes control commands to physical actuators, so a
// missed -token flag or unset env var must fail closed, not silently serve
// unauthenticated on an externally reachable interface.
func requireAuthOrLoopback(addr, token string, insecure bool) {
	if token != "" || insecure {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return
	}
	log.Fatalf("refusing to start: no -token set and -addr %q is not loopback-only; "+
		"set a bearer token or pass -insecure-no-auth to explicitly allow unauthenticated access (dev only)", addr)
}
