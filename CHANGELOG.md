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

[Unreleased]: https://github.com/TPT-Solutions/tpt-datacenter/commits/main
