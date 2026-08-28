// Where this terminal's credential lives.
//
// The device secret is the thing that makes this machine a registered terminal.
// It is long-lived, it is not a token, and if it leaks somebody else's computer
// can sell as this till until an owner notices and revokes it.
//
// # It never enters this layer
//
// The same shape as keystore.rs and for the same reason: there is `pair`, there
// is `forget`, there is `isPaired`, and there is no `get`. The Rust shell holds
// the secret in the OS keystore — Windows DPAPI via the Credential Manager —
// and performs the two operations that need it. The web layer can ask the shell
// to ACT with the credential; it can never hold it.
//
// That rules out, structurally rather than by discipline: the secret in
// localStorage, in the SQLite file, in a request body this layer builds, in a
// console log, in a React state tree a devtools extension can read, or in a
// crash report.
//
// 01-invoice-zatca-engine.md §7 makes the same argument about the CSID key and
// reaches the same place. This is a second credential with the same custody, not
// a second custody model.
//
// # Outside Tauri
//
// In a browser during development there is no keystore, so `available()` is
// false and every call refuses. Refusing is the honest answer: a fallback to
// localStorage "just for development" is exactly how a secret ends up there in
// production.

import { invoke } from '@tauri-apps/api/core';

import type { Session } from '@rawsyst/shared/api/client';

/** What a paired terminal knows about itself. Never includes the secret. */
export interface TerminalIdentity {
  device_id: string;
  terminal_label: string;
  store_id: string;
  company_id: string;
}

/** Why a terminal cannot act, in a form the screen can branch on.
 *
 * `revoked` and `paused` are separated from `unrecognised` because the three
 * need different words in front of a cashier: one is permanent, one an owner
 * can undo from Devices, and one means this machine was never set up. */
export type PairingFault =
  | { kind: 'not_paired' }
  | { kind: 'revoked'; message: string }
  | { kind: 'paused'; message: string }
  | { kind: 'unrecognised'; message: string }
  // The server answered and said no, in words worth repeating. Separate from
  // `offline` because the two ask for opposite things from whoever is standing
  // at the till: one is "check the cable", the other is "the code is wrong".
  | { kind: 'refused'; message: string }
  | { kind: 'offline'; message: string }
  | { kind: 'no_keystore'; message: string };

/** Whether this build can hold a credential at all. False in a plain browser. */
export async function available(): Promise<boolean> {
  try {
    return await invoke<boolean>('terminal_keystore_available');
  } catch {
    return false;
  }
}

/** Whether this machine has already been paired. */
export async function isPaired(): Promise<boolean> {
  try {
    return await invoke<boolean>('terminal_is_paired');
  } catch {
    return false;
  }
}

/**
 * Redeems an enrolment code and stores the secret.
 *
 * The whole exchange happens in Rust: the code goes in, the HTTP call is made
 * there, and the secret goes straight from the response into the keystore
 * without ever being a JavaScript value. What comes back is the terminal's
 * identity, which is not secret.
 */
export async function pair(
  apiBaseUrl: string,
  code: string,
): Promise<TerminalIdentity> {
  return invoke<TerminalIdentity>('terminal_pair', { apiBaseUrl, code });
}

/**
 * Asks the server who this terminal is, presenting the stored secret.
 *
 * The call a till makes on startup. It is also how a terminal learns it was
 * revoked or switched off, because the server names the state in its refusal.
 */
export async function identify(apiBaseUrl: string): Promise<TerminalIdentity> {
  return invoke<TerminalIdentity>('terminal_identity', { apiBaseUrl });
}

/**
 * Signs a cashier in ON this terminal.
 *
 * Rust attaches the device secret as a header, so the session that comes back
 * is bound to this till and every sale records which one rang it up. The tokens
 * returned are short-lived and are not secrets of this kind — they are what the
 * web layer is supposed to hold.
 */
export async function signIn(
  apiBaseUrl: string,
  email: string,
  password: string,
): Promise<Session | null> {
  // Null means "this is not a terminal — sign in the ordinary way".
  //
  // The check is on the KEYSTORE, not on whether the call happened to fail. A
  // terminal that has a keystore and whose sign-in failed must surface that
  // failure, never quietly fall back to an unbound session: an unbound session
  // on a real till would sell without recording which machine rang it up, and
  // nothing downstream could tell afterwards.
  if (!(await available())) return null;

  // The shell speaks the wire shape; the app speaks its own. Converted here so
  // the rest of the terminal never has to know there are two.
  const wire = await invoke<{ access_token: string; refresh_token: string }>(
    'terminal_sign_in',
    { apiBaseUrl, email, password },
  );
  return { accessToken: wire.access_token, refreshToken: wire.refresh_token };
}

/**
 * Forgets this terminal's credential.
 *
 * Local only. It does not revoke anything on the server — that is an owner's
 * decision from Devices, and a till must not be able to revoke itself or a lost
 * machine could cover its tracks. This is for the other case: a machine being
 * re-purposed or handed on, where leaving a working credential behind would be
 * the mistake.
 */
export async function forget(): Promise<void> {
  await invoke('terminal_forget');
}

/**
 * What the shell actually threw, as text.
 *
 * Tauri rejects an `Err(TerminalError)` with the SERIALIZED struct — a plain
 * `{ message }` object — not with an Error. Reading `err.message` only after an
 * `instanceof Error` check therefore produced "[object Object]" for every
 * refusal the Rust side raised, so nothing matched below and every one of them
 * fell through to "offline". A revoked terminal looked merely disconnected and
 * carried on offering to sign a cashier in.
 *
 * Found by revoking a terminal and watching the installed application, which is
 * the only place the shapes actually meet.
 */
function messageOf(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === 'string') return err;
  if (err && typeof err === 'object' && 'message' in err) {
    const message = (err as { message?: unknown }).message;
    if (typeof message === 'string') return message;
  }
  // Nothing readable — a number, a bare object, `undefined`. Empty rather than
  // String(err), because "[object Object]" is not something the server said
  // and must not be shown to a cashier or reasoned about as a refusal.
  return '';
}

/** Turns whatever the shell threw into something the screen can act on. */
export function faultFrom(err: unknown): PairingFault {
  const text = messageOf(err);

  // Order matters, and the tests pin it. The server's generic refusal — "This
  // terminal is not recognised. It may have been revoked, or it may need to be
  // paired again." — contains the word "revoked", so checking for that first
  // made a terminal the server explicitly could not identify report itself as
  // definitely revoked. The definitive answers are matched before the hedged
  // one, and the hedged one before the word it happens to contain.
  if (/not paired|no credential/i.test(text)) return { kind: 'not_paired' };
  if (/not recognised|not registered/i.test(text)) {
    return { kind: 'unrecognised', message: text };
  }
  if (/revoked/i.test(text)) return { kind: 'revoked', message: text };
  if (/inactive|switched off|paused/i.test(text)) {
    return { kind: 'paused', message: text };
  }
  if (/keystore|unavailable command|not allowed/i.test(text)) {
    return {
      kind: 'no_keystore',
      message:
        'This build cannot store a terminal credential securely, so it cannot ' +
        'be paired. Use the installed RawSyst application on the till.',
    };
  }
  // Everything else the SERVER said, in its own words.
  //
  // This used to fall through to `offline`, and the pairing screen's offline
  // branch says "This terminal cannot reach the server. Check the connection"
  // and "The code is still valid — try again once you are back online." Both
  // sentences were wrong for every refusal that reached them. A mistyped code
  // ("That enrolment code is not valid. Codes expire after 15 minutes and can
  // only be used once") and a rate-limited terminal ("Too many enrolment
  // attempts from this device. Wait a few minutes, then ask for a fresh code")
  // both sent somebody to look at a network cable, on the first screen a new
  // till ever shows, while the message that told them what to do was thrown
  // away. Found by pairing the packaged application (e2e/tauri.mjs).
  //
  // `offline` is now what it says: the transport failed, or nothing readable
  // came back at all. The first is the one sentence the Rust shell raises when
  // reqwest cannot complete the call. The second is a shape this code did not
  // expect, and a till that cannot read the reason must keep trading on what
  // it already knows rather than lock a counter with a queue at it.
  const transport =
    text.trim() === '' || /cannot reach the server/i.test(text);
  if (transport) {
    return {
      kind: 'offline',
      message:
        'This terminal cannot reach the server. Check the connection and try again.',
    };
  }
  return { kind: 'refused', message: text };
}
