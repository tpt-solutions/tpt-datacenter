// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package control

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// Server exposes the control override API.
type Server struct {
	store     *Store
	addr      string
	authToken string
	cors      string
	http      *http.Server
}

// ServerConfig configures the control API server.
type ServerConfig struct {
	Addr      string
	Store     *Store
	AuthToken string
	// CORS is the allowed Origin header value ("*" to allow any). Empty disables CORS.
	CORS      string
}

// NewServer builds the control API server.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{store: cfg.Store, addr: cfg.Addr, authToken: cfg.AuthToken, cors: cfg.CORS}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/control/devices", s.handleDevices)
	mux.HandleFunc("/control/state", s.handleState)
	mux.HandleFunc("/control/override", s.handleOverride)
	mux.HandleFunc("/control/reset", s.handleReset)
	mux.HandleFunc("/control/latch", s.handleLatch)
	mux.HandleFunc("/control/audit", s.handleAudit)
	s.http = &http.Server{Addr: cfg.Addr, Handler: s.corsMiddleware(s.auth(mux))}
	return s
}

// auth wraps handlers with optional constant-time bearer-token checks.
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

// corsMiddleware echoes a configured allowed origin so the dashboard can call
// the API cross-origin during local development. In production the API sits
// behind the nginx reverse proxy (same origin) and CORS can be left empty.
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
func (s *Server) Start() error {
	log.Printf("[control-api] listening on %s", s.addr)
	return s.http.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"devices": s.store.Devices()})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	device := r.URL.Query().Get("device")
	if device == "" {
		http.Error(w, "device is required", http.StatusBadRequest)
		return
	}
	st := s.store.State(device)
	if st == nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleOverride(w http.ResponseWriter, r *http.Request) {
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
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.Device == "" || body.Command == "" {
		http.Error(w, "device and command are required", http.StatusBadRequest)
		return
	}
	st, clamped, err := s.store.Override(body.Device, Command(body.Command), body.Value, body.Operator, body.Reason)
	if err != nil {
		switch err {
		case ErrUnknownDevice:
			http.Error(w, err.Error(), http.StatusNotFound)
		case ErrLatchedSafe:
			http.Error(w, err.Error(), http.StatusConflict)
		case ErrUnknownCommand:
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "applied",
		"clamped":   clamped,
		"state":     st,
		"message":   clampedMsg(clamped, body.Command),
	})
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Device   string `json:"device"`
		Operator string `json:"operator"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Device == "" {
		http.Error(w, "device is required", http.StatusBadRequest)
		return
	}
	st, err := s.store.Reset(body.Device, body.Operator, body.Reason)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "reset", "state": st})
}

func (s *Server) handleLatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Device   string `json:"device"`
		Operator string `json:"operator"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Device == "" {
		http.Error(w, "device is required", http.StatusBadRequest)
		return
	}
	st, err := s.store.LatchSafe(body.Device, body.Operator, body.Reason)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "latched_safe", "state": st})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := parseInt(l); err == nil && n > 0 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.store.Audit(limit)})
}

func clampedMsg(clamped bool, cmd string) string {
	if clamped {
		return "value was clamped to the safety envelope for " + cmd
	}
	return "value applied within safe bounds"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseInt(s string) (int, error) {
	return strconv.Atoi(s)
}
