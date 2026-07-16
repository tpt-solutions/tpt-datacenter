// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command hardware runs the TPT DataCenter Hardware Management API: compute
// server power control (Redfish + IPMI), dynamic CPU power throttling tied to
// a grid-stress signal, and the live grid-stress status endpoint (todo.md
// Phase 7).
//
// The grid-stress source defaults to a static "none" stub; a real TPT
// Dynamo/Relay feed is injected by implementing hardware.GridStressSource and
// passing it via the manager. Everything is guarded by the same bearer-auth and
// audit envelope as the manual control API.
//
//	go run ./cmd/hardware -addr :8083 -token <secret> \
//	  -redfish-url https://bmc-1 -redfish-user admin -redfish-oem dell
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

	"github.com/TPT-Solutions/tpt-datacenter/api/internal/hardware"
)

func main() {
	var (
		addr      = flag.String("addr", ":8083", "hardware management API listen address")
		token     = flag.String("token", os.Getenv("HW_API_TOKEN"), "bearer token required on all endpoints (empty = no auth, dev only)")
		cors      = flag.String("cors", os.Getenv("HW_CORS_ORIGIN"), "allowed CORS origin ('*' to allow any; empty disables)")
		nameplate = flag.Float64("nameplate-w", 400.0, "per-server nameplate power (W) for throttle caps")
		critical  = flag.String("critical-shutdown", "", "comma-separated server IDs to shed under critical grid stress")
		insecure  = flag.Bool("insecure-no-auth", false, "explicitly allow running without -token on a non-loopback address (dev only)")
	)
	flag.Parse()
	requireAuthOrLoopback(*addr, *token, *insecure)

	rf, ok := hardware.LoadRedfishEndpoint()
	if !ok {
		log.Printf("[hardware] REDFISH_URL not set; Redfish endpoints will report unavailable")
	}

	ipmi := hardware.NewIPMIClient(
		os.Getenv("IPMI_HOST"),
		os.Getenv("IPMI_USER"),
		os.Getenv("IPMI_PASS"),
	)

	grid := hardware.NewGridStressMonitor(nil) // static "none" stub
	policy := hardware.DefaultThrottlePolicy(*nameplate, splitList(*critical))

	mgr := hardware.NewManager(rf, ipmi, grid, policy, hardware.NewAuditLog())
	srv := hardware.NewServer(hardware.ServerConfig{
		Addr: *addr, Manager: mgr, AuthToken: *token, CORS: *cors,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go grid.Run(ctx, 15*time.Second)
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("[hardware] server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("[hardware] shutdown error: %v", err)
	}
	log.Printf("[hardware] stopped")
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0)
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// requireAuthOrLoopback refuses to start unauthenticated on a non-loopback
// address: this API can power-cycle and throttle real compute hardware, so a
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
