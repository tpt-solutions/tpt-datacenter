# Architecture

TPT DataCenter is an open-source, AI-driven physical plant controller for
hyperscale data centers. It manages power and cooling — PDUs, UPS/BMS, cooling
valves/fans, and compute servers — behind a single control and observability
surface, with an RL "AI brain" that optimizes cooling energy use.

## Components

```
                ┌──────────────────────────────────────────────────────────┐
                │                      Dashboard (web)                       │
                │   telemetry · heatmap · topology · override · alerts       │
                └───────────────┬──────────────────────────────────────────┘
                                │  (reverse-proxied, same origin)
        ┌───────────────┬───────┴────────┬─────────────────┬───────────────┐
        ▼               ▼                ▼                 ▼               ▼
  Control API    Hardware Mgmt API   Telemetry Query    Topology API    Orchestrator
  (manual        (Redfish/IPMI,      (QuestDB REST)     (graph)         (routing +
   override+     CPU throttle,                                         policy+audit)
   audit)         grid-stress)
        │               │                │                 │               │
        └───────────────┴────────────────┴─────────────────┴───────┬───────┘
                                │ routes commands                    │
                                ▼                                     ▼
                       Edge Agents (rust-edge)              Telemetry Ingest (go-telemetry)
                       supervisors: PDU / UPS / Cooling     Redfish · Modbus · IPMI → QuestDB (ILP)
                                │
                        ┌───────┴────────┐
                        ▼                ▼
                  HAL: Simulator    HAL: Real (Redfish/Modbus/IPMI)
                        │                │
                        └───────┬────────┘
                                ▼
                   Physical plant: racks, PDUs, UPS, cooling loops, servers
```

| Component | Language | Role | Phase |
| --- | --- | --- | --- |
| `rust-edge` | Rust (`no_std`-capable) | Edge control-loop supervisors (PDU/UPS/Cooling), safety interlocks, HAL (Simulator + Real backends), signed firmware (`firmware`). | 1, 2, 10 |
| `go-telemetry` | Go | Concurrent ingestion (Redfish/Modbus/IPMI → QuestDB), storage, query API, topology graph. | 3, 4, 5 |
| `ai-brain` | Rust (`burn`) | RL thermal model: state/action/reward, training env on the Simulator HAL, hotspot net, `GuardedPolicy`, serving `Command`s. | 6 |
| `api` | Go | Core platform: manual override + audit (`control`), Hardware Management API (`hardware`), orchestration (`orchestration`), gRPC contract (`proto`). | 7, 8 |
| `dashboard` | HTML/JS + Go | Operator SPA + reverse proxy. | 9 |

## Data flow

1. **Telemetry in:** collectors poll the HAL/physical plant, points flow over a
   bounded backpressure bus to QuestDB (ILP). The topology graph is authored
   from a JSON spec and enriched by discovery.
2. **Decision:** the AI brain (or an operator) produces actuator commands. The
   AI brain's `Command` converts into `rust_edge::hal::types::ControlCommand`
   via `From<ai_brain::serve::Command>`.
3. **Routing:** the orchestrator enforces the safety/policy envelope, records an
   audit entry, and forwards the command to the target device's edge
   supervisor (`HalCommandSink`).
4. **Actuation:** the supervisor drives the HAL (`command`), which talks to the
   Simulator or Real backend. Safety interlocks latch to `SafeState` on
   consecutive faults or comms loss.
5. **Observability:** the dashboard reads telemetry/heatmap/topology/alerts and
   issues overrides through the control API.

## Safety model

- Every actuator path (operator override, AI command, hardware API) is clamped
  to the same envelope (`control.DefaultLimits` / `orchestration.Policy` /
  `ai-brain` `GuardedPolicy`).
- Fail-safe is the *default*: latched `SafeState` drives cooling to max.
- Every control action is append-only audited with device, command, value,
  operator/source, and timestamp.

See `docs/security/threat-model.md` and `docs/security/no-backdoors.md` for the
security and verifiability story.
