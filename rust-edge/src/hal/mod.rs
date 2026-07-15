// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Hardware Abstraction Layer.
//!
//! [`HardwareAbstractionLayer`] is the single interface every backend
//! (Simulator, Redfish, Modbus, IPMI) implements. The rest of the edge
//! control loops, telemetry, and AI brain depend only on this trait — never
//! on a concrete device protocol — which is what lets the same control logic
//! run unchanged against synthetic telemetry or live hardware.
//!
//! [`Hal`] is a convention-over-configuration facade: a tagged enum you can
//! construct from [`crate::hal::config::HalConfig`] that dispatches to the
//! right backend, including per-device routing between Simulator and Real.

pub mod config;
pub mod simulator;
pub mod types;

#[cfg(feature = "real")]
pub mod real;

#[cfg(test)]
mod tests;

use types::{ControlCommand, DeviceId, DeviceInfo, Reading, SensorId};

pub use types::HalError;

/// The unified HAL interface. All methods are `async` so a single trait can
/// back both the in-memory Simulator and network-bound Real clients.
///
/// `async fn` in a trait is used deliberately; we do not need to relax the
/// auto-trait (`Send`) bounds on the returned futures for this crate's use.
#[allow(async_fn_in_trait)]
pub trait HardwareAbstractionLayer {
    /// Read one sensor/actuator channel on a device.
    async fn read(&self, device: &DeviceId, sensor: &SensorId) -> Result<Reading, HalError>;

    /// Read every channel the device exposes.
    async fn read_all(&self, device: &DeviceId) -> Result<Vec<Reading>, HalError>;

    /// Issue an actuator command to a device.
    async fn command(&self, device: &DeviceId, cmd: ControlCommand) -> Result<(), HalError>;

    /// List devices the backend currently manages.
    async fn list_devices(&self) -> Result<Vec<DeviceInfo>, HalError>;
}

/// Tagged facade over every backend, built from configuration.
///
/// Use [`Hal::from_config`] (via [`crate::hal::config::HalConfig`]) to pick a
/// backend for the whole deployment, or [`config::RoutingHal`] for
/// per-device selection.
pub enum Hal {
    /// Software simulator backend (always available).
    Simulator(simulator::SimulatorHal),
    /// Real-hardware backend (requires the `real` feature).
    #[cfg(feature = "real")]
    Real(real::RealHal),
}

impl Hal {
    /// Construct the in-process simulator with `num_racks` virtual racks.
    pub fn simulator(num_racks: usize) -> Self {
        Hal::Simulator(simulator::SimulatorHal::new(num_racks))
    }

    /// Build a HAL from a configuration block.
    pub fn from_config(cfg: &config::HalConfig) -> Result<Self, HalError> {
        config::HalConfig::build_root(cfg)
    }
}

impl HardwareAbstractionLayer for Hal {
    async fn read(&self, device: &DeviceId, sensor: &SensorId) -> Result<Reading, HalError> {
        match self {
            Hal::Simulator(s) => s.read(device, sensor).await,
            #[cfg(feature = "real")]
            Hal::Real(r) => r.read(device, sensor).await,
        }
    }

    async fn read_all(&self, device: &DeviceId) -> Result<Vec<Reading>, HalError> {
        match self {
            Hal::Simulator(s) => s.read_all(device).await,
            #[cfg(feature = "real")]
            Hal::Real(r) => r.read_all(device).await,
        }
    }

    async fn command(&self, device: &DeviceId, cmd: ControlCommand) -> Result<(), HalError> {
        match self {
            Hal::Simulator(s) => s.command(device, cmd).await,
            #[cfg(feature = "real")]
            Hal::Real(r) => r.command(device, cmd).await,
        }
    }

    async fn list_devices(&self) -> Result<Vec<DeviceInfo>, HalError> {
        match self {
            Hal::Simulator(s) => s.list_devices().await,
            #[cfg(feature = "real")]
            Hal::Real(r) => r.list_devices().await,
        }
    }
}
