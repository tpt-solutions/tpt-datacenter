// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Thermal AI Brain — model definitions.
//!
//! Two things live here:
//! 1. [`HeuristicController`] — a real, deterministic, rule/PID-based baseline
//!    controller. It is what the dashboard can show *today* and what the RL
//!    model is trained to beat (todo.md Phase 6 "baseline/heuristic
//!    controller for comparison").
//! 2. [`BrainModel`] — a small [`burn`](https://github.com/tracel-ai/burn)
//!    multi-layer perceptron used for inference. It compiles and runs on the
//!    CPU [`NdArray`](burn::backend::NdArray) backend, and is the inference
//!    skeleton the full RL training loop (see [`train`]) will populate with
//!    learned weights.
//!
//! Both expose the same [`Policy`] surface so the rest of the stack is agnostic
//! to whether a decision came from the heuristic or the neural model.

use burn::backend::{ndarray::NdArrayDevice, NdArray};
use burn::module::Module;
use burn::nn::{Linear, LinearConfig};
use burn::tensor::activation::relu;
use burn::tensor::{Tensor, TensorData};

/// Backend used for inference. CPU-only, no GPU libs required (so it builds in
/// CI and on the edge). The training pipeline may use `wgpu`/`tch` instead.
pub type BrainBackend = NdArray;

/// Normalized observation of one rack's thermal/electrical state.
///
/// Values are normalized to roughly [-1, 1] so the network sees a stable input
/// distribution regardless of absolute temperature scale.
#[derive(Debug, Clone, Copy)]
pub struct State {
    /// Rack air temperature minus setpoint, normalized by the warning band.
    pub temp_error: f32,
    /// Inlet (supply) air temperature, normalized 0..1 over [10, 45] °C.
    pub inlet_temp: f32,
    /// IT load as a fraction of nameplate (0..1).
    pub it_load: f32,
    /// UPS state of charge (0..1).
    pub ups_soc: f32,
    /// Current valve position (0..1).
    pub valve: f32,
    /// Current fan speed (0..1).
    pub fan: f32,
    /// Coolant supply temperature, normalized 0..1 over [10, 30] °C.
    pub coolant_temp: f32,
    /// Bias term (1.0) to give the affine layers an intercept.
    pub bias: f32,
}

impl State {
    /// Pack the observation into the `[1, 8]` tensor the network expects.
    pub fn to_array(&self) -> [f32; 8] {
        [
            self.temp_error,
            self.inlet_temp,
            self.it_load,
            self.ups_soc,
            self.valve,
            self.fan,
            self.coolant_temp,
            self.bias,
        ]
    }
}

/// A cooling action: how far to open the valve and spin the fans, both 0..1.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Action {
    pub valve: f32,
    pub fan: f32,
}

impl Action {
    /// Clamp both channels into the physical [0, 1] envelope.
    pub fn clamped(&self) -> Action {
        Action {
            valve: self.valve.clamp(0.0, 1.0),
            fan: self.fan.clamp(0.0, 1.0),
        }
    }
}

/// A control policy: maps a [`State`] to a cooling [`Action`].
pub trait Policy {
    /// Decide the cooling action for the given state.
    fn decide(&self, state: &State) -> Action;
    /// Human-readable name for logs / dashboards.
    fn name(&self) -> &'static str;
}

/// A small feed-forward network: 8 -> 16 -> 16 -> 2 (valve, fan).
#[derive(Module, Debug)]
pub struct BrainNet<B: burn::tensor::backend::Backend> {
    pub lin1: Linear<B>,
    pub lin2: Linear<B>,
    pub lin3: Linear<B>,
}

impl<B: burn::tensor::backend::Backend> BrainNet<B> {
    /// Build an untrained network on `device`.
    pub fn new(device: &B::Device) -> Self {
        let lin1 = LinearConfig::new(8, 16).with_bias(true).init(device);
        let lin2 = LinearConfig::new(16, 16).with_bias(true).init(device);
        let lin3 = LinearConfig::new(16, 2).with_bias(true).init(device);
        BrainNet { lin1, lin2, lin3 }
    }

    /// Forward pass; returns a `[batch, 2]` tensor of (valve, fan) in raw logits.
    pub fn forward(&self, x: Tensor<B, 2>) -> Tensor<B, 2> {
        let x = relu(self.lin1.forward(x));
        let x = relu(self.lin2.forward(x));
        self.lin3.forward(x)
    }
}

/// Hotspot-prediction head: 8 -> 16 -> 1, squashed to a 0..1 risk score.
///
/// Trained in a supervised fashion (see [`crate::rl::train_hotspot`]) from
/// labelled (state, will-exceed-setpoint) transitions, it lets the dashboard
/// and edge flag an impending thermal violation *before* it happens, so the
/// cooling policy can pre-empt it (todo.md Phase 6.6).
#[derive(Module, Debug)]
pub struct HotspotNet<B: burn::tensor::backend::Backend> {
    pub lin1: Linear<B>,
    pub lin2: Linear<B>,
}

impl<B: burn::tensor::backend::Backend> HotspotNet<B> {
    /// Build an untrained hotspot head on `device`.
    pub fn new(device: &B::Device) -> Self {
        HotspotNet {
            lin1: LinearConfig::new(8, 16).with_bias(true).init(device),
            lin2: LinearConfig::new(16, 1).with_bias(true).init(device),
        }
    }

    /// Forward pass; returns a `[batch, 1]` tensor of raw logits.
    pub fn forward(&self, x: Tensor<B, 2>) -> Tensor<B, 2> {
        let x = relu(self.lin1.forward(x));
        self.lin2.forward(x)
    }
}

/// Inference wrapper around [`BrainNet`] on the CPU backend.
pub struct BrainModel {
    net: BrainNet<BrainBackend>,
    hotspot: HotspotNet<BrainBackend>,
    device: NdArrayDevice,
}

impl BrainModel {
    /// Create an untrained model. Weights are random; calling [`Policy::decide`]
    /// before training yields a (roughly) neutral action — useful for
    /// smoke-testing the inference path and as the skeleton the RL trainer fills.
    pub fn new() -> Self {
        let device = NdArrayDevice::default();
        BrainModel {
            net: BrainNet::new(&device),
            hotspot: HotspotNet::new(&device),
            device,
        }
    }

    /// Wrap already-constructed inference networks (e.g. ones whose weights
    /// were produced by the RL trainer in [`crate::rl`]).
    pub fn from_net(net: BrainNet<BrainBackend>) -> Self {
        let device = NdArrayDevice::default();
        BrainModel {
            net,
            hotspot: HotspotNet::new(&device),
            device,
        }
    }

    /// Build from both a control net and a hotspot net (used after training).
    pub fn from_nets(net: BrainNet<BrainBackend>, hotspot: HotspotNet<BrainBackend>) -> Self {
        BrainModel {
            net,
            hotspot,
            device: NdArrayDevice::default(),
        }
    }

    /// Produce the raw (valve, fan) pair as floats before clamping.
    pub fn raw(&self, state: &State) -> (f32, f32) {
        let input = TensorData::from([state.to_array()]);
        let x = Tensor::<BrainBackend, 2>::from_data(input, &self.device);
        let out = self.net.forward(x);
        let v = out.to_data().into_vec::<f32>().unwrap();
        // saturate the raw logits into 0..1 via sigmoid for a sane default.
        let sig = |x: f32| 1.0 / (1.0 + (-x).exp());
        (sig(v[0]), sig(v[1]))
    }

    /// Predict the probability (0..1) that this rack will thermally violate the
    /// setpoint in the near future, given the current state.
    pub fn predict_hotspot(&self, state: &State) -> f32 {
        let input = TensorData::from([state.to_array()]);
        let x = Tensor::<BrainBackend, 2>::from_data(input, &self.device);
        let out = self.hotspot.forward(x);
        let v = out.to_data().into_vec::<f32>().unwrap();
        let sig = |x: f32| 1.0 / (1.0 + (-x).exp());
        sig(v[0]).clamp(0.0, 1.0)
    }

    /// Borrow the underlying control network (e.g. to retrain a hotspot head).
    pub fn net(&self) -> &BrainNet<BrainBackend> {
        &self.net
    }
}

impl Default for BrainModel {
    fn default() -> Self {
        Self::new()
    }
}

impl Policy for BrainModel {
    fn decide(&self, state: &State) -> Action {
        let (valve, fan) = self.raw(state);
        Action { valve, fan }.clamped()
    }

    fn name(&self) -> &'static str {
        "burn-mlp"
    }
}

/// Deterministic proportional baseline controller.
///
/// Drives valve + fan up as rack air temperature climbs above the setpoint, and
/// eases off as it approaches. This is the "dumb but safe" controller the RL
/// model must beat on energy use while holding the same thermal envelope.
pub struct HeuristicController {
    /// Cooling setpoint in °C.
    setpoint_c: f32,
    /// Proportional gain on the temperature error (per °C).
    gain: f32,
    /// Floor applied to the fan even when cold (keeps airflow moving).
    fan_floor: f32,
}

impl HeuristicController {
    /// Build a baseline controller.
    pub fn new(setpoint_c: f32, gain: f32, fan_floor: f32) -> Self {
        HeuristicController {
            setpoint_c,
            gain,
            fan_floor,
        }
    }

    /// Build with the same defaults the rust-edge cooling agent uses.
    pub fn default_for(setpoint_c: f32) -> Self {
        HeuristicController::new(setpoint_c, 6.0, 0.2)
    }
}

impl Policy for HeuristicController {
    fn decide(&self, state: &State) -> Action {
        // Reconstruct the absolute rack-air temp from the normalized error.
        let temp = self.setpoint_c + state.temp_error * 10.0;
        let error = temp - self.setpoint_c;
        let valve = (self.gain * error / 100.0).clamp(0.0, 1.0);
        let fan = (self.fan_floor + self.gain * error / 100.0).clamp(0.0, 1.0);
        Action { valve, fan }.clamped()
    }

    fn name(&self) -> &'static str {
        "heuristic-pid"
    }
}

/// Cost of a trajectory segment: penalize both thermal violation and energy.
///
/// Returns a positive scalar; lower is better. `temp_c` is the rack air temp,
/// `action` is the cooling effort, `setpoint_c` the target.
pub fn thermal_cost(temp_c: f32, action: &Action, setpoint_c: f32) -> f32 {
    let temp_penalty = if temp_c > setpoint_c {
        (temp_c - setpoint_c).powi(2)
    } else {
        0.0
    };
    let energy = 0.6 * action.valve + 0.4 * action.fan;
    temp_penalty + 0.1 * energy
}
