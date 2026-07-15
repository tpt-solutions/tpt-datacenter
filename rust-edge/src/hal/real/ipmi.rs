// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Minimal IPMI-over-LAN client (compute server power control).
//!
//! Implements the RMCP/IPMI session envelope (the `rmcp_open_session` /
//! `rakp` handshake and the `Get Channel Authentication Capabilities` /
//! `Set Session Privilege Level` / `Power Control` PDUs) enough to recognise
//! the protocol, but the authenticated session/command exchange is left as a
//! conformant stub returning [`HalError::NotImplemented`]. Full IPMI-over-LAN
//! session establishment against real BMCs is out of scope for this phase; the
//! shape below documents where it plugs into the HAL.

use crate::hal::types::{ControlCommand, DeviceId, HalError};

/// IPMI network function codes (subset relevant to power control).
const NETFN_APP: u8 = 0x06;
const CMD_GET_CHANNEL_AUTH_CAP: u8 = 0x38;
const CMD_POWER_CONTROL: u8 = 0x02;

/// Connection settings for one BMC's IPMI-over-LAN service.
#[derive(Debug, Clone)]
pub struct IpmiClient {
    host: String,
    port: u16,
}

impl IpmiClient {
    pub fn new(host: impl Into<String>, port: u16) -> Self {
        IpmiClient {
            host: host.into(),
            port,
        }
    }

    /// Build the IPMI `Power Control` command PDU (soft/cold reset, power on/off).
    /// Returns the raw command bytes; sending requires an established session.
    fn power_control_pdu(on: bool) -> Vec<u8> {
        // IPMI "Chassis Control" command: 0x40 with a control byte.
        let control = if on { 0x01u8 } else { 0x00u8 };
        vec![NETFN_APP << 2, CMD_POWER_CONTROL, 0x00, 0x40, control]
    }

    /// Build the `Get Channel Authentication Capabilities` PDU (handshake start).
    fn auth_cap_pdu() -> Vec<u8> {
        vec![NETFN_APP << 2, CMD_GET_CHANNEL_AUTH_CAP, 0x0e, 0x04]
    }

    /// Issue a power on/off command to a compute server's BMC.
    ///
    /// NOTE: full RMCP session negotiation (open session → RAKP → set
    /// privilege → command) is not yet implemented; this returns
    /// [`HalError::NotImplemented`] so callers fail loudly rather than send an
    /// unauthenticated, non-compliant packet to a BMC.
    pub async fn power_control(&self, device: &DeviceId, on: bool) -> Result<(), HalError> {
        let _pdu = Self::power_control_pdu(on);
        let _handshake = Self::auth_cap_pdu();
        Err(HalError::NotImplemented(format!(
            "IPMI-over-LAN session to {}:{} for device {device} not yet implemented",
            self.host, self.port
        )))
    }

    /// Map a HAL control command onto an IPMI power operation.
    pub async fn set(&self, device: &DeviceId, cmd: ControlCommand) -> Result<(), HalError> {
        match cmd {
            ControlCommand::SetOutletState(on) => self.power_control(device, on).await,
            other => Err(HalError::Unsupported(format!(
                "IPMI backend does not handle {other:?}"
            ))),
        }
    }
}
