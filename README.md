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
