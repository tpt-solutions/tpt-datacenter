# ai-brain

Thermal AI Brain. A reinforcement learning model, written in Rust using the
[`burn`](https://github.com/tracel-ai/burn) deep learning framework, that
predicts thermal hotspots and optimizes liquid cooling flow rates and fan
speeds.

Written in Rust (not Python/PyTorch) to keep the entire stack — edge agents,
telemetry, and AI — memory-safe and auditable, consistent with the project's
"no backdoors, mathematically verifiable" security story. This is a deliberate
tradeoff: `burn`'s RL ecosystem is far less mature than PyTorch's (no
Stable-Baselines3 equivalent), so RL algorithms (e.g. PPO/DQN) and training
loops are implemented from scratch rather than reused off the shelf.

This crate is a member of the root Cargo workspace and is intended to be
published to crates.io as a standalone, reusable RL/thermal-modeling crate
once stable.

See [todo.md](../todo.md) Phase 6.
