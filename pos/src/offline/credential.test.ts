import { describe, expect, it } from 'vitest';

import { faultFrom } from './credential';

// Tauri rejects an `Err(TerminalError)` with the SERIALIZED struct — a plain
// `{ message }` object, never an Error. Reading `err.message` only after an
// `instanceof Error` check produced "[object Object]" for every refusal the
// Rust side raised, so a revoked terminal read as merely offline and the app
// carried on offering to sign a cashier in. Found by revoking a real terminal
// and watching the installed application.

describe('reading a refusal from the shell', () => {
  const tauri = (message: string) => ({ message });

  it('reads the plain object Tauri actually rejects with', () => {
    const fault = faultFrom(tauri('Counter 1 has been revoked and can no longer be used.'));
    expect(fault.kind).toBe('revoked');
  });

  it('separates a switched-off terminal from a revoked one', () => {
    // Different words to a cashier: one an owner can undo, one is permanent.
    expect(faultFrom(tauri('Till 2 is inactive, so it cannot be used.')).kind).toBe('paused');
  });

  it('does not over-claim on the server’s hedged refusal', () => {
    // Revoking clears the stored credential, so the server can no longer tell a
    // revoked terminal from an invented secret. Its wording says "may have been
    // revoked" — and matching the word `revoked` first turned that hedge into a
    // definite "this terminal has been revoked" screen.
    expect(
      faultFrom(
        tauri(
          'This terminal is not recognised. It may have been revoked, or it may need to be paired again.',
        ),
      ).kind,
    ).toBe('unrecognised');
  });

  it('recognises a terminal that was never paired', () => {
    expect(faultFrom(tauri('This terminal is not paired.')).kind).toBe('not_paired');
  });

  it('still reads a real Error, which is what a browser throws', () => {
    expect(faultFrom(new Error('This terminal is not paired.')).kind).toBe('not_paired');
  });

  it('reads a bare string', () => {
    expect(faultFrom('This terminal is not paired.').kind).toBe('not_paired');
  });

  it('treats anything unreadable as offline, so a paired till keeps trading', () => {
    // The fallback matters: a till that cannot classify a failure must keep
    // working on what it already knows rather than locking the counter.
    for (const odd of [undefined, null, 42, {}, { message: 7 }]) {
      expect(faultFrom(odd).kind).toBe('offline');
    }
  });

  it('does not mistake an object for the string "[object Object]"', () => {
    // The exact defect: this used to land in `offline`.
    const fault = faultFrom(tauri('Counter 1 has been revoked and can no longer be used.'));
    expect(fault.kind).not.toBe('offline');
    if (fault.kind !== 'not_paired') {
      expect(fault.message).toContain('revoked');
    }
  });
});

// A refusal the server wrote is not a dead network.
//
// # What went wrong
//
// Everything `faultFrom` did not recognise fell through to `offline`, and the
// pairing screen's offline branch says two things, both of them wrong for a
// refusal: "This terminal cannot reach the server. Check the connection and
// try again", and "The code is still valid — try again once you are back
// online."
//
// So a mistyped code, an expired one, one already used on another machine, and
// a terminal the server was rate-limiting all sent whoever was standing at the
// counter to look at a network cable — on the first screen a new till ever
// shows, while the sentence that told them what to do was discarded.
//
// Found by pairing the packaged Windows application under tauri-driver
// (e2e/tauri.mjs). A browser cannot pair at all, so nothing before it could
// have seen this.
describe('a refusal the server wrote', () => {
  const tauri = (message: string) => ({ message });

  it('keeps the words the server chose', () => {
    const fault = faultFrom(
      tauri(
        'That enrolment code is not valid. Codes expire after 15 minutes and ' +
          'can only be used once — ask for a new one in the back office.',
      ),
    );
    expect(fault.kind).toBe('refused');
    if (fault.kind === 'refused') {
      expect(fault.message).toContain('expire after 15 minutes');
    }
  });

  it('does not call a rate limit a connection problem', () => {
    const fault = faultFrom(
      tauri(
        'Too many enrolment attempts from this device. Wait a few minutes, ' +
          'then ask for a fresh code in the back office.',
      ),
    );
    expect(fault.kind).not.toBe('offline');
    if (fault.kind === 'refused') {
      expect(fault.message).toContain('Wait a few minutes');
    }
  });

  it('still calls a real transport failure offline', () => {
    // The one sentence the Rust shell raises when reqwest cannot complete the
    // call. This is the case where "check the connection" is the right advice.
    expect(faultFrom(tauri('This terminal cannot reach the server.')).kind).toBe(
      'offline',
    );
  });

  it('leaves the definitive refusals where they were', () => {
    // `refused` must not swallow the three kinds that mean a till may not
    // trade: each shows different words and offers a different next step.
    expect(
      faultFrom(tauri('Counter 1 has been revoked and can no longer be used.'))
        .kind,
    ).toBe('revoked');
    expect(faultFrom(tauri('Till 2 is inactive, so it cannot be used.')).kind).toBe(
      'paused',
    );
    expect(
      faultFrom(tauri('This terminal is not recognised.')).kind,
    ).toBe('unrecognised');
  });
});
