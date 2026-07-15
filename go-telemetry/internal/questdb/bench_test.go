// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package questdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// largeResult builds an /exec response with n rows to simulate a heavy query.
func largeResult(n int) string {
	var b strings.Builder
	b.WriteString(`{"query":"","columns":[{"name":"ts","type":"TIMESTAMP"},{"name":"value","type":"DOUBLE"}],"dataset":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(fmt.Sprintf(`["2024-01-01T00:00:00.000000Z",%f]`, float64(i)))
	}
	b.WriteString(fmt.Sprintf(`],"count":%d}`, n))
	return b.String()
}

// BenchmarkClientExec measures the client's JSON decode + cell conversion
// throughput — the per-query overhead the API server adds on top of QuestDB.
// End-to-end DB latency must be measured against a live instance (see
// deploy/questdb); this isolates the Go layer.
func BenchmarkClientExec(b *testing.B) {
	const rows = 10_000
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(largeResult(rows)))
	}))
	defer ts.Close()
	c := NewClient(ts.URL)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := c.Exec(context.Background(), "select ts, value from readings")
		if err != nil {
			b.Fatal(err)
		}
		if res.Count != rows {
			b.Fatalf("got %d rows", res.Count)
		}
	}
}

// BenchmarkServerTimeseries measures request handling throughput for the
// high-level timeseries endpoint (SQL build + decode + re-encode).
func BenchmarkServerTimeseries(b *testing.B) {
	const rows = 10_000
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(largeResult(rows)))
	}))
	defer ts.Close()
	c := NewClient(ts.URL)
	srv := NewServer(ServerConfig{Addr: ":0", Client: c})
	h := srv.Handler()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet,
			"/api/timeseries?device=rack-01&sensor=chan-0&metric=temperature&from=0&to=1", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status %d", w.Code)
		}
		_ = json.NewDecoder(w.Body).Decode(&struct {
			Points []SeriesPoint `json:"points"`
		}{})
	}
}
