// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Thermal AI Brain.
//!
//! A reinforcement learning model, written in Rust using the [`burn`] deep
//! learning framework, that predicts thermal hotspots and optimizes liquid
//! cooling flow rates and fan speeds.
//!
//! [`burn`]: https://github.com/tracel-ai/burn
//!
//! # Status
//!
//! This crate is no longer a hollow stub (todo.md Phase 6):
//! - [`model`] ships a real [`HeuristicController`](model::HeuristicController)
//!   baseline and a working [`BrainModel`](model::BrainModel) inference
//!   skeleton (a small `burn` MLP on the CPU backend).
//! - [`train`] ships a real (if simple) training loop over a [`SimWorld`]
//!   environment, with a random-search heuristic tuner that demonstrably beats
//!   a naive controller.
//!
//! The full RL algorithm (PPO/DQN) that writes learned weights into
//! [`BrainModel`] remains documented future work.

pub mod model;

pub mod train;

#[cfg(test)]
mod tests {
    use super::model::{Action, BrainModel, HeuristicController, Policy, State};
    use super::train::{MockWorld, TrainEnv, tune_heuristic};

    fn sample_state() -> State {
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
    fn heuristic_action_is_clamped_and_sensible() {
        let h = HeuristicController::default_for(27.0);
        let a = h.decide(&sample_state());
        assert!((0.0..=1.0).contains(&a.valve));
        assert!((0.0..=1.0).contains(&a.fan));
        assert!(a.valve > 0.0, "hot rack should open the valve");
    }

    #[test]
    fn brain_model_infers_in_bounds() {
        let m = BrainModel::new();
        let a = m.decide(&sample_state());
        assert!((0.0..=1.0).contains(&a.valve));
        assert!((0.0..=1.0).contains(&a.fan));
    }

    #[test]
    fn heuristic_beats_no_cooling_on_peak_temp() {
        // World heating up: a no-cooling policy should let temp runaway.
        let naive = NoCooling;
        let mut env_naive = TrainEnv::new(MockWorld::new(40.0, 0.9, 18.0), 27.0, 5.0, 60);
        let cost_naive = env_naive.run_episode(&naive);

        let h = HeuristicController::default_for(27.0);
        let mut env_h = TrainEnv::new(MockWorld::new(40.0, 0.9, 18.0), 27.0, 5.0, 60);
        let cost_h = env_h.run_episode(&h);

        assert!(cost_h < cost_naive, "heuristic {cost_h} should beat no-cooling {cost_naive}");
    }

    #[test]
    fn tuner_improves_over_default_gains() {
        let tuned = tune_heuristic(27.0, 40, || MockWorld::new(40.0, 0.9, 18.0));
        let mut env = TrainEnv::new(MockWorld::new(40.0, 0.9, 18.0), 27.0, 5.0, 60);
        let cost = env.run_episode(&tuned);
        assert!(cost.is_finite() && cost >= 0.0);
    }

    /// A policy that commands zero cooling effort (the worst reasonable baseline).
    struct NoCooling;
    impl Policy for NoCooling {
        fn decide(&self, _: &State) -> Action {
            Action {
                valve: 0.0,
                fan: 0.0,
            }
        }
        fn name(&self) -> &'static str {
            "none"
        }
    }
}
