// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package questdb provides a thin, dependency-free client for QuestDB's REST
// query API plus the schema, retention/downsampling policy, and an HTTP query
// API consumed by the dashboard and the AI brain. It complements the ILP
// writer in the writer package: the writer streams samples in, this package
// reads aggregated data back out.
package questdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Result is the decoded body of a QuestDB /exec response.
type Result struct {
	Query   string   `json:"query"`
	Columns []Column `json:"columns"`
	Dataset [][]any  `json:"dataset"`
	Count   int      `json:"count"`
	Error   string   `json:"error"`
}

// Column describes a single result column.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Client talks to a QuestDB REST endpoint (default http://host:9000/exec).
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient builds a QuestDB REST client. baseURL should include scheme and
// host, e.g. "http://localhost:9000".
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Exec runs a single SQL statement and returns the decoded result.
func (c *Client) Exec(ctx context.Context, sql string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/exec", strings.NewReader(url.Values{"query": {sql}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("questdb: exec: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("questdb: read: %w", err)
	}
	var r Result
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("questdb: decode (status %d): %w: %s", resp.StatusCode, err, body)
	}
	if r.Error != "" {
		return &r, fmt.Errorf("questdb: %s", r.Error)
	}
	if resp.StatusCode >= 400 {
		return &r, fmt.Errorf("questdb: http %d: %s", resp.StatusCode, body)
	}
	return &r, nil
}

// ExecMany runs each statement in order, returning on the first error.
func (c *Client) ExecMany(ctx context.Context, stmts ...string) error {
	for _, s := range stmts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, err := c.Exec(ctx, s); err != nil {
			return fmt.Errorf("questdb: statement %q: %w", s, err)
		}
	}
	return nil
}

// Ping verifies connectivity via the /status endpoint.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/status", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("questdb: ping: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("questdb: ping status %d", resp.StatusCode)
	}
	return nil
}
