// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Common data model shared across every HAL backend.
//!
//! A [`Reading`] is a single time-stamped telemetry sample (temperature,
//! voltage, amperage, airflow, …) and a [`ControlCommand`] is an actuator
//! command (open a cooling valve, spin a fan, toggle a PDU outlet, …).
//! Both the Simulator and Real backends speak exclusively in these types, so
//! the rest of the stack never needs to know which physical device produced
//! a reading.

use serde::{Deserialize, Serialize};

/// Errors returned by any HAL backend.
#[derive(Debug, thiserror::Error)]
pub enum HalError {
    /// No device with this id is known to the backend.
    #[error("device not found: {0}")]
    DeviceNotFound(DeviceId),
    /// The named sensor/actuator is not exposed by the device.
    #[error("sensor not found: {0}")]
    SensorNotFound(SensorId),
    /// The backend does not support the requested operation.
    #[error("unsupported operation: {0}")]
    Unsupported(String),
    /// Wired up but not yet implemented (e.g. full IPMI session handling).
    #[error("not implemented: {0}")]
    NotImplemented(String),
    /// Low-level transport failure (socket, HTTP, framing).
    #[error("transport error: {0}")]
    Transport(String),
    /// Simulator-internal inconsistency (should not happen in practice).
    #[error("simulation error: {0}")]
    Simulation(String),
}

/// Stable identifier for a physical device (rack, PDU, UPS, cooling loop).
pub type DeviceId = String;

/// Stable identifier for a sensor or actuator channel within a device.
pub type SensorId = String;

/// Kind of physical device, used for discovery and routing.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum DeviceKind {
    Rack,
    Pdu,
    Ups,
    CoolingLoop,
}

/// Physical quantity a reading measures. The [`Unit`] is implied by the
/// variant via [`Metric::unit`].
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Metric {
    Temperature,
    Voltage,
    Amperage,
    Airflow,
    Power,
    Humidity,
    /// Battery state of charge, percent.
    StateOfCharge,
    /// Cooling fan speed, percent of maximum.
    FanSpeed,
    /// Cooling valve position, percent open.
    ValvePosition,
    /// PDU outlet on/off state.
    OutletState,
}

impl Metric {
    /// The canonical unit for this metric.
    pub fn unit(&self) -> Unit {
        match self {
            Metric::Temperature => Unit::Celsius,
            Metric::Voltage => Unit::Volt,
            Metric::Amperage => Unit::Ampere,
            Metric::Airflow => Unit::CubicMetersPerSec,
            Metric::Power => Unit::Watt,
            Metric::Humidity | Metric::StateOfCharge | Metric::FanSpeed | Metric::ValvePosition => {
                Unit::Percent
            }
            Metric::OutletState => Unit::Boolean,
        }
    }
}

/// Unit of measure attached to a reading value.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Unit {
    Celsius,
    Volt,
    Ampere,
    CubicMetersPerSec,
    Watt,
    Percent,
    Boolean,
    Ratio,
}

/// A single time-stamped telemetry sample.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Reading {
    pub device: DeviceId,
    pub sensor: SensorId,
    pub metric: Metric,
    pub value: f64,
    pub unit: Unit,
    /// Epoch milliseconds at which the sample was taken.
    pub timestamp_ms: u64,
}

/// Actuator command issued to a device through the HAL.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub enum ControlCommand {
    /// Cooling valve position, 0.0–100.0 percent open.
    SetValvePosition(f64),
    /// Cooling fan speed, 0.0–100.0 percent of maximum.
    SetFanSpeed(f64),
    /// PDU outlet on/off state.
    SetOutletState(bool),
    /// UPS maximum discharge rate, 0.0–100.0 percent of capacity.
    SetDischargeLimit(f64),
}

impl ControlCommand {
    /// Clamp any numeric argument to its valid range (0–100 for percentages).
    pub fn clamped(self) -> Self {
        let clamp = |v: f64| v.clamp(0.0, 100.0);
        match self {
            ControlCommand::SetValvePosition(v) => ControlCommand::SetValvePosition(clamp(v)),
            ControlCommand::SetFanSpeed(v) => ControlCommand::SetFanSpeed(clamp(v)),
            ControlCommand::SetDischargeLimit(v) => ControlCommand::SetDischargeLimit(clamp(v)),
            ControlCommand::SetOutletState(b) => ControlCommand::SetOutletState(b),
        }
    }
}

/// Discovery record describing a device and the sensors it exposes.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct DeviceInfo {
    pub id: DeviceId,
    pub kind: DeviceKind,
    pub sensors: Vec<SensorId>,
}
