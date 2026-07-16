// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package questdb

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Server exposes a small query API over QuestDB for the dashboard and the AI
// brain. It is intentionally a thin, safe proxy: ad-hoc SQL is accepted only
// on the authenticated /api/query endpoint, while the high-level endpoints
// build parameterized, injection-safe statements.
type Server struct {
	addr      string
	client    *Client
	authToken string
	http      *http.Server
}

// ServerConfig configures the query API server.
type ServerConfig struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// Client is the QuestDB REST client.
	Client *Client
	// AuthToken, if set, requires "Authorization: Bearer <token>" on all
	// endpoints. Leave empty to disable auth (dev only).
	AuthToken string
}

// NewServer builds a query API server.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{addr: cfg.Addr, client: cfg.Client, authToken: cfg.AuthToken}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/query", s.handleQuery)
	mux.HandleFunc("/api/latest", s.handleLatest)
	mux.HandleFunc("/api/timeseries", s.handleTimeseries)
	mux.HandleFunc("/api/devices", s.handleDevices)
	s.http = &http.Server{Addr: cfg.Addr, Handler: s.auth(mux)}
	return s
}

// auth wraps handlers with optional bearer-token checks.
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

// Start begins serving (blocks until the server stops).
func (s *Server) Start() error {
	log.Printf("[questdb-api] listening on %s", s.addr)
	return s.http.ListenAndServe()
}

// Handler returns the HTTP handler (useful for tests).
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.client.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "down", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleQuery proxies an ad-hoc SQL statement. Body: {"sql": "..."} or a
// "sql" form value.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SQL == "" {
		req.SQL = r.FormValue("sql")
	}
	if req.SQL == "" {
		http.Error(w, "missing sql", http.StatusBadRequest)
		return
	}
	res, err := s.client.Exec(r.Context(), req.SQL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	device, sensor, metric := q.Get("device"), q.Get("sensor"), q.Get("metric")
	if device == "" || metric == "" {
		http.Error(w, "device and metric are required", http.StatusBadRequest)
		return
	}
	sql := fmt.Sprintf(
		`SELECT ts, value, unit FROM readings
		 WHERE device = '%s' AND sensor = '%s' AND metric = '%s'
		 LATEST ON ts PARTITION BY device, sensor, metric`,
		escapeSQL(device), escapeSQL(sensor), escapeSQL(metric))
	res, err := s.client.Exec(r.Context(), sql)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	lv, err := latestFrom(res)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, lv)
}

func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	device, sensor, metric := q.Get("device"), q.Get("sensor"), q.Get("metric")
	if device == "" || metric == "" {
		http.Error(w, "device and metric are required", http.StatusBadRequest)
		return
	}
	from, to, err := parseWindow(q.Get("from"), q.Get("to"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	table := "readings"
	col := "value"
	if roll := q.Get("rollup"); roll == "1m" || roll == "1h" {
		table = "readings_" + roll
		col = "avg"
	}
	sql := fmt.Sprintf(
		`SELECT ts, %s FROM %s
		 WHERE device = '%s' AND sensor = '%s' AND metric = '%s'
		   AND ts >= to_timestamp(%d, 'ns') AND ts < to_timestamp(%d, 'ns')
		 ORDER BY ts`,
		col, table, escapeSQL(device), escapeSQL(sensor), escapeSQL(metric), from, to)
	res, err := s.client.Exec(r.Context(), sql)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	pts, err := seriesFrom(res)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": device, "sensor": sensor, "metric": metric, "points": pts})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	res, err := s.client.Exec(r.Context(),
		`SELECT device, kind, last_seen FROM devices LATEST ON last_seen PARTITION BY device`)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- response helpers ---

// parseWindow resolves from/to query params into [fromNs, toNs] unix
// nanoseconds, defaulting to the last 24h when a bound is omitted. Values may
// be RFC3339 or numeric epochs (auto-detected: seconds/ms/µs/ns).
func parseWindow(from, to string) (int64, int64, error) {
	now := time.Now().UnixNano()
	f, t := now-int64(24*time.Hour), now
	var err error
	if from != "" {
		if f, err = parseTimeArg(from); err != nil {
			return 0, 0, err
		}
	}
	if to != "" {
		if t, err = parseTimeArg(to); err != nil {
			return 0, 0, err
		}
	}
	return f, t, nil
}

func parseTimeArg(s string) (int64, error) {
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm.UnixNano(), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	switch {
	case n < 1e12: // seconds
		return n * 1e9, nil
	case n < 1e15: // milliseconds
		return n * 1e6, nil
	case n < 1e18: // microseconds
		return n * 1e3, nil
	default: // nanoseconds
		return n, nil
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type SeriesPoint struct {
	TS    int64   `json:"ts"` // unix nanoseconds
	Value float64 `json:"value"`
}

type Latest struct {
	Device string  `json:"device"`
	Sensor string  `json:"sensor"`
	Metric string  `json:"metric"`
	TS     int64   `json:"ts"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
}

func seriesFrom(res *Result) ([]SeriesPoint, error) {
	if len(res.Columns) < 2 {
		return nil, fmt.Errorf("expected >=2 columns, got %d", len(res.Columns))
	}
	out := make([]SeriesPoint, 0, len(res.Dataset))
	for _, row := range res.Dataset {
		if len(row) < 2 {
			continue
		}
		ts, err := toNanos(row[0])
		if err != nil {
			return nil, err
		}
		v, err := toFloat(row[1])
		if err != nil {
			return nil, err
		}
		out = append(out, SeriesPoint{TS: ts, Value: v})
	}
	return out, nil
}

func latestFrom(res *Result) (*Latest, error) {
	if len(res.Dataset) == 0 {
		return nil, fmt.Errorf("no data")
	}
	row := res.Dataset[0]
	if len(row) < 3 {
		return nil, fmt.Errorf("unexpected row width %d", len(row))
	}
	ts, err := toNanos(row[0])
	if err != nil {
		return nil, err
	}
	v, err := toFloat(row[1])
	if err != nil {
		return nil, err
	}
	unit, _ := row[2].(string)
	return &Latest{TS: ts, Value: v, Unit: unit}, nil
}

// toNanos converts a QuestDB timestamp cell (ISO-8601 string or numeric epoch)
// into unix nanoseconds.
func toNanos(v any) (int64, error) {
	switch t := v.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000000Z"} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed.UnixNano(), nil
			}
		}
		return 0, fmt.Errorf("cannot parse timestamp %q", t)
	case float64:
		return int64(t), nil
	case int64:
		return t, nil
	case json.Number:
		return t.Int64()
	default:
		return 0, fmt.Errorf("unknown timestamp type %T", v)
	}
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case int64:
		return float64(t), nil
	case string:
		return strconv.ParseFloat(t, 64)
	case json.Number:
		return t.Float64()
	default:
		return 0, fmt.Errorf("unknown numeric type %T", v)
	}
}
