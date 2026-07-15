# TPT DataCenter

Open-source, AI-driven physical plant controller for hyperscale data centers.
See [spec.txt](spec.txt) for the design document and [todo.md](todo.md) for the
phased project checklist.

Dual-licensed under MIT OR Apache-2.0. Copyright TPT Solutions.

## Repo layout (monorepo)

| Directory | Purpose |
|---|---|
| [rust-edge/](rust-edge/) | Real-Time Facility Edge agents (Rust, no_std) for PDUs, UPS/BMS, cooling valves |
| [go-telemetry/](go-telemetry/) | Telemetry Ingestion Engine (Go) — Redfish/Modbus TCP/IPMI into QuestDB |
| [ai-brain/](ai-brain/) | Thermal AI Brain — Rust (`burn` framework) RL model for hotspot prediction and cooling optimization, structured as a publishable crate |
| [topology-graph/](topology-graph/) | Physical Topology Graph (racks, PDUs, cooling loops, cabling) |
| [api/](api/) | Core platform gRPC API, orchestration, and Hardware Management API (Redfish/IPMI) |
| [dashboard/](dashboard/) | Web dashboard for facility operators |
| docs/ | Architecture and operational documentation |

A monorepo was chosen over polyrepo because the components share protobuf
schemas and a common Simulator backend, and the project is early-stage with a
single release cadence — see todo.md Phase 0.

## License

TPT DataCenter is dual-licensed under either of:

- [MIT License](LICENSE-MIT)
- [Apache License, Version 2.0](LICENSE-APACHE)

at your option.

Copyright (c) 2024 TPT Solutions.

Unless you explicitly state otherwise, any contribution intentionally submitted
for inclusion in the work by you shall be dual-licensed as above, without any
additional terms or conditions.

### SPDX headers

Every source file should carry an SPDX short-form header, for example:

```text
// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0
```

(Rust/Go use `//`; shell/yml use `#`; and so on.) CI enforces header presence
for new files — see `.github/workflows/ci.yml`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, branching, and
PR guidelines, [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community standards,
and [SECURITY.md](SECURITY.md) for the vulnerability disclosure policy.
