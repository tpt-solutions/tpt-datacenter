# go-telemetry

Telemetry Ingestion Engine (Go) — **Phase 3** of the TPT DataCenter stack.

Concurrent, backpressure-aware pipeline that ingests sensor data
(temperature, voltage, amperage, airflow, power, …) from the physical plant
via **Redfish**, **Modbus TCP**, and **IPMI** collectors, batches the samples,
and writes them to **QuestDB** using the InfluxDB Line Protocol (ILP) over TCP.

```
                 ┌──────────────┐  poll  ┌──────────────┐
   Redfish ──────►              │        │              │
   Modbus  ──────►  Collectors  ├──points─►  Ingest bus  │ (bounded channel =
   IPMI    ──────►              │        │  (backpressure│  backpressure)
   Synthetic─────►              │        │   + workers)  │
                 └──────────────┘        └──────┬───────┘
                                                 │ batch
                                                 ▼
                                          ┌──────────────┐
                                          │ QuestDB (ILP)│
                                          │  / dev log   │
                                          └──────────────┘
```

## Layout

| Package | Responsibility |
| --- | --- |
| `internal/point` | Internal message schema (`DataPoint`) + versioned binary wire format and JSON. |
| `internal/collector` | `Collector` interface + Redfish, Modbus TCP, IPMI, and Synthetic sources. |
| `internal/pipeline` | Concurrent ingest engine: bounded bus, worker pool, per-worker batching, graceful shutdown. |
| `internal/writer` | `TimeSeriesWriter` sink: QuestDB ILP writer, dev log writer, null sink. |
| `internal/metrics` | Atomic counters, snapshot, and a bounded dead-letter sink. |

## Internal message schema

Every sample is a `point.DataPoint` (`Device`, `Sensor`, `Metric`, `Value`,
`Unit`, `Timestamp`). On the ingest bus it is serialized with a compact,
**versioned binary framing** (`MarshalBinary`/`UnmarshalBinary`) that is
protobuf-shaped and forwards-compatible, so a future protobuf IDL can replace
it without breaking the wire contract. A JSON form is provided for debugging
and the dev writer.

## Run

```bash
# Simulator mode: synthetic source, ILP to stdout (no QuestDB needed)
go run ./cmd -mode=sim -sink=log

# Real mode: collectors from env (REDFISH_URL, MODBUS_ADDR, IPMI_HOST, …)
export REDFISH_URL=https://bmc-1 REDFISH_USER=... REDFISH_PASS=...
export MODBUS_ADDR=192.168.10.50:502 IPMI_HOST=bmc-2 IPMI_USER=... IPMI_PASS=...
go run ./cmd -mode=real -questdb=questdb:9009

# Throughput load test (null sink)
go run ./cmd/loadtest -duration 10s -workers 8 -batch 8192
```

## Backpressure & batching

- The ingest channel is **bounded** (`-buf`); when full, collectors block on
  send, naturally throttling producers instead of exhausting memory.
- Each of the N workers (`-workers`) maintains its own batch, flushing when it
  reaches `-batch` points **or** after `-batch-timeout**, whichever comes first.
- Failed batches are routed to a **bounded dead-letter sink** (oldest evicted
  first) so a poison stream can never OOM the host.

## Observability

Atomic counters (`ingested`, `written`, `batches`, `write_errors`,
`dead_letters`, `poll_errors`, `per_source`) are exposed via
`metrics.Registry.Snapshot()` and logged periodically (`-metrics`). Dead
letters are logged as JSON for audit/replay.

## Status

- Redfish / Modbus TCP collectors implement real wire protocols.
- IPMI implements an in-process RMCP/ASF presence ping and delegates sensor
  reads to the `ipmitool` binary (a future in-process IPMI 2.0 RAKP session can
  replace this without changing the `Collector` surface — consistent with the
  `NotImplemented` stance in the Rust HAL).
- No external dependencies: QuestDB ILP is written natively over TCP.

## Phase 4 — Time-Series Storage (QuestDB)

`go-telemetry` also owns the storage layer and the read path back out:

| Path | Responsibility |
| --- | --- |
| `internal/questdb` | QuestDB REST client, schema DDL, retention/downsampling policy, and the HTTP query API. |
| `cmd/api` | Runs the query API (and can apply schema / run maintenance). |
| `deploy/questdb` | Dev/staging/prod `docker-compose` + QuestDB `.conf` + `schema.sql`. |

The writer (Phase 3) streams samples **in** via ILP on port 9009; the
`internal/questdb` package reads aggregated data **out** via the REST API on
port 9000 and exposes it to the dashboard and AI brain.

### Schema

Raw samples land in `readings` (partitioned by DAY, WAL + dedup so re-sent ILP
lines are idempotent). Two rollups — `readings_1m` and `readings_1h` — are
pre-aggregated (`avg`/`min`/`max`/`count`) by the maintenance job. A `devices`
registry tracks fleet presence. Full DDL: `deploy/questdb/schema.sql`.

### Retention & downsampling

`MaintenancePlan` produces an idempotent, re-runnable set of statements:
rollups first (`SAMPLE BY … FILL(NULL)`), then `ALTER TABLE … DROP PARTITION`
to age out raw data (default 90d) and rollups (default 2y). Run it via:

```bash
go run ./cmd/api -questdb http://localhost:9000 -maintenance
# or on a schedule:
go run ./cmd/api -questdb http://localhost:9000 -maintain-every 1h
```

### Query API (dashboard + AI brain)

```bash
go run ./cmd/api -addr :8080 -questdb http://localhost:9000 -token <secret>
```

| Endpoint | Purpose |
| --- | --- |
| `GET /health` | DB reachability (no auth). |
| `POST /api/query` | Ad-hoc SQL (bearer-auth). |
| `GET /api/latest?device=&sensor=&metric=` | Latest reading. |
| `GET /api/timeseries?device=&sensor=&metric=&from=&to=&rollup=` | Windowed series. |
| `GET /api/devices` | Device registry. |

### Bring-up & benchmarking

```bash
docker compose -f deploy/questdb/docker-compose.dev.yml up -d
go run ./cmd/api -questdb http://localhost:9000 -apply-schema
go run ./cmd/loadtest -duration 30s   # sustained write throughput
```

Write throughput is measured by `cmd/loadtest` (single host: ~1M pts/sec;
scale out workers/sources toward the target). Query-layer (Go) throughput is
measured by `go test -bench . ./internal/questdb` — end-to-end DB latency
should be validated against a live instance under representative load.

## Phase 5 — Physical Topology Graph

`go-telemetry` also serves the facility topology graph (what powers/cools what).

| Path | Responsibility |
| --- | --- |
| `internal/topology` | Graph model, authoring (JSON spec + telemetry discovery), and query API. |
| `cmd/topology` | Runs the topology API server. |
| `deploy/topology/facility.json` | Example manual topology spec. |

**Storage choice:** an in-memory adjacency graph (no external graph DB). At
facility scale node counts are bounded (thousands) and change slowly, so a
dedicated graph database would be operational overhead without benefit. The
graph is authored from a JSON spec and enriched by auto-discovery from the
telemetry `devices` registry, and snapshotted to JSON for persistence/audit.

**Model:** nodes (`room`, `rack`, `pdu`, `ups`, `cooling_loop`, `server`,
`sensor`, `cable`) joined by typed edges (`powers`, `cools`, `feeds`,
`contains`, `connects`).

**Relationship queries** (the answers the AI brain and dashboard consume):

```bash
go run ./cmd/topology -spec deploy/topology/facility.json -addr :8081
# what feeds this PDU?   GET /topology/powers?id=pdu-1
# what cools this rack?  GET /topology/cools?id=rack-01
# downstream of a loop?  GET /topology/cooling?id=loop-1
# export the whole graph GET /topology/graph
```

The AI brain uses `powers`/`cools` to scope its thermal optimization to the
devices a control action actually affects; the dashboard renders the graph
from `/topology/graph`.
