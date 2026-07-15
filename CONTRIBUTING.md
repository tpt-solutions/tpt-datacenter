# Contributing to TPT DataCenter

Thanks for your interest in contributing! This document covers how to set up a
development environment, the project's conventions, and the pull-request process.

## Code of Conduct

By participating, you agree to abide by our
[Code of Conduct](CODE_OF_CONDUCT.md).

## Repository layout

This is a monorepo. The major components are:

| Directory | Stack | Purpose |
|---|---|---|
| `rust-edge/` | Rust | Real-Time Facility Edge agents (PDUs, UPS/BMS, cooling) |
| `ai-brain/` | Rust (`burn`) | Thermal AI Brain RL model (publishable crate) |
| `go-telemetry/` | Go | Telemetry Ingestion Engine → QuestDB |
| `topology-graph/` | TBD | Physical topology graph |
| `api/` | TBD | Core platform gRPC API + Hardware Management API |
| `dashboard/` | TBD | Web dashboard |

## Prerequisites

- **Rust** ≥ 1.74 (see `rust-toolchain.toml` once added) and `cargo`.
- **Go** ≥ 1.22 and `go` toolchain.
- **pre-commit** (`pip install pre-commit`) for local hook enforcement.

## Getting started

```sh
# Rust workspace
cargo build --workspace
cargo test  --workspace
cargo clippy --workspace --all-targets -- -D warnings
cargo fmt   --all

# Go modules
cd go-telemetry
go build ./...
go test  ./...
go vet   ./...

# pre-commit
pre-commit install
pre-commit run --all-files
```

## Branching & commits

- Default branch is `main`. Open feature branches as `feat/...`, `fix/...`,
  `docs/...`, etc.
- Keep commits focused and write imperative, conventional-ish messages
  (`feat:`, `fix:`, `docs:`, `chore:`, `test:`).
- All PRs must pass CI (build, lint, test) before merge.

## Licensing

By contributing, you agree that your contributions are dual-licensed under
[MIT](LICENSE-MIT) OR [Apache-2.0](LICENSE-APACHE), matching the project.

Every source file must include an SPDX header, for example:

```text
// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0
```

## Reporting security issues

Please follow the [Security Policy](SECURITY.md). **Do not** open public issues
for vulnerabilities.
