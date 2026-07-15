// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Training environment and RL algorithm scaffolding (todo.md Phase 6).
//!
//! This module is deliberately *incremental*: it ships a working, if simple,
//! training loop so `ai-brain` is no longer a hollow stub, while the full RL
//! algorithm (PPO/DQN) is explicitly left as future work.
//!
//! What is real today:
//! - [`SimWorld`] — the environment interface a cooling policy is trained
//!   against. The rust-edge [`SimulatorHal`](rust_edge::hal::simulator::SimulatorHal)
//!   satisfies this contract (see the integration note below), so the same
//!   trainer can optimize against the project's own physics model.
//! - [`TrainEnv`] — runs an episode and scores it with [`thermal_cost`].
//! - [`MockWorld`] — a self-contained first-order thermal stand-in so the
//!   trainer and its tests run without booting the simulator.
//! - [`tune_heuristic`] — a real random-search trainer that finds heuristic
//!   gains beating a naive controller, proving the loop end-to-end.
//!
//! What is future work (documented, not faked): the burn MLP weights are
//! initialized randomly and [`BrainModel`] currently acts as a neutral prior;
//! replacing [`tune_heuristic`] with a policy-gradient / PPO update that writes
//! learned weights into [`BrainModel`] is the Phase 6 deliverable.

use crate::model::{Action, Policy, State};

/// An environment the policy is trained/evaluated against.
///
/// Implementors advance a rack's thermal state one step under a cooling
/// [`Action`] and expose the resulting normalized [`State`].
pub trait SimWorld {
    /// Advance the world by `dt_s` seconds under `action`; return the resulting
    /// rack air temperature in °C.
    fn step(&mut self, action: &Action, dt_s: f32) -> f32;

    /// Current normalized observation for the given cooling setpoint.
    fn observe(&self, setpoint_c: f32) -> State;
}

/// Episode runner + scorer.
pub struct TrainEnv<W: SimWorld> {
    /// The environment.
    pub world: W,
    /// Cooling setpoint (°C).
    pub setpoint_c: f32,
    /// Step length (seconds).
    pub dt_s: f32,
    /// Number of steps per episode.
    pub horizon: usize,
}

impl<W: SimWorld> TrainEnv<W> {
    /// Build a training environment.
    pub fn new(world: W, setpoint_c: f32, dt_s: f32, horizon: usize) -> Self {
        TrainEnv {
            world,
            setpoint_c,
            dt_s,
            horizon,
        }
    }

    /// Run one episode under `policy`, returning the summed [`thermal_cost`].
    pub fn run_episode(&mut self, policy: &dyn Policy) -> f32 {
        let mut cost = 0.0f32;
        for _ in 0..self.horizon {
            let s = self.world.observe(self.setpoint_c);
            let a = policy.decide(&s);
            let t = self.world.step(&a, self.dt_s);
            cost += crate::model::thermal_cost(t, &a, self.setpoint_c);
        }
        cost
    }
}

/// A self-contained first-order thermal model for fast, dependency-free
/// training and tests. Heat enters with IT load; cooling removes heat
/// proportional to the temperature gap above the coolant supply, scaled by the
/// valve + fan effort.
pub struct MockWorld {
    temp_c: f32,
    it_load: f32,
    coolant_temp: f32,
    ambient: f32,
}

impl MockWorld {
    /// Build a mock rack starting at `start_temp_c` under `it_load` (0..1).
    pub fn new(start_temp_c: f32, it_load: f32, coolant_temp: f32) -> Self {
        MockWorld {
            temp_c: start_temp_c,
            it_load,
            coolant_temp,
            ambient: 22.0,
        }
    }
}

impl SimWorld for MockWorld {
    fn step(&mut self, action: &Action, dt_s: f32) -> f32 {
        let heat_in = self.it_load * 0.8;
        let gap = (self.temp_c - self.coolant_temp).max(0.0);
        let cooling = (action.valve * 0.5 + action.fan * 0.3) * gap;
        // Gentle pull toward ambient when nearly idle.
        let relax = 0.02 * (self.ambient - self.temp_c);
        let d = (heat_in - cooling + relax) * dt_s * 0.05;
        self.temp_c = (self.temp_c + d).clamp(0.0, 80.0);
        self.temp_c
    }

    fn observe(&self, setpoint_c: f32) -> State {
        State {
            temp_error: (self.temp_c - setpoint_c) / 10.0,
            inlet_temp: ((self.ambient - 10.0) / 35.0).clamp(0.0, 1.0),
            it_load: self.it_load,
            ups_soc: 0.8,
            valve: 0.5,
            fan: 0.5,
            coolant_temp: ((self.coolant_temp - 10.0) / 20.0).clamp(0.0, 1.0),
            bias: 1.0,
        }
    }
}

/// Random-search tuner: sample heuristic gains, keep the best by episode cost.
///
/// This is a deliberately simple but *real* optimizer — it demonstrably
/// improves over a naive fixed controller and exercises the full
/// [`SimWorld`]/[`TrainEnv`] loop. Replacing it with PPO is the Phase 6 task.
pub fn tune_heuristic(
    setpoint_c: f32,
    trials: usize,
    seed_world: impl Fn() -> MockWorld,
) -> crate::model::HeuristicController {
    let mut best = crate::model::HeuristicController::new(setpoint_c, 6.0, 0.2);
    let mut best_cost = f32::INFINITY;

    for _ in 0..trials {
        let gain = 1.0 + (fast_rand() % 120) as f32 / 10.0; // 1.0 .. 13.0
        let floor = (fast_rand() % 40) as f32 / 100.0; // 0.0 .. 0.39
        let cand = crate::model::HeuristicController::new(setpoint_c, gain, floor);
        let mut env = TrainEnv::new(seed_world(), setpoint_c, 5.0, 60);
        let cost = env.run_episode(&cand);
        if cost < best_cost {
            best_cost = cost;
            best = cand;
        }
    }
    best
}

/// Tiny deterministic LCG so the trainer has no external RNG dependency.
fn fast_rand() -> u32 {
    use std::sync::atomic::{AtomicU32, Ordering};
    static S: AtomicU32 = AtomicU32::new(0x9E37_79B9);
    let v = S.load(Ordering::Relaxed);
    let n = v.wrapping_mul(1_664_525).wrapping_add(1_013_904_223);
    S.store(n, Ordering::Relaxed);
    n >> 8
}
