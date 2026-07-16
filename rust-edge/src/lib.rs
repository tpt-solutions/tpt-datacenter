// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Real-Time Facility Edge agents.
//!
//! Bare-metal / `no_std` control loops for PDUs, UPS/BMS, and cooling valves.
//!
//! The [`hal`] module (Hardware Abstraction Layer) is the hosted/development
//! surface: it provides the Simulator backend and the Real (Redfish/Modbus/
//! IPMI) backends behind a single trait, plus the config-driven router. It
//! relies on `std` and is excluded from `no_std` builds; bare-metal targets
//! implement a narrower, platform-specific HAL subset directly.
//!
//! See [`todo.md`](https://github.com/TPT-Solutions/tpt-datacenter/blob/main/todo.md)
//! Phase 1 (HAL) and Phase 2 (edge agents).

#![cfg_attr(feature = "no_std", no_std)]

#[cfg(not(feature = "no_std"))]
pub mod hal;

#[cfg(not(feature = "no_std"))]
pub mod control;

/// Secure firmware update verification (Ed25519 signed updates, Phase 10).
/// Available in both std and `no_std` (alloc) builds.
pub mod firmware;

#[cfg(feature = "no_std")]
extern crate alloc;
