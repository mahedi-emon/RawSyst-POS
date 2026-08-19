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
