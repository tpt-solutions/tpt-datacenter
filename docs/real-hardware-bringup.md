# Real-Hardware Bring-Up Test Plan

This is the **Phase 11** bring-up checklist for deploying TPT DataCenter edge
agents in **Real mode** (the `real` feature of `rust-edge`) and the Real HAL
backends (Redfish / Modbus TCP / IPMI). It is a prerequisite for the Phase 13
field pilot.

> Prerequisite: all **[BLOCKER]** items in `docs/security/threat-model.md`
> (network isolation, RBAC/mTLS, signed firmware, third-party pen-test) must be
> closed before any non-simulator deployment.

## 0. Prerequisites

- [ ] Target controllers physically installed: at least one Raspberry Pi CM4 /
      industrial ARM controller per rack zone.
- [ ] BMC/management LAN isolated from the data plane; Modbus on a dedicated
      VLAN with strict ACLs.
- [ ] TPT release public key published; edge firmware built reproducibly and
      signed (`rust-edge::firmware`).
- [ ] QuestDB, telemetry, API, and dashboard services reachable from the
      orchestration host.

## 1. Simulator parity (gate)

Run the full stack in Simulator mode (`-mode=sim`) and confirm:

- [ ] Edge supervisors reach steady-state thermal control (peak → setpoint).
- [ ] AI brain serves commands within the `GuardedPolicy` envelope.
- [ ] Orchestrator routes overrides end-to-end (see `orchestration` e2e tests).
- [ ] Dashboard shows telemetry, heatmap, topology, and overrides.

No Real-mode work starts until Simulator parity is green.

## 2. HAL backend bring-up (per protocol)

### Redfish (compute BMCs)
- [ ] `REDFISH_URL` reachable over HTTPS; `REDFISH_USER`/`REDFISH_OEM` set
      (dell | hpe | supermicro).
- [ ] `GET /redfish/v1/Systems/{id}` returns power state.
- [ ] `POST .../Actions/ComputerSystem.Reset` acknowledged.
- [ ] CPU power cap (`SetCPUPowerCap`) reflected in BMC telemetry.

### Modbus TCP (PDUs)
- [ ] Controller on the Modbus VLAN; holding/coil registers enumerated.
- [ ] Read of outlet state + write of outlet command verified on a **non-
      critical** PDU first.
- [ ] Comms-loss test: unplug link → supervisor latches to `SafeState`.

### IPMI (compute power)
- [ ] `ipmitool` present on the management host (or RMCP session delegated).
- [ ] `chassis power on/off/cycle` works against a test server.
- [ ] Weak ciphers disabled on BMC.

## 3. Edge agent deployment

- [ ] Cross-compile: `cargo build --release --target aarch64-unknown-linux-gnu
      -p rust-edge --features real`.
- [ ] Flash signed image; confirm boot + heartbeat.
- [ ] `RoutingHal` config maps each device to its backend (simulator for
      unverified racks, real for verified ones).
- [ ] Safety interlock: force N consecutive faults → verify latch into
      `SafeState` (max cooling).

## 4. Chaos / failure testing (Real mode)

- [ ] Sensor dropout: zero a sensor → agent degrades gracefully, no crash.
- [ ] Agent crash recovery: kill `tpt-edge` → supervisor restarts, resumes
      auto mode.
- [ ] Orchestrator ↔ edge link loss → command rejected + audited (see
      `TestChaos_SinkFailure`), no silent apply.
- [ ] Firmware rollback: feed an old-but-valid signed image → accepted; feed an
      unsigned image → rejected by `verify_update`.

## 5. Performance & savings validation

- [ ] Telemetry ingestion throughput meets target (millions of pts/sec) under
      representative load (`go run ./cmd/loadtest`).
- [ ] AI-brain energy savings (20–30%) reproduced against Real physics over a
      multi-day window, compared to the legacy baseline.
- [ ] Command latency (override → actuator) within SLA (< 2s on the mgmt LAN).

## 6. Sign-off

- [ ] Independent pen-test clean (Phase 10).
- [ ] Operations runbook + on-call documented.
- [ ] Audit log retention verified.
- [ ] Pilot scope approved (limited non-critical subset, Phase 13).
