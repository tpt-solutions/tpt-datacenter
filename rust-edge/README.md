# rust-edge

Real-Time Facility Edge agents (Rust). Bare-metal/no_std control loops for PDUs,
UPS/BMS, and cooling valves, running on Raspberry Pi CM4 / industrial ARM controllers.

See [todo.md](../todo.md) Phase 1 (HAL) and Phase 2 (edge agents).

## Hardware Abstraction Layer (`hal`)

The `hal` module is the hosted/development surface and the foundation of Phase 1:

- `hal::types` — common data model: [`Reading`] (telemetry: temperature, voltage,
  amperage, airflow, power, SoC, fan/valve %) and [`ControlCommand`] (valve, fan,
  PDU outlet, UPS discharge), plus [`HalError`].
- `hal::HardwareAbstractionLayer` — the single trait every backend implements
  (`read`, `read_all`, `command`, `list_devices`).
- `hal::simulator::SimulatorHal` — in-process virtual facility with a lumped
  thermal/electrical physics model (heat load, cooling valve + fan, PDU, UPS/BMS).
- `hal::real` — Real-hardware backends (Redfish, Modbus TCP, IPMI) behind the same
  trait, enabled by the `real` Cargo feature.
- `hal::config` — config-driven backend selection via `HalConfig` / `RoutingHal`,
  supporting both per-deployment and per-device Simulator↔Real switching.

```rust
use rust_edge::hal::{Hal, HardwareAbstractionLayer};

let hal = Hal::simulator(4);              // 4 virtual racks
let temp = hal.read(&"rack-00".into(), &"rack_air_temp".into()).await?;
hal.command(&"rack-00".into(), rust_edge::hal::types::ControlCommand::SetValvePosition(80.0)).await?;
```

## Control framework (`control`)

Phase 2: deterministic, auditable control loops that run any
[`HardwareAbstractionLayer`] backend, guarded by safety interlocks.

- `control::pid` — discrete PID controller (pure arithmetic, no I/O).
- `control::safety` — [`SafetyEnforcer`] trips to a [`SafeState`] on a critical
  breach (over-temp, over/under-voltage, overload) and clamps every requested
  command to the physical envelope.
- `control::agents` — the per-device control laws: [`CoolingAgent`] (PID on rack
  air temperature driving valve + fan), [`PduAgent`] (electrical over/under-voltage
  and overload interlock that sheds the outlet and never auto-restores), and
  [`UpsAgent`] (state-of-charge based discharge limiting). [`CompositeAgent`]
  runs several on one device.
- `control::supervisor` — the loop driver. Per cycle it reads the device, asks
  the agent for commands, enforces safety, and writes them. On comms loss it
  applies the fail-safe state blindly and latches after N consecutive failures;
  it emits a [`Heartbeat`] every `heartbeat_every` cycles.

```rust
use rust_edge::control::supervisor::{rack_supervisor, SupervisorOptions};
use rust_edge::control::safety::SafeState;

let opts = SupervisorOptions {
    tick_period_ms: 1000,
    heartbeat_every: 20,
    max_consecutive_errors: 5,
    fail_safe: SafeState::FullCooling,
};
let mut sup = rack_supervisor("rack-00".to_string(), 27.0, opts);
// In a real runner: loop `sup.run_once(&hal, now_ms).await` and advance time.
```

The `tpt-edge` binary is the Phase 2 bench test: it boots a virtual facility in
the Simulator, heats every rack, then runs the rack supervisor for a fixed number
of cycles and prints heartbeats + the final thermal state.

```sh
cargo run -p rust-edge --bin tpt-edge    # demonstrates cooling toward setpoint
```

Build/test:

```sh
cargo test -p rust-edge                 # Simulator backend (default features)
cargo test -p rust-edge --features real # include Real backends (Redfish/Modbus/IPMI)
cargo run -p rust-edge --bin tpt-edge   # run the edge demo against the Simulator
```
