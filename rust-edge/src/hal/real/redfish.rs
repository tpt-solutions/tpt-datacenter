// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Minimal Redfish client (BMC management via RESTful Redfish).
//!
//! This is a deliberately small, dependency-light HTTP/1.1 client built on
//! `tokio::net::TcpStream` (no TLS stack required — BMCs on an isolated
//! management LAN typically serve plain HTTP). It performs the two operations
//! the HAL needs: `GET` a sensor resource and `POST` an actuator setting, and
//! maps the Redfish JSON payload onto the common [`Reading`] / [`ControlCommand`]
//! model. It is intentionally not a full Redfish service consumer; it reads
//! the well-known `Reading` / `ReadingUnits` fields and posts to the vendor
//! chassis/power/thermal URIs.

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

use crate::hal::types::{ControlCommand, DeviceId, HalError, Metric, Reading, SensorId, Unit};

/// Connection settings for one Redfish (BMC) endpoint.
#[derive(Debug, Clone)]
pub struct RedfishClient {
    host: String,
    port: u16,
    username: String,
    password: String,
}

impl RedfishClient {
    pub fn new(host: impl Into<String>, port: u16) -> Self {
        RedfishClient {
            host: host.into(),
            port,
            username: String::new(),
            password: String::new(),
        }
    }

    /// Set basic-auth credentials (sent in the `Authorization` header).
    pub fn with_credentials(
        mut self,
        username: impl Into<String>,
        password: impl Into<String>,
    ) -> Self {
        self.username = username.into();
        self.password = password.into();
        self
    }

    async fn request(
        &self,
        method: &str,
        path: &str,
        body: Option<&str>,
    ) -> Result<String, HalError> {
        let mut stream = TcpStream::connect((self.host.as_str(), self.port))
            .await
            .map_err(|e| {
                HalError::Transport(format!("redfish connect {}:{}: {e}", self.host, self.port))
            })?;

        let auth = if self.username.is_empty() {
            String::new()
        } else {
            let token = base64_credentials(&self.username, &self.password);
            format!("Authorization: Basic {token}\r\n")
        };
        let payload = body.unwrap_or("");
        let req = format!(
            "{method} {path} HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\nContent-Type: application/json\r\n{auth}Content-Length: {len}\r\n\r\n{payload}",
            host = self.host,
            len = payload.len(),
        );
        stream
            .write_all(req.as_bytes())
            .await
            .map_err(|e| HalError::Transport(format!("redfish write: {e}")))?;

        let mut buf = Vec::new();
        stream
            .read_to_end(&mut buf)
            .await
            .map_err(|e| HalError::Transport(format!("redfish read: {e}")))?;

        let text = String::from_utf8_lossy(&buf);
        let body_start = text.find("\r\n\r\n").map(|i| i + 4).unwrap_or(text.len());
        let header = &text[..text.find("\r\n\r\n").unwrap_or(0)];
        if !header.contains(" 200 ") && !header.contains(" 204 ") {
            return Err(HalError::Transport(format!(
                "redfish {method} {path} -> non-2xx: {header}"
            )));
        }
        Ok(text[body_start..].to_string())
    }

    /// Read one sensor from a chassis' Redfish `Sensors` collection.
    pub async fn read_sensor(
        &self,
        device: &DeviceId,
        sensor: &SensorId,
    ) -> Result<Reading, HalError> {
        let path = format!("/redfish/v1/Chassis/{device}/Sensors/{sensor}");
        let body = self.request("GET", &path, None).await?;
        let v: serde_json::Value = serde_json::from_str(&body)
            .map_err(|e| HalError::Transport(format!("redfish json: {e}")))?;
        let value = v
            .get("Reading")
            .and_then(|r| r.as_f64())
            .ok_or_else(|| HalError::Transport("redfish sensor missing 'Reading'".into()))?;
        let (metric, unit) = infer_metric(&v, sensor);
        Ok(Reading {
            device: device.clone(),
            sensor: sensor.clone(),
            metric,
            value,
            unit,
            timestamp_ms: now_ms(),
        })
    }

    /// Push an actuator command to a chassis/thermal resource.
    pub async fn set(&self, device: &DeviceId, cmd: ControlCommand) -> Result<(), HalError> {
        let (path, payload) = match cmd {
            ControlCommand::SetValvePosition(p) => (
                format!("/redfish/v1/Chassis/{device}/Thermal"),
                format!(r#"{{"CoolingValvePercent":{p}}}"#),
            ),
            ControlCommand::SetFanSpeed(s) => (
                format!("/redfish/v1/Chassis/{device}/Thermal"),
                format!(r#"{{"FanSpeedPercent":{s}}}"#),
            ),
            ControlCommand::SetOutletState(on) => (
                format!("/redfish/v1/Chassis/{device}/Power"),
                format!(r#"{{"PowerEnabled":{on}}}"#),
            ),
            ControlCommand::SetDischargeLimit(l) => (
                format!("/redfish/v1/Chassis/{device}/Power#/Batteries"),
                format!(r#"{{"DischargeLimitPercent":{l}}}"#),
            ),
        };
        self.request("POST", &path, Some(&payload)).await?;
        Ok(())
    }
}

fn infer_metric(v: &serde_json::Value, sensor: &SensorId) -> (Metric, Unit) {
    let units = v.get("ReadingUnits").and_then(|u| u.as_str()).unwrap_or("");
    match units.to_uppercase().as_str() {
        "C" | "CELSIUS" => (Metric::Temperature, Unit::Celsius),
        "V" | "VOLTS" => (Metric::Voltage, Unit::Volt),
        "A" | "AMPS" => (Metric::Amperage, Unit::Ampere),
        "W" | "WATTS" => (Metric::Power, Unit::Watt),
        "PERCENT" | "%" => guess_percent_metric(sensor),
        _ => guess_percent_metric(sensor),
    }
}

fn guess_percent_metric(sensor: &SensorId) -> (Metric, Unit) {
    let s = sensor.as_str().to_lowercase();
    if s.contains("valve") {
        (Metric::ValvePosition, Unit::Percent)
    } else if s.contains("fan") {
        (Metric::FanSpeed, Unit::Percent)
    } else if s.contains("soc") || s.contains("charge") {
        (Metric::StateOfCharge, Unit::Percent)
    } else {
        (Metric::Temperature, Unit::Celsius)
    }
}

fn base64_credentials(user: &str, pass: &str) -> String {
    use base64::engine::general_purpose::STANDARD;
    use base64::Engine;
    STANDARD.encode(format!("{user}:{pass}"))
}

fn now_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}
