// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Real-hardware HAL backend.
//!
//! Holds one client per southbound protocol (Redfish, Modbus TCP, IPMI) and
//! routes each HAL call to the right client based on a per-device protocol
//! mapping declared in [`RealConfig`]. This is what lets a single deployment
//! mix simulator racks (configured as `simulator`) with real PDUs (Modbus),
//! real UPS (Redfish) and real compute BMCs (IPMI) — all behind the same
//! [`HardwareAbstractionLayer`] trait.

pub mod ipmi;
pub mod modbus;
pub mod redfish;

use std::collections::HashMap;

use serde::{Deserialize, Serialize};

use crate::hal::types::{ControlCommand, DeviceId, DeviceInfo, HalError, Reading, SensorId};

use super::HardwareAbstractionLayer;

/// Southbound protocol used to talk to a device.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Protocol {
    Redfish,
    Modbus,
    Ipmi,
}

/// Host/port of one protocol endpoint.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Endpoint {
    pub host: String,
    #[serde(default = "default_port")]
    pub port: u16,
}

fn default_port() -> u16 {
    0
}

/// A device reachable over a real protocol, with the sensors it exposes.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RealDevice {
    pub id: DeviceId,
    pub protocol: Protocol,
    #[serde(default)]
    pub sensors: Vec<SensorId>,
}

/// Configuration for the Real HAL backend.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct RealConfig {
    #[serde(default = "redfish_endpoint")]
    pub redfish: Endpoint,
    #[serde(default = "modbus_endpoint")]
    pub modbus: Endpoint,
    #[serde(default = "ipmi_endpoint")]
    pub ipmi: Endpoint,
    #[serde(default)]
    pub devices: Vec<RealDevice>,
}

fn redfish_endpoint() -> Endpoint {
    Endpoint {
        host: "127.0.0.1".into(),
        port: 80,
    }
}
fn modbus_endpoint() -> Endpoint {
    Endpoint {
        host: "127.0.0.1".into(),
        port: 502,
    }
}
fn ipmi_endpoint() -> Endpoint {
    Endpoint {
        host: "127.0.0.1".into(),
        port: 623,
    }
}

impl RealConfig {
    /// Construct an endpoint set from explicit addresses.
    pub fn new(redfish: Endpoint, modbus: Endpoint, ipmi: Endpoint) -> Self {
        RealConfig {
            redfish,
            modbus,
            ipmi,
            devices: Vec::new(),
        }
    }
}

/// Real-hardware HAL backend.
pub struct RealHal {
    redfish: redfish::RedfishClient,
    modbus: modbus::ModbusTcpClient,
    ipmi: ipmi::IpmiClient,
    protocol: HashMap<DeviceId, Protocol>,
    sensors: HashMap<DeviceId, Vec<SensorId>>,
}

impl RealHal {
    /// Build the backend from configuration.
    pub fn new(cfg: &RealConfig) -> Self {
        let redfish = redfish::RedfishClient::new(cfg.redfish.host.clone(), cfg.redfish.port);
        let modbus = modbus::ModbusTcpClient::new(cfg.modbus.host.clone(), cfg.modbus.port, 1);
        let ipmi = ipmi::IpmiClient::new(cfg.ipmi.host.clone(), cfg.ipmi.port);

        let mut protocol = HashMap::new();
        let mut sensors = HashMap::new();
        for d in &cfg.devices {
            protocol.insert(d.id.clone(), d.protocol);
            sensors.insert(d.id.clone(), d.sensors.clone());
        }

        RealHal {
            redfish,
            modbus,
            ipmi,
            protocol,
            sensors,
        }
    }

    fn protocol_for(&self, device: &DeviceId) -> Result<Protocol, HalError> {
        self.protocol
            .get(device)
            .copied()
            .ok_or_else(|| HalError::DeviceNotFound(device.clone()))
    }
}

impl HardwareAbstractionLayer for RealHal {
    async fn read(&self, device: &DeviceId, sensor: &SensorId) -> Result<Reading, HalError> {
        match self.protocol_for(device)? {
            Protocol::Redfish => self.redfish.read_sensor(device, sensor).await,
            Protocol::Modbus => self.modbus.read_sensor(device, sensor).await,
            Protocol::Ipmi => Err(HalError::Unsupported(
                "IPMI exposes no readable sensors via this backend".into(),
            )),
        }
    }

    async fn read_all(&self, device: &DeviceId) -> Result<Vec<Reading>, HalError> {
        let sensors = self
            .sensors
            .get(device)
            .cloned()
            .ok_or_else(|| HalError::DeviceNotFound(device.clone()))?;
        let mut out = Vec::with_capacity(sensors.len());
        for s in &sensors {
            out.push(self.read(device, s).await?);
        }
        Ok(out)
    }

    async fn command(&self, device: &DeviceId, cmd: ControlCommand) -> Result<(), HalError> {
        match self.protocol_for(device)? {
            Protocol::Redfish => self.redfish.set(device, cmd).await,
            Protocol::Modbus => self.modbus.set(device, cmd).await,
            Protocol::Ipmi => self.ipmi.set(device, cmd).await,
        }
    }

    async fn list_devices(&self) -> Result<Vec<DeviceInfo>, HalError> {
        Ok(self
            .protocol
            .keys()
            .map(|id| DeviceInfo {
                id: id.clone(),
                kind: crate::hal::types::DeviceKind::Rack,
                sensors: self.sensors.get(id).cloned().unwrap_or_default(),
            })
            .collect())
    }
}
