# Getting Started (Simulator Mode)

Run the entire TPT DataCenter stack locally with **no real hardware** — the
edge agents and HAL run in Simulator mode, and all services talk over localhost.

## Prerequisites

- Rust toolchain (edition 2021, `rust-version` 1.74+) with the `wgpu` feature
  optional for `ai-brain` training.
- Go 1.23+.
- (Optional) Docker + docker compose for QuestDB and the one-command stack; otherwise the telemetry query
  API can run against a dev log sink.

## One-command stack (recommended)

```bash
# Build + run the entire simulator stack in containers (edge, all Go services,
# QuestDB, dashboard) — see docker-compose.yml and the Makefile.
make demo
# then open http://localhost:8085  (API token: devtoken)
```

To tear it down: `make demo-down`. The `demo` target is a thin wrapper around
`docker compose up --build`; all services run on loopback and auth is disabled
for local exploration only (`-insecure-no-auth`), so never expose this compose
to a shared network.

To run the same stack directly on your host instead of Docker, use
`make demo-local` (requires the prerequisites below).

## 1. Build everything

```bash
# Rust workspace (edge + AI brain)
cargo build --workspace

# Go modules (use the workspace)
cd api && go build ./... && cd ..
cd dashboard && go build ./... && cd ..
cd go-telemetry && go build ./... && cd ..
```

## 2. Start QuestDB (optional but recommended)

```bash
docker compose -f deploy/questdb/docker-compose.dev.yml up -d
```

## 3. Start the backend services

Each service listens on its own port. In a real deployment put them behind the
dashboard reverse proxy; for local exploration you can run them directly.

```bash
# Telemetry query API (QuestDB) on :8080
cd go-telemetry && go run ./cmd/api -addr :8080 -questdb http://localhost:9000 &

# Topology API on :8081 (uses deploy/topology/facility.json)
go run ./cmd/topology -spec deploy/topology/facility.json -addr :8081 &

# Control (manual override + audit) on :8082
cd ../api && go run ./cmd/control -addr :8082 -spec ../deploy/topology/facility.json -token devtoken &

# Hardware Management API on :8083 (grid-stress stub by default)
go run ./cmd/hardware -addr :8083 -token devtoken &

# Orchestrator on :8084 (Simulator sink)
go run ./cmd/orchestrator -addr :8084 -token devtoken &
```

## 4. Run the edge demo (Simulator HAL)

```bash
cd rust-edge && cargo run --bin tpt-edge
# drives the Simulator HAL; observe peak temp settle toward setpoint.
```

## 5. Open the dashboard

```bash
cd dashboard && go run ./cmd/dashboard -addr :8085 \
  -control http://localhost:8082 -hardware http://localhost:8083 \
  -telemetry http://localhost:8080 -topology http://localhost:8081 \
  -orchestrator http://localhost:8084
```

Open <http://localhost:8085>, paste `devtoken` into the API token field, and
explore: telemetry cards, thermal heatmap, topology graph, and manual control
overrides (each clamped + audited).

## 6. Try a manual override (curl)

```bash
curl -X POST localhost:8082/control/override \
  -H 'Authorization: Bearer devtoken' -H 'Content-Type: application/json' \
  -d '{"device":"rack-01","command":"valve","value":75,"operator":"you","reason":"demo"}'
```

## 7. (Optional) Run the telemetry ingestion engine

`go-telemetry` also ships a standalone ingestion binary (`go-telemetry/cmd`,
built by CI) that runs the collect→batch→QuestDB pipeline. In `sim` mode it
uses a synthetic source and writes line-protocol to stdout, so you can watch
the pipeline without hardware:

```bash
cd go-telemetry && go run ./cmd -mode=sim -sink=log
```

The full set of flags (real-mode collectors from env, batching, backpressure)
is documented in `go-telemetry/README.md`.

## Next steps

- Train/extend the AI brain: see `ai-brain/README.md` (`train_rl`, hotspot net).
- Deploy against real hardware: see `docs/real-hardware-bringup.md`.
- Understand the safety/security model: `docs/architecture.md`,
  `docs/security/threat-model.md`, `docs/security/no-backdoors.md`.
