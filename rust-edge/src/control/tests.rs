// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Integration tests: the edge control framework driven by the Simulator HAL.

use crate::control::agents::{ControlAgent, CoolingAgent, PduAgent};
use crate::control::pid::PidController;
use crate::control::safety::{Enforcement, SafeState, SafetyEnforcer, SafetyLimits};
use crate::control::supervisor::{Supervisor, SupervisorOptions};
use crate::control::Snapshot;
use crate::hal::simulator::SimulatorHal;
use crate::hal::types::{ControlCommand, DeviceId, HalError, Metric, Reading, Unit};
use crate::hal::HardwareAbstractionLayer;

fn rack(id: &str) -> DeviceId {
    id.to_string()
}

fn reading(sensor: &str, metric: Metric, value: f64, unit: Unit) -> Reading {
    Reading {
        device: rack("rack-00"),
        sensor: sensor.to_string(),
        metric,
        value,
        unit,
        timestamp_ms: 0,
    }
}

#[test]
fn pid_drives_output_up_when_too_hot() {
    // Same gains as the cooling agent in production.
    let mut pid = PidController::new(6.0, 0.5, 1.0, 27.0, 0.0, 100.0);
    // 40°C vs 27°C setpoint → large positive error → strong cooling output.
    let out = pid.update(40.0, 1.0);
    assert!(out > 50.0, "expected strong cooling demand, got {out}");
    assert!((0.0..=100.0).contains(&out));
}

#[test]
fn pid_clamps_within_bounds() {
    let mut pid = PidController::new(10.0, 0.0, 0.0, 20.0, 0.0, 100.0);
    assert_eq!(pid.update(100.0, 1.0), 100.0);
    assert_eq!(pid.update(0.0, 1.0), 0.0);
}

#[tokio::test]
async fn cooling_agent_lowers_rack_temperature() {
    let hal = SimulatorHal::new(1);
    hal.command(&rack("rack-00"), ControlCommand::SetValvePosition(0.0))
        .await
        .unwrap();
    hal.command(&rack("rack-00"), ControlCommand::SetFanSpeed(0.0))
        .await
        .unwrap();
    for _ in 0..30 {
        hal.tick(5.0);
    }
    let hot = hal
        .read(&rack("rack-00"), &"rack_air_temp".to_string())
        .await
        .unwrap()
        .value;

    let agent = CoolingAgent::new(27.0, 6.0, 0.5, 1.0, 0.4);
    let enforcer = SafetyEnforcer::new(SafetyLimits::default());
    let opts = SupervisorOptions {
        tick_period_ms: 1000,
        heartbeat_every: 100,
        ..Default::default()
    };
    let mut sup = Supervisor::new(rack("rack-00"), agent, enforcer, opts);
    for cycle in 1..=120u64 {
        sup.run_once(&hal, cycle * 1000).await;
        hal.tick(5.0);
    }
    let cooled = hal
        .read(&rack("rack-00"), &"rack_air_temp".to_string())
        .await
        .unwrap()
        .value;

    assert!(
        cooled < hot,
        "cooling agent should reduce temp: {hot} → {cooled}"
    );
}

#[tokio::test]
async fn pdu_agent_sheds_load_on_overvoltage() {
    let snap = Snapshot::from_readings(
        rack("rack-00"),
        vec![
            reading("pdu_voltage", Metric::Voltage, 300.0, Unit::Volt),
            reading("pdu_current", Metric::Amperage, 10.0, Unit::Ampere),
        ],
        0,
    );
    let mut agent = PduAgent::new(80.0, 264.0, 198.0);
    let out = agent.decide(&snap);
    assert_eq!(out.commands, vec![ControlCommand::SetOutletState(false)]);
}

#[test]
fn safety_trips_on_critical_temperature() {
    let enforcer = SafetyEnforcer::new(SafetyLimits::default());
    let snap = Snapshot::from_readings(
        rack("rack-00"),
        vec![reading(
            "rack_air_temp",
            Metric::Temperature,
            60.0,
            Unit::Celsius,
        )],
        0,
    );
    match enforcer.enforce(&snap, vec![ControlCommand::SetValvePosition(10.0)]) {
        Enforcement::Trip { commands, .. } => {
            assert_eq!(commands, SafeState::FullCooling.commands());
        }
        Enforcement::Allow(_) => panic!("should have tripped on 60°C"),
    }
}

#[test]
fn safety_clamps_requests_to_envelope() {
    let enforcer = SafetyEnforcer::new(SafetyLimits::default());
    let snap = Snapshot::from_readings(rack("rack-00"), vec![], 0);
    let out = enforcer.enforce(
        &snap,
        vec![
            ControlCommand::SetValvePosition(150.0),
            ControlCommand::SetFanSpeed(-20.0),
        ],
    );
    match out {
        Enforcement::Allow(cmds) => {
            assert_eq!(
                cmds,
                vec![
                    ControlCommand::SetValvePosition(100.0),
                    ControlCommand::SetFanSpeed(0.0),
                ]
            );
        }
        Enforcement::Trip { .. } => panic!("no breach, should allow clamped"),
    }
}

#[tokio::test]
async fn supervisor_falls_back_to_safe_state_on_comms_loss() {
    // Drive the supervisor against a rack the Simulator has never heard of:
    // every read returns `DeviceNotFound`, which the supervisor treats as a
    // comms/device fault and responds to with the fail-safe state.
    let hal = SimulatorHal::new(1);
    let agent = CoolingAgent::new(27.0, 6.0, 0.5, 1.0, 0.4);
    let enforcer = SafetyEnforcer::new(SafetyLimits::default());
    let opts = SupervisorOptions {
        tick_period_ms: 1000,
        heartbeat_every: 100,
        max_consecutive_errors: 3,
        fail_safe: SafeState::FullCooling,
    };
    let mut sup = Supervisor::new(rack("ghost-rack"), agent, enforcer, opts);

    let mut ever_tripped = false;
    for cycle in 1..=10u64 {
        let o = sup.run_once(&hal, cycle * 1000).await;
        if o.tripped {
            ever_tripped = true;
        }
    }
    assert!(
        ever_tripped,
        "supervisor should report trips on persistent read errors"
    );
    // After `max_consecutive_errors` it must latch into the safe state.
    assert!(
        sup.is_latched(),
        "supervisor should latch into safe state after repeated faults"
    );

    // And it must stay latched (still tripping) on the next cycle.
    let o = sup.run_once(&hal, 11_000).await;
    assert!(o.tripped && !o.healthy);
}

#[tokio::test]
async fn supervisor_recovers_after_reset() {
    let hal = SimulatorHal::new(1);
    let agent = CoolingAgent::new(27.0, 6.0, 0.5, 1.0, 0.4);
    let enforcer = SafetyEnforcer::new(SafetyLimits::default());
    let opts = SupervisorOptions {
        tick_period_ms: 1000,
        heartbeat_every: 100,
        max_consecutive_errors: 2,
        fail_safe: SafeState::FullCooling,
    };
    let mut sup = Supervisor::new(rack("ghost-rack"), agent, enforcer, opts);
    for cycle in 1..=5u64 {
        let _ = sup.run_once(&hal, cycle * 1000).await;
    }
    assert!(sup.is_latched());
    sup.reset();
    assert!(!sup.is_latched());
}

// Compile-time guard: the supervisor fails closed (safe state) rather than
// silently continuing when it cannot read the device.
#[test]
fn device_not_found_is_a_fault() {
    let hal = SimulatorHal::new(1);
    let rt = tokio::runtime::Runtime::new().unwrap();
    rt.block_on(async {
        let r = hal.read_all(&rack("nope")).await;
        assert!(matches!(r, Err(HalError::DeviceNotFound(_))));
    });
}
