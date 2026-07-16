// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package hardware — see redfish.go. This file exposes the Hardware Management
// API HTTP surface (todo.md Phase 7): compute-server power control (Redfish +
// IPMI), CPU power throttling tied to the grid-stress signal, and the live
// grid-stress status endpoint. It reuses the same bearer-auth and CORS
// conventions as api/internal/control.

package hardware

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Manager wires together the southbound clients and the grid-stress monitor.
type Manager struct {
	redfish *RedfishClient
	ipmi    *IPMIClient
	grid    *GridStressMonitor
	policy  ThrottlePolicy
	audit   AuditSink
}

// AuditSink records control actions. The control.Store audit log is the
// natural implementation; we take a minimal interface to avoid an import cycle.
type AuditSink interface {
	Record(entry AuditEntry)
}

// AuditEntry is a control-action audit record.
type AuditEntry struct {
	ID       string    `json:"id,omitempty"`
	TS       time.Time `json:"ts"`
	Domain   string    `json:"domain"` // "hardware"
	Server   string    `json:"server"`
	Action   string    `json:"action"`
	Detail   any       `json:"detail,omitempty"`
	Operator string    `json:"operator,omitempty"`
	Note     string    `json:"note,omitempty"`
}

// NewManager builds a hardware manager. Any client may be nil (the matching
// endpoints then report unavailable rather than crashing).
func NewManager(rf *RedfishClient, ipmi *IPMIClient, grid *GridStressMonitor, policy ThrottlePolicy, audit AuditSink) *Manager {
	return &Manager{redfish: rf, ipmi: ipmi, grid: grid, policy: policy, audit: audit}
}

// Server is the Hardware Management API HTTP server.
type Server struct {
	mgr       *Manager
	addr      string
	authToken string
	cors      string
	http      *http.Server
}

// ServerConfig configures the hardware management API server.
type ServerConfig struct {
	Addr      string
	Manager   *Manager
	AuthToken string
	CORS      string
}

// NewServer builds the hardware management API server.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{mgr: cfg.Manager, addr: cfg.Addr, authToken: cfg.AuthToken, cors: cfg.CORS}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/hw/grid", s.handleGrid)
	mux.HandleFunc("/hw/power", s.handlePower)
	mux.HandleFunc("/hw/boot", s.handleBoot)
	mux.HandleFunc("/hw/powercap", s.handlePowerCap)
	mux.HandleFunc("/hw/throttle/resolve", s.handleThrottleResolve)
	mux.HandleFunc("/hw/audit", s.handleAudit)
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
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
	log.Printf("[hardware-api] listening on %s", s.addr)
	return s.http.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGrid(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.grid.Latest())
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Server   string `json:"server"`
		SystemID string `json:"system_id"`
		Action   string `json:"action"` // On/Off/GracefulShutdown/GracefulRestart/ForceOff/ForceRestart/PushPowerButton
		Via      string `json:"via"`    // "redfish" | "ipmi" | "" (auto)
		Operator string `json:"operator"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.Server == "" {
		http.Error(w, "server is required", http.StatusBadRequest)
		return
	}
	system := body.SystemID
	if system == "" {
		system = body.Server
	}

	var err error
	switch {
	case body.Via == "ipmi" || (body.Via == "" && s.mgr.redfish == nil && s.mgr.ipmi != nil):
		if s.mgr.ipmi == nil {
			http.Error(w, "ipmi backend unavailable", http.StatusServiceUnavailable)
			return
		}
		err = s.mgr.ipmi.PowerControl(r.Context(), PowerAction(body.Action))
	case body.Via == "redfish" || (body.Via == "" && s.mgr.redfish != nil):
		if s.mgr.redfish == nil {
			http.Error(w, "redfish backend unavailable", http.StatusServiceUnavailable)
			return
		}
		err = s.mgr.redfish.SetSystemPower(r.Context(), system, body.Action)
	default:
		http.Error(w, "no backend configured", http.StatusServiceUnavailable)
		return
	}

	s.record(body.Server, "power:"+body.Action, map[string]any{"via": body.Via}, body.Operator, body.Note)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "server": body.Server, "action": body.Action})
}

func (s *Server) handleBoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Server   string `json:"server"`
		SystemID string `json:"system_id"`
		Target   string `json:"target"` // Pxe, Hdd, Bios, Cd, UsbFloppy, ...
		Operator string `json:"operator"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Server == "" || body.Target == "" {
		http.Error(w, "server and target are required", http.StatusBadRequest)
		return
	}
	system := body.SystemID
	if system == "" {
		system = body.Server
	}
	if s.mgr.redfish == nil {
		http.Error(w, "redfish backend unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.mgr.redfish.BootOverride(r.Context(), system, body.Target); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.record(body.Server, "boot:"+body.Target, nil, body.Operator, body.Note)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "server": body.Server, "target": body.Target})
}

func (s *Server) handlePowerCap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Server    string  `json:"server"`
		ChassisID string  `json:"chassis_id"`
		CapW      float64 `json:"cap_w"`
		Operator  string  `json:"operator"`
		Note      string  `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Server == "" {
		http.Error(w, "server is required", http.StatusBadRequest)
		return
	}
	chassis := body.ChassisID
	if chassis == "" {
		chassis = body.Server
	}
	if s.mgr.redfish == nil {
		http.Error(w, "redfish backend unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.mgr.redfish.SetCPUPowerCap(r.Context(), chassis, CPUPowerLimit{PowerWatts: body.CapW}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.record(body.Server, "powercap", map[string]any{"cap_w": body.CapW}, body.Operator, body.Note)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "server": body.Server, "cap_w": body.CapW})
}

// handleThrottleResolve returns the per-server throttle action implied by the
// current grid-stress signal and the configured policy. It does not perform
// any I/O against hardware; it is the decision surface the orchestrator would
// act on, and is exposed so operators can preview what a signal would do.
func (s *Server) handleThrottleResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Servers []string `json:"servers"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if len(body.Servers) == 0 {
		http.Error(w, "servers is required", http.StatusBadRequest)
		return
	}
	sig := s.mgr.grid.Latest()
	acts := make([]ThrottleAction, 0, len(body.Servers))
	for _, srv := range body.Servers {
		acts = append(acts, s.mgr.policy.Resolve(srv, sig))
	}
	writeJSON(w, http.StatusOK, map[string]any{"signal": sig, "actions": acts})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if s.mgr.audit == nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []AuditEntry{}})
		return
	}
	log, ok := s.mgr.audit.(*AuditLog)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []AuditEntry{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": log.Recent(limit)})
}

func (s *Server) record(server, action string, detail any, operator, note string) {
	if s.mgr.audit == nil {
		return
	}
	s.mgr.audit.Record(AuditEntry{
		TS: time.Now().UTC(), Domain: "hardware", Server: server,
		Action: action, Detail: detail, Operator: operator, Note: note,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
