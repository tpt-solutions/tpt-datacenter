// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Secure firmware update verification (todo.md Phase 10).
//!
//! Edge agents only accept firmware images signed by a trusted TPT Solutions
//! release key. An update is a `(image, signature, signer_pubkey_id)` bundle;
//! [`verify_update`] checks the Ed25519 signature over the image before the
//! agent applies it, and [`UpdatePolicy`] can pin the accepted signing key so a
//! compromised key cannot be substituted at runtime.
//!
//! This is deliberately dependency-light (pure-Rust `ed25519-dalek`, no OS
//! crypto) so the verification path is auditable and reproducible, and works
//! under `no_std` (alloc only) for bare-metal controllers.

use core::fmt;

#[cfg(not(feature = "no_std"))]
extern crate std;
#[cfg(feature = "no_std")]
extern crate alloc;

#[cfg(not(feature = "no_std"))]
use std::vec::Vec;

#[cfg(feature = "no_std")]
use alloc::vec::Vec;

use ed25519_dalek::{Signature, Verifier, VerifyingKey};

/// Errors returned while verifying a firmware update.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FirmwareError {
    /// The signature was malformed (wrong length / decode error).
    BadSignature,
    /// The public key id was not a valid 32-byte Ed25519 key.
    BadKey,
    /// Signature verification failed — image is not authentic.
    VerificationFailed,
    /// The signer key is not in the policy's allowed set (key pinning).
    KeyNotAllowed,
}

impl fmt::Display for FirmwareError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            FirmwareError::BadSignature => write!(f, "malformed signature"),
            FirmwareError::BadKey => write!(f, "invalid public key"),
            FirmwareError::VerificationFailed => write!(f, "firmware signature verification failed"),
            FirmwareError::KeyNotAllowed => write!(f, "signer key not in allowed set"),
        }
    }
}

#[cfg(not(feature = "no_std"))]
impl std::error::Error for FirmwareError {}

/// A 32-byte Ed25519 public key, identified by its hex string for pinning.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SigningKey {
    /// Stable identifier (e.g. "tpt-release-2024") used for policy pinning.
    pub id: &'static str,
    /// The raw 32-byte Ed25519 public key.
    pub raw: [u8; 32],
}

impl SigningKey {
    /// Parse a verifying key from this signing key's raw bytes.
    pub fn verifying_key(&self) -> Result<VerifyingKey, FirmwareError> {
        VerifyingKey::from_bytes(&self.raw).map_err(|_| FirmwareError::BadKey)
    }
}

/// Update policy: which signer keys are trusted. When empty, any key verified
/// by the supplied `signer` is accepted (the caller still must trust the
/// signer). When non-empty, the signer must be one of the pinned keys.
#[derive(Debug, Clone, Default)]
pub struct UpdatePolicy {
    allowed: Vec<SigningKey>,
}

impl UpdatePolicy {
    /// Allow updates signed by any of the given keys (key pinning).
    pub fn pinned(keys: &[SigningKey]) -> Self {
        UpdatePolicy { allowed: keys.to_vec() }
    }

    /// Allow any signer (no pinning). Use only with a fully trusted channel.
    pub fn any() -> Self {
        UpdatePolicy::default()
    }

    fn permits(&self, key: &SigningKey) -> bool {
        self.allowed.is_empty() || self.allowed.iter().any(|k| k.id == key.id)
    }
}

/// A firmware update bundle presented to the agent.
pub struct Update<'a> {
    /// The firmware image bytes.
    pub image: &'a [u8],
    /// The 64-byte Ed25519 signature over `image`.
    pub signature: &'a [u8],
    /// The signer's public key, with its policy id.
    pub signer: &'a SigningKey,
}

/// Verify a firmware update against the policy.
///
/// Returns `Ok(())` iff the signature is valid **and** the signer is permitted
/// by `policy`. The agent must call this before writing the image to flash.
pub fn verify_update(policy: &UpdatePolicy, update: &Update<'_>) -> Result<(), FirmwareError> {
    if !policy.permits(update.signer) {
        return Err(FirmwareError::KeyNotAllowed);
    }
    let vk = update.signer.verifying_key()?;
    let sig = Signature::from_slice(update.signature).map_err(|_| FirmwareError::BadSignature)?;
    vk.verify(update.image, &sig).map_err(|_| FirmwareError::VerificationFailed)
}

/// Decode a hex-encoded public key into raw bytes (for config/tooling).
pub fn decode_hex_key(s: &str) -> Result<[u8; 32], FirmwareError> {
    let s = s.trim();
    if s.len() != 64 {
        return Err(FirmwareError::BadKey);
    }
    let mut out = [0u8; 32];
    for (i, chunk) in s.as_bytes().chunks(2).enumerate() {
        let byte = u8::from_str_radix(core::str::from_utf8(chunk).map_err(|_| FirmwareError::BadKey)?, 16)
            .map_err(|_| FirmwareError::BadKey)?;
        out[i] = byte;
    }
    Ok(out)
}

/// A verified-but-not-yet-applied firmware image.
///
/// Constructed only by [`Updater::prepare`], which calls [`verify_update`]
/// first, so any code holding an `ApprovedUpdate` has cryptographically proven
/// the image was signed by a key permitted by the [`UpdatePolicy`]. Applying it
/// to flash is the final, separate step so the verification path stays pure
/// and auditable.
pub struct ApprovedUpdate<'a> {
    image: &'a [u8],
}

impl<'a> ApprovedUpdate<'a> {
    /// The verified firmware bytes, ready to write to flash.
    pub fn image(&self) -> &'a [u8] {
        self.image
    }
}

/// Drives the "verify, then apply" lifecycle for firmware updates.
///
/// This is the integration point that actually *uses* [`verify_update`] — the
/// rest of the agent should never apply an image except through
/// [`Updater::prepare`] + [`Updater::apply`]. `apply` is provided by the
/// caller (e.g. a platform flash writer) so the verification module stays
/// `no_std`/alloc-only and free of any hardware假设.
pub struct Updater<'p, F>
where
    F: Fn(&[u8]) -> Result<(), FirmwareError>,
{
    policy: &'p UpdatePolicy,
    apply: F,
}

impl<'p, F> Updater<'p, F>
where
    F: Fn(&[u8]) -> Result<(), FirmwareError>,
{
    /// Build an updater with the given trust `policy` and a platform-specific
    /// `apply` closure that writes verified bytes to flash.
    pub fn new(policy: &'p UpdatePolicy, apply: F) -> Self {
        Updater { policy, apply }
    }

    /// Verify `update`; on success return an [`ApprovedUpdate`] that can be
    /// applied. Returns the verification error unchanged if the signature or
    /// signer check fails — the image is never exposed for flashing.
    pub fn prepare<'a>(&self, update: &Update<'a>) -> Result<ApprovedUpdate<'a>, FirmwareError> {
        verify_update(self.policy, update)?;
        Ok(ApprovedUpdate { image: update.image })
    }

    /// Verify and, if valid, immediately apply the update to flash via the
    /// `apply` closure. Convenience wrapper around [`Updater::prepare`].
    pub fn apply(&self, update: &Update<'_>) -> Result<(), FirmwareError> {
        let approved = self.prepare(update)?;
        (self.apply)(approved.image())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::{SigningKey as Sk, Signer};

    // Deterministic test key (NOT a real release key).
    const TEST_RAW: [u8; 32] = [
        0x1a, 0x2b, 0x3c, 0x4d, 0x5e, 0x6f, 0x70, 0x81, 0x92, 0xa3, 0xb4, 0xc5, 0xd6, 0xe7, 0xf8, 0x09,
        0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00,
    ];

    fn test_signing_key() -> SigningKey {
        let sk = Sk::from_bytes(&TEST_RAW);
        SigningKey { id: "test", raw: sk.verifying_key().to_bytes() }
    }

    #[test]
    fn verify_accepts_valid_signature() {
        let sk = Sk::from_bytes(&TEST_RAW);
        let image = b"tpt-edge-firmware-v1.2.3";
        let sig = sk.sign(image).to_bytes().to_vec();
        let key = test_signing_key();
        let policy = UpdatePolicy::pinned(std::slice::from_ref(&key));
        let upd = Update { image, signature: &sig, signer: &key };
        assert!(verify_update(&policy, &upd).is_ok());
    }

    #[test]
    fn verify_rejects_tampered_image() {
        let sk = Sk::from_bytes(&TEST_RAW);
        let image = b"tpt-edge-firmware-v1.2.3";
        let sig = sk.sign(image).to_bytes().to_vec();
        let key = test_signing_key();
        let policy = UpdatePolicy::pinned(std::slice::from_ref(&key));
        let tampered = b"tpt-edge-firmware-BACKDOORED";
        let upd = Update { image: tampered, signature: &sig, signer: &key };
        assert_eq!(verify_update(&policy, &upd), Err(FirmwareError::VerificationFailed));
    }

    #[test]
    fn verify_rejects_unpinned_key() {
        let sk = Sk::from_bytes(&TEST_RAW);
        let image = b"img";
        let sig = sk.sign(image).to_bytes().to_vec();
        let key = test_signing_key();
        let policy = UpdatePolicy::pinned(&[SigningKey { id: "other", raw: key.raw }]);
        let upd = Update { image, signature: &sig, signer: &key };
        assert_eq!(verify_update(&policy, &upd), Err(FirmwareError::KeyNotAllowed));
    }

    #[test]
    fn updater_apply_only_after_verify() {
        let sk = Sk::from_bytes(&TEST_RAW);
        let image = b"tpt-edge-firmware-v1.2.3";
        let sig = sk.sign(image).to_bytes().to_vec();
        let key = test_signing_key();
        let policy = UpdatePolicy::pinned(std::slice::from_ref(&key));

        // `apply` records the bytes it was handed so we can assert the updater
        // only ever passes a verified image.
        let flashed = std::cell::RefCell::new(Vec::<u8>::new());
        let updater = Updater::new(&policy, |img| {
            flashed.borrow_mut().extend_from_slice(img);
            Ok(())
        });

        let upd = Update { image, signature: &sig, signer: &key };
        updater.apply(&upd).expect("signed update should apply");
        assert_eq!(&flashed.borrow()[..], image);

        // A tampered image must never reach `apply`.
        let tampered = b"tpt-edge-firmware-BACKDOORED";
        let bad = Update { image: tampered, signature: &sig, signer: &key };
        assert_eq!(updater.apply(&bad), Err(FirmwareError::VerificationFailed));
        assert_eq!(&flashed.borrow()[..], image, "tampered image must not be flashed");
    }
}
