# Security Policy

## Supported Versions

TPT DataCenter is pre-alpha. Only the latest `main` branch is supported for
security fixes. Once a stable release line exists, supported versions will be
listed here.

| Version | Supported |
|---|---|
| `main`  | ✅ |
| others  | ❌ |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, report them privately using one of the following channels:

- **GitHub private vulnerability reporting**: open a security advisory via the
  "Security" tab → "Report a vulnerability".
- **Email**: security@tpt.example (PGP key to be published here).

Please include:

- A description of the vulnerability and its impact.
- Steps to reproduce or a proof of concept.
- Affected component(s) and version(s) (`rust-edge`, `ai-brain`, `go-telemetry`,
  `api`, `dashboard`, etc.).
- Any suggested mitigation, if known.

You will receive an acknowledgment within **5 business days**. We aim to provide
a substantive response and a remediation timeline within **30 days**.

## Disclosure Policy

- We practice **coordinated disclosure**: fixes are developed privately and
  released before public details are shared.
- We will credit reporters who wish to be acknowledged, unless they prefer to
  remain anonymous.

## Scope & Attack Surface

Areas of particular interest for this project (see `spec.txt` and `todo.md`):

- Redfish / Modbus TCP / IPMI attack surface (Phase 7, Phase 10)
- Edge agent firmware memory-safety and secure update mechanism (Phase 2, Phase 10)
- Authentication/authorization and audit logging for the platform API (Phase 8)
- "No backdoors" verifiability story: reproducible builds and open audit process

## Security Best Practices for Contributors

- All code must be memory-safe where feasible; Rust components avoid `unsafe`
  unless justified and reviewed.
- New source files must carry SPDX license headers.
- Dependencies are pinned and audited; run `cargo audit` and `go vet` in CI.
- Secrets, private keys, and credentials must never be committed.
