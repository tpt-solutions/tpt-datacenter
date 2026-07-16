# TPT DataCenter — Threat Model

**Scope:** Redfish / Modbus TCP / IPMI southbound attack surface, the edge
agents (`rust-edge`), the API/orchestration layer (`api`), and the dashboard
(`dashboard`). Covers todo.md **Phase 10**.

This is a pre-pilot threat model. Items tagged **[BLOCKER]** must be closed
before any real-hardware pilot (Phase 13).

## Assets

- Physical plant: PDU outlets, UPS/BMS, cooling valves/fans, compute servers.
- Telemetry: temperature, voltage, amperage, airflow, power, SOC.
- Control authority: the ability to drive actuators (valve, fan, outlet,
  discharge limit, server power, CPU power cap).
- Firmware images flashed to edge controllers.
- Audit log integrity (proves who changed what, when).

## Trust boundaries

1. **Operator → Control API.** A human with a bearer token can override
   actuators. Token compromise = full local control. Mitigated by bearer auth
   (constant-time compare), the safety envelope clamp, and the append-only
   audit log. **[BLOCKER before pilot]** add role-based access + mutual TLS.
2. **Dashboard → APIs.** Same origin via the dashboard reverse proxy; in dev
   the API may be CORS-open. Prod must run behind the proxy (no public CORS).
3. **API → Edge HAL (rust-edge).** The orchestrator forwards commands to edge
   supervisors. The seam is `HalCommandSink` (`api/internal/orchestration`);
   in Real mode this crosses a network boundary. **[BLOCKER]** authenticate and
   integrity-protect this channel (mTLS or signed commands).
4. **Edge → Real hardware (Redfish / Modbus / IPMI).**
   - **Redfish** typically serves plain HTTP on an isolated BMC LAN. No TLS,
     Basic auth only → trivially sniffable/MITM-able if the mgmt LAN is not
     isolated. **[BLOCKER]** isolate the BMC/management network; prefer Redfish
     over HTTPS where the BMC supports it.
   - **Modbus TCP** has **no authentication or encryption** by design. Any host
     on the Modbus VLAN can read/coil-write. **[BLOCKER]** place Modbus on a
     dedicated VLAN with strict ACLs; treat every peer as untrusted.
   - **IPMI** (IPMI-over-LAN) uses RMCP with weak (MD5/RAKP) auth historically;
     our client currently delegates to `ipmitool` and the in-process session is
     a stub. **[BLOCKER]** require IPMI 2.0 + RAKP HMAC-SHA256, disable Cipher 0.
5. **Telemetry ingestion → QuestDB.** ILP (port 9009) and REST (9000) are
   unauthenticated by default. ILP injection can spoof readings → poison the AI
   brain. Mitigated by the envelope clamp on the *write* path (commands), but
   *readings* are trusted. **[BLOCKER]** bind QuestDB to the ingest network
   only; authenticate REST.
6. **Firmware supply chain.** A forged image could subvert an edge controller.
   Mitigated by Ed25519 signed updates (`rust-edge::firmware`) with key
   pinning. **[BLOCKER]** protect the release signing key (offline/HSM); publish
   the public key and a transparency log.

## Threats (STRIDE)

| # | Threat | Surface | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- | --- | --- |
| T1 | Spoofed telemetry | QuestDB ILP | Med | High | Network isolation; signed/poison-evicted dead-letter; anomaly alerts |
| T2 | Unauthenticated Modbus coil write | Modbus VLAN | High | High | Dedicated VLAN + ACLs; fail-safe latch on comms loss |
| T3 | Redfish MITM / credential theft | BMC LAN | Med | High | Isolated mgmt LAN; Redfish HTTPS; vault-stored creds |
| T4 | IPMI Cipher-0 / RAKP downgrade | BMC | Med | High | IPMI 2.0 HMAC-SHA256 only; disable weak ciphers |
| T5 | Forged firmware | Update channel | Low | Critical | Ed25519 signed updates + key pinning (`firmware`) |
| T6 | Token/key theft → actuator takeover | Control API | Med | High | RBAC + mTLS; short-lived tokens; audit log |
| T7 | AI-brain command abuse | Orchestrator | Low | Med | `GuardedPolicy` envelope + operator override; audit |
| T8 | Denial of service (ingest flood) | Telemetry bus | Med | Med | Bounded backpressure channel; worker pool cap |

## Residual risk before pilot

All **[BLOCKER]** items above, plus an independent third-party pen-test
(todo.md Phase 10) of the Redfish/Modbus/IPMI surface and the control/orch
APIs. No real-hardware pilot begins until the pen-test is clean and the
network isolation controls are verified on-site.
