//! Terminal credential custody, blueprint H3.
//!
//! The device secret is what makes this machine a registered terminal. It is
//! long-lived, it is not a token, and if it leaks somebody else's computer can
//! sell as this till until an owner revokes it.
//!
//! # The shape of this module is the security property
//!
//! Exactly like `keystore`: there is `pair`, there is `forget`, there is
//! `is_paired`, and there is **no getter**. The secret is written into the OS
//! secure store and read back only inside this file, by the two functions that
//! must present it. It is never returned across the Tauri boundary, so it can
//! never be a JavaScript value — which rules out, structurally rather than by
//! discipline, the secret ending up in `localStorage`, in the SQLite file, in a
//! React state tree, in a console log or in a crash report.
//!
//! Adding a `get_device_secret` command later would undo all of that in one
//! line, which is why the absence is documented here rather than left to be
//! noticed.
//!
//! # Why the HTTP calls live here rather than in the web layer
//!
//! Both operations that need the secret — proving this terminal, and signing a
//! cashier in on it — are performed here so the secret never crosses the
//! boundary. What crosses back is the terminal's identity (not secret) and the
//! short-lived session tokens (which the web layer is supposed to hold).
//!
//! Nothing in this file touches the CSID key or ZATCA. Pairing a terminal is
//! not onboarding it for e-invoicing; those are separate acts and the second is
//! behind the P1 verification gate.

use serde::{Deserialize, Serialize};

/// The keyring entry this terminal's credential lives under.
///
/// One entry per installation. A second terminal on the same machine is not a
/// supported arrangement — each till is its own device with its own ZATCA chain
/// (E1.3 RULE 5), and sharing a machine between two would give one chain two
/// writers.
const SERVICE: &str = "com.rawsyst.pos";
const ACCOUNT: &str = "terminal-device-secret";

/// The header a paired terminal identifies itself with.
///
/// A header rather than a body field or a query parameter, so the secret never
/// lands in a request log that captures bodies, or a proxy log that captures
/// URLs.
const SECRET_HEADER: &str = "X-Device-Secret";

/// What a paired terminal knows about itself. Never includes the secret.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TerminalIdentity {
    pub device_id: String,
    pub terminal_label: String,
    pub store_id: String,
    pub company_id: String,
}

/// The short-lived session a cashier signs in for.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TerminalSession {
    pub access_token: String,
    pub refresh_token: String,
}

/// What the server said when it refused.
///
/// Carried across as a message rather than a code because the server already
/// phrases these for the person standing at the till — "Till 2 has been revoked
/// and can no longer be used" — and re-deriving that here would produce two
/// wordings for one situation that could drift apart.
#[derive(Debug, Serialize)]
pub struct TerminalError {
    pub message: String,
}

impl TerminalError {
    fn of(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
        }
    }
}

fn entry() -> Result<keyring::Entry, TerminalError> {
    keyring::Entry::new(SERVICE, ACCOUNT)
        .map_err(|_| TerminalError::of("This terminal has no secure store available."))
}

/// Reads the stored secret. **Private on purpose** — see the module comment.
fn secret() -> Result<String, TerminalError> {
    entry()?
        .get_password()
        .map_err(|_| TerminalError::of("This terminal is not paired."))
}

/// Whether this build can hold a credential at all.
#[tauri::command]
pub fn terminal_keystore_available() -> bool {
    entry().is_ok()
}

/// Whether this machine has already been paired.
#[tauri::command]
pub fn terminal_is_paired() -> bool {
    secret().is_ok()
}

/// Redeems an enrolment code and stores the secret.
///
/// The secret goes straight from the response into the keystore. It is held in
/// a local binding for the length of this function and never returned.
#[tauri::command]
pub async fn terminal_pair(
    api_base_url: String,
    code: String,
) -> Result<TerminalIdentity, TerminalError> {
    #[derive(Deserialize)]
    struct Enrolled {
        device_id: String,
        device_secret: String,
        terminal_label: String,
        store_id: String,
        company_id: String,
    }

    let body = serde_json::json!({
        "code": code,
        "os": std::env::consts::OS,
        "app_version": env!("CARGO_PKG_VERSION"),
    });

    let response = reqwest::Client::new()
        .post(format!(
            "{}/api/v1/devices/enrol",
            api_base_url.trim_end_matches('/')
        ))
        .json(&body)
        .send()
        .await
        .map_err(|_| TerminalError::of("This terminal cannot reach the server."))?;

    if !response.status().is_success() {
        return Err(TerminalError::of(server_message(response).await));
    }

    let enrolled: Enrolled = response.json().await.map_err(|_| {
        TerminalError::of("The server sent something this terminal did not understand.")
    })?;

    entry()?
        .set_password(&enrolled.device_secret)
        .map_err(|_| TerminalError::of("This terminal could not store its credential securely."))?;

    Ok(TerminalIdentity {
        device_id: enrolled.device_id,
        terminal_label: enrolled.terminal_label,
        store_id: enrolled.store_id,
        company_id: enrolled.company_id,
    })
}

/// Asks the server who this terminal is, presenting the stored secret.
#[tauri::command]
pub async fn terminal_identity(api_base_url: String) -> Result<TerminalIdentity, TerminalError> {
    let response = reqwest::Client::new()
        .get(format!(
            "{}/api/v1/devices/identity",
            api_base_url.trim_end_matches('/')
        ))
        .header(SECRET_HEADER, secret()?)
        .send()
        .await
        .map_err(|_| TerminalError::of("This terminal cannot reach the server."))?;

    if !response.status().is_success() {
        return Err(TerminalError::of(server_message(response).await));
    }

    response.json().await.map_err(|_| {
        TerminalError::of("The server sent something this terminal did not understand.")
    })
}

/// Signs a cashier in ON this terminal.
///
/// The device secret rides as a header so the session that comes back is bound
/// to this till, and every sale records which one rang it up.
#[tauri::command]
pub async fn terminal_sign_in(
    api_base_url: String,
    email: String,
    password: String,
) -> Result<TerminalSession, TerminalError> {
    let response = reqwest::Client::new()
        .post(format!(
            "{}/api/v1/auth/login",
            api_base_url.trim_end_matches('/')
        ))
        .header(SECRET_HEADER, secret()?)
        .json(&serde_json::json!({ "email": email, "password": password }))
        .send()
        .await
        .map_err(|_| TerminalError::of("This terminal cannot reach the server."))?;

    if !response.status().is_success() {
        return Err(TerminalError::of(server_message(response).await));
    }

    response.json().await.map_err(|_| {
        TerminalError::of("The server sent something this terminal did not understand.")
    })
}

/// Forgets this terminal's credential.
///
/// Local only. It does not revoke anything on the server — that is an owner's
/// decision from Devices, and a till must not be able to revoke itself or a
/// stolen machine could cover its tracks.
#[tauri::command]
pub fn terminal_forget() -> Result<(), TerminalError> {
    match entry()?.delete_credential() {
        Ok(()) => Ok(()),
        // Already gone is the outcome the caller asked for.
        Err(keyring::Error::NoEntry) => Ok(()),
        Err(_) => Err(TerminalError::of(
            "This terminal could not forget its credential.",
        )),
    }
}

/// Pulls the server's own wording out of an error response.
///
/// The API phrases these for the person at the till, so they are passed through
/// rather than re-derived. Split from the reading of the body so the parsing —
/// the part that can be wrong — is testable without any HTTP machinery.
async fn server_message(response: reqwest::Response) -> String {
    match response.text().await {
        Ok(body) => message_from(&body),
        Err(_) => FALLBACK_REFUSAL.to_string(),
    }
}

/// What to say when the response is not the shape this client expects.
///
/// Never an empty string: a refusal with no words tells a cashier nothing, and
/// they are the person who has to decide what to do next.
const FALLBACK_REFUSAL: &str = "The server refused this terminal.";

fn message_from(body: &str) -> String {
    #[derive(Deserialize)]
    struct Envelope {
        error: Option<Detail>,
    }
    #[derive(Deserialize)]
    struct Detail {
        message: Option<String>,
    }

    match serde_json::from_str::<Envelope>(body) {
        Ok(Envelope {
            error: Some(Detail {
                message: Some(text),
            }),
        }) if !text.trim().is_empty() => text,
        _ => FALLBACK_REFUSAL.to_string(),
    }
}

// Deliberately absent: any command returning the device secret.
//
// See the module comment. If one is ever wanted for a test it belongs behind a
// build tag that cannot reach a release binary — not here.

#[cfg(test)]
mod tests {
    use super::*;

    /// A keyring entry the real tests can use without touching the one a real
    /// terminal would hold.
    fn scratch() -> keyring::Entry {
        keyring::Entry::new(SERVICE, "test-scratch").expect("a keyring entry")
    }

    /// Custody, against the real OS store.
    ///
    /// Not a mock. The whole claim of this module is that the secret lives in
    /// the Windows Credential Manager (or Keychain, or Secret Service) rather
    /// than in a file this application controls, and only the real thing can
    /// show that.
    #[test]
    fn a_secret_round_trips_through_the_os_store() {
        let entry = scratch();
        let _ = entry.delete_credential();

        entry.set_password("NOT-A-REAL-SECRET").expect("store");
        assert_eq!(entry.get_password().expect("read"), "NOT-A-REAL-SECRET");

        entry.delete_credential().expect("delete");
        assert!(
            entry.get_password().is_err(),
            "a deleted credential is still readable"
        );
    }

    /// Forgetting twice is not an error.
    ///
    /// A till being re-purposed may be told to forget more than once, and the
    /// second call has already achieved what the caller asked for.
    #[test]
    fn forgetting_is_idempotent() {
        let entry = scratch();
        let _ = entry.delete_credential();
        match entry.delete_credential() {
            Err(keyring::Error::NoEntry) => {}
            Err(other) => panic!("unexpected error deleting nothing: {other:?}"),
            Ok(()) => {}
        }
    }

    /// The server phrases refusals for the person standing at the till, so they
    /// are passed through rather than re-derived here.
    #[test]
    fn a_refusal_keeps_the_server_wording() {
        let body = r#"{"error":{"code":"unauthenticated","message":"Till 2 has been revoked and can no longer be used."}}"#;
        assert_eq!(
            message_from(body),
            "Till 2 has been revoked and can no longer be used."
        );
    }

    /// Anything that is not the expected shape still says something honest.
    #[test]
    fn an_unreadable_refusal_does_not_produce_an_empty_message() {
        for body in [
            "<html>gateway</html>",
            "",
            "{}",
            r#"{"error":{}}"#,
            r#"{"error":{"message":"  "}}"#,
        ] {
            assert_eq!(
                message_from(body),
                FALLBACK_REFUSAL,
                "an empty refusal tells a cashier nothing: {body:?}"
            );
        }
    }

    // --- The real flow, against a running server ---------------------------
    //
    // `#[ignore]` because these need a live API, a freshly issued enrolment
    // code and this machine's own keystore — none of which a plain `cargo test`
    // can assume. Run deliberately during verification:
    //
    //   $env:RAWSYST_API="http://127.0.0.1:8080"; $env:RAWSYST_CODE="ABCD-1234"
    //   $env:RAWSYST_EMAIL="..."; $env:RAWSYST_PASSWORD="..."
    //   cargo test -- --ignored --nocapture
    //
    // They prove what no unit test can: that these commands actually pair a
    // terminal against the real API, that the stored secret authenticates on
    // its own afterwards, and that a sign-in through them is bound to the till.

    fn env(key: &str) -> String {
        std::env::var(key).unwrap_or_else(|_| panic!("{key} must be set"))
    }

    #[tokio::test]
    #[ignore = "needs a live API and a freshly issued enrolment code"]
    async fn a_terminal_pairs_and_the_stored_secret_works_on_its_own() {
        let api = env("RAWSYST_API");

        assert!(
            terminal_keystore_available(),
            "no OS keystore here, so nothing below could hold a credential"
        );

        // Start from nothing, so a leftover credential cannot make this pass.
        terminal_forget().expect("forget any previous credential");
        assert!(!terminal_is_paired());

        let identity = terminal_pair(api.clone(), env("RAWSYST_CODE"))
            .await
            .unwrap_or_else(|e| panic!("pair: {}", e.message));
        println!(
            "PAIRED {} ({})",
            identity.terminal_label, identity.device_id
        );

        assert!(
            terminal_is_paired(),
            "pairing left no credential in the keystore"
        );

        // The stored secret authenticates on its own — nothing was carried over
        // in memory from the call above.
        let who = terminal_identity(api.clone())
            .await
            .unwrap_or_else(|e| panic!("identity: {}", e.message));
        assert_eq!(who.device_id, identity.device_id);
        println!("IDENTIFIED {}", who.terminal_label);

        // And a sign-in through the shell is bound to this terminal.
        let session = terminal_sign_in(api, env("RAWSYST_EMAIL"), env("RAWSYST_PASSWORD"))
            .await
            .unwrap_or_else(|e| panic!("sign in: {}", e.message));
        assert!(
            !session.access_token.is_empty(),
            "no access token came back"
        );
        println!("ACCESS_TOKEN={}", session.access_token);
    }

    /// Run AFTER an owner has revoked the terminal. The credential that worked a
    /// moment ago must stop working at once.
    ///
    /// Note WHICH refusal this gets, because there are two and they differ for a
    /// reason. A live device-bound TOKEN is refused by the middleware, which can
    /// still read the terminal and names it: "Rust Till has been revoked and can
    /// no longer be used." This path is different — revoking wipes the stored
    /// credential, so the selector matches nothing and the server genuinely
    /// cannot tell a revoked terminal from an invented secret. It says so
    /// generically, which is also the safer answer: a precise reply here would
    /// confirm to anyone holding a stale secret that the terminal was real.
    ///
    /// The screen handles both — see TerminalBlocked in PairingScreen.tsx.
    #[tokio::test]
    #[ignore = "run after revoking the terminal"]
    async fn a_revoked_terminal_is_refused_immediately() {
        match terminal_identity(env("RAWSYST_API")).await {
            Ok(who) => panic!("a revoked terminal still works: {}", who.terminal_label),
            Err(e) => {
                println!("REFUSED {}", e.message);
                assert!(
                    e.message.contains("not recognised"),
                    "unexpected refusal: {}",
                    e.message
                );
                // And it must not leak whether that terminal ever existed.
                assert!(
                    !e.message.contains("Rust Till"),
                    "the refusal names a terminal to an unauthenticated caller: {}",
                    e.message
                );
            }
        }
        terminal_forget().expect("clean up");
    }
}
