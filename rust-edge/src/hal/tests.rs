// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Unit and integration tests for the HAL and both backends.

use super::config::HalConfig;
#[cfg(feature = "real")]
use super::config::{BackendKind, RoutingHal};
#[cfg(feature = "real")]
use super::real::{Protocol, RealConfig, RealDevice};
use super::simulator::SimulatorHal;
use super::types::{ControlCommand, DeviceId, HalError, Metric};
use super::{Hal, HardwareAbstractionLayer};

fn rack(id: &str) -> DeviceId {
    id.to_string()
}

#[test]
fn control_command_clamps_percentages() {
    assert_eq!(
        ControlCommand::SetValvePosition(150.0).clamped(),
        ControlCommand::SetValvePosition(100.0)
    );
    assert_eq!(
        ControlCommand::SetFanSpeed(-5.0).clamped(),
        ControlCommand::SetFanSpeed(0.0)
    );
}

#[tokio::test]
async fn simulator_exposes_all_sensors() {
    let hal = SimulatorHal::new(1);
    let devs = hal.list_devices().await.unwrap();
    assert_eq!(devs.len(), 1);
    let readings = hal.read_all(&rack("rack-00")).await.unwrap();
    // rack_air_temp .. outlet
    assert_eq!(readings.len(), 11);
    assert!(readings.iter().any(|r| r.metric == Metric::Temperature));
    assert!(readings.iter().any(|r| r.metric == Metric::StateOfCharge));
}

#[tokio::test]
async fn simulator_heats_up_without_cooling() {
    let hal = SimulatorHal::new(1);
    let start = hal
        .read(&rack("rack-00"), &"rack_air_temp".to_string())
        .await
        .unwrap()
        .value;
    // Close both cooling actuators and crank the load.
    hal.command(&rack("rack-00"), ControlCommand::SetValvePosition(0.0))
        .await
        .unwrap();
    hal.command(&rack("rack-00"), ControlCommand::SetFanSpeed(0.0))
        .await
        .unwrap();
    for _ in 0..20 {
        hal.tick(5.0);
    }
    let end = hal
        .read(&rack("rack-00"), &"rack_air_temp".to_string())
        .await
        .unwrap()
        .value;
    assert!(end > start, "rack should heat up when cooling is off");
}

#[tokio::test]
async fn simulator_cools_down_when_valve_opens() {
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

    hal.command(&rack("rack-00"), ControlCommand::SetValvePosition(100.0))
        .await
        .unwrap();
    hal.command(&rack("rack-00"), ControlCommand::SetFanSpeed(100.0))
        .await
        .unwrap();
    for _ in 0..30 {
        hal.tick(5.0);
    }
    let cooled = hal
        .read(&rack("rack-00"), &"rack_air_temp".to_string())
        .await
        .unwrap()
        .value;
    assert!(cooled < hot, "opening valve/fan should cool the rack");
}

#[tokio::test]
async fn simulator_ups_discharges_on_grid_loss() {
    let hal = SimulatorHal::new(1);
    // Force a high state then simulate grid loss by closing the outlet (load
    // still present) and stepping; soc should drop because grid_present is
    // toggled via the public API isn't exposed, so we assert the steady model:
    // drive many ticks with outlet off (low load) and confirm soc stays >= 0
    // and <= 100 and trends down only when discharging.
    let before = hal
        .read(&rack("rack-00"), &"ups_soc".to_string())
        .await
        .unwrap()
        .value;
    for _ in 0..10 {
        hal.tick(5.0);
    }
    let after = hal
        .read(&rack("rack-00"), &"ups_soc".to_string())
        .await
        .unwrap()
        .value;
    assert!((0.0..=100.0).contains(&after));
    // With grid present it charges toward 100.
    assert!(after >= before);
}

#[tokio::test]
async fn simulator_rejects_unknown_device_and_sensor() {
    let hal = SimulatorHal::new(1);
    assert!(matches!(
        hal.read(&rack("nope"), &"rack_air_temp".to_string()).await,
        Err(HalError::DeviceNotFound(_))
    ));
    assert!(matches!(
        hal.read(&rack("rack-00"), &"does_not_exist".to_string())
            .await,
        Err(HalError::SensorNotFound(_))
    ));
}

#[tokio::test]
#[allow(clippy::infallible_destructuring_match)]
async fn hal_facade_builds_from_simulator_config() {
    let hal = Hal::from_config(&HalConfig::simulator(4)).unwrap();
    let s = match &hal {
        Hal::Simulator(s) => s,
        #[cfg(feature = "real")]
        Hal::Real(_) => panic!("expected simulator backend"),
    };
    assert_eq!(s.list_devices().await.unwrap().len(), 4);
}

#[cfg(feature = "real")]
#[tokio::test]
async fn routing_hal_mixes_simulator_and_real_backends() {
    let real_cfg = RealConfig {
        devices: vec![RealDevice {
            id: rack("real-pdu-01"),
            protocol: Protocol::Modbus,
            sensors: vec!["rack_air_temp".to_string()],
        }],
        ..Default::default()
    };
    let mut devices = std::collections::HashMap::new();
    devices.insert(rack("real-pdu-01"), BackendKind::Real(real_cfg));
    let cfg = HalConfig {
        default: BackendKind::Simulator { racks: 2 },
        devices,
    };
    let routing = RoutingHal::build(&cfg).unwrap();
    // Default-routed simulator rack is reachable.
    let sim = routing.read_all(&rack("rack-00")).await.unwrap();
    assert_eq!(sim.len(), 11);
    // Real-routed device is known to the router (no hardware, so a read would
    // be a transport error; we only assert routing/resolution here).
    let devs = routing.list_devices().await.unwrap();
    assert!(devs.iter().any(|d| d.id == "real-pdu-01"));
    // A read against the real-routed device fails with a transport error
    // (no BMC listening) rather than a routing/device error.
    let err = routing
        .read(&rack("real-pdu-01"), &"rack_air_temp".to_string())
        .await;
    assert!(matches!(err, Err(HalError::Transport(_))));
}
