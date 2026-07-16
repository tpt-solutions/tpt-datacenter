# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Versioning policy

- The **root workspace** and each publishable component follow SemVer (`MAJOR.MINOR.PATCH`).
- `0.x.y` releases are pre-alpha: the public API is not stable and MINOR may
  introduce breaking changes.
- Component-level versions are coordinated from the root `Cargo.toml`
  `[workspace.package]` (Rust) and each `go.mod` (Go). When a component is
  published independently (e.g. `ai-brain` to crates.io), it carries its own
  version but stays in lockstep with the workspace during the pre-1.0 phase.
- Release commits are tagged `vX.Y.Z` and trigger the release workflow.

## [Unreleased]

### Added
- Monorepo scaffolding: Cargo workspace (`rust-edge`, `ai-brain`) and Go module
  (`go-telemetry`).
- Dual MIT OR Apache-2.0 licensing with SPDX headers.
- Repository governance: CONTRIBUTING, CODE_OF_CONDUCT, SECURITY.
- CI (Rust/Go build/lint/test + SPDX header check) and pre-commit hooks.
- **Phase 1 — HAL & Simulator** (`rust-edge`): common telemetry/control data model,
  `HardwareAbstractionLayer` trait, in-process `SimulatorHal` with lumped
  thermal/electrical physics, Real backends (Redfish / Modbus TCP / IPMI) behind the
  same trait (gated behind the `real` feature), and config-driven Simulator↔Real
  switching per-deployment and per-device (`HalConfig` / `RoutingHal`).
- **Phase 2 — Real-Time Facility Edge** (`rust-edge`): deterministic control-loop
  framework, PDU/UPS/cooling agents, safety interlocks / fail-safe `SafeState`
  latch, heartbeat reporting, cross-compile profiles, and a bench test
  (`tpt-edge`) driving `SimulatorHal` from 54°C → 28.5°C.
- **Phase 3 — Telemetry Ingestion** (`go-telemetry`): concurrent ingestion
  pipeline, Redfish/Modbus/IPMI collectors, protobuf message schema, batched
  QuestDB writer, load testing, and observability.
- **Phase 4 — Time-Series Storage**: QuestDB dev/staging/prod configs, sensor
  schema, retention/downsampling policy, query layer, and write/query benchmarks.
- **Phase 5 — Physical Topology Graph**: in-memory graph library, rack/PDU/cooling
  schema, authoring tooling, relationship query API, and dashboard/AI integration.
- **Phase 6 — Thermal AI Brain** (`ai-brain`): reinforcement learning for cooling
  optimization and hotspot prediction, implemented from scratch in Rust/burn.
  - `burn` MLP inference model (`BrainModel`) on the CPU `NdArray` backend, plus a
    `HotspotNet` hotspot-prediction head.
  - `HeuristicController` baseline and the shared `Policy` / `State` / `Action` types.
  - `SimWorld` training environment + self-contained `MockWorld` physics; the
    `rust-edge` `SimulatorHal` satisfies the same `SimWorld` contract.
  - From-scratch Gaussian policy-gradient actor–critic (`train_rl`) over `burn`
    autodiff, plus supervised `train_hotspot` / `train_brain`.
  - `GuardedPolicy` safety guardrails (physical envelope, emergency max-cooling,
    per-step rate limiting, operator override / safe-state latch).
  - `BrainServer` serving loop turning a guarded policy into edge actuator
    `Command`s (fractions → 0–100 %); the edge integrates via
    `From<ai_brain::serve::Command>` for `rust_edge::hal::types::ControlCommand`.
   - Crates.io prep: `cargo doc` builds and `cargo publish --dry-run` passes.
 - **Phase 7 — Hardware Management API** (`api`): Redfish server/client for
   compute-server management (per-OEM Dell/HPE/Supermicro power-cap, boot,
   reset), IPMI power control integration, dynamic CPU power throttling tied to
   a grid-stress signal (with a stub `GridStressSource` for future TPT
   Dynamo/Relay integration), append-only audit logging (`/hw/audit`), and
   constant-time bearer-token auth.
 - **Phase 8 — Core Platform API & Orchestration** (`api`): orchestration
   service routing control intents through policy enforcement to a
   `HalCommandSink` (Simulator sink in dev), constant-time bearer-token auth on
   all control/hardware/orchestration endpoints, append-only audit logging for
   every control action, and the documented public API contract
   (`docs/api-reference.md`). The schema of record is `api/proto/tpt/v1/tpt.proto`;
   the running services are HTTP/JSON (gRPC stubs are not yet implemented).
 - **Phase 14 — Hardening & Adoption**: network-segmentation guidance for
   plaintext Redfish/Modbus/IPMI; root `docker-compose.yml` + `Makefile` `demo`
   target; CI branch trigger fixed to `master`; Go toolchain unified to 1.23;
   `ai-brain` `GuardedPolicy::decide()` now delegates to the rate-limited guard
   path (no bypass); `rust-edge::firmware::verify_update` wired into a real
   verify-then-apply `Updater` integration point.

[Unreleased]: https://github.com/TPT-Solutions/tpt-datacenter/commits/master
