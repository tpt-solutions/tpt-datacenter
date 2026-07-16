// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package topology

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// Server exposes the topology graph over HTTP for the dashboard and the AI
// brain. It answers relationship queries ("what cools this rack", "what feeds
// this PDU") and accepts graph edits.
type Server struct {
	graph     *Graph
	addr      string
	authToken string
	http      *http.Server
}

// ServerConfig configures the topology API server.
type ServerConfig struct {
	Addr      string
	Graph     *Graph
	AuthToken string
}

// NewServer builds the topology API server.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{graph: cfg.Graph, addr: cfg.Addr, authToken: cfg.AuthToken}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/topology/node/", s.handleNode)
	mux.HandleFunc("/topology/cools", s.handleCools)
	mux.HandleFunc("/topology/feeds", s.handleFeeds)
	mux.HandleFunc("/topology/powers", s.handlePowers)
	mux.HandleFunc("/topology/cooling", s.handleCooling)
	mux.HandleFunc("/topology/graph", s.handleGraph)
	mux.HandleFunc("/topology/upsert", s.handleUpsert)
	s.http = &http.Server{Addr: cfg.Addr, Handler: s.auth(mux)}
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

// Start blocks serving.
func (s *Server) Start() error {
	log.Printf("[topology-api] listening on %s", s.addr)
	return s.http.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/topology/node/")
	n, ok := s.graph.Node(id)
	if !ok {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleCools(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "cools": s.graph.Cools(id)})
}

func (s *Server) handleFeeds(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "feeds": s.graph.Feeds(id)})
}

func (s *Server) handlePowers(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "powers": s.graph.PowersChain(id)})
}

func (s *Server) handleCooling(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "cooling": s.graph.CoolingChain(id)})
}

func (s *Server) handleGraph(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.graph.Spec())
}

// handleUpsert accepts {"node": {...}} or {"edge": {...}}.
func (s *Server) handleUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Node *Node `json:"node"`
		Edge *Edge `json:"edge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	switch {
	case body.Node != nil:
		if err := s.graph.UpsertNode(*body.Node); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "node upserted"})
	case body.Edge != nil:
		if err := s.graph.UpsertEdge(*body.Edge); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "edge upserted"})
	default:
		http.Error(w, "expected node or edge", http.StatusBadRequest)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
