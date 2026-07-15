// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Discrete PID controller.
//!
//! Pure arithmetic with no allocation or I/O, so it is safe to run inside a
//! deterministic control loop on the edge. The control law is written for a
//! "more output cools/protects" convention: `error = measurement - setpoint`,
//! so a positive error (too hot / over limit) drives a positive (cooling)
//! output. Callers clamp the output to the actuator's physical range.

/// A discrete-time PID controller with integral windup limiting.
///
/// `update` is called once per control cycle with the latest measurement and
/// the elapsed `dt` (seconds). It returns the (clamped) control output.
#[derive(Debug, Clone)]
pub struct PidController {
    pub kp: f64,
    pub ki: f64,
    pub kd: f64,
    pub setpoint: f64,
    pub out_min: f64,
    pub out_max: f64,
    /// Anti-windup bound on the accumulated integral term.
    pub integral_limit: f64,
    integral: f64,
    prev_error: f64,
}

impl PidController {
    /// Create a controller. `out_min`/`out_max` bound the actuator (e.g. 0–100
    /// for a valve/fan percentage).
    pub fn new(kp: f64, ki: f64, kd: f64, setpoint: f64, out_min: f64, out_max: f64) -> Self {
        PidController {
            kp,
            ki,
            kd,
            setpoint,
            out_min,
            out_max,
            integral_limit: (out_max - out_min).abs().max(1.0),
            integral: 0.0,
            prev_error: 0.0,
        }
    }

    /// Reset accumulated state (used after a fault trip or on agent start).
    pub fn reset(&mut self) {
        self.integral = 0.0;
        self.prev_error = 0.0;
    }

    /// Advance one control step and return the clamped output.
    pub fn update(&mut self, measurement: f64, dt: f64) -> f64 {
        let error = measurement - self.setpoint;
        let dt = dt.max(0.0);

        self.integral += error * dt;
        self.integral = self
            .integral
            .clamp(-self.integral_limit, self.integral_limit);

        let derivative = if dt > 0.0 {
            (error - self.prev_error) / dt
        } else {
            0.0
        };
        self.prev_error = error;

        let raw = self.kp * error + self.ki * self.integral + self.kd * derivative;
        raw.clamp(self.out_min, self.out_max)
    }
}
