# API Reference

TPT DataCenter exposes its control/management surface as **HTTP+JSON** (the
canonical schema of record is the proto3 contract in
`api/proto/tpt/v1/tpt.proto`, generated into `api/internal/orchestration/pb`).
The gRPC service stubs are **not yet implemented** — until then every service
below is reachable over HTTP/JSON, and `tpt.proto` is the source of truth for
field names and semantics (used today for documentation and future codegen).
All mutating endpoints require
`Authorization: Bearer <token>` (the dashboard proxy adds it from the token
field). Read endpoints (`/health`, telemetry) are bearer-optional per
deployment.

## Control API (`:8082`, `cmd/control`)

Manual override + append-only audit. State is held in memory, seeded from the
facility topology spec.

| Method | Path | Body | Description |
| --- | --- | --- | --- |
| GET | `/health` | — | Liveness. |
| GET | `/control/devices` | — | All device actuator states. |
| GET | `/control/state?device=` | — | One device state. |
| POST | `/control/override` | `{device,command,value,operator,reason}` | Clamp + apply a manual command (`valve`/`fan`/`discharge_limit` numeric, `outlet` bool). |
| POST | `/control/reset` | `{device,operator,reason}` | Return device to auto mode. |
| POST | `/control/latch` | `{device,operator,reason}` | Latch into fail-safe (max cooling). |
| GET | `/control/audit?limit=` | — | Recent audit entries (newest first). |

`command` values: `valve`, `fan`, `outlet`, `discharge_limit`. Values are
clamped to `DefaultLimits` (valve/fan/discharge 0–100; outlet true/false).

## Hardware Management API (`:8083`, `cmd/hardware`)

Compute-server management (todo.md Phase 7). Redfish/IPMI backends are enabled
via env (`REDFISH_URL`, `REDFISH_USER`, `REDFISH_PASS`, `REDFISH_OEM`;
`IPMI_HOST`, `IPMI_USER`, `IPMI_PASS`). Grid-stress defaults to a static "none"
stub.

| Method | Path | Body | Description |
| --- | --- | --- | --- |
| GET | `/health` | — | Liveness. |
| GET | `/hw/grid` | — | Current `GridStressSignal`. |
| POST | `/hw/power` | `{server,system_id,action,via,operator,note}` | Server power control via Redfish or IPMI. |
| POST | `/hw/boot` | `{server,system_id,target,...}` | Next-boot device override (PXE/disk/...). |
| POST | `/hw/powercap` | `{server,chassis_id,cap_w,...}` | CPU power cap (W), vendor-aware Oem payload. |
| POST | `/hw/throttle/resolve` | `{servers:[...]}` | Preview throttle actions implied by current grid signal + policy. |

`action` (Redfish): `On`, `Off`, `GracefulShutdown`, `GracefulRestart`,
`ForceOff`, `ForceRestart`, `PushPowerButton`. `via`: `redfish` | `ipmi` | `""`
(auto).

## Orchestrator (`:8084`, `cmd/orchestrator`)

Central routing + policy enforcement + audit (todo.md Phase 8). Forwards to a
`HalCommandSink` (Simulator sink in dev; real edge supervisors in prod).

| Method | Path | Body | Description |
| --- | --- | --- | --- |
| GET | `/health` | — | Liveness. |
| POST | `/orc/submit` | `{device,command,value,operator,reason,source}` | Route a control intent: clamp → audit → forward. |
| GET | `/orc/policy` | — | Current enforced safety/policy envelope. |
| GET | `/orc/audit?limit=` | — | Orchestration audit trail. |

The `source` field is `operator`, `ai_brain`, or `hardware_api`. With
`-two-person`, extreme valve/fan setpoints from `operator` require a second
acknowledgement (rejected otherwise; `ai_brain` bypasses).

## Telemetry Query API (`:8080`, `go-telemetry/cmd/api`)

Thin, safe proxy over QuestDB for the dashboard and AI brain.

| Method | Path | Description |
| --- | --- | --- |
| GET | `/health` | DB reachability. |
| POST | `/api/query` | Ad-hoc SQL (bearer-auth). |
| GET | `/api/latest?device=&sensor=&metric=` | Latest reading. |
| GET | `/api/timeseries?device=&sensor=&metric=&from=&to=&rollup=` | Windowed series. |
| GET | `/api/devices` | Device registry. |

## Topology API (`:8081`, `go-telemetry/cmd/topology`)

In-memory physical graph (racks/PDUs/UPS/cooling loops/servers).

| Method | Path | Description |
| --- | --- | --- |
| GET | `/topology/graph` | Full node/edge graph (dashboard render). |
| GET | `/topology/powers?id=` | What powers this device. |
| GET | `/topology/cools?id=` | What cools this device. |
| GET | `/topology/cooling?id=` | Downstream of a cooling loop. |

## gRPC contract (schema of record, not yet served)

`api/proto/tpt/v1/tpt.proto` defines `ControlService`, `HardwareService`,
`GridService`, and `Orchestrator` with the same semantics as the HTTP paths
above. The generated Go message types live in `api/internal/orchestration/pb`,
but the gRPC *service* stubs (`RegisterXxxServer`, client interfaces) are not
yet implemented — `orchestration/server.go` and the other services are plain
HTTP/JSON. The proto is kept in sync as the documented contract and will back
a future gRPC transport without changing the HTTP surface.
