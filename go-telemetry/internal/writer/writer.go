// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package writer defines the time-series sink abstraction and concrete
// writers: a QuestDB InfluxDB-Line-Protocol (ILP) writer and a dev/stdout
// writer used in simulator mode.
package writer

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
)

// TimeSeriesWriter is the sink the pipeline batches into.
type TimeSeriesWriter interface {
	// Name reports the sink name for observability.
	Name() string
	// WriteBatch commits a batch of points. Implementations must be safe for
	// concurrent use.
	WriteBatch(ctx context.Context, pts []point.DataPoint) error
	// Close flushes and releases resources.
	Close() error
}

// QuestDBConfig configures the QuestDB ILP writer.
type QuestDBConfig struct {
	// Address is host:port of the QuestDB ILP receiver (default 9009).
	Address string
	// Table is the target table name.
	Table string
	// Dial optionally overrides connection establishment (used by tests).
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// QuestDB writes points using the QuestDB InfluxDB Line Protocol over TCP.
//
// Wire form per point:
//
//	readings,device=...,sensor=...,metric=... value=<f64> <ns-timestamp>\n
type QuestDB struct {
	cfg  QuestDBConfig
	mu   sync.Mutex
	conn net.Conn
	dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewQuestDB builds a QuestDB ILP writer.
func NewQuestDB(cfg QuestDBConfig) *QuestDB {
	if cfg.Address == "" {
		cfg.Address = "localhost:9009"
	}
	if cfg.Table == "" {
		cfg.Table = "readings"
	}
	dial := cfg.Dial
	if dial == nil {
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, address)
		}
	}
	return &QuestDB{cfg: cfg, dial: dial}
}

// Name reports the writer name.
func (q *QuestDB) Name() string { return "questdb" }

// WriteBatch emits one ILP line per point.
func (q *QuestDB) WriteBatch(ctx context.Context, pts []point.DataPoint) error {
	if len(pts) == 0 {
		return nil
	}
	var b strings.Builder
	for _, p := range pts {
		b.WriteString(q.line(p))
		b.WriteByte('\n')
	}
	payload := []byte(b.String())
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn == nil {
		conn, err := q.dial(ctx, "tcp", q.cfg.Address)
		if err != nil {
			return fmt.Errorf("questdb: dial %s: %w", q.cfg.Address, err)
		}
		q.conn = conn
	}
	if err := q.conn.SetDeadline(deadline(ctx)); err != nil {
		return err
	}
	if _, err := q.conn.Write(payload); err != nil {
		q.conn = nil
		return fmt.Errorf("questdb: write: %w", err)
	}
	return nil
}

// Close releases the connection.
func (q *QuestDB) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn == nil {
		return nil
	}
	err := q.conn.Close()
	q.conn = nil
	return err
}

func (q *QuestDB) line(p point.DataPoint) string {
	ts := p.Timestamp.UnixNano()
	if ts <= 0 {
		ts = time.Now().UnixNano()
	}
	return fmt.Sprintf("%s,device=%s,sensor=%s,metric=%s value=%s %d",
		q.cfg.Table, escape(p.Device), escape(p.Sensor), escape(string(p.Metric)),
		formatFloat(p.Value), ts)
}

// escape applies InfluxDB line-protocol identifier escaping.
func escape(s string) string {
	s = strings.ReplaceAll(s, ` `, `\ `)
	s = strings.ReplaceAll(s, `,`, `\,`)
	s = strings.ReplaceAll(s, `=`, `\=`)
	return s
}

// Log is a dev TimeSeriesWriter that renders ILP lines to an io.Writer
// (typically os.Stdout). It is used in simulator mode where no QuestDB is
// available.
type Log struct {
	w     io.Writer
	table string
}

// NewLogWriter builds a dev log writer.
func NewLogWriter(w io.Writer, table string) *Log {
	if table == "" {
		table = "readings"
	}
	return &Log{w: w, table: table}
}

// Name reports the writer name.
func (l *Log) Name() string { return "log" }

// WriteBatch writes ILP lines to the underlying writer.
func (l *Log) WriteBatch(_ context.Context, pts []point.DataPoint) error {
	var b strings.Builder
	for _, p := range pts {
		ts := p.Timestamp.UnixNano()
		fmt.Fprintf(&b, "%s,device=%s,sensor=%s,metric=%s value=%s %d\n",
			l.table, escape(p.Device), escape(p.Sensor), escape(string(p.Metric)),
			formatFloat(p.Value), ts)
	}
	_, err := io.WriteString(l.w, b.String())
	return err
}

// Close is a no-op for the log writer.
func (l *Log) Close() error { return nil }

// Null is a sink that discards all points. It is used by load/throughput
// tests where the goal is to measure the pipeline, not the storage engine.
type Null struct{}

// NewNullWriter builds a discarding writer.
func NewNullWriter() *Null { return &Null{} }

// Name reports the writer name.
func (n *Null) Name() string { return "null" }

// WriteBatch is a no-op.
func (n *Null) WriteBatch(_ context.Context, _ []point.DataPoint) error { return nil }

// Close is a no-op.
func (n *Null) Close() error { return nil }

func deadline(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return time.Now().Add(10 * time.Second)
}

// formatFloat renders floats compactly (no scientific notation for typical
// telemetry magnitudes).
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
