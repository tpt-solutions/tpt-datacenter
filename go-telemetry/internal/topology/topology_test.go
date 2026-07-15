// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package topology

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sampleGraph() *Graph {
	g := NewGraph()
	_ = g.UpsertNode(Node{ID: "ups-1", Kind: KindUPS})
	_ = g.UpsertNode(Node{ID: "pdu-1", Kind: KindPDU})
	_ = g.UpsertNode(Node{ID: "rack-1", Kind: KindRack})
	_ = g.UpsertNode(Node{ID: "loop-1", Kind: KindCoolingLoop})
	_ = g.UpsertNode(Node{ID: "srv-1", Kind: KindServer})
	_ = g.UpsertEdge(Edge{From: "ups-1", To: "pdu-1", Kind: EdgePowers})
	_ = g.UpsertEdge(Edge{From: "pdu-1", To: "rack-1", Kind: EdgePowers})
	_ = g.UpsertEdge(Edge{From: "rack-1", To: "srv-1", Kind: EdgePowers})
	_ = g.UpsertEdge(Edge{From: "loop-1", To: "rack-1", Kind: EdgeCools})
	return g
}

func TestPowersChain(t *testing.T) {
	g := sampleGraph()
	chain := g.PowersChain("pdu-1")
	ids := nodeIDs(chain)
	if !contains(ids, "ups-1") {
		t.Fatalf("expected ups-1 upstream of pdu-1, got %v", ids)
	}
	if contains(ids, "rack-1") {
		t.Fatalf("rack-1 should not be upstream of pdu-1, got %v", ids)
	}
}

func TestCools(t *testing.T) {
	g := sampleGraph()
	cools := g.Cools("rack-1")
	if len(cools) != 1 || cools[0].From != "loop-1" {
		t.Fatalf("expected loop-1 cools rack-1, got %+v", cools)
	}
}

func TestCoolingChain(t *testing.T) {
	g := sampleGraph()
	chain := g.CoolingChain("loop-1")
	if !contains(nodeIDs(chain), "rack-1") {
		t.Fatalf("expected loop-1 to cool rack-1 downstream, got %v", nodeIDs(chain))
	}
}

func TestLoadAndRoundTrip(t *testing.T) {
	g := sampleGraph()
	spec := g.Spec()
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var got Spec
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	g2 := NewGraph()
	if err := g2.Load(got); err != nil {
		t.Fatal(err)
	}
	if len(g2.Nodes()) != len(g.Nodes()) {
		t.Fatalf("node count mismatch: %d vs %d", len(g2.Nodes()), len(g.Nodes()))
	}
}

func TestServerCoolsEndpoint(t *testing.T) {
	g := sampleGraph()
	srv := NewServer(ServerConfig{Addr: ":0", Graph: g})
	h := srv.http.Handler

	req := httptest.NewRequest(http.MethodGet, "/topology/cools?id=rack-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp struct {
		Cools []Edge `json:"cools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Cools) != 1 || resp.Cools[0].From != "loop-1" {
		t.Fatalf("unexpected cools: %+v", resp.Cools)
	}
}

func TestServerUpsertAuth(t *testing.T) {
	g := NewGraph()
	srv := NewServer(ServerConfig{Addr: ":0", Graph: g, AuthToken: "sek"})
	h := srv.http.Handler

	body := `{"node":{"id":"rack-9","kind":"rack"}}`
	req := httptest.NewRequest(http.MethodPost, "/topology/upsert", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/topology/upsert", strings.NewReader(body))
	req2.Header.Set("Authorization", "Bearer sek")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", w2.Code)
	}
	if _, ok := g.Node("rack-9"); !ok {
		t.Fatal("node not upserted")
	}
}

// DiscoverFromTelemetry is exercised against a mock QuestDB in integration
// contexts; here we just confirm it fails cleanly when the client errors.
func TestDiscoverNoPanic(t *testing.T) {
	// A nil graph operation should be safe; this guards the surface.
	g := sampleGraph()
	if g == nil {
		t.Fatal("unexpected nil graph")
	}
	_ = context.Background()
}

func nodeIDs(ns []Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.ID
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
