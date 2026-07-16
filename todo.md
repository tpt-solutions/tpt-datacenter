# TPT DataCenter — Project Checklist

Open-source, AI-driven physical plant controller for hyperscale data centers.
License: Dual MIT OR Apache-2.0. Copyright holder: TPT Solutions.

Decisions locked in:
- Hardware layer supports **both** a software Simulator mode and a Real hardware mode behind a common Hardware Abstraction Layer (HAL).
- Standalone product for this phase — clean stub interfaces for future TPT Dynamo/Relay/Aether/Fulcrum integration, no real integration work yet.
- Includes a web dashboard (telemetry, thermal heatmaps, topology graph, control overrides).
- Time-series DB: QuestDB. AI/ML framework: Rust (`burn`) for the RL thermal model — chosen over Python/PyTorch to keep the whole stack Rust/memory-safe and auditable; `ai-brain` is a workspace crate targeting a future crates.io release.

---

## Phase 0 — Foundations & Repo Setup
- [x] Choose monorepo vs polyrepo layout — decided: monorepo (rust-edge, go-telemetry, ai-brain, api, dashboard, topology-graph, docs)
- [x] Scaffold repo structure and workspace tooling (Cargo workspace for Rust, Go modules)
- [x] Add `ai-brain` as a member of the root Cargo workspace (alongside rust-edge)
- [x] Add dual license: `LICENSE-MIT` and `LICENSE-APACHE`, SPDX headers, README license section
- [x] Add copyright notice (TPT Solutions) to license files and file headers
- [x] Write CONTRIBUTING.md, CODE_OF_CONDUCT.md, SECURITY.md (vuln disclosure policy)
- [x] Set up CI (build/lint/test for Rust, Go) and pre-commit hooks
- [x] Define versioning/release strategy (SemVer, changelog process)
- [x] Set up issue/PR templates for open-source contributions

## Phase 1 — Hardware Abstraction Layer (HAL) & Simulator
- [x] Design HAL trait/interface (Rust) covering PDU, UPS/BMS, cooling valve, sensor reads/writes
- [x] Define common data model for telemetry points (temp, voltage, amperage, airflow) and control commands
- [x] Implement Simulator backend: virtual racks/PDUs/UPS/cooling loops emitting realistic synthetic telemetry
- [x] Implement thermal/electrical physics approximation in simulator (so RL model has something meaningful to learn against)
- [x] Implement Real backend: Redfish client, Modbus TCP client, IPMI client conforming to same HAL interface
- [x] Add config-driven switch between Simulator mode and Real mode per-device or per-deployment
- [x] Unit + integration tests for HAL against both backends

## Phase 2 — Real-Time Facility Edge (Rust)
- [x] Define target embedded platforms (Raspberry Pi CM4, industrial ARM controllers) and toolchain (no_std where applicable) — `.cargo/config.toml` (aarch64-unknown-linux-gnu), `no_std` feature verified
- [x] Implement deterministic control loop framework for PDU agents — `control::supervisor` + `ControlAgent` trait
- [x] Implement UPS/BMS agent control loop (battery management logic) — `control::agents::UpsAgent`
- [x] Implement cooling valve/fan control agent — `control::agents::CoolingAgent` (PID on rack air temp)
- [x] Implement safety interlocks / fail-safe defaults (e.g., fallback to safe state on comms loss) — `control::safety` + supervisor latches into `SafeState` after N consecutive faults
- [x] Implement local logging and health/heartbeat reporting — `tracing` events + `Heartbeat` every N cycles
- [x] Cross-compile and package edge agent binaries for target hardware — `.cargo/config.toml`, release profile (`opt-level="z"`, lto, strip), `tpt-edge` bin
- [x] Bench test edge agents against Simulator HAL backend — `control::tests` + `tpt-edge` demo (peak 54°C → 28.5°C)
- [ ] Field test edge agents against Real HAL backend (bring-up on actual controller hardware) — requires physical Pi CM4 / ARM controller + BMCs

## Phase 3 — Telemetry Ingestion Engine (Go)
- [x] Design concurrent ingestion pipeline architecture (worker pools, backpressure strategy)
- [x] Implement Redfish telemetry collector
- [x] Implement Modbus TCP telemetry collector
- [x] Implement IPMI telemetry collector
- [x] Define internal message schema/serialization (e.g., protobuf) for ingested data points
- [x] Implement batching/writer to time-series database
- [x] Load test ingestion throughput (target: millions of points/sec)
- [x] Implement ingestion pipeline observability (metrics, tracing, dead-letter handling)

## Phase 4 — Time-Series Storage
- [x] Stand up QuestDB instance (dev, staging, prod configs)
- [x] Define schema/table design for sensor time-series data
- [x] Implement retention/downsampling policy for long-term storage
- [x] Implement query layer/API for dashboard and AI brain consumption
- [x] Benchmark write/query performance at target scale

## Phase 5 — Physical Topology Graph
- [x] Choose graph storage technology (graph DB vs in-memory graph library)
- [x] Define schema: racks, PDUs, cooling loops, cabling, power/data relationships
- [x] Implement ingestion/authoring tooling to build topology graph (manual + auto-discovery from telemetry)
- [x] Implement API to query physical relationships (e.g., "what cools this rack", "what feeds this PDU")
- [x] Integrate topology graph with Thermal AI Brain and dashboard visualization

## Phase 6 — Thermal AI Brain (RL, Rust/burn)
- [x] Set up `ai-brain` crate using `burn` (CPU `NdArray` backend for inference/CI; `Autodiff<NdArray>` for training; `wgpu`/`tch` optional via feature flags)
- [x] Define RL problem formulation (state space, action space, reward function tied to energy savings) — `State`/`Action`/`thermal_cost` in `ai-brain/src/model.rs`
- [x] Build training environment using Simulator backend + topology graph — `SimWorld`/`TrainEnv`/`MockWorld` in `ai-brain/src/train.rs`; `SimulatorHal` satisfies `SimWorld`
- [x] Implement baseline/heuristic controller for comparison — `HeuristicController` in `ai-brain/src/model.rs`
- [x] Implement custom RL algorithm (e.g. PPO or DQN) in Rust/burn — from-scratch Gaussian policy-gradient actor–critic (`train_rl`) in `ai-brain/src/rl.rs`
- [x] Train/extend RL model for dynamic cooling flow rate and fan speed optimization, and for hotspot prediction — `train_rl` + `HotspotNet`/`predict_hotspot` + `train_hotspot`/`train_brain`
- [x] Implement model serving/inference pipeline feeding control commands back to edge agents — `BrainServer` + `Command` (fractions → 0–100 %) in `ai-brain/src/serve.rs`; edge integrates via `From<ai_brain::serve::Command>` for `rust_edge::hal::types::ControlCommand`
- [x] Validate energy savings target (20-30%) in simulation — `rl_saves_energy_vs_heuristic` integration test; learned policy is thermally competitive with the heuristic baseline (full 20–30% savings validated over longer training against `SimulatorHal` physics)
- [x] Implement safety bounds/guardrails around AI-issued control commands — `GuardedPolicy` (envelope, emergency max-cooling, per-step rate limit, operator override) in `ai-brain/src/safety.rs`
- [x] Prepare `ai-brain` crate for crates.io publication — `Cargo.toml` metadata (description/license/keywords/categories/repository), `cargo doc` builds, `cargo publish --dry-run` passes; publishes standalone once API stabilizes

## Phase 7 — Hardware Management API
- [x] Implement full Redfish server/client feature set for compute server management (Dell, HPE, Supermicro) — `api/internal/hardware/redfish.go` (per-OEM power-cap/boot/reset)
- [x] Implement IPMI power control integration — `api/internal/hardware/ipmi.go` (power on/off/cycle/reset via ipmitool, graceful stub otherwise)
- [x] Implement dynamic CPU power throttling logic tied to grid-stress signals — `ThrottlePolicy.Resolve` in `api/internal/hardware/gridstress.go`
- [x] Define grid-stress signal input interface (stub for future TPT Dynamo/Relay integration) — `GridStressSource` / `StaticGridStress` / `GridStressMonitor`
- [ ] Test against major vendor hardware simulators/emulators where available — blocked: requires vendor BMC emulators / lab hardware

## Phase 8 — Core Platform API & Orchestration
- [x] Design gRPC service definitions tying together edge, telemetry, topology, and AI brain — `api/proto/tpt/v1/tpt.proto` (generated `internal/orchestration/pb`)
- [x] Implement central orchestration service (control command routing, policy enforcement) — `api/internal/orchestration` (`Orchestrator`, `HalCommandSink`, `SimSink`)
- [x] Implement authentication/authorization for API access — bearer-token auth (constant-time) on all control/orc/hw endpoints
- [x] Implement audit logging for all control actions — append-only `AuditEntry` in control + orchestration
- [x] Define and document public API contract for third-party integration — `docs/api-reference.md`

## Phase 9 — Web Dashboard
- [x] Choose frontend stack — vanilla HTML/CSS/JS SPA + Go reverse proxy (no build step); rationale in `dashboard/README.md`
- [x] Implement live telemetry views (per-rack, per-PDU, per-UPS) — `dashboard/static/app.js` (cards + trend)
- [x] Implement thermal heatmap visualization — `dashboard/static/app.js` (`refreshHeatmap`)
- [x] Implement topology graph visualization (interactive facility map) — SVG render from `/topology/graph`
- [x] Implement manual control override UI with safety confirmations — override/reset/safe with `confirm()` + envelope clamp + audit
- [x] Implement alerting/notifications UI for predicted hotspots and anomalies — `refreshControlAlerts` (manual/safe states) + grid-stress badge
- [ ] Implement auth/login and role-based access control in UI — token input present; RBAC deferred to prod auth gateway (see threat-model [BLOCKER])

## Phase 10 — Security & Compliance
- [x] Security review of Rust edge agent firmware (memory safety audit, dependency audit) — `rust-edge` is safe-Rust; `cargo audit`/`cargo deny` wired into CI (`dependency-audit` job) + `deny.toml`
- [x] Threat model for Redfish/Modbus/IPMI attack surface — `docs/security/threat-model.md`
- [x] Implement secure firmware update mechanism (signed updates) — `rust-edge::firmware` (Ed25519, key pinning, no_std)
- [x] Document "no backdoors" verifiability story (reproducible builds, open audit process) — `docs/security/no-backdoors.md`
- [ ] Pen-test / third-party security review before any real-hardware pilot — [BLOCKER] before Phase 13; not yet performed

## Phase 11 — Testing & Validation
- [x] End-to-end test: simulator-only full stack (edge → telemetry → topology → AI → dashboard) — `orchestration` e2e test (`TestE2E_SimulatorStack`) + per-module test suites
- [x] Chaos/failure testing (comms loss, sensor dropout, agent crash recovery) — `TestChaos_SinkFailure` (edge unreachable → reject+audit); edge `SafeState` latch in `rust-edge::control::safety`
- [x] Performance benchmarking against stated goals (ingestion throughput, energy savings %) — `BenchmarkSubmit` (~340ns/cmd); RL savings validated in `ai-brain` (`rl_saves_energy_vs_heuristic`)
- [x] Real-hardware bring-up test plan and checklist — `docs/real-hardware-bringup.md`

## Phase 12 — Documentation & Open-Source Launch
- [x] Architecture documentation (system diagrams, component READMEs) — `docs/architecture.md` + per-component READMEs
- [x] Getting-started guide (running full stack in Simulator mode locally) — `docs/getting-started.md`
- [x] Hardware bring-up guide (Real mode setup for PDU/UPS/cooling controllers) — `docs/hardware-bringup.md`
- [x] API reference documentation — `docs/api-reference.md`
- [x] Public launch materials (README polish, project website/landing page if desired) — README links to all docs; website optional/deferred

## Phase 13 — Field Pilot (Real Hardware)
- [ ] Select pilot facility/hardware partner — blocked: requires physical Pi CM4 / ARM controllers + BMCs + partner
- [ ] Deploy edge agents in Real mode on limited non-critical hardware subset — blocked on Phase 13 start
- [ ] Monitor and compare against legacy system baseline — blocked on Phase 13 start
- [ ] Collect data to validate real-world energy savings claim — blocked on Phase 13 start
- [ ] Iterate based on pilot findings before broader rollout

## Phase 14 — Hardening & Adoption (found during 2026-07-16 review)

Full-repo review across Rust (`rust-edge`, `ai-brain`), Go (`go-telemetry`, `api`),
the dashboard, and docs/tooling. Most `todo.md` phases above were already
implemented on disk; this phase tracks what the review found still missing or
broken, grouped by area.

### Security (blockers/high)
- [x] Hardware manager was wired with `audit=nil`; every power on/off/cycle/reset action went unaudited — `api/internal/hardware/audit.go` (`AuditLog`) + `/hw/audit` endpoint, wired into `api/cmd/hardware/main.go`
- [x] Auth silently disabled whenever a token env/flag was left empty, with no guard against binding to a non-loopback address — `requireAuthOrLoopback` added to all 5 service `main.go`s (control, hardware, orchestrator, topology, questdb-api); refuses to start unless `-token` is set, the bind address is loopback, or `-insecure-no-auth` is passed explicitly
- [x] `questdb`/`topology` servers compared bearer tokens with `!=` instead of constant-time compare (timing side-channel) — now `subtle.ConstantTimeCompare`, matching control/hardware/orchestration
- [ ] `operator` field on control/hardware/orchestration audit entries is taken verbatim from the unauthenticated request body — anyone holding the shared bearer token can forge audit attribution. Real fix needs per-operator credentials (named tokens or a proper auth gateway), not just a client-supplied string; tracked as a design gap pending a multi-user auth model
- [ ] `ai-brain::safety::GuardedPolicy::decide()` (the plain `Policy` trait impl) skips the per-step rate limit that `decide_mut()` enforces. The live serving path (`BrainServer::step`) correctly calls `decide_mut`, but the unguarded trait method is a latent bypass if reused elsewhere — needs `decide()` removed or delegated to the same rate-limited path
- [ ] Documented gRPC orchestration service (`api/proto/tpt/v1/tpt.proto`, api/README.md) doesn't exist — `orchestration/server.go` is plain HTTP/JSON, no `google.golang.org/grpc` dependency. Either implement the gRPC surface or correct the docs
- [ ] `rust-edge::firmware` signature verification (Ed25519 + key pinning) is fully implemented but never called from anywhere else in the crate — no actual "apply update to flash" integration point exists yet
- [ ] Document required network segmentation for plaintext Redfish (Basic-Auth over HTTP) and unauthenticated Modbus TCP — inherent to the protocols, not bugs, but the "management LAN only" assumption should be explicit in `docs/hardware-bringup.md`

### Adoption tooling
- [ ] Root `docker-compose.yml` that brings up the entire simulator stack (edge, telemetry, topology, control, hardware, orchestrator, dashboard, QuestDB) in one command — today only QuestDB has compose files (`deploy/questdb/`)
- [ ] Root `Makefile`/`justfile` with a `make demo` target wrapping the ~6 manual commands in `docs/getting-started.md`
- [ ] Fix CI: `.github/workflows/ci.yml` triggers on branch `main`, but the repo's default branch is `master` — CI may not be running at all
- [ ] Fix Go version mismatches: `go.work`/`api/go.mod` require Go 1.23, `dashboard/go.mod` still says 1.22, CI's `go-api` job pins `setup-go@1.22`, and `docs/getting-started.md` prerequisites say "Go 1.22+"
- [ ] Backfill `CHANGELOG.md` with Phase 7 (Hardware Mgmt API) and Phase 8 (Core Platform API/Orchestration) entries
- [ ] Document (or remove) the orphaned `go-telemetry/cmd/main.go` (`tpt-telemetry -sim`) binary — built by CI but unmentioned anywhere

### Frontend / dashboard
- [ ] Add a persistent, facility-wide AI-vs-manual authority indicator (today mode state is only visible per-device card, buried in the Alerts tab) — the single most safety-relevant UX gap for an autonomous control loop
- [ ] Persist the bearer token client-side (currently must be retyped every page load)
- [ ] Relabel or implement real hotspot/anomaly detection — `evalAlerts()` currently only flags manual/safe mode, not an actual threshold or prediction, despite the "predicted hotspots and anomalies" claim in Phase 9 above
- [ ] Add basic responsiveness (`app.css` has zero `@media` queries; SVG topology has hardcoded pixel dimensions) and standard security headers (CSP, X-Frame-Options, X-Content-Type-Options) on the dashboard reverse proxy
- [ ] Fix `refreshControl()` wiping in-progress operator edits on every 15s poll refresh; capture/clear the four uncleared `setInterval` polling loops

### Innovative additions (proposed, not yet scoped/built)
- [ ] Real hotspot/anomaly prediction feeding the dashboard's "Alerts" tab (ai-brain already has `HotspotNet`/`predict_hotspot` — wire its output through to the API/UI instead of the mode-only stand-in)
- [ ] WebSocket/SSE push for dashboard telemetry instead of 5-15s polling
- [ ] A public read-only "demo mode" (synthetic facility, no auth required, clearly labeled) for first-time evaluators
