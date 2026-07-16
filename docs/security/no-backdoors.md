# No-Backdoors Verifiability Story

TPT DataCenter is published under a **dual MIT OR Apache-2.0** license with the
explicit goal that anyone can *verify* there are no backdoors in the firmware
or control plane. This document explains how (todo.md **Phase 10**).

## 1. Everything builds from source, reproducibly

- The edge agents (`rust-edge`) are pure Rust with a `no_std`-capable core and
  a size-optimized `release` profile (`opt-level="z"`, `lto`, `strip`). Binaries
  are produced by `cargo build --release --target aarch64-unknown-linux-gnu`
  from pinned `Cargo.lock` dependencies.
- **Reproducible builds:** pin the toolchain (`rust-version = "1.74"`) and the
  lockfile, then compare the produced `tpt-edge` binary against a build from
  the published source + lockfile. CI asserts byte-equality for the pinned
  toolchain. (Enable `cargo build --workspace --profile release` determinism in
  CI; track `CARGO_INCREMENTAL=0` and a fixed `RUSTFLAGS`.)
- The Go services (`api`, `go-telemetry`, `dashboard`) build with `go build`
  against a `go.sum` with summed dependencies; `go vet` and `go test` run in CI.

## 2. The code is open and auditable

- No obfuscation, no prebuilt blobs flashed to controllers. Every actuator
  decision path — the edge supervisors, the safety interlocks, the AI brain
  guardrails, and the orchestrator policy — is plain source in this repo.
- The control and orchestration APIs log **every** action to an append-only
  audit trail (`api/internal/control`, `api/internal/orchestration`). There is
  no silent control path: an actuator only moves through `Override` /
  `Submit`, both of which are audited and clamped to the safety envelope.

## 3. Firmware is signed, not trusted

- Edge firmware updates are verified with **Ed25519** signatures
  (`rust-edge::firmware`) using a pure-Rust, no_std-capable verifier — no OS
  crypto, no platform backdoor surface. An update is rejected unless:
  1. the signature verifies over the image, **and**
  2. the signer key is in the pinned allow-set (`UpdatePolicy::pinned`).
- The TPT Solutions release public key is published; the signing private key is
  kept offline. Anyone can confirm a binary was produced by TPT and that no
  other key can be substituted at runtime (key pinning). A forged or tampered
  image fails `verify_update` and is never written to flash (see
  `firmware::tests`).

## 4. Safety interlocks cannot be silently disabled

- The edge supervisors latch into a fail-safe state after N consecutive faults
  (`rust-edge::control::safety`), and operator overrides are bounded by the
  same envelope the AI brain enforces. The "safe" latch drives cooling to max —
  the safest possible physical state — and can only be cleared by an explicit,
  audited reset.

## 5. Independent verification checklist

A skeptic can confirm "no backdoors" by:

1. `git clone` the repo; inspect the HAL, control, safety, AI guardrails, and
   orchestrator source. Search for any network callback, hidden command, or
   unsigned code path (none exist by design).
2. Reproduce the edge binary and diff it against the released artifact.
3. Generate their own Ed25519 key pair, rebuild with `UpdatePolicy::pinned` set
   to their key, and confirm the agent rejects TPT-signed images (proving key
   pinning is real, not cosmetic).
4. Run the full stack in **Simulator mode** and exercise the audit log: every
   override, reset, and AI command appears with operator/source and timestamp.

## 6. What we do NOT claim

- We do **not** claim the dependency tree is free of vulnerabilities; we run
  `cargo audit` / `cargo deny` and `go vet` in CI and track advisories, but
  users should re-run these against their own supply-chain policy.
- We do **not** ship a third-party pen-test yet (todo.md Phase 10) — that is a
  **[BLOCKER]** before any real-hardware pilot.
