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
- [ ] Design concurrent ingestion pipeline architecture (worker pools, backpressure strategy)
- [ ] Implement Redfish telemetry collector
- [ ] Implement Modbus TCP telemetry collector
- [ ] Implement IPMI telemetry collector
- [ ] Define internal message schema/serialization (e.g., protobuf) for ingested data points
- [ ] Implement batching/writer to time-series database
- [ ] Load test ingestion throughput (target: millions of points/sec)
- [ ] Implement ingestion pipeline observability (metrics, tracing, dead-letter handling)

## Phase 4 — Time-Series Storage
- [ ] Stand up QuestDB instance (dev, staging, prod configs)
- [ ] Define schema/table design for sensor time-series data
- [ ] Implement retention/downsampling policy for long-term storage
- [ ] Implement query layer/API for dashboard and AI brain consumption
- [ ] Benchmark write/query performance at target scale

## Phase 5 — Physical Topology Graph
- [ ] Choose graph storage technology (graph DB vs in-memory graph library)
- [ ] Define schema: racks, PDUs, cooling loops, cabling, power/data relationships
- [ ] Implement ingestion/authoring tooling to build topology graph (manual + auto-discovery from telemetry)
- [ ] Implement API to query physical relationships (e.g., "what cools this rack", "what feeds this PDU")
- [ ] Integrate topology graph with Thermal AI Brain and dashboard visualization

## Phase 6 — Thermal AI Brain (RL, Rust/burn)
- [ ] Set up `ai-brain` crate using `burn` (choose backend: `wgpu` for portability, `tch`/CUDA optional for training speed)
- [ ] Define RL problem formulation (state space, action space, reward function tied to energy savings)
- [ ] Build training environment using Simulator backend + topology graph
- [ ] Implement baseline/heuristic controller for comparison
- [ ] Implement custom RL algorithm (e.g. PPO or DQN) in Rust/burn — no Stable-Baselines3 equivalent exists, so this is built from scratch
- [ ] Train/extend RL model for dynamic cooling flow rate and fan speed optimization, and for hotspot prediction
- [ ] Implement model serving/inference pipeline feeding control commands back to edge agents
- [ ] Validate energy savings target (20-30%) in simulation
- [ ] Implement safety bounds/guardrails around AI-issued control commands
- [ ] Prepare `ai-brain` crate for crates.io publication: `Cargo.toml` metadata (description/license/keywords/repository), API docs (`cargo doc`), versioning, `cargo publish` dry-run, and decide whether it publishes standalone or stays workspace-internal until stable

## Phase 7 — Hardware Management API
- [ ] Implement full Redfish server/client feature set for compute server management (Dell, HPE, Supermicro)
- [ ] Implement IPMI power control integration
- [ ] Implement dynamic CPU power throttling logic tied to grid-stress signals
- [ ] Define grid-stress signal input interface (stub for future TPT Dynamo/Relay integration)
- [ ] Test against major vendor hardware simulators/emulators where available

## Phase 8 — Core Platform API & Orchestration
- [ ] Design gRPC service definitions tying together edge, telemetry, topology, and AI brain
- [ ] Implement central orchestration service (control command routing, policy enforcement)
- [ ] Implement authentication/authorization for API access
- [ ] Implement audit logging for all control actions
- [ ] Define and document public API contract for third-party integration

## Phase 9 — Web Dashboard
- [ ] Choose frontend stack
- [ ] Implement live telemetry views (per-rack, per-PDU, per-UPS)
- [ ] Implement thermal heatmap visualization
- [ ] Implement topology graph visualization (interactive facility map)
- [ ] Implement manual control override UI with safety confirmations
- [ ] Implement alerting/notifications UI for predicted hotspots and anomalies
- [ ] Implement auth/login and role-based access control in UI

## Phase 10 — Security & Compliance
- [ ] Security review of Rust edge agent firmware (memory safety audit, dependency audit)
- [ ] Threat model for Redfish/Modbus/IPMI attack surface
- [ ] Implement secure firmware update mechanism (signed updates)
- [ ] Document "no backdoors" verifiability story (reproducible builds, open audit process)
- [ ] Pen-test / third-party security review before any real-hardware pilot

## Phase 11 — Testing & Validation
- [ ] End-to-end test: simulator-only full stack (edge → telemetry → topology → AI → dashboard)
- [ ] Chaos/failure testing (comms loss, sensor dropout, agent crash recovery)
- [ ] Performance benchmarking against stated goals (ingestion throughput, energy savings %)
- [ ] Real-hardware bring-up test plan and checklist

## Phase 12 — Documentation & Open-Source Launch
- [ ] Architecture documentation (system diagrams, component READMEs)
- [ ] Getting-started guide (running full stack in Simulator mode locally)
- [ ] Hardware bring-up guide (Real mode setup for PDU/UPS/cooling controllers)
- [ ] API reference documentation
- [ ] Public launch materials (README polish, project website/landing page if desired)

## Phase 13 — Field Pilot (Real Hardware)
- [ ] Select pilot facility/hardware partner
- [ ] Deploy edge agents in Real mode on limited non-critical hardware subset
- [ ] Monitor and compare against legacy system baseline
- [ ] Collect data to validate real-world energy savings claim
- [ ] Iterate based on pilot findings before broader rollout
