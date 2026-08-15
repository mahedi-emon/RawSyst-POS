//! Local ZATCA signing — the seam, and the gate.
//!
//! Blueprint E1.3 RULE 1 is marked LOCKED: *"the device's CSID private key and
//! the local ICV/PIH chain must exist on the terminal itself, not only in the
//! cloud. Signing is a LOCAL operation."* This module is where that happens.
//!
//! # It refuses, and that is the point
//!
//! Nothing here signs anything yet. The byte-level UBL 2.1 XML, the
//! canonicalisation, and the QR TLV layout must come from ZATCA's published
//! standards, and those values are still marked `__VERIFY__` in the regulatory
//! registry. A terminal that produced a plausible-looking document would get
//! invoices rejected at scale — and each rejected invoice has already consumed
//! an ICV that cannot be given back.
//!
//! So this mirrors the server exactly: `zatca::HasherFor` and
//! `zatca::SubmitterFor` refuse for the same reason, and a sale that cannot be
//! signed stays unsigned and visible rather than being quietly finished.
//!
//! # What the private key must never do
//!
//! Leave this module. Not to the React layer, not into a Tauri command result,
//! not into a log line, not into the local SQLite file. The signing operation
//! takes bytes and returns a signature; the key is read from the OS keystore
//! inside `sign` and dropped before it returns. There is deliberately no
//! function that exports it, because ZATCA §6.5 forbids the affordance
//! existing at all.

use serde::{Deserialize, Serialize};

/// What the terminal produces once signing is implemented.
///
/// Three separate artefacts, because they are three different things and the
/// server stores them in three columns: the signed document, the signature
/// over it, and the payload derived from that signature for the receipt.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SignedDocument {
    /// The canonical signed UBL 2.1 document. This is what ZATCA receives.
    pub xml: String,
    /// The ECDSA stamp over that document.
    pub stamp: String,
    /// The base64 TLV payload printed on the receipt.
    pub qr_tlv: String,
}

/// Why signing is unavailable, in words a cashier can act on.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SigningUnavailable {
    pub reason: String,
    pub detail: String,
}

/// Reports whether this terminal can sign.
///
/// False until the document format is verified. The UI reads this and says so
/// plainly — a till that silently behaved as though everything were fine would
/// let a shop trade for a week before anyone discovered nothing was reportable.
pub fn signing_available() -> bool {
    false
}

/// Signs an invoice locally.
///
/// Returns the refusal until the format is verified. When it is, this is the
/// only place that changes: it reads the CSID key from the OS keystore, builds
/// the UBL, signs, derives the QR, and returns all three. Nothing above it
/// needs to know.
pub fn sign_invoice(_payload: &str) -> Result<SignedDocument, SigningUnavailable> {
    Err(SigningUnavailable {
        reason: "e_invoicing_not_verified".into(),
        detail: "This terminal cannot sign invoices yet: the document format \
                 has not been verified against ZATCA's published standard. \
                 Sales are recorded and queued, and none has been reported."
            .into(),
    })
}
