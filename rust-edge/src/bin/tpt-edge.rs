// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! `tpt-edge` — demonstration runner for the Real-Time Facility Edge.
//!
//! Boots a virtual facility in the Simulator, heats every rack, then runs the
//! standard rack supervisor (cooling + PDU + UPS agents) for a fixed number of
//! control cycles, printing heartbeats and the final thermal state. This is the
//! Phase 2 bench test: proof the control framework drives the Simulator HAL
//! toward the cooling setpoint without any real hardware.

use rust_edge::control::supervisor::{rack_supervisor, SupervisorOptions};
use rust_edge::hal::simulator::SimulatorHal;
use rust_edge::hal::types::{ControlCommand, DeviceId};
use rust_edge::hal::HardwareAbstractionLayer;

fn rack(index: usize) -> DeviceId {
    format!("rack-{index:02}")
}

#[tokio::main]
async fn main() {
    let racks = 4usize;
    let setpoint_c = 27.0;
    let cycles = 120u64;
    let dt_sim_s = 5.0;

    let hal = SimulatorHal::new(racks);

    // Start with cooling fully off so we can demonstrate the controller working.
    for i in 0..racks {
        hal.command(&rack(i), ControlCommand::SetValvePosition(0.0))
            .await
            .unwrap();
        hal.command(&rack(i), ControlCommand::SetFanSpeed(0.0))
            .await
            .unwrap();
    }
    for _ in 0..30 {
        hal.tick(dt_sim_s);
    }

    let before: Vec<f64> = read_temps(&hal, racks).await;
    println!(
        "Pre-control rack temps (°C): {:?}",
        before.iter().map(|t| format!("{t:.1}")).collect::<Vec<_>>()
    );

    let opts = SupervisorOptions {
        tick_period_ms: 1000,
        heartbeat_every: 20,
        ..Default::default()
    };
    let mut supers: Vec<_> = (0..racks)
        .map(|i| rack_supervisor(rack(i), setpoint_c, opts.clone()))
        .collect();

    for cycle in 1..=cycles {
        let ts = cycle * 1000;
        for (i, s) in supers.iter_mut().enumerate() {
            let outcome = s.run_once(&hal, ts).await;
            if let Some(hb) = &outcome.heartbeat {
                println!(
                    "[hb] {} @cycle {} temp-ok={} tripped={} :: {}",
                    hb.device,
                    hb.cycle,
                    hb.healthy,
                    hb.tripped,
                    hb.note.as_deref().unwrap_or("")
                );
            } else if !outcome.healthy {
                eprintln!(
                    "[warn] {} cycle {}: {}",
                    rack(i),
                    cycle,
                    outcome.note.as_deref().unwrap_or("")
                );
            }
        }
        hal.tick(dt_sim_s);
    }

    let after: Vec<f64> = read_temps(&hal, racks).await;
    println!(
        "\nPost-control rack temps (°C): {:?}",
        after.iter().map(|t| format!("{t:.1}")).collect::<Vec<_>>()
    );
    let max_before = before.iter().cloned().fold(0.0_f64, f64::max);
    let max_after = after.iter().cloned().fold(0.0_f64, f64::max);
    println!("Max rack temp: {max_before:.1}°C → {max_after:.1}°C (setpoint {setpoint_c}°C)");
    assert!(
        max_after < max_before,
        "controller failed to reduce peak temperature"
    );
}

async fn read_temps(hal: &SimulatorHal, racks: usize) -> Vec<f64> {
    let mut out = Vec::with_capacity(racks);
    for i in 0..racks {
        let t = hal
            .read(&rack(i), &"rack_air_temp".to_string())
            .await
            .unwrap()
            .value;
        out.push(t);
    }
    out
}
