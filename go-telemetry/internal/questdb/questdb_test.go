// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package questdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockServer returns a QuestDB REST mock that answers /status and /exec.
// execBody is the JSON returned for every /exec query.
func mockServer(t *testing.T, execBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"up"}`))
		case r.URL.Path == "/exec":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(execBody))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestExecOK(t *testing.T) {
	body := `{"query":"select 1","columns":[{"name":"ts","type":"TIMESTAMP"}],"dataset":[["2024-01-01T00:00:00.000000Z",1.5]],"count":1}`
	ts := mockServer(t, body)
	defer ts.Close()

	c := NewClient(ts.URL)
	res, err := c.Exec(context.Background(), "select 1")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Count != 1 || len(res.Dataset) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestExecError(t *testing.T) {
	ts := mockServer(t, `{"query":"x","error":"table does not exist"}`)
	defer ts.Close()
	c := NewClient(ts.URL)
	if _, err := c.Exec(context.Background(), "x"); err == nil {
		t.Fatal("expected error from error field")
	}
}

func TestPing(t *testing.T) {
	ts := mockServer(t, `{}`)
	defer ts.Close()
	c := NewClient(ts.URL)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestDownsampleAndRetentionSQL(t *testing.T) {
	stmt := DownsampleStmt("1m", "readings", "readings_1m", "dateadd('d', -2, now())", "dateadd('d', -1, now())")
	for _, want := range []string{"INSERT INTO readings_1m", "FROM readings", "SAMPLE BY 1m FILL(NULL)", "avg(value)"} {
		if !strings.Contains(stmt, want) {
			t.Errorf("downsample stmt missing %q:\n%s", want, stmt)
		}
	}
	drop := RetentionDropStmt("readings", "dateadd('d', -90, now())")
	if !strings.Contains(drop, "ALTER TABLE readings DROP PARTITION WHERE ts < dateadd('d', -90, now())") {
		t.Errorf("unexpected drop stmt: %s", drop)
	}
	plan := MaintenancePlan("dateadd('d', -90, now())", "dateadd('y', -2, now())")
	if len(plan) != 4 {
		t.Fatalf("expected 4 maintenance statements, got %d", len(plan))
	}
}

func TestApplySchema(t *testing.T) {
	ts := mockServer(t, `{"query":"","columns":[],"dataset":[],"count":0}`)
	defer ts.Close()
	c := NewClient(ts.URL)
	if err := c.Apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func TestServerLatest(t *testing.T) {
	body := `{"query":"","columns":[{"name":"ts","type":"TIMESTAMP"},{"name":"value","type":"DOUBLE"},{"name":"unit","type":"SYMBOL"}],"dataset":[["2024-01-01T00:00:00.000000Z",30.5,"celsius"]],"count":1}`
	ts := mockServer(t, body)
	defer ts.Close()

	c := NewClient(ts.URL)
	srv := NewServer(ServerConfig{Addr: ":0", Client: c})
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/latest?device=rack-01&metric=temperature", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var lv Latest
	if err := json.Unmarshal(w.Body.Bytes(), &lv); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if lv.Value != 30.5 || lv.Unit != "celsius" {
		t.Fatalf("unexpected latest: %+v", lv)
	}
}

func TestServerAuthRequired(t *testing.T) {
	ts := mockServer(t, `{}`)
	defer ts.Close()
	c := NewClient(ts.URL)
	srv := NewServer(ServerConfig{Addr: ":0", Client: c, AuthToken: "secret"})
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req2.Header.Set("Authorization", "Bearer secret")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", w2.Code)
	}
}
