// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command dashboard serves the TPT DataCenter web dashboard (todo.md Phase 9):
// a self-contained single-page app plus a thin reverse proxy that fans API
// calls out to the control, hardware, telemetry (QuestDB query), topology, and
// orchestrator services. Running everything behind this one origin keeps the
// deployment CORS-free and lets the SPA use relative paths.
//
//	go run ./cmd/dashboard -addr :8085 \
//	  -control :8082 -hardware :8083 -telemetry :8080 \
//	  -topology :8081 -orchestrator :8084
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var (
		addr         = flag.String("addr", ":8085", "dashboard listen address")
		staticDir    = flag.String("static", defaultStatic(), "directory served as the dashboard SPA")
		control      = flag.String("control", envOr("CONTROL_ADDR", "http://localhost:8082"), "control API upstream")
		hardware     = flag.String("hardware", envOr("HARDWARE_ADDR", "http://localhost:8083"), "hardware API upstream")
		telemetry    = flag.String("telemetry", envOr("TELEMETRY_ADDR", "http://localhost:8080"), "telemetry/QuestDB query upstream")
		topology     = flag.String("topology", envOr("TOPOLOGY_ADDR", "http://localhost:8081"), "topology API upstream")
		orchestrator = flag.String("orchestrator", envOr("ORCHESTRATOR_ADDR", "http://localhost:8084"), "orchestrator upstream")
	)
	flag.Parse()

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(*staticDir)))
	mux.Handle("/control/", proxy(*control))
	mux.Handle("/hw/", proxy(*hardware))
	mux.Handle("/api/", proxy(*telemetry))
	mux.Handle("/topology/", proxy(*topology))
	mux.Handle("/orc/", proxy(*orchestrator))

	// Standard browser hardening headers for the dashboard origin.
	handler := secureHeaders(mux)

	srv := &http.Server{Addr: *addr, Handler: handler}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("[dashboard] listening on %s (static=%s)", *addr, *staticDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[dashboard] server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
	log.Printf("[dashboard] stopped")
}

// secureHeaders applies baseline browser hardening to every response served by
// the dashboard origin (the SPA and its same-origin API proxies).
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func proxy(target string) http.Handler {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("bad upstream %q: %v", target, err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		orig(r)
		r.Host = u.Host
	}
	return rp
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func defaultStatic() string {
	// Best-effort: <repo>/dashboard/static relative to CWD.
	return "static"
}
