// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Minimal Modbus TCP client (legacy chillers / PDUs over Modbus TCP).
//!
//! Implements the Modbus TCP framing (MBAP header + PDU) over a raw
//! `tokio::net::TcpStream` and the two function codes the HAL needs:
//! `Read Holding Registers` (0x03) and `Write Single Register` (0x06).
//! A small register map adapts the common [`ControlCommand`] / [`Metric`]
//! model onto holding-register addresses.

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

use crate::hal::types::{ControlCommand, DeviceId, HalError, Metric, Reading, SensorId, Unit};

/// Modbus function codes used here.
const FN_READ_HOLDING: u8 = 0x03;
const FN_WRITE_SINGLE: u8 = 0x06;

/// Connection settings for one Modbus TCP slave.
#[derive(Debug, Clone)]
pub struct ModbusTcpClient {
    host: String,
    port: u16,
    /// Modbus unit/slave identifier (1–247).
    unit_id: u8,
}

impl ModbusTcpClient {
    pub fn new(host: impl Into<String>, port: u16, unit_id: u8) -> Self {
        ModbusTcpClient {
            host: host.into(),
            port,
            unit_id,
        }
    }

    /// Exchange one Modbus/TCP transaction: send `pdu`, return the response PDU.
    async fn transaction(&self, pdu: &[u8]) -> Result<Vec<u8>, HalError> {
        let mut stream = TcpStream::connect((self.host.as_str(), self.port))
            .await
            .map_err(|e| {
                HalError::Transport(format!("modbus connect {}:{}: {e}", self.host, self.port))
            })?;

        // MBAP header: trans-id(2) proto-id(2)=0 len(2) unit-id(1)
        let tx_id: u16 = 0x0001;
        let length = (pdu.len() + 1) as u16;
        let mut frame = Vec::with_capacity(pdu.len() + 7);
        frame.extend_from_slice(&tx_id.to_be_bytes());
        frame.extend_from_slice(&0u16.to_be_bytes());
        frame.extend_from_slice(&length.to_be_bytes());
        frame.push(self.unit_id);
        frame.extend_from_slice(pdu);

        stream
            .write_all(&frame)
            .await
            .map_err(|e| HalError::Transport(format!("modbus write: {e}")))?;

        let mut header = [0u8; 7];
        stream
            .read_exact(&mut header)
            .await
            .map_err(|e| HalError::Transport(format!("modbus read header: {e}")))?;
        let resp_len = u16::from_be_bytes([header[4], header[5]]) as usize;
        let mut body = vec![0u8; resp_len];
        stream
            .read_exact(&mut body)
            .await
            .map_err(|e| HalError::Transport(format!("modbus read body: {e}")))?;

        if body.first().copied() == Some(pdu.first().copied().unwrap_or(0) | 0x80) {
            return Err(HalError::Transport(format!(
                "modbus exception code {}",
                body.get(1).copied().unwrap_or(0)
            )));
        }
        Ok(body)
    }

    /// Read `count` holding registers starting at `addr`.
    pub async fn read_holding_registers(
        &self,
        addr: u16,
        count: u16,
    ) -> Result<Vec<u16>, HalError> {
        let pdu = [
            FN_READ_HOLDING,
            (addr >> 8) as u8,
            (addr & 0xff) as u8,
            (count >> 8) as u8,
            (count & 0xff) as u8,
        ];
        let resp = self.transaction(&pdu).await?;
        // resp: [fn, byte-count, reg0_hi, reg0_lo, ...]
        if resp.len() < 2 || resp[0] != FN_READ_HOLDING {
            return Err(HalError::Transport(
                "modbus unexpected read response".into(),
            ));
        }
        let byte_count = resp[1] as usize;
        let mut regs = Vec::with_capacity(byte_count / 2);
        let mut i = 2;
        while i + 1 < 2 + byte_count {
            regs.push(u16::from_be_bytes([resp[i], resp[i + 1]]));
            i += 2;
        }
        Ok(regs)
    }

    /// Write a single holding register.
    pub async fn write_single_register(&self, addr: u16, value: u16) -> Result<(), HalError> {
        let pdu = [
            FN_WRITE_SINGLE,
            (addr >> 8) as u8,
            (addr & 0xff) as u8,
            (value >> 8) as u8,
            (value & 0xff) as u8,
        ];
        let resp = self.transaction(&pdu).await?;
        if resp.len() < 5 || resp[0] != FN_WRITE_SINGLE {
            return Err(HalError::Transport(
                "modbus unexpected write response".into(),
            ));
        }
        Ok(())
    }

    /// Read one sensor mapped to a holding register.
    pub async fn read_sensor(
        &self,
        device: &DeviceId,
        sensor: &SensorId,
    ) -> Result<Reading, HalError> {
        let addr = register_for_sensor(sensor);
        let regs = self.read_holding_registers(addr, 1).await?;
        let raw = regs.first().copied().unwrap_or(0) as f64;
        let (metric, unit, scale) = scale_for_sensor(sensor);
        Ok(Reading {
            device: device.clone(),
            sensor: sensor.clone(),
            metric,
            value: raw * scale,
            unit,
            timestamp_ms: now_ms(),
        })
    }

    /// Write one actuator command mapped to a holding register.
    pub async fn set(&self, _device: &DeviceId, cmd: ControlCommand) -> Result<(), HalError> {
        let (addr, value) = match cmd {
            ControlCommand::SetValvePosition(p) => (0x10u16, (p * 100.0) as u16),
            ControlCommand::SetFanSpeed(s) => (0x11u16, (s * 100.0) as u16),
            ControlCommand::SetOutletState(on) => (0x12u16, on as u16),
            ControlCommand::SetDischargeLimit(l) => (0x13u16, (l * 100.0) as u16),
        };
        self.write_single_register(addr, value).await
    }
}

/// Holding-register address for a sensor channel.
fn register_for_sensor(sensor: &SensorId) -> u16 {
    match sensor.as_str() {
        "rack_air_temp" => 0x00,
        "pdu_voltage" => 0x01,
        "pdu_current" => 0x02,
        "ups_soc" => 0x03,
        "it_load" => 0x04,
        _ => 0x00,
    }
}

/// (metric, unit, raw→engineering scale) for a sensor channel.
fn scale_for_sensor(sensor: &SensorId) -> (Metric, Unit, f64) {
    match sensor.as_str() {
        "rack_air_temp" => (Metric::Temperature, Unit::Celsius, 0.1),
        "pdu_voltage" => (Metric::Voltage, Unit::Volt, 0.1),
        "pdu_current" => (Metric::Amperage, Unit::Ampere, 0.01),
        "ups_soc" => (Metric::StateOfCharge, Unit::Percent, 0.1),
        "it_load" => (Metric::Power, Unit::Watt, 1.0),
        _ => (Metric::Temperature, Unit::Celsius, 1.0),
    }
}

fn now_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}
