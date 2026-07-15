-- SPDX-FileCopyrightText: 2024 TPT Solutions
-- SPDX-License-Identifier: MIT OR Apache-2.0
--
-- TPT DataCenter — QuestDB schema for physical-plant telemetry.
-- Applied by `go-telemetry` (cmd/api -apply-schema) and documented here for
-- operators standing up the database manually.
--
-- Design notes:
--   * device / sensor / metric are SYMBOL (low-cardinality, dictionary-encoded)
--     so WHERE/HOUR/LATEST filters are fast and storage is compact.
--   * Raw samples land in `readings`, partitioned by DAY with WAL + dedup so
--     re-sent ILP lines (at-least-once from the ingest pipeline) are idempotent.
--   * `readings_1m` / `readings_1h` are pre-aggregated rollups produced by the
--     retention/downsampling job (see internal/questdb/retention.go).

CREATE TABLE IF NOT EXISTS readings (
  ts     TIMESTAMP,
  device SYMBOL CAPACITY 1024 CACHE,
  sensor SYMBOL CAPACITY 4096 CACHE,
  metric SYMBOL CAPACITY 64  CACHE,
  value  DOUBLE,
  unit   SYMBOL CAPACITY 32  CACHE
) TIMESTAMP(ts) PARTITION BY DAY WAL
  DEDUP UPSERT KEYS(ts, device, sensor, metric);

CREATE TABLE IF NOT EXISTS readings_1m (
  ts     TIMESTAMP,
  device SYMBOL CAPACITY 1024 CACHE,
  sensor SYMBOL CAPACITY 4096 CACHE,
  metric SYMBOL CAPACITY 64  CACHE,
  avg    DOUBLE,
  min    DOUBLE,
  max    DOUBLE,
  count  LONG
) TIMESTAMP(ts) PARTITION BY MONTH WAL;

CREATE TABLE IF NOT EXISTS readings_1h (
  ts     TIMESTAMP,
  device SYMBOL CAPACITY 1024 CACHE,
  sensor SYMBOL CAPACITY 4096 CACHE,
  metric SYMBOL CAPACITY 64  CACHE,
  avg    DOUBLE,
  min    DOUBLE,
  max    DOUBLE,
  count  LONG
) TIMESTAMP(ts) PARTITION BY YEAR WAL;

-- Device registry: which devices exist and when they were last heard from.
-- Fed by the ingest pipeline / discovery; consumed by the topology graph.
CREATE TABLE IF NOT EXISTS devices (
  device    SYMBOL CAPACITY 1024 CACHE,
  kind      SYMBOL CAPACITY 32  CACHE,
  first_seen TIMESTAMP,
  last_seen  TIMESTAMP
) TIMESTAMP(first_seen) PARTITION BY YEAR WAL;
