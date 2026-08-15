//! CSID key custody.
//!
//! Blueprint H1 forbids plain key storage and E1.3 requires the key to live on
//! the terminal. Those two together mean the OS secure store: Windows DPAPI via
//! the Credential Manager, the macOS Keychain, the Secret Service on Linux.
//!
//! # The shape of this module is the security property
//!
//! There is `store`, there is `has_key`, and there is no `get`. A caller can
//! ask whether this terminal is onboarded; it cannot ask for the key. Signing
//! reads it internally in `signing::sign_invoice` and drops it before
//! returning, so the private key never becomes a value any other layer can
//! hold, log, serialise or accidentally send.
//!
//! ZATCA §6.5 forbids any key-export affordance. That is easy to satisfy by not
//! writing the function — and easy to violate later by adding a "just for
//! debugging" getter, which is why the absence is documented here rather than
//! left to be noticed.

use serde::{Deserialize, Serialize};

/// Where a terminal is in its ZATCA onboarding.
///
/// Mirrors `egs_unit.csid_status` on the server so the two cannot describe the
/// same terminal differently.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum CsidStatus {
    NotStarted,
    ComplianceCsid,
    ProductionCsid,
    Live,
    Revoked,
    Expired,
}

/// What the UI may know about this terminal's key.
///
/// Metadata only: whether a key exists, which serial it carries, when it
/// expires. Never the key.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KeyPresence {
    pub present: bool,
    pub status: CsidStatus,
    pub serial: Option<String>,
    pub expires_at: Option<String>,
}

/// Reports whether this terminal holds a CSID key.
///
/// Not implemented against a real keystore yet: onboarding produces the key,
/// and onboarding needs the verified format. Returning "not started" honestly
/// is better than a stub that claims a key exists.
pub fn key_presence() -> KeyPresence {
    KeyPresence {
        present: false,
        status: CsidStatus::NotStarted,
        serial: None,
        expires_at: None,
    }
}

// Deliberately absent: any function returning private key material.
//
// If one is ever needed for a test, it belongs behind a build tag that cannot
// reach a release binary — not here.
