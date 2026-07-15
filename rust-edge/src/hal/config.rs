// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Configuration-driven HAL construction.
//!
//! A [`HalConfig`] selects a backend for the whole deployment (`default`) and
//! optionally overrides it per device (`devices`). [`RoutingHal`] implements
//! the HAL by dispatching each call to the backend responsible for the target
//! device — so a single deployment can run some racks in Simulator mode and
//! others against real Redfish/Modbus/IPMI hardware.

use std::collections::HashMap;

use serde::{Deserialize, Serialize};

use super::{Hal, HardwareAbstractionLayer};
use crate::hal::types::{ControlCommand, DeviceId, DeviceInfo, HalError, Reading, SensorId};

/// Which backend backs a device.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "backend", rename_all = "snake_case")]
pub enum BackendKind {
    /// Software simulator with `racks` virtual racks.
    Simulator { racks: usize },
    /// Real hardware (Redfish / Modbus / IPMI).
    #[cfg(feature = "real")]
    Real(super::real::RealConfig),
}

/// Top-level HAL configuration.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HalConfig {
    /// Backend for devices not listed in [`HalConfig::devices`].
    pub default: BackendKind,
    /// Per-device backend overrides.
    #[serde(default)]
    pub devices: HashMap<DeviceId, BackendKind>,
}

impl HalConfig {
    /// Convenience: a simulator-only config with `racks` racks.
    pub fn simulator(racks: usize) -> Self {
        HalConfig {
            default: BackendKind::Simulator { racks },
            devices: HashMap::new(),
        }
    }

    /// Build a single [`Hal`] for the `default` backend (no per-device routing).
    pub fn build_root(cfg: &HalConfig) -> Result<Hal, HalError> {
        build_backend(&cfg.default)
    }
}

/// Construct one [`Hal`] from a [`BackendKind`].
fn build_backend(kind: &BackendKind) -> Result<Hal, HalError> {
    match kind {
        BackendKind::Simulator { racks } => {
            Ok(Hal::Simulator(super::simulator::SimulatorHal::new(*racks)))
        }
        #[cfg(feature = "real")]
        BackendKind::Real(cfg) => Ok(Hal::Real(super::real::RealHal::new(cfg))),
    }
}

/// HAL that routes each call to the backend responsible for the target device.
pub struct RoutingHal {
    routes: HashMap<DeviceId, Hal>,
    default: Hal,
}

impl RoutingHal {
    /// Build a routing HAL from configuration.
    pub fn build(cfg: &HalConfig) -> Result<Self, HalError> {
        let mut routes = HashMap::new();
        for (id, kind) in &cfg.devices {
            routes.insert(id.clone(), build_backend(kind)?);
        }
        let default = build_backend(&cfg.default)?;
        Ok(RoutingHal { routes, default })
    }

    fn backend_for(&self, device: &DeviceId) -> &Hal {
        self.routes.get(device).unwrap_or(&self.default)
    }
}

impl HardwareAbstractionLayer for RoutingHal {
    async fn read(&self, device: &DeviceId, sensor: &SensorId) -> Result<Reading, HalError> {
        self.backend_for(device).read(device, sensor).await
    }

    async fn read_all(&self, device: &DeviceId) -> Result<Vec<Reading>, HalError> {
        self.backend_for(device).read_all(device).await
    }

    async fn command(&self, device: &DeviceId, cmd: ControlCommand) -> Result<(), HalError> {
        self.backend_for(device).command(device, cmd).await
    }

    async fn list_devices(&self) -> Result<Vec<DeviceInfo>, HalError> {
        let mut seen = std::collections::HashSet::new();
        let mut out = Vec::new();
        for hal in self.routes.values().chain(std::iter::once(&self.default)) {
            for dev in hal.list_devices().await? {
                if seen.insert(dev.id.clone()) {
                    out.push(dev);
                }
            }
        }
        Ok(out)
    }
}
