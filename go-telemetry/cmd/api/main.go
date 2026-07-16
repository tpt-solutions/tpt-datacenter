// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command api runs the QuestDB query API used by the dashboard and the AI
// brain. It can also apply the schema and run the retention/downsampling
// maintenance plan (one-shot or on a schedule).
//
//	go run ./cmd/api -addr :8080 -questdb http://localhost:9000 -apply-schema
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
)

func main() {
	var (
		addr         = flag.String("addr", ":8080", "query API listen address")
		questdbURL   = flag.String("questdb", "http://localhost:9000", "QuestDB REST base URL")
		token        = flag.String("token", os.Getenv("QUESTDB_API_TOKEN"), "bearer token required on all endpoints (empty = no auth)")
		applySchema  = flag.Bool("apply-schema", false, "apply the table schema on startup and exit")
		maintenance  = flag.Bool("maintenance", false, "run the retention/downsampling plan once and exit")
		maintainLoop = flag.Duration("maintain-every", 0, "run maintenance on this interval (0 disables)")
		rawKeep      = flag.String("raw-retention", "dateadd('d', -90, now())", "raw `readings` retention window")
		rollupKeep   = flag.String("rollup-retention", "dateadd('y', -2, now())", "rollup retention window")
		insecure     = flag.Bool("insecure-no-auth", false, "explicitly allow running without -token on a non-loopback address (dev only)")
	)
	flag.Parse()

	client := questdb.NewClient(*questdbURL)

	if *applySchema {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Apply(ctx); err != nil {
			log.Fatalf("apply schema: %v", err)
		}
		log.Printf("[api] schema applied")
		if !*maintenance && *maintainLoop == 0 {
			return
		}
	}

	if *maintenance {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := client.RunMaintenance(ctx, *rawKeep, *rollupKeep); err != nil {
			log.Fatalf("maintenance: %v", err)
		}
		log.Printf("[api] maintenance complete")
		if *maintainLoop == 0 {
			return
		}
	}

	if *maintainLoop > 0 {
		go func() {
			t := time.NewTicker(*maintainLoop)
			defer t.Stop()
			for range t.C {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				if err := client.RunMaintenance(ctx, *rawKeep, *rollupKeep); err != nil {
					log.Printf("[api] maintenance error: %v", err)
				} else {
					log.Printf("[api] maintenance ran")
				}
				cancel()
			}
		}()
	}

	requireAuthOrLoopback(*addr, *token, *insecure)

	srv := questdb.NewServer(questdb.ServerConfig{
		Addr:      *addr,
		Client:    client,
		AuthToken: *token,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("[api] server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("[api] shutdown error: %v", err)
	}
	log.Printf("[api] stopped")
}

// requireAuthOrLoopback refuses to start unauthenticated on a non-loopback
// address: a missed -token flag or unset env var must fail closed, not
// silently serve telemetry (and the ad-hoc SQL endpoint) unauthenticated to
// anyone who can reach the port.
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
