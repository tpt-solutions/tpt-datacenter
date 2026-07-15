// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Software Simulator HAL backend.
//!
//! Models a facility of `N` virtual racks. Each rack carries an IT heat load
//! (power draw), a liquid cooling valve and a fan, a PDU, and a UPS. A simple
//! lumped-parameter thermal/electrical model advances the rack air
//! temperature and battery state of charge on every [`SimulatorHal::tick`],
//! giving the RL brain something physically meaningful to learn against:
//! opening the valve/fan cools the rack but costs pumping/fan energy.

use std::sync::Mutex;

use crate::hal::types::{
    ControlCommand, DeviceId, DeviceInfo, DeviceKind, HalError, Metric, Reading, SensorId, Unit,
};

use super::HardwareAbstractionLayer;

/// (sensor id, metric) pairs exposed by every simulated rack.
pub fn rack_sensors() -> &'static [(&'static str, Metric)] {
    &[
        ("rack_air_temp", Metric::Temperature),
        ("inlet_temp", Metric::Temperature),
        ("coolant_supply_temp", Metric::Temperature),
        ("valve_position", Metric::ValvePosition),
        ("fan_speed", Metric::FanSpeed),
        ("airflow", Metric::Airflow),
        ("pdu_voltage", Metric::Voltage),
        ("pdu_current", Metric::Amperage),
        ("it_load", Metric::Power),
        ("ups_soc", Metric::StateOfCharge),
        ("outlet", Metric::OutletState),
    ]
}

/// Thermal/electrical state of a single virtual rack.
#[derive(Debug, Clone)]
pub struct RackState {
    pub id: DeviceId,
    // Thermal
    pub temp_c: f64,
    pub inlet_temp_c: f64,
    pub ambient_c: f64,
    pub coolant_supply_temp_c: f64,
    pub it_load_w: f64,
    // Cooling actuators
    pub valve_position: f64,
    pub fan_speed: f64,
    pub max_airflow: f64,
    // PDU
    pub pdu_voltage: f64,
    pub pdu_current: f64,
    pub outlets: Vec<bool>,
    // UPS / BMS
    pub soc: f64,
    pub ups_capacity_wh: f64,
    pub grid_present: bool,
    pub discharge_limit: f64,
    // Physics coefficients
    pub thermal_mass: f64,
    pub k_valve: f64,
    pub k_fan: f64,
}

impl RackState {
    fn new(index: usize) -> Self {
        RackState {
            id: format!("rack-{index:02}"),
            temp_c: 24.0,
            inlet_temp_c: 22.0,
            ambient_c: 24.0,
            coolant_supply_temp_c: 18.0,
            it_load_w: 12_000.0,
            valve_position: 50.0,
            fan_speed: 50.0,
            max_airflow: 2.5,
            pdu_voltage: 230.0,
            pdu_current: 12_000.0 / 230.0,
            outlets: vec![true],
            soc: 100.0,
            ups_capacity_wh: 8_000.0,
            grid_present: true,
            discharge_limit: 100.0,
            thermal_mass: 60_000.0,
            k_valve: 1_800.0,
            k_fan: 900.0,
        }
    }

    /// Current airflow in m³/s derived from fan speed.
    fn airflow(&self) -> f64 {
        (self.fan_speed / 100.0) * self.max_airflow
    }

    /// Read a single sensor channel.
    fn read_sensor(&self, sensor: &SensorId, timestamp_ms: u64) -> Result<Reading, HalError> {
        let (value, unit) = match sensor.as_str() {
            "rack_air_temp" => (self.temp_c, Unit::Celsius),
            "inlet_temp" => (self.inlet_temp_c, Unit::Celsius),
            "coolant_supply_temp" => (self.coolant_supply_temp_c, Unit::Celsius),
            "valve_position" => (self.valve_position, Unit::Percent),
            "fan_speed" => (self.fan_speed, Unit::Percent),
            "airflow" => (self.airflow(), Unit::CubicMetersPerSec),
            "pdu_voltage" => (self.pdu_voltage, Unit::Volt),
            "pdu_current" => (self.pdu_current, Unit::Ampere),
            "it_load" => (self.it_load_w, Unit::Watt),
            "ups_soc" => (self.soc, Unit::Percent),
            "outlet" => {
                return Ok(Reading {
                    device: self.id.clone(),
                    sensor: sensor.clone(),
                    metric: Metric::OutletState,
                    value: if self.outlets.first().copied().unwrap_or(false) {
                        1.0
                    } else {
                        0.0
                    },
                    unit: Unit::Boolean,
                    timestamp_ms,
                });
            }
            other => return Err(HalError::SensorNotFound(other.to_string())),
        };
        Ok(Reading {
            device: self.id.clone(),
            sensor: sensor.clone(),
            metric: metric_for(sensor),
            value,
            unit,
            timestamp_ms,
        })
    }

    /// Apply a control command to the rack actuators.
    fn apply(&mut self, cmd: ControlCommand) {
        match cmd {
            ControlCommand::SetValvePosition(v) => self.valve_position = v.clamp(0.0, 100.0),
            ControlCommand::SetFanSpeed(v) => self.fan_speed = v.clamp(0.0, 100.0),
            ControlCommand::SetOutletState(b) => {
                if !self.outlets.is_empty() {
                    self.outlets[0] = b;
                }
            }
            ControlCommand::SetDischargeLimit(v) => self.discharge_limit = v.clamp(0.0, 100.0),
        }
    }

    /// Advance the lumped thermal/electrical model by `dt` seconds.
    fn step(&mut self, dt: f64) {
        let valve_frac = self.valve_position / 100.0;
        let fan_frac = self.fan_speed / 100.0;

        let q_it = self.it_load_w
            * if self.outlets.first().copied().unwrap_or(false) {
                1.0
            } else {
                0.2
            };
        let q_cool = self.k_valve * valve_frac * (self.temp_c - self.coolant_supply_temp_c)
            + self.k_fan * fan_frac * (self.temp_c - self.ambient_c);

        let d_temp = (q_it - q_cool) / self.thermal_mass * dt;
        self.temp_c = (self.temp_c + d_temp).clamp(self.coolant_supply_temp_c, 200.0);

        // Electrical: PDU current tracks the load.
        self.pdu_current = self.it_load_w / self.pdu_voltage;

        // UPS / BMS state of charge.
        if self.grid_present {
            // Charging from grid (capped at 100%).
            self.soc = (self.soc + 2.0 * dt).min(100.0);
        } else {
            let draw = self.it_load_w * (self.discharge_limit / 100.0);
            let pct_per_sec = (draw / self.ups_capacity_wh) * (100.0 / 3600.0);
            self.soc = (self.soc - pct_per_sec * dt).max(0.0);
        }
    }
}

fn metric_for(sensor: &SensorId) -> Metric {
    for (id, metric) in rack_sensors() {
        if *id == sensor.as_str() {
            return *metric;
        }
    }
    Metric::Temperature
}

/// Whole-facility simulator state.
#[derive(Debug)]
pub struct FacilityState {
    pub racks: Vec<RackState>,
    pub time_ms: u64,
}

/// Simulator HAL backend. Cheap to construct; uses interior mutability so the
/// [`HardwareAbstractionLayer`] methods can take `&self`.
pub struct SimulatorHal {
    state: Mutex<FacilityState>,
}

impl SimulatorHal {
    /// Create a simulator with `num_racks` virtual racks.
    pub fn new(num_racks: usize) -> Self {
        let racks = (0..num_racks.max(1)).map(RackState::new).collect();
        SimulatorHal {
            state: Mutex::new(FacilityState { racks, time_ms: 0 }),
        }
    }

    /// Advance the simulation by `dt_secs` seconds.
    pub fn tick(&self, dt_secs: f64) {
        let mut state = self.state.lock().expect("simulator mutex poisoned");
        let dt = dt_secs.clamp(0.0, 3600.0);
        for rack in &mut state.racks {
            rack.step(dt);
        }
        state.time_ms += (dt * 1000.0) as u64;
    }

    /// Current facility wall-clock (epoch ms of the last tick).
    pub fn now_ms(&self) -> u64 {
        self.state.lock().expect("simulator mutex poisoned").time_ms
    }

    /// Direct (non-async) access to rack state for tests/inspection.
    pub fn with_racks<R>(&self, f: impl FnOnce(&[RackState]) -> R) -> R {
        let state = self.state.lock().expect("simulator mutex poisoned");
        f(&state.racks)
    }
}

impl HardwareAbstractionLayer for SimulatorHal {
    async fn read(&self, device: &DeviceId, sensor: &SensorId) -> Result<Reading, HalError> {
        let state = self.state.lock().expect("simulator mutex poisoned");
        let rack = state
            .racks
            .iter()
            .find(|r| &r.id == device)
            .ok_or_else(|| HalError::DeviceNotFound(device.clone()))?;
        rack.read_sensor(sensor, state.time_ms)
    }

    async fn read_all(&self, device: &DeviceId) -> Result<Vec<Reading>, HalError> {
        let state = self.state.lock().expect("simulator mutex poisoned");
        let rack = state
            .racks
            .iter()
            .find(|r| &r.id == device)
            .ok_or_else(|| HalError::DeviceNotFound(device.clone()))?;
        let ts = state.time_ms;
        rack_sensors()
            .iter()
            .map(|(id, _)| rack.read_sensor(&(*id).to_string(), ts))
            .collect()
    }

    async fn command(&self, device: &DeviceId, cmd: ControlCommand) -> Result<(), HalError> {
        let mut state = self.state.lock().expect("simulator mutex poisoned");
        let rack = state
            .racks
            .iter_mut()
            .find(|r| &r.id == device)
            .ok_or_else(|| HalError::DeviceNotFound(device.clone()))?;
        rack.apply(cmd.clamped());
        Ok(())
    }

    async fn list_devices(&self) -> Result<Vec<DeviceInfo>, HalError> {
        let state = self.state.lock().expect("simulator mutex poisoned");
        Ok(state
            .racks
            .iter()
            .map(|r| DeviceInfo {
                id: r.id.clone(),
                kind: DeviceKind::Rack,
                sensors: rack_sensors()
                    .iter()
                    .map(|(id, _)| (*id).to_string())
                    .collect(),
            })
            .collect())
    }
}
