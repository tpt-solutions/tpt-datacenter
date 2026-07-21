// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! From-scratch reinforcement learning for the Thermal AI Brain.
//!
//! `burn` ships no Stable-Baselines3 equivalent, so the RL algorithm is
//! implemented here directly — there is no off-the-shelf PPO/DQN crate to lean
//! on. What lives in this module is a genuine **Gaussian policy-gradient
//! actor–critic** (REINFORCE with a learned value baseline). That is exactly
//! the core of PPO: a stochastic Gaussian actor, a value-function critic, and
//! a loss of `-log π(a|s) · A(s,a)` for the actor and `MSE(V(s), G)` for the
//! critic. Clipped surrogate / GAE are documented as straightforward
//! extensions on top of the same machinery.
//!
//! Training runs entirely on the CPU [`NdArray`] backend wrapped in
//! [`Autodiff`] so we get automatic differentiation for free; weights are
//! updated with `burn`'s own `Sgd` optimizer over the gradients returned by
//! [`AutodiffBackend::backward`].
//!
//! The learned actor mean is exported back into the inference
//! [`BrainModel`](crate::model::BrainModel) (CPU, no autodiff) that the rest
//! of the stack consumes through the [`Policy`](crate::model::Policy) trait.

use burn::backend::autodiff::Autodiff;
use burn::backend::ndarray::{NdArray, NdArrayDevice};
use burn::module::Param;
use burn::nn::{Linear, LinearConfig};
use burn::optim::{Optimizer, SgdConfig};
use burn::tensor::activation::relu;
use burn::tensor::backend::Backend;
use burn::tensor::{Distribution, Tensor, TensorData};

use crate::model::{Action, BrainModel, BrainNet, HotspotNet, State};

/// Training backend: CPU NdArray with automatic differentiation.
pub type RLBackend = Autodiff<NdArray>;

/// Inference backend: CPU NdArray, no autodiff (what [`BrainModel`] uses).
pub type InfBackend = NdArray;

const STATE_DIM: usize = 8;
const HIDDEN: usize = 16;
const ACTION_DIM: usize = 2;

/// Squash any tensor into (0,1) with a numerically stable sigmoid.
fn sigmoid<B: Backend, const D: usize>(x: Tensor<B, D>) -> Tensor<B, D> {
    x.neg().exp().add_scalar(1.0).recip()
}

/// Gaussian actor: 8 → 16 → 16 → 2 mean action logits.
///
/// The two logits are squashed through `sigmoid` into the physical [0, 1]
/// action box (valve, fan). The covariance is a single learned log-std shared
/// across both channels (diagonal Gaussian).
#[derive(burn::module::Module, Debug)]
pub struct ActorNet<B: burn::tensor::backend::Backend> {
    lin1: Linear<B>,
    lin2: Linear<B>,
    lin3: Linear<B>,
    /// Diagonal log-standard-deviation (length `ACTION_DIM`). Learned.
    log_std: Tensor<B, 1>,
}

impl<B: burn::tensor::backend::Backend<FloatElem = f32>> ActorNet<B> {
    /// Build a fresh (random) actor on `device`.
    pub fn new(device: &B::Device) -> Self {
        ActorNet {
            lin1: LinearConfig::new(STATE_DIM, HIDDEN)
                .with_bias(true)
                .init(device),
            lin2: LinearConfig::new(HIDDEN, HIDDEN)
                .with_bias(true)
                .init(device),
            lin3: LinearConfig::new(HIDDEN, ACTION_DIM)
                .with_bias(true)
                .init(device),
            log_std: Tensor::from_data(TensorData::from([(-0.5f32); ACTION_DIM]), device),
        }
    }

    /// Mean action (squashed to [0,1]) for a batch of states `[batch, 2]`.
    pub fn mean(&self, x: Tensor<B, 2>) -> Tensor<B, 2> {
        let x = relu(self.lin1.forward(x));
        let x = relu(self.lin2.forward(x));
        sigmoid(self.lin3.forward(x))
    }
}

/// Value (critic) network: 8 → 16 → 1 scalar state value.
#[derive(burn::module::Module, Debug)]
pub struct ValueNet<B: burn::tensor::backend::Backend> {
    lin1: Linear<B>,
    lin2: Linear<B>,
}

impl<B: burn::tensor::backend::Backend<FloatElem = f32>> ValueNet<B> {
    /// Build a fresh (random) critic on `device`.
    pub fn new(device: &B::Device) -> Self {
        ValueNet {
            lin1: LinearConfig::new(STATE_DIM, HIDDEN)
                .with_bias(true)
                .init(device),
            lin2: LinearConfig::new(HIDDEN, 1).with_bias(true).init(device),
        }
    }

    /// State value for a batch of states, shape `[batch, 1]`.
    pub fn forward(&self, x: Tensor<B, 2>) -> Tensor<B, 2> {
        let x = relu(self.lin1.forward(x));
        self.lin2.forward(x)
    }
}

/// A single (state, action, reward) transition collected during a rollout.
#[derive(Debug, Clone)]
struct Step {
    state: State,
    #[allow(dead_code)]
    action: Action,
    reward: f32,
}

/// Stochastic Gaussian policy wrapper around an [`ActorNet`] on the autodiff
/// backend. Provides reparameterized sampling, the squashed mean, and the
/// log-probability of a taken action under the diagonal Gaussian.
struct GaussianActor {
    net: ActorNet<RLBackend>,
    device: NdArrayDevice,
}

impl GaussianActor {
    fn new(device: &NdArrayDevice) -> Self {
        GaussianActor {
            net: ActorNet::<RLBackend>::new(device),
            device: *device,
        }
    }

    /// Squashed mean action (valve, fan) in [0,1] for a single state.
    #[allow(dead_code)]
    fn mean_action(&self, s: &State) -> (f32, f32) {
        let input =
            Tensor::<RLBackend, 2>::from_data(TensorData::from([s.to_array()]), &self.device);
        let m = self.net.mean(input);
        let v = m.to_data().into_vec::<f32>().unwrap();
        (v[0], v[1])
    }

    /// Reparameterized sample `a ~ N(mean, std)` and the squashed mean, both
    /// `[1, 2]` autodiff tensors.
    fn sample(&self, s: &State) -> (Tensor<RLBackend, 2>, Tensor<RLBackend, 2>) {
        let input =
            Tensor::<RLBackend, 2>::from_data(TensorData::from([s.to_array()]), &self.device);
        let mean = self.net.mean(input); // [1, 2] in (0,1)
        let std = self.net.log_std.clone().exp().unsqueeze(); // [1, 2]
        let noise = Tensor::<RLBackend, 2>::random_like(&mean, Distribution::Normal(0.0, 1.0));
        let action = mean.clone() + noise * std;
        (action, mean)
    }

    /// Log-probability of `action` under the diagonal Gaussian with squashed
    /// mean `mean` and learned std. Returns a `[1, 1]` tensor (summed over the
    /// two action channels).
    fn log_prob(
        &self,
        action: Tensor<RLBackend, 2>,
        mean: Tensor<RLBackend, 2>,
    ) -> Tensor<RLBackend, 2> {
        let std = self.net.log_std.clone().exp().unsqueeze(); // [1, 2]
        let var = std.clone().powf_scalar(2.0);
        let two_pi = 2.0 * std::f32::consts::PI;
        // log N(a; mean, std) = -0.5 * ((a-mean)^2 / var + log(2π var))
        let diff = action - mean;
        let inner = diff.powf_scalar(2.0).div(var.clone()) + var.mul_scalar(two_pi).log();
        let logp = inner.neg().mul_scalar(0.5);
        logp.sum_dim(1) // [1, 1]
    }
}

/// Train a Gaussian actor–critic against `world` and return an inference
/// [`BrainModel`] whose valve/fan decisions reproduce the learned policy mean.
///
/// The returned model emits the *mean* of the learned Gaussian policy — the
/// deterministic deployment policy. Stochastic sampling is only used for
/// exploration during training.
pub fn train_rl(
    world: &mut dyn crate::train::SimWorld,
    setpoint_c: f32,
    dt_s: f32,
    horizon: usize,
    episodes: usize,
    lr: f32,
) -> BrainModel {
    let device = NdArrayDevice::default();
    let mut actor = GaussianActor::new(&device);
    let mut critic = ValueNet::<RLBackend>::new(&device);

    let mut actor_opt = SgdConfig::new().init();
    let mut critic_opt = SgdConfig::new().init();
    let gamma = 0.99f32;

    for ep in 0..episodes {
        // --- Rollout under the current stochastic policy ------------------
        let mut steps: Vec<Step> = Vec::with_capacity(horizon);
        for _ in 0..horizon {
            let s = world.observe(setpoint_c);
            let (action_t, _mean_t) = actor.sample(&s);
            let a = action_t.clamp(0.0, 1.0);
            let v = a.to_data().into_vec::<f32>().unwrap();
            let act = Action {
                valve: v[0].clamp(0.0, 1.0),
                fan: v[1].clamp(0.0, 1.0),
            };
            // Reward = -cost: penalize thermal violation and energy use.
            let temp_c = world.step(&act, dt_s);
            let reward = -crate::model::thermal_cost(temp_c, &act, setpoint_c);
            steps.push(Step {
                state: s,
                action: act,
                reward,
            });
        }

        // --- Discounted returns G_t --------------------------------------
        let mut g = 0.0f32;
        let mut returns: Vec<f32> = vec![0.0; steps.len()];
        for t in (0..steps.len()).rev() {
            g = steps[t].reward + gamma * g;
            returns[t] = g;
        }

        // --- Actor + critic forward/backward over the episode ------------
        let mut actor_loss = Tensor::<RLBackend, 2>::zeros([1, 1], &device);
        let mut critic_loss = Tensor::<RLBackend, 2>::zeros([1, 1], &device);

        for (t, step) in steps.iter().enumerate() {
            let obs = Tensor::<RLBackend, 2>::from_data(
                TensorData::from([step.state.to_array()]),
                &device,
            );
            let value = critic.forward(obs.clone()); // [1, 1]
            let (action_t, mean_t) = actor.sample(&step.state);
            let a = action_t.clamp(0.0, 1.0);
            let logp = actor.log_prob(a, mean_t); // [1, 1]

            let adv = returns[t] - value.clone().to_data().into_vec::<f32>().unwrap()[0];
            // Policy loss: -log π(a|s) · A  (REINFORCE with baseline).
            actor_loss = actor_loss + logp.neg().mul_scalar(adv);
            // Value loss: (V(s) - G)^2.
            let g_t = Tensor::<RLBackend, 2>::from_data(TensorData::from([[returns[t]]]), &device);
            critic_loss = critic_loss + value.sub(g_t).powf_scalar(2.0);
        }

        let n = steps.len() as f32;
        actor_loss = actor_loss.div_scalar(n);
        critic_loss = critic_loss.div_scalar(n);

        // --- Backward + SGD update ---------------------------------------
        let actor_grads =
            burn::optim::GradientsParams::from_grads(actor_loss.backward(), &actor.net);
        let critic_grads =
            burn::optim::GradientsParams::from_grads(critic_loss.backward(), &critic);
        actor.net = actor_opt.step(lr as f64, actor.net, actor_grads);
        critic = critic_opt.step((lr * 0.5) as f64, critic, critic_grads);

        if ep % 10 == 0 || ep == episodes - 1 {
            let mean_ret: f32 = returns.iter().sum::<f32>() / returns.len() as f32;
            tracing::debug!("rl ep {ep}: mean_return {mean_ret:.3}");
        }
    }

    export_actor_to_brain(actor.net, &device)
}

/// Build an inference [`BrainModel`] whose forward pass reproduces the learned
/// actor's *mean* action, by copying the three linear layers' weights into a
/// fresh [`BrainNet`] on the inference backend.
fn export_actor_to_brain(actor: ActorNet<RLBackend>, _device: &NdArrayDevice) -> BrainModel {
    let device = NdArrayDevice::default();
    let mut net = BrainNet::<InfBackend>::new(&device);

    let l1w = Tensor::<NdArray, 2>::from_data(
        actor.lin1.weight.val().to_data().convert::<f32>(),
        &device,
    );
    let l1b = Tensor::<NdArray, 1>::from_data(
        actor
            .lin1
            .bias
            .expect("bias present")
            .val()
            .to_data()
            .convert::<f32>(),
        &device,
    );
    let l2w = Tensor::<NdArray, 2>::from_data(
        actor.lin2.weight.val().to_data().convert::<f32>(),
        &device,
    );
    let l2b = Tensor::<NdArray, 1>::from_data(
        actor
            .lin2
            .bias
            .expect("bias present")
            .val()
            .to_data()
            .convert::<f32>(),
        &device,
    );
    let l3w = Tensor::<NdArray, 2>::from_data(
        actor.lin3.weight.val().to_data().convert::<f32>(),
        &device,
    );
    let l3b = Tensor::<NdArray, 1>::from_data(
        actor
            .lin3
            .bias
            .expect("bias present")
            .val()
            .to_data()
            .convert::<f32>(),
        &device,
    );

    net.lin1.weight = Param::from_tensor(l1w);
    net.lin1.bias = Some(Param::from_tensor(l1b));
    net.lin2.weight = Param::from_tensor(l2w);
    net.lin2.bias = Some(Param::from_tensor(l2b));
    net.lin3.weight = Param::from_tensor(l3w);
    net.lin3.bias = Some(Param::from_tensor(l3b));

    BrainModel::from_net(net)
}

/// A single labelled hotspot example: a state and whether the rack later
/// thermally violated the setpoint (`label` = 1.0) or not (0.0).
#[derive(Debug, Clone)]
pub struct HotspotSample {
    pub state: State,
    pub label: f32,
}

/// Train the [`HotspotNet`] head in a supervised fashion (binary
/// cross-entropy) and return a [`BrainModel`] whose
/// [`predict_hotspot`](crate::model::BrainModel::predict_hotspot) uses it.
///
/// `base` is a control net (e.g. from [`train_rl`]) carried over so the
/// returned model still issues cooling actions; only the hotspot head is
/// trained here.
pub fn train_hotspot(
    base: BrainNet<InfBackend>,
    samples: &[HotspotSample],
    epochs: usize,
    lr: f32,
) -> BrainModel {
    let device = NdArrayDevice::default();
    let mut head = HotspotNet::<RLBackend>::new(&device);
    let mut opt = SgdConfig::new().init();

    for ep in 0..epochs {
        let mut total_loss = 0.0f32;
        for s in samples {
            let input =
                Tensor::<RLBackend, 2>::from_data(TensorData::from([s.state.to_array()]), &device);
            let logit = head.forward(input); // [1, 1]
            let target = Tensor::<RLBackend, 2>::from_data(TensorData::from([[s.label]]), &device);
            // Binary cross-entropy with logits:
            // L = max(logit,0) - logit*target + log(1+exp(-|logit|))
            let z = logit.clone();
            let loss = z.clone().clamp(0.0, f32::MAX) - z.clone() * target.clone()
                + (z.clone().neg().exp().add_scalar(1.0)).log();
            total_loss += loss.to_data().into_vec::<f32>().unwrap()[0];

            let grads = burn::optim::GradientsParams::from_grads(loss.backward(), &head);
            head = opt.step(lr as f64, head, grads);
        }
        if ep % 10 == 0 || ep == epochs - 1 {
            tracing::debug!(
                "hotspot ep {ep}: mean_loss {:.4}",
                total_loss / samples.len().max(1) as f32
            );
        }
    }

    // Export the trained head to the inference backend.
    let device = NdArrayDevice::default();
    let mut out_head = HotspotNet::<InfBackend>::new(&device);
    out_head.lin1.weight = Param::from_tensor(Tensor::<NdArray, 2>::from_data(
        head.lin1.weight.val().to_data().convert::<f32>(),
        &device,
    ));
    out_head.lin1.bias = Some(Param::from_tensor(Tensor::<NdArray, 1>::from_data(
        head.lin1
            .bias
            .expect("bias present")
            .val()
            .to_data()
            .convert::<f32>(),
        &device,
    )));
    out_head.lin2.weight = Param::from_tensor(Tensor::<NdArray, 2>::from_data(
        head.lin2.weight.val().to_data().convert::<f32>(),
        &device,
    ));
    out_head.lin2.bias = Some(Param::from_tensor(Tensor::<NdArray, 1>::from_data(
        head.lin2
            .bias
            .expect("bias present")
            .val()
            .to_data()
            .convert::<f32>(),
        &device,
    )));

    BrainModel::from_nets(base, out_head)
}

/// Convenience: train the full brain (control + hotspot) from a world.
///
/// Returns a [`BrainModel`] ready for [`serve::BrainServer`].
pub fn train_brain(
    world: &mut dyn crate::train::SimWorld,
    setpoint_c: f32,
    dt_s: f32,
    horizon: usize,
    episodes: usize,
    lr: f32,
) -> BrainModel {
    let control = train_rl(world, setpoint_c, dt_s, horizon, episodes, lr);
    // Re-train a hotspot head on analytically-derived labels. We keep the
    // control net and only learn the hotspot head.
    let base = control.net().clone();
    let samples = simple_hotspot_labels(setpoint_c);
    train_hotspot(base, &samples, 40, 0.05)
}

/// Deterministic hotspot labels for the demo: states with a large positive
/// temperature error (already hot) are labelled as future-violation risks.
fn simple_hotspot_labels(setpoint_c: f32) -> Vec<HotspotSample> {
    let mut v = Vec::new();
    for err_i in 0..20 {
        let err = -1.0 + err_i as f32 * 0.1; // -1.0 .. +1.0
        let s = State {
            temp_error: err,
            inlet_temp: 0.4,
            it_load: 0.7,
            ups_soc: 0.8,
            valve: 0.5,
            fan: 0.5,
            coolant_temp: 0.5,
            bias: 1.0,
        };
        // Positive label when the implied temperature is at/above setpoint.
        let temp = setpoint_c + err * 10.0;
        v.push(HotspotSample {
            state: s,
            label: if temp >= setpoint_c { 1.0 } else { 0.0 },
        });
    }
    v
}
