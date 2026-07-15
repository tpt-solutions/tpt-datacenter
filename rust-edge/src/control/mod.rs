// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Deterministic control-loop framework for the Real-Time Facility Edge.
//!
//! This module is the Phase 2 surface: a small, auditable framework that runs
//! a [`ControlAgent`] (PDU, UPS/BMS, cooling valve/fan) against any
//! [`HardwareAbstractionLayer`] backend, guarded by [`safety`] interlocks and a
//! [`supervisor`] that emits heartbeats and falls back to a safe state on
//! comms loss.
//!
//! The control *laws* ([`pid`], [`safety`], [`agents`]) are pure arithmetic over
//! a [`Snapshot`] and contain no I/O, which is what keeps them deterministic and
//! unit-testable. The [`supervisor`] is the only part that touches the async HAL
//! and the wall clock.

pub mod agents;
pub mod pid;
pub mod safety;
pub mod supervisor;

#[cfg(test)]
mod tests;

use std::collections::HashMap;

use crate::hal::types::{DeviceId, Reading, SensorId};

/// A point-in-time view of every channel a device exposes.
///
/// Agents and the [`safety`] enforcer reason only over a `Snapshot`; they never
/// see the concrete HAL backend, so the same control law runs unchanged against
/// the Simulator or real hardware.
#[derive(Debug, Clone)]
pub struct Snapshot {
    pub device: DeviceId,
    pub timestamp_ms: u64,
    readings: HashMap<SensorId, Reading>,
}

impl Snapshot {
    /// Build a snapshot from a `read_all` result.
    pub fn from_readings(device: DeviceId, readings: Vec<Reading>, timestamp_ms: u64) -> Self {
        let readings = readings
            .into_iter()
            .map(|r| (r.sensor.clone(), r))
            .collect();
        Snapshot {
            device,
            timestamp_ms,
            readings,
        }
    }

    /// Raw value of a sensor, if present.
    pub fn get(&self, sensor: &str) -> Option<f64> {
        self.readings.get(sensor).map(|r| r.value)
    }

    /// Rack air temperature (°C), if the device exposes it.
    pub fn temp_c(&self) -> Option<f64> {
        self.get("rack_air_temp")
    }

    /// Inlet (supply) air temperature (°C).
    pub fn inlet_temp_c(&self) -> Option<f64> {
        self.get("inlet_temp")
    }

    /// Liquid coolant supply temperature (°C).
    pub fn coolant_supply_temp_c(&self) -> Option<f64> {
        self.get("coolant_supply_temp")
    }

    /// Current valve position (% open).
    pub fn valve_position(&self) -> Option<f64> {
        self.get("valve_position")
    }

    /// Current fan speed (% of maximum).
    pub fn fan_speed(&self) -> Option<f64> {
        self.get("fan_speed")
    }

    /// PDU bus voltage (V).
    pub fn pdu_voltage(&self) -> Option<f64> {
        self.get("pdu_voltage")
    }

    /// PDU current draw (A).
    pub fn pdu_current(&self) -> Option<f64> {
        self.get("pdu_current")
    }

    /// IT load (W).
    pub fn it_load_w(&self) -> Option<f64> {
        self.get("it_load")
    }

    /// UPS battery state of charge (%).
    pub fn ups_soc(&self) -> Option<f64> {
        self.get("ups_soc")
    }
}
