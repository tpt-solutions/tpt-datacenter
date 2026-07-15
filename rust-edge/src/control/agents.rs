// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Control agents: the per-device control laws.
//!
//! Each agent implements [`ControlAgent::decide`], which turns a [`Snapshot`]
//! into a set of [`ControlCommand`]s plus an optional human-readable note. The
//! agents are pure (mutating only their own internal state, e.g. the PID
//! integrator) and never touch the HAL, which keeps them deterministic and
//! unit-testable in isolation.

use crate::control::pid::PidController;
use crate::control::Snapshot;
use crate::hal::types::ControlCommand;

/// Output of a single control decision.
#[derive(Debug, Clone, PartialEq)]
pub struct AgentOutput {
    pub commands: Vec<ControlCommand>,
    pub note: Option<String>,
}

/// A control law for one device.
pub trait ControlAgent {
    /// Stable name for logging / heartbeats (e.g. `"cooling"`).
    fn name(&self) -> &str;

    /// Decide what to command given the current snapshot.
    fn decide(&mut self, snap: &Snapshot) -> AgentOutput;
}

/// Runs multiple agents on the same device, concatenating their commands.
///
/// Useful when one rack is governed by a cooling agent, a PDU protection agent,
/// and a UPS/BMS agent simultaneously.
pub struct CompositeAgent {
    name: String,
    agents: Vec<Box<dyn ControlAgent>>,
}

impl CompositeAgent {
    pub fn new(name: impl Into<String>, agents: Vec<Box<dyn ControlAgent>>) -> Self {
        CompositeAgent {
            name: name.into(),
            agents,
        }
    }
}

impl ControlAgent for CompositeAgent {
    fn name(&self) -> &str {
        &self.name
    }

    fn decide(&mut self, snap: &Snapshot) -> AgentOutput {
        let mut commands = Vec::new();
        let mut notes = Vec::new();
        for a in &mut self.agents {
            let out = a.decide(snap);
            commands.extend(out.commands);
            if let Some(n) = out.note {
                notes.push(format!("{}: {}", a.name(), n));
            }
        }
        AgentOutput {
            commands,
            note: if notes.is_empty() {
                None
            } else {
                Some(notes.join("; "))
            },
        }
    }
}

/// Cooling valve/fan agent: a PID loop on rack air temperature.
///
/// The PID output (0–100) is the cooling demand. Liquid cooling (valve) is the
/// primary actuator and gets the full demand; the fan gets a share of it to
/// handle transient hotspots. Opening the valve/fan cools the rack but costs
/// pumping/fan energy — that trade-off is what the AI brain later optimizes.
#[derive(Debug, Clone)]
pub struct CoolingAgent {
    pid: PidController,
    /// Fraction of the cooling demand routed to the fan (0–1); the remainder
    /// (implicitly) goes to the valve.
    fan_share: f64,
    /// Last snapshot timestamp (ms), used to derive the real control period.
    last_ts: Option<u64>,
}

impl CoolingAgent {
    /// `setpoint_c` is the target rack-air temperature. `fan_share` routes that
    /// fraction of the cooling demand to the fan (the rest drives the valve).
    pub fn new(setpoint_c: f64, kp: f64, ki: f64, kd: f64, fan_share: f64) -> Self {
        let pid = PidController::new(kp, ki, kd, setpoint_c, 0.0, 100.0);
        CoolingAgent {
            pid,
            fan_share: fan_share.clamp(0.0, 1.0),
            last_ts: None,
        }
    }
}

impl ControlAgent for CoolingAgent {
    fn name(&self) -> &str {
        "cooling"
    }

    fn decide(&mut self, snap: &Snapshot) -> AgentOutput {
        let Some(temp) = snap.temp_c() else {
            return AgentOutput {
                commands: vec![ControlCommand::SetValvePosition(50.0)],
                note: Some("no temperature reading; holding valve at 50%".into()),
            };
        };

        let dt = match self.last_ts {
            Some(prev) if snap.timestamp_ms > prev => {
                ((snap.timestamp_ms - prev) as f64 / 1000.0).clamp(0.05, 60.0)
            }
            _ => DT_SECONDS,
        };
        self.last_ts = Some(snap.timestamp_ms);

        let demand = self.pid.update(temp, dt);
        let valve = demand;
        let fan = (demand * self.fan_share).clamp(0.0, 100.0);
        AgentOutput {
            commands: vec![
                ControlCommand::SetValvePosition(valve),
                ControlCommand::SetFanSpeed(fan),
            ],
            note: Some(format!("temp {temp:.1}°C → demand {demand:.1}%")),
        }
    }
}

/// PDU protection agent: electrical over/under-voltage and overload interlock.
///
/// On a fault it sheds the PDU outlet (fail-safe) and never auto-restores it —
/// restoring load is an explicit operator action. Under nominal conditions it
/// issues no commands and leaves the outlet as-is.
#[derive(Debug, Clone)]
pub struct PduAgent {
    rated_current_a: f64,
    over_voltage_v: f64,
    under_voltage_v: f64,
}

impl PduAgent {
    pub fn new(rated_current_a: f64, over_voltage_v: f64, under_voltage_v: f64) -> Self {
        PduAgent {
            rated_current_a,
            over_voltage_v,
            under_voltage_v,
        }
    }
}

impl ControlAgent for PduAgent {
    fn name(&self) -> &str {
        "pdu"
    }

    fn decide(&mut self, snap: &Snapshot) -> AgentOutput {
        let v = snap.pdu_voltage().unwrap_or(f64::NAN);
        let i = snap.pdu_current().unwrap_or(0.0);

        if v.is_nan() {
            return AgentOutput {
                commands: vec![],
                note: Some("no PDU voltage reading".into()),
            };
        }
        if v > self.over_voltage_v || v < self.under_voltage_v {
            return AgentOutput {
                commands: vec![ControlCommand::SetOutletState(false)],
                note: Some(format!(
                    "voltage {v:.1}V out of band; shedding outlet",
                    v = v
                )),
            };
        }
        if i > self.rated_current_a {
            return AgentOutput {
                commands: vec![ControlCommand::SetOutletState(false)],
                note: Some(format!(
                    "current {i:.1}A over rated {}A; shedding outlet",
                    self.rated_current_a
                )),
            };
        }
        AgentOutput {
            commands: vec![],
            note: None,
        }
    }
}

/// UPS / battery management agent.
///
/// Sets the discharge limit based on state of charge: a low battery is allowed
/// to discharge freely (to keep the load up), a full battery is conserved by
/// capping discharge. This is the heuristic BMS logic the RL brain will later
/// replace/refine.
#[derive(Debug, Clone)]
pub struct UpsAgent {
    /// SoC (%) below which we allow maximum discharge.
    low_soc: f64,
    /// SoC (%) above which we cap discharge to conserve the pack.
    high_soc: f64,
    /// Discharge cap (%) applied when SoC is high.
    conserve_limit: f64,
}

impl UpsAgent {
    pub fn new(low_soc: f64, high_soc: f64, conserve_limit: f64) -> Self {
        UpsAgent {
            low_soc,
            high_soc,
            conserve_limit,
        }
    }
}

impl ControlAgent for UpsAgent {
    fn name(&self) -> &str {
        "ups"
    }

    fn decide(&mut self, snap: &Snapshot) -> AgentOutput {
        let Some(soc) = snap.ups_soc() else {
            return AgentOutput {
                commands: vec![],
                note: Some("no SoC reading".into()),
            };
        };
        let limit = if soc <= self.low_soc {
            100.0
        } else if soc >= self.high_soc {
            self.conserve_limit
        } else {
            80.0
        };
        AgentOutput {
            commands: vec![ControlCommand::SetDischargeLimit(limit)],
            note: Some(format!("SoC {soc:.1}% → discharge limit {limit:.0}%")),
        }
    }
}

/// Default control period (seconds) when a snapshot has no prior timestamp.
///
/// The supervisor runs the loop at this cadence; agents use the real elapsed
/// time between snapshots for the PID derivative/integral terms but fall back
/// to this constant on the first sample.
pub const DT_SECONDS: f64 = 1.0;
