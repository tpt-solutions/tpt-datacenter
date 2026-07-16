// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package orchestration

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// SimSink is an in-memory HalCommandSink used for local Simulator-mode
// bring-up and tests. It enforces the same numeric envelope as the orchestrator
// and tracks the latest actuator state per device. A real deployment replaces
// it with a sink that forwards to rust-edge supervisors over the wire.
type SimSink struct {
	mu    sync.RWMutex
	state map[string]*ActuatorState
}

// NewSimSink builds an empty simulator sink.
func NewSimSink() *SimSink {
	return &SimSink{state: make(map[string]*ActuatorState)}
}

// Apply stores and returns the resulting actuator state for a device.
func (s *SimSink) Apply(_ context.Context, device string, cmd Command, value any) (*ActuatorState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state[device]
	if st == nil {
		st = &ActuatorState{Device: device, Mode: "auto"}
	}
	switch cmd {
	case CmdValve:
		st.Valve = toF(value)
	case CmdFan:
		st.Fan = toF(value)
	case CmdDischarge:
		st.Discharge = toF(value)
	case CmdOutlet:
		st.Outlet = value.(bool)
	}
	st.UpdatedAt = timeNow()
	s.state[device] = st
	c := *st
	return &c, nil
}

// State returns the current simulated state of a device (nil if unknown).
func (s *SimSink) State(device string) *ActuatorState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.state[device]
	if !ok {
		return nil
	}
	c := *st
	return &c
}

func toF(v any) float64 {
	f, _ := toFloat(v)
	return f
}

// Server exposes the orchestration endpoints over HTTP.
type Server struct {
	orc       *Orchestrator
	addr      string
	authToken string
	cors      string
	http      *http.Server
}

// ServerConfig configures the orchestration server.
type ServerConfig struct {
	Addr      string
	Orc       *Orchestrator
	AuthToken string
	CORS      string
}

// NewServer builds the orchestration API server.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{orc: cfg.Orc, addr: cfg.Addr, authToken: cfg.AuthToken, cors: cfg.CORS}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/orc/submit", s.handleSubmit)
	mux.HandleFunc("/orc/policy", s.handlePolicy)
	mux.HandleFunc("/orc/audit", s.handleAudit)
	s.http = &http.Server{Addr: cfg.Addr, Handler: s.corsMiddleware(s.auth(mux))}
	return s
}

func (s *Server) auth(next http.Handler) http.Handler {
	if s.authToken == "" {
		return next
	}
	want := []byte(s.authToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	if s.cors == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.cors)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Start blocks serving.
func (s *Server) Start() error { return s.http.ListenAndServe() }

// Handler returns the HTTP handler (useful for tests).
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Device   string `json:"device"`
		Command  string `json:"command"`
		Value    any    `json:"value"`
		Operator string `json:"operator"`
		Reason   string `json:"reason"`
		Source   string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Device == "" || body.Command == "" {
		http.Error(w, "device and command are required", http.StatusBadRequest)
		return
	}
	resp, err := s.orc.Submit(r.Context(), SubmitRequest{
		Device: body.Device, Command: Command(body.Command), Value: body.Value,
		Operator: body.Operator, Reason: body.Reason, Source: body.Source,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	p := s.orc.Policy()
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := parseInt(l); err == nil && n > 0 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.orc.Audit(limit)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseInt(s string) (int, error) {
	return strconvAtoi(s)
}
