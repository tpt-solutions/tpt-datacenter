// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Thermal AI Brain.
//!
//! A reinforcement learning model, written in Rust using the [`burn`] deep
//! learning framework, that predicts thermal hotspots and optimizes liquid
//! cooling flow rates and fan speeds.
//!
//! [`burn`]: https://github.com/tracel-ai/burn

pub mod model {
    //! RL model definitions (state/action spaces, network, inference).
}

pub mod train {
    //! Training environment and RL algorithm (PPO/DQN) implemented from scratch.
}
