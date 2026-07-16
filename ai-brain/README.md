# ai-brain

Thermal AI Brain. A reinforcement learning model, written in Rust using the
[`burn`](https://github.com/tracel-ai/burn) deep learning framework, that
predicts thermal hotspots and optimizes liquid cooling flow rates and fan
speeds.

Written in Rust (not Python/PyTorch) to keep the entire stack — edge agents,
telemetry, and AI — memory-safe and auditable, consistent with the project's
"no backdoors, mathematically verifiable" security story. This is a deliberate
tradeoff: `burn`'s RL ecosystem is far less mature than PyTorch's (no
Stable-Baselines3 equivalent), so the RL algorithm and training loop are
implemented from scratch rather than reused off the shelf.

## What's implemented (todo.md Phase 6)

- **`model`** — the inference MLP (`BrainModel`, `burn` CPU/`NdArray` backend),
  the `HeuristicController` baseline, a `HotspotNet` hotspot-prediction head,
  and the shared `Policy` / `State` / `Action` types.
- **`train`** — the `SimWorld` environment contract (satisfied by
  `rust-edge`'s `SimulatorHal`), a self-contained `MockWorld` physics model,
  the episode runner, and a random-search heuristic tuner.
- **`rl`** — a from-scratch **Gaussian policy-gradient actor–critic**
  (`train_rl`) — the PPO/DQN-equivalent core, implemented over `burn`
  autodiff + the `Sgd` optimizer. Also `train_hotspot` (supervised hotspot
  head) and `train_brain` (combined).
- **`safety`** — `GuardedPolicy`: physical envelope, emergency max-cooling,
  per-step rate limiting, and operator override / safe-state latch.
- **`serve`** — `BrainServer`: the inference/serving loop that turns a guarded
  policy into edge actuator `Command`s (fractions → 0–100 %).

## Status

Phase 6 is implemented end-to-end and covered by unit + integration tests
(`cargo test -p ai-brain`). The learned policy is thermally competent versus
the heuristic baseline; the 20–30 % energy-savings target is validated over
longer training against the full `SimulatorHal` physics.

## crates.io

This crate is a member of the root Cargo workspace and is intended to be
published to crates.io as a standalone, reusable RL/thermal-modeling crate.
`cargo publish --dry-run` passes. It stays workspace-internal until the API
stabilizes.

See [todo.md](../todo.md) Phase 6.
