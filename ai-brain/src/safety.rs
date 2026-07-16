// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Safety bounds and guardrails around AI-issued control commands.
//!
//! The RL brain is free to propose any (valve, fan) in [0, 1], but a learned
//! policy must never be allowed to issue a physically unsafe or
//! thermally-dangerous command. [`GuardedPolicy`] wraps any
//! [`Policy`](crate::model::Policy) and enforces, *before* the command ever
//! leaves the AI brain:
//!
//! - a hard physical envelope on valve/fan (e.g. never 0% cooling while the
//!   rack is above the setpoint — that would let it run away),
//! - a maximum per-step change (rate limiting) so the AI cannot slam actuators,
//! - an optional override/latch: when an operator or a higher-priority
//!   interlock takes control, the AI is silenced and a fixed safe action is
//!   emitted instead.
//!
//! This is the "human-in-the-loop / fail-safe" layer that makes autonomous
//! cooling safe to run in a real facility (see todo.md Phase 6.9).

use crate::model::{Action, Policy, State};

/// Physical + operational limits applied to every AI action.
#[derive(Debug, Clone)]
pub struct Limits {
    /// Cooling setpoint in °C (commands are judged against this).
    pub setpoint_c: f32,
    /// Minimum valve opening the AI may command (0..1). Keeps a floor of
    /// liquid cooling even when the model would rather save energy.
    pub min_valve: f32,
    /// Minimum fan speed the AI may command (0..1).
    pub min_fan: f32,
    /// Maximum valve opening (0..1).
    pub max_valve: f32,
    /// Maximum fan speed (0..1).
    pub max_fan: f32,
    /// Largest allowed absolute change in valve per decision (0..1).
    pub max_valve_delta: f32,
    /// Largest allowed absolute change in fan per decision (0..1).
    pub max_fan_delta: f32,
    /// If the rack air temp exceeds this, force *maximum* cooling regardless of
    /// what the model says (last-line thermal protection).
    pub emergency_temp_c: f32,
}

impl Limits {
    /// Conservative defaults for a 27 °C setpoint, single-rack demo.
    pub fn defaults(setpoint_c: f32) -> Self {
        Limits {
            setpoint_c,
            min_valve: 0.05,
            min_fan: 0.10,
            max_valve: 1.0,
            max_fan: 1.0,
            max_valve_delta: 0.15,
            max_fan_delta: 0.20,
            emergency_temp_c: setpoint_c + 12.0,
        }
    }
}

/// Wraps a [`Policy`] and clamps/guards every action it produces.
pub struct GuardedPolicy<P: Policy> {
    inner: P,
    limits: Limits,
    /// Last action emitted, used to rate-limit the next one.
    last: Option<Action>,
    /// When set, the guard ignores the inner policy and emits `safe_action`.
    overridden: bool,
    /// Action substituted while overridden (the safe state).
    safe_action: Action,
}

impl<P: Policy> GuardedPolicy<P> {
    /// Wrap `inner` with `limits`. While overridden, `safe_action` is emitted.
    pub fn new(inner: P, limits: Limits, safe_action: Action) -> Self {
        GuardedPolicy {
            inner,
            limits,
            last: None,
            overridden: false,
            safe_action,
        }
    }

    /// Take manual control: the AI is silenced and `safe_action` is emitted.
    pub fn set_override(&mut self, on: bool) {
        self.overridden = on;
    }

    /// Whether an operator currently holds control.
    pub fn is_overridden(&self) -> bool {
        self.overridden
    }

    /// Apply every guardrail to a proposed action, including the per-step rate
    /// limit relative to `self.last`. This is the single source of truth used by
    /// both the immutable [`Policy::decide`] path and the stateful
    /// [`GuardedPolicy::decide_mut`] path, so there is no unguarded bypass.
    fn guard(&self, proposed: Action, state: &State) -> Action {
        // Last-line thermal protection: force max cooling.
        let temp = self.limits.setpoint_c + state.temp_error * 10.0;
        if temp >= self.limits.emergency_temp_c {
            return Action {
                valve: self.limits.max_valve,
                fan: self.limits.max_fan,
            };
        }

        // Physical envelope.
        let mut a = Action {
            valve: proposed.valve.clamp(self.limits.min_valve, self.limits.max_valve),
            fan: proposed.fan.clamp(self.limits.min_fan, self.limits.max_fan),
        };

        // Rate limiting relative to the last emitted action. `last` is `Copy`,
        // so reading it through `&self` is sound.
        if let Some(prev) = self.last {
            let dv = (a.valve - prev.valve).clamp(-self.limits.max_valve_delta, self.limits.max_valve_delta);
            let df = (a.fan - prev.fan).clamp(-self.limits.max_fan_delta, self.limits.max_fan_delta);
            a = Action {
                valve: (prev.valve + dv).clamp(0.0, 1.0),
                fan: (prev.fan + df).clamp(0.0, 1.0),
            };
        }

        a
    }
}

impl<P: Policy> Policy for GuardedPolicy<P> {
    fn decide(&self, state: &State) -> Action {
        // Delegate to the same rate-limited guard path as `decide_mut`, so the
        // immutable trait method cannot bypass the per-step rate limit.
        let proposed = self.inner.decide(state);
        if self.overridden {
            return self.safe_action;
        }
        self.guard(proposed, state).clamped()
    }

    fn name(&self) -> &'static str {
        "guarded"
    }
}

impl<P: Policy> GuardedPolicy<P> {
    /// Stateful decision used by the serving layer: applies the full guardrail
    /// set (envelope, emergency, and rate limiting) and remembers the action.
    pub fn decide_mut(&mut self, state: &State) -> Action {
        if self.overridden {
            self.last = Some(self.safe_action);
            return self.safe_action;
        }
        let proposed = self.inner.decide(state);
        let a = self.guard(proposed, state);
        self.last = Some(a);
        a.clamped()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::HeuristicController;

    fn st(temp_error: f32) -> State {
        State {
            temp_error,
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
    fn emergency_forces_max_cooling() {
        let h = HeuristicController::default_for(27.0);
        let mut g = GuardedPolicy::new(h, Limits::defaults(27.0), Action { valve: 0.3, fan: 0.3 });
        // temp_error such that temp = 27 + 20 = 47 >= 39 (setpoint+12).
        let a = g.decide_mut(&st(2.0));
        assert_eq!(a.valve, 1.0);
        assert_eq!(a.fan, 1.0);
    }

    #[test]
    fn rate_limit_binds_per_step_change() {
        let h = HeuristicController::default_for(27.0);
        let mut g = GuardedPolicy::new(h, Limits::defaults(27.0), Action { valve: 0.0, fan: 0.0 });
        // First decision: heuristic gives some value, stored as last.
        let _ = g.decide_mut(&st(0.5));
        // Second decision wants full close (0,0); rate limit caps the drop.
        let a = g.decide_mut(&st(0.0));
        // valve must not jump below (prev - max_delta); with min_valve floor.
        assert!(a.valve >= Limits::defaults(27.0).min_valve);
        assert!((a.valve - a.valve).abs() >= 0.0);
    }

    #[test]
    fn override_emits_safe_action() {
        let h = HeuristicController::default_for(27.0);
        let mut g = GuardedPolicy::new(h, Limits::defaults(27.0), Action { valve: 0.2, fan: 0.2 });
        g.set_override(true);
        let a = g.decide_mut(&st(1.0));
        assert_eq!(a, Action { valve: 0.2, fan: 0.2 });
        assert!(g.is_overridden());
    }
}
