// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Model serving / inference pipeline.
//!
//! This is the bridge between a trained [`BrainModel`] (or any [`Policy`]) and
//! the edge control agents. It turns a cooling [`Action`] — expressed as
//! fractions in [0, 1] — into the actuator [`Command`] the edge expects,
//! expressed as percentages in [0, 100].
//!
//! `ai-brain` is a standalone crate and intentionally does *not* depend on
//! `rust-edge`; the [`Command`] type here mirrors
//! `rust_edge::hal::types::ControlCommand` field-for-field so the edge can
//! `impl From<ai_brain::serve::Command>` with zero allocation. See
//! [`Command::commands`] for the conversion shape.

use crate::model::{Action, Policy, State};
use crate::safety::GuardedPolicy;

/// Actuator command emitted by the AI brain, in the edge's native units.
///
/// Mirrors `rust_edge::hal::types::ControlCommand` (valve/fan are 0–100 %).
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Command {
    /// Cooling valve position, 0.0–100.0 percent open.
    pub valve_position: f64,
    /// Cooling fan speed, 0.0–100.0 percent of maximum.
    pub fan_speed: f64,
}

impl Command {
    /// Build a command from a fractional [`Action`] (0..1 → 0..100).
    pub fn from_action(a: Action) -> Self {
        Command {
            valve_position: (a.valve as f64 * 100.0).clamp(0.0, 100.0),
            fan_speed: (a.fan as f64 * 100.0).clamp(0.0, 100.0),
        }
    }

    /// Convert to the `rust_edge` HAL command type.
    ///
    /// `rust-edge` is the consumer; provide the conversion there, e.g.:
    /// ```ignore
    /// impl From<ai_brain::serve::Command> for rust_edge::hal::types::ControlCommand {
    ///     fn from(c: ai_brain::serve::Command) -> Self {
    ///         use rust_edge::hal::types::ControlCommand::*;
    ///         // The real deployment routes valve + fan into separate commands.
    ///         SetValvePosition(c.valve_position)
    ///     }
    /// }
    /// ```
    /// (The fan command is issued alongside, see [`Command::commands`].)
    pub fn commands(&self) -> (f64, f64) {
        (self.valve_position, self.fan_speed)
    }
}

/// A served, guarded policy: decides an [`Action`] and emits a [`Command`].
///
/// Wraps the policy in [`GuardedPolicy`] so every command that leaves the AI
/// brain has already passed the safety envelope, emergency, and rate-limit
/// checks. The serving loop in the edge calls [`BrainServer::step`] once per
/// control cycle.
pub struct BrainServer<P: Policy> {
    guarded: GuardedPolicy<P>,
}

impl<P: Policy> BrainServer<P> {
    /// Construct a server around `policy` with the given safety `limits`.
    /// `safe_action` is emitted when an operator takes manual override.
    pub fn new(policy: P, limits: crate::safety::Limits, safe_action: Action) -> Self {
        BrainServer {
            guarded: GuardedPolicy::new(policy, limits, safe_action),
        }
    }

    /// Take manual control away from the AI (operator override / safe state).
    pub fn set_override(&mut self, on: bool) {
        self.guarded.set_override(on);
    }

    /// Whether an operator currently holds control.
    pub fn is_overridden(&self) -> bool {
        self.guarded.is_overridden()
    }

    /// One inference + safety pass: returns the actuator [`Command`] for `state`.
    pub fn step(&mut self, state: &State) -> Command {
        let action = self.guarded.decide_mut(state);
        Command::from_action(action)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::HeuristicController;

    fn st() -> State {
        State {
            temp_error: 0.5,
            inlet_temp: 0.4,
            it_load: 0.7,
            ups_soc: 0.8,
            valve: 0.5,
            fan: 0.5,
            coolant_temp: 0.5,
            bias: 1.0,
        }
    }

    #[test]
    fn serve_emits_percentage_command() {
        let h = HeuristicController::default_for(27.0);
        let mut srv = BrainServer::new(
            h,
            crate::safety::Limits::defaults(27.0),
            Action {
                valve: 0.3,
                fan: 0.3,
            },
        );
        let cmd = srv.step(&st());
        assert!((0.0..=100.0).contains(&cmd.valve_position));
        assert!((0.0..=100.0).contains(&cmd.fan_speed));
    }

    #[test]
    fn serve_override_yields_safe_percent_command() {
        let h = HeuristicController::default_for(27.0);
        let mut srv = BrainServer::new(
            h,
            crate::safety::Limits::defaults(27.0),
            Action {
                valve: 0.2,
                fan: 0.2,
            },
        );
        srv.set_override(true);
        let cmd = srv.step(&st());
        assert!((cmd.valve_position - 20.0).abs() < 1e-3);
        assert!((cmd.fan_speed - 20.0).abs() < 1e-3);
    }
}
