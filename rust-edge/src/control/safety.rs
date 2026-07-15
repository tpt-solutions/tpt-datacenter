// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Safety interlocks and fail-safe states.
//!
//! The [`SafetyEnforcer`] is the last gate before any actuator command reaches
//! the HAL. It (1) trips to a [`SafeState`] when a reading breaches a critical
//! limit, and (2) clamps any *requested* command to the physical safe envelope
//! so a buggy agent can never drive hardware outside its bounds.
//!
//! The same safe states are also applied blindly by the supervisor when the HAL
//! is unreachable (comms loss), which is the "fall back to a safe state" rule.

use crate::control::Snapshot;
use crate::hal::types::ControlCommand;

/// Physical safe envelope for the actuators and the alarm thresholds.
#[derive(Debug, Clone)]
pub struct SafetyLimits {
    /// Critical rack-air temperature — above this we trip every cycle.
    pub crit_air_temp_c: f64,
    /// Warning rack-air temperature — logged but not yet tripped.
    pub warn_air_temp_c: f64,
    /// Minimum rack-air temperature — below this, trip (freezing / over-cool).
    pub min_air_temp_c: f64,
    /// PDU voltage ceiling — above this implies a fault.
    pub max_pdu_voltage: f64,
    /// PDU voltage floor — below this implies a brownout / loss.
    pub min_pdu_voltage: f64,
    /// PDU current ceiling (A) — above this we shed load.
    pub max_pdu_current: f64,
    /// Hard floor/clip for valve position (%).
    pub min_valve: f64,
    pub max_valve: f64,
    /// Hard floor/clip for fan speed (%).
    pub min_fan: f64,
    pub max_fan: f64,
}

impl Default for SafetyLimits {
    fn default() -> Self {
        SafetyLimits {
            crit_air_temp_c: 45.0,
            warn_air_temp_c: 35.0,
            min_air_temp_c: 5.0,
            max_pdu_voltage: 264.0,
            min_pdu_voltage: 198.0,
            max_pdu_current: 80.0,
            min_valve: 0.0,
            max_valve: 100.0,
            min_fan: 0.0,
            max_fan: 100.0,
        }
    }
}

/// Fail-safe actuator configuration applied on a trip or comms loss.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SafeState {
    /// Maximum cooling: valve and fan to 100% (used for thermal trips).
    FullCooling,
    /// Shed IT load and cool hard: valve + fan to 100%, PDU outlets off.
    ShedLoad,
}

impl SafeState {
    /// The actuator commands that realize this safe state.
    pub fn commands(&self) -> Vec<ControlCommand> {
        match self {
            SafeState::FullCooling => vec![
                ControlCommand::SetValvePosition(100.0),
                ControlCommand::SetFanSpeed(100.0),
            ],
            SafeState::ShedLoad => vec![
                ControlCommand::SetValvePosition(100.0),
                ControlCommand::SetFanSpeed(100.0),
                ControlCommand::SetOutletState(false),
            ],
        }
    }
}

/// Result of enforcing safety on a set of requested commands.
#[derive(Debug, Clone, PartialEq)]
pub enum Enforcement {
    /// Commands are within bounds and safe to apply.
    Allow(Vec<ControlCommand>),
    /// A hard limit was breached; apply the safe-state commands instead.
    Trip {
        commands: Vec<ControlCommand>,
        reason: String,
    },
}

/// Enforces [`SafetyLimits`] over a [`Snapshot`] and requested commands.
#[derive(Debug, Clone)]
pub struct SafetyEnforcer {
    pub limits: SafetyLimits,
}

impl SafetyEnforcer {
    pub fn new(limits: SafetyLimits) -> Self {
        SafetyEnforcer { limits }
    }

    /// Check the snapshot for critical breaches and clamp requested commands.
    pub fn enforce(&self, snap: &Snapshot, requested: Vec<ControlCommand>) -> Enforcement {
        let l = &self.limits;

        if let Some(t) = snap.temp_c() {
            if t > l.crit_air_temp_c {
                return Enforcement::Trip {
                    commands: SafeState::FullCooling.commands(),
                    reason: format!(
                        "rack_air_temp {t:.1}°C exceeds critical {}°C",
                        l.crit_air_temp_c
                    ),
                };
            }
            if t < l.min_air_temp_c {
                return Enforcement::Trip {
                    commands: SafeState::FullCooling.commands(),
                    reason: format!(
                        "rack_air_temp {t:.1}°C below minimum {}°C",
                        l.min_air_temp_c
                    ),
                };
            }
        }

        if let Some(v) = snap.pdu_voltage() {
            if v > l.max_pdu_voltage || v < l.min_pdu_voltage {
                return Enforcement::Trip {
                    commands: SafeState::ShedLoad.commands(),
                    reason: format!(
                        "pdu_voltage {v:.1}V outside safe band [{}..{}]V",
                        l.min_pdu_voltage, l.max_pdu_voltage
                    ),
                };
            }
        }

        if let Some(i) = snap.pdu_current() {
            if i > l.max_pdu_current {
                return Enforcement::Trip {
                    commands: SafeState::ShedLoad.commands(),
                    reason: format!("pdu_current {i:.1}A exceeds limit {}A", l.max_pdu_current),
                };
            }
        }

        Enforcement::Allow(self.clamp(requested))
    }

    /// Clamp every numeric actuator command to the safe envelope.
    fn clamp(&self, commands: Vec<ControlCommand>) -> Vec<ControlCommand> {
        let l = &self.limits;
        commands
            .into_iter()
            .map(|c| match c {
                ControlCommand::SetValvePosition(v) => {
                    ControlCommand::SetValvePosition(v.clamp(l.min_valve, l.max_valve))
                }
                ControlCommand::SetFanSpeed(v) => {
                    ControlCommand::SetFanSpeed(v.clamp(l.min_fan, l.max_fan))
                }
                other => other.clamped(),
            })
            .collect()
    }
}
