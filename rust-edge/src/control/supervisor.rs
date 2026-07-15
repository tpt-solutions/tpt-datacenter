// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Control-loop supervisor: the only part that drives the async HAL.
//!
//! Per cycle the supervisor:
//! 1. reads the device (`read_all`);
//! 2. on comms loss, applies the configured [`SafeState`] blindly and
//!    increments an error counter (latching into the safe state after N
//!    consecutive failures);
//! 3. otherwise asks the [`ControlAgent`] for commands, passes them through the
//!    [`SafetyEnforcer`], and writes the (possibly clamped / tripped) commands;
//! 4. emits a [`Heartbeat`] every `heartbeat_every` cycles and keeps a local
//!    health/error count for observability.

use std::time::Duration;

use crate::control::agents::ControlAgent;
use crate::control::safety::{SafeState, SafetyEnforcer, SafetyLimits};
use crate::control::Snapshot;
use crate::hal::types::{ControlCommand, DeviceId};
use crate::hal::HardwareAbstractionLayer;

/// Supervisor tuning / behavior.
#[derive(Debug, Clone)]
pub struct SupervisorOptions {
    /// Control period in milliseconds (used by `run`).
    pub tick_period_ms: u64,
    /// Emit a heartbeat every this-many cycles.
    pub heartbeat_every: u64,
    /// Consecutive HAL failures before the supervisor latches into the safe
    /// state until [`Supervisor::reset`].
    pub max_consecutive_errors: u32,
    /// Safe state applied on comms loss / latched fault.
    pub fail_safe: SafeState,
}

impl Default for SupervisorOptions {
    fn default() -> Self {
        SupervisorOptions {
            tick_period_ms: 1000,
            heartbeat_every: 10,
            max_consecutive_errors: 5,
            fail_safe: SafeState::FullCooling,
        }
    }
}

/// Record emitted for observability / heartbeat reporting.
#[derive(Debug, Clone)]
pub struct Heartbeat {
    pub device: DeviceId,
    pub agent: String,
    pub cycle: u64,
    pub timestamp_ms: u64,
    pub healthy: bool,
    pub tripped: bool,
    pub consecutive_errors: u32,
    pub note: Option<String>,
}

/// Outcome of a single [`Supervisor::run_once`] cycle.
#[derive(Debug, Clone)]
pub struct CycleOutcome {
    pub cycle: u64,
    pub timestamp_ms: u64,
    /// Commands the agent/safety logic wanted to apply this cycle.
    pub requested: Vec<ControlCommand>,
    /// Commands actually written to the HAL this cycle.
    pub applied: Vec<ControlCommand>,
    pub healthy: bool,
    pub tripped: bool,
    pub note: Option<String>,
    /// Present only on cycles that are heartbeats.
    pub heartbeat: Option<Heartbeat>,
}

/// Drives one [`ControlAgent`] against one device, guarded by a
/// [`SafetyEnforcer`].
pub struct Supervisor<A: ControlAgent> {
    device: DeviceId,
    agent: A,
    enforcer: SafetyEnforcer,
    opts: SupervisorOptions,
    cycle: u64,
    consecutive_errors: u32,
    latched_safe: bool,
}

impl<A: ControlAgent> Supervisor<A> {
    pub fn new(
        device: DeviceId,
        agent: A,
        enforcer: SafetyEnforcer,
        opts: SupervisorOptions,
    ) -> Self {
        Supervisor {
            device,
            agent,
            enforcer,
            opts,
            cycle: 0,
            consecutive_errors: 0,
            latched_safe: false,
        }
    }

    /// Current cycle count.
    pub fn cycle(&self) -> u64 {
        self.cycle
    }

    /// Clear a latched safe state (operator-acknowledged recovery).
    pub fn reset(&mut self) {
        self.latched_safe = false;
        self.consecutive_errors = 0;
    }

    /// Whether the supervisor has latched into the fail-safe state.
    pub fn is_latched(&self) -> bool {
        self.latched_safe
    }

    /// Perform exactly one control cycle against `hal`.
    pub async fn run_once<H: HardwareAbstractionLayer>(
        &mut self,
        hal: &H,
        timestamp_ms: u64,
    ) -> CycleOutcome {
        self.cycle += 1;
        let cycle = self.cycle;

        // A latched safe state means we keep applying the safe state every
        // cycle until an explicit reset — no agent involvement.
        if self.latched_safe {
            let safe = self.opts.fail_safe.commands();
            for c in &safe {
                let _ = hal.command(&self.device, c.clone()).await;
            }
            return self.finish(
                cycle,
                timestamp_ms,
                safe.clone(),
                safe,
                false,
                true,
                Some("latched safe state (awaiting reset)".into()),
            );
        }

        let readings = hal.read_all(&self.device).await;
        let readings = match readings {
            Ok(r) => r,
            Err(e) => {
                self.consecutive_errors += 1;
                let safe = self.opts.fail_safe.commands();
                for c in &safe {
                    let _ = hal.command(&self.device, c.clone()).await;
                }
                if self.consecutive_errors >= self.opts.max_consecutive_errors {
                    self.latched_safe = true;
                }
                let reason = format!("comms loss: {e}");
                return self.finish(
                    cycle,
                    timestamp_ms,
                    safe.clone(),
                    safe,
                    false,
                    true,
                    Some(reason),
                );
            }
        };
        self.consecutive_errors = 0;

        let snap = Snapshot::from_readings(self.device.clone(), readings, timestamp_ms);
        let out = self.agent.decide(&snap);
        let note = out.note.clone();

        let applied = match self.enforcer.enforce(&snap, out.commands.clone()) {
            crate::control::safety::Enforcement::Allow(cmds) => {
                for c in &cmds {
                    let _ = hal.command(&self.device, c.clone()).await;
                }
                cmds
            }
            crate::control::safety::Enforcement::Trip { commands, reason } => {
                for c in &commands {
                    let _ = hal.command(&self.device, c.clone()).await;
                }
                return self.finish(
                    cycle,
                    timestamp_ms,
                    out.commands,
                    commands,
                    false,
                    true,
                    Some(reason),
                );
            }
        };

        self.finish(
            cycle,
            timestamp_ms,
            out.commands,
            applied,
            true,
            false,
            note,
        )
    }

    #[allow(clippy::too_many_arguments)]
    fn finish(
        &self,
        cycle: u64,
        timestamp_ms: u64,
        requested: Vec<ControlCommand>,
        applied: Vec<ControlCommand>,
        healthy: bool,
        tripped: bool,
        note: Option<String>,
    ) -> CycleOutcome {
        let is_heartbeat = cycle % self.opts.heartbeat_every.max(1) == 0;
        let heartbeat = if is_heartbeat {
            Some(Heartbeat {
                device: self.device.clone(),
                agent: self.agent.name().to_string(),
                cycle,
                timestamp_ms,
                healthy,
                tripped,
                consecutive_errors: self.consecutive_errors,
                note: note.clone(),
            })
        } else {
            None
        };
        CycleOutcome {
            cycle,
            timestamp_ms,
            requested,
            applied,
            healthy,
            tripped,
            note,
            heartbeat,
        }
    }

    /// Run the loop forever (or for `max_cycles` if set), sleeping
    /// `tick_period_ms` between cycles and advancing `timestamp_ms` by the same.
    ///
    /// `tick` is an optional closure invoked once per cycle *after* the control
    /// step (e.g. to advance a Simulator). It receives the current cycle and
    /// timestamp.
    pub async fn run<H, F>(
        mut self,
        hal: &H,
        mut timestamp_ms: u64,
        max_cycles: Option<u64>,
        tick: F,
    ) -> Vec<CycleOutcome>
    where
        H: HardwareAbstractionLayer,
        F: Fn(u64, u64),
    {
        let mut out = Vec::new();
        loop {
            let outcome = self.run_once(hal, timestamp_ms).await;
            if let Some(hb) = &outcome.heartbeat {
                tracing::info!(
                    device = %hb.device,
                    agent = %hb.agent,
                    cycle = hb.cycle,
                    healthy = hb.healthy,
                    tripped = hb.tripped,
                    "heartbeat"
                );
            }
            if !outcome.healthy {
                tracing::warn!(
                    device = %self.device,
                    cycle = outcome.cycle,
                    tripped = outcome.tripped,
                    note = outcome.note.as_deref().unwrap_or(""),
                    "control cycle unhealthy"
                );
            }
            out.push(outcome);

            tick(self.cycle, timestamp_ms);
            timestamp_ms += self.opts.tick_period_ms;

            if let Some(max) = max_cycles {
                if self.cycle >= max {
                    break;
                }
            }

            tokio::time::sleep(Duration::from_millis(self.opts.tick_period_ms)).await;
        }
        out
    }
}

/// Build the standard supervisor used for a single rack: a [`CompositeAgent`]
/// of cooling + PDU + UPS agents behind the default [`SafetyEnforcer`].
pub fn rack_supervisor(
    device: DeviceId,
    setpoint_c: f64,
    opts: SupervisorOptions,
) -> Supervisor<crate::control::agents::CompositeAgent> {
    use crate::control::agents::{CompositeAgent, CoolingAgent, PduAgent, UpsAgent};
    let agents: Vec<Box<dyn ControlAgent>> = vec![
        Box::new(CoolingAgent::new(setpoint_c, 6.0, 0.5, 1.0, 0.4)),
        Box::new(PduAgent::new(80.0, 264.0, 198.0)),
        Box::new(UpsAgent::new(20.0, 80.0, 50.0)),
    ];
    let agent = CompositeAgent::new(format!("rack-{}", device), agents);
    Supervisor::new(
        device,
        agent,
        SafetyEnforcer::new(SafetyLimits::default()),
        opts,
    )
}
