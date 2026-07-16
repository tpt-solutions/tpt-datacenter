# Hardware Bring-Up Guide (Real Mode)

How to run TPT DataCenter against **real** PDU / UPS / cooling / compute
hardware using the `real` feature of `rust-edge` and the Real HAL backends.
This complements the engineering checklist in `docs/real-hardware-bringup.md`
— read that first for the gated test plan.

> Before any real deployment, close the **[BLOCKER]** items in
> `docs/security/threat-model.md` (network isolation, RBAC/mTLS, signed
> firmware, third-party pen-test).

## 0. Network segmentation (required)

Several of the protocols below carry **no transport encryption and no
authentication** by design. A successful attacker on the same segment can read
telemetry and issue actuation commands. These assumptions are inherent to the
protocols (not bugs in this codebase) and must be enforced by the deployment
network:

- **Redfish over HTTP (Basic-Auth)** — when the BMC does not offer HTTPS,
  credentials and commands traverse the wire in cleartext. Treat the management
  path as fully trusted and isolated.
- **Modbus TCP (port 502)** — no auth, no encryption. Any host that can reach
  the PLC can read/coil-write.
- **IPMI / RMCP (port 623)** — IPMI 2.0 RAKP still exchanges key material over
  a weak hash; disable Cipher 0.

**Rule:** all HAL/Redfish/Modbus/IPMI traffic is confined to a dedicated,
firewalled **management LAN/VLAN** that is *not* routable from tenant or
general-purpose networks. The TPT DataCenter services (edge agents, API,
dashboard) should reach the management LAN only through this controlled path.
Document the segment in your site's network diagram and review it during the
pre-pilot security review.

## 1. Build with the real feature

```bash
cargo build --release --target aarch64-unknown-linux-gnu \
  -p rust-edge --features real
```

For bare-metal UPS/cooling controllers, also verify the `no_std` core compiles:

```bash
cargo build -p rust-edge --no-default-features --features no_std
```

## 2. Configure the HAL

`rust-edge` selects a backend per device via `HalConfig`
(`rust-edge/src/hal/config.rs`). A mixed deployment can run some racks in
Simulator mode and others against real hardware:

```json
{
  "default": { "backend": "simulator", "racks": 4 },
  "devices": {
    "pdu-1": {
      "backend": "real",
      "redfish": { "host": "bmc-pdu-1", "port": 443 },
      "modbus":  { "host": "modbus-1",   "port": 502 },
      "ipmi":    { "host": "bmc-1",      "port": 623 },
      "devices": [
        { "id": "pdu-1", "protocol": "modbus", "sensors": ["outlet_1", "current_a"] }
      ]
    }
  }
}
```

Build it with `HalConfig::build_root` (single backend) or `RoutingHal::build`
(per-device routing).

## 3. Per-protocol setup

### Redfish (compute BMCs)
- Use HTTPS where the BMC supports it; set `REDFISH_URL`, `REDFISH_USER`,
  `REDFISH_PASS`, `REDFISH_OEM` (`dell` | `hpe` | `supermicro`).
- The client (`rust-edge/src/hal/real/redfish.rs`) maps `ControlCommand` onto
  the chassis Thermal/Power resources and vendor Oem power-limit extensions in
  the Hardware Management API.

### Modbus TCP (PDUs)
- Place the controller on a dedicated VLAN with strict ACLs (Modbus has no
  auth). Enumerate holding/coil registers in the device `sensors` list.
- Verify a read + a single non-critical outlet write before widening scope.

### IPMI (compute power)
- The in-process RMCP session is a conformant stub; the Hardware Management API
  delegates `chassis power` to `ipmitool`. Ensure `ipmitool` is on the
  management host and the BMC uses IPMI 2.0 + RAKP HMAC-SHA256 (disable Cipher 0).

## 4. Sign and flash firmware

- Build reproducibly, then sign with the TPT release Ed25519 key.
- The agent verifies every update via `rust-edge::firmware::verify_update` with
  key pinning (`UpdatePolicy::pinned`). An unsigned or tampered image is
  rejected before it is written to flash.
- Publish the release public key and confirm key pinning by rebuilding with
  your own key (see `docs/security/no-backdoors.md`).

## 5. Verify safety interlocks

- Force N consecutive faults or unplug the Modbus/Redfish link → the supervisor
  must latch into `SafeState` (max cooling). Clear only via an explicit,
  audited reset.
- Confirm the orchestrator rejects commands when the edge sink is unreachable
  and records them in the audit log (see `orchestration` chaos tests).
