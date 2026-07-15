// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package questdb

import (
	"context"
	"strconv"
	"strings"
)

// Schema is the DDL applied to a fresh QuestDB instance. It mirrors
// deploy/questdb/schema.sql. Statements are idempotent (CREATE TABLE IF NOT
// EXISTS) so Apply is safe to re-run.
var Schema = []string{
	`CREATE TABLE IF NOT EXISTS readings (
		ts     TIMESTAMP,
		device SYMBOL CAPACITY 1024 CACHE,
		sensor SYMBOL CAPACITY 4096 CACHE,
		metric SYMBOL CAPACITY 64  CACHE,
		value  DOUBLE,
		unit   SYMBOL CAPACITY 32  CACHE
	) TIMESTAMP(ts) PARTITION BY DAY WAL
	  DEDUP UPSERT KEYS(ts, device, sensor, metric)`,

	`CREATE TABLE IF NOT EXISTS readings_1m (
		ts     TIMESTAMP,
		device SYMBOL CAPACITY 1024 CACHE,
		sensor SYMBOL CAPACITY 4096 CACHE,
		metric SYMBOL CAPACITY 64  CACHE,
		avg    DOUBLE,
		min    DOUBLE,
		max    DOUBLE,
		count  LONG
	) TIMESTAMP(ts) PARTITION BY MONTH WAL`,

	`CREATE TABLE IF NOT EXISTS readings_1h (
		ts     TIMESTAMP,
		device SYMBOL CAPACITY 1024 CACHE,
		sensor SYMBOL CAPACITY 4096 CACHE,
		metric SYMBOL CAPACITY 64  CACHE,
		avg    DOUBLE,
		min    DOUBLE,
		max    DOUBLE,
		count  LONG
	) TIMESTAMP(ts) PARTITION BY YEAR WAL`,

	`CREATE TABLE IF NOT EXISTS devices (
		device    SYMBOL CAPACITY 1024 CACHE,
		kind      SYMBOL CAPACITY 32  CACHE,
		first_seen TIMESTAMP,
		last_seen  TIMESTAMP
	) TIMESTAMP(first_seen) PARTITION BY YEAR WAL`,
}

// Apply installs the full schema. It is safe to call repeatedly.
func (c *Client) Apply(ctx context.Context) error {
	return c.ExecMany(ctx, Schema...)
}

// UpsertDevice records a device's presence window. It is called by the ingest
// pipeline / discovery to keep the device registry current.
func (c *Client) UpsertDevice(ctx context.Context, device, kind string, firstSeenNs, lastSeenNs int64) error {
	sql := `INSERT INTO devices (device, kind, first_seen, last_seen) VALUES ('` +
		escapeSQL(device) + `', '` + escapeSQL(kind) + `', ` +
		tsNanos(firstSeenNs) + `, ` + tsNanos(lastSeenNs) + `)`
	_, err := c.Exec(ctx, sql)
	return err
}

// escapeSQL escapes a single-quoted SQL literal.
func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// tsNanos renders a nanosecond epoch as a QuestDB timestamp literal.
func tsNanos(ns int64) string {
	return `to_timestamp(` + strconv.FormatInt(ns, 10) + `, 'ns')`
}
