// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command topology runs the physical topology graph service. It loads the
// manual topology spec, optionally enriches it from the telemetry device
// registry, and serves relationship queries for the dashboard and AI brain.
//
//	go run ./cmd/topology -spec deploy/topology/facility.json -addr :8081
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

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/questdb"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/topology"
)

func main() {
	var (
		addr       = flag.String("addr", ":8081", "topology API listen address")
		spec       = flag.String("spec", "", "path to topology JSON spec (manual authoring)")
		questdbURL = flag.String("questdb", "", "QuestDB REST URL for telemetry auto-discovery")
		discover   = flag.Bool("discover", false, "enrich graph from the telemetry device registry on startup")
		token      = flag.String("token", os.Getenv("TOPOLOGY_API_TOKEN"), "bearer token for all endpoints (empty = no auth)")
		insecure   = flag.Bool("insecure-no-auth", false, "explicitly allow running without -token on a non-loopback address (dev only)")
	)
	flag.Parse()
	requireAuthOrLoopback(*addr, *token, *insecure)

	g := topology.NewGraph()
	if *spec != "" {
		loaded, err := topology.LoadSpec(*spec)
		if err != nil {
			log.Fatalf("load spec: %v", err)
		}
		g = loaded
		log.Printf("[topology] loaded %d nodes from %s", len(g.Nodes()), *spec)
	}

	if *discover && *questdbURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		client := questdb.NewClient(*questdbURL)
		if err := g.DiscoverFromTelemetry(ctx, client); err != nil {
			log.Printf("[topology] discovery error: %v", err)
		} else {
			log.Printf("[topology] after discovery: %d nodes", len(g.Nodes()))
		}
	}

	srv := topology.NewServer(topology.ServerConfig{Addr: *addr, Graph: g, AuthToken: *token})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("[topology] server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("[topology] shutdown error: %v", err)
	}
	log.Printf("[topology] stopped")
}

// requireAuthOrLoopback refuses to start unauthenticated on a non-loopback
// address: a missed -token flag or unset env var must fail closed, not
// silently serve the facility topology unauthenticated to anyone who can
// reach the port.
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
