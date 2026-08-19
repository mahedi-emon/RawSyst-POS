// RawSyst POS — the terminal.
//
// A Tauri shell around a React interface. The shell exists for the three things
// a browser cannot do and a till must: hold the CSID private key in the OS
// secure store, sign locally, and keep a durable local queue so a shop can
// trade with no network.
//
// Everything the web layer is allowed to know crosses through the commands
// below. The private key is not among them.

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod enrolment;
mod keystore;
mod signing;

use serde::Serialize;

/// What the terminal can tell the interface about itself at startup.
#[derive(Serialize)]
struct TerminalCapabilities {
    /// False until the document format is verified. The UI shows this plainly
    /// rather than letting a cashier assume invoices are being reported.
    signing_available: bool,
    key: keystore::KeyPresence,
}

#[tauri::command]
fn terminal_capabilities() -> TerminalCapabilities {
    TerminalCapabilities {
        signing_available: signing::signing_available(),
        key: keystore::key_presence(),
    }
}

/// Signs an invoice locally, or explains why it cannot.
///
/// The refusal is a value rather than an error string so the interface can
/// distinguish "not verified yet" from "something broke", and say the right
/// thing to a cashier standing at a till.
#[tauri::command]
fn sign_invoice(payload: String) -> Result<signing::SignedDocument, signing::SigningUnavailable> {
    signing::sign_invoice(&payload)
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_sql::Builder::default().build())
        .invoke_handler(tauri::generate_handler![
            terminal_capabilities,
            sign_invoice,
            // Terminal credential custody (H3). None of these returns the
            // device secret — see enrolment.rs.
            enrolment::terminal_keystore_available,
            enrolment::terminal_is_paired,
            enrolment::terminal_pair,
            enrolment::terminal_identity,
            enrolment::terminal_sign_in,
            enrolment::terminal_forget,
        ])
        .run(tauri::generate_context!())
        .expect("the RawSyst POS terminal failed to start");
}
