// The lookup that must never throw.
//
// It is read on the handover screen, which is the only screen in the product
// that shows a password — issued once, never stored in readable form, never
// fetchable again. A throw there costs the operator the credential and the
// client their account until somebody resets it.
//
// So the cases that matter are the ones the type says cannot happen. The value
// crosses the wire from a separate container, and a type is not a promise the
// network keeps.

import { describe, expect, it } from 'vitest';

import { NOBODY_WAS_TOLD, mailOutcome } from './mail-outcome';

describe('what the operator is told about the welcome message', () => {
  it('names each of the four outcomes the API can send', () => {
    for (const status of [
      'queued',
      'queued_for_logging',
      'queued_no_provider',
      'not_configured',
    ]) {
      const [key, tone] = mailOutcome(status);
      expect(key, status).toBeTruthy();
      expect(['ok', 'warn'], status).toContain(tone);
    }
  });

  it('treats only a wired provider as reassuring', () => {
    // The other three all mean the owner receives nothing, and none of them
    // should render calmly.
    expect(mailOutcome('queued')[1]).toBe('ok');
    for (const status of [
      'queued_for_logging',
      'queued_no_provider',
      'not_configured',
    ]) {
      expect(mailOutcome(status)[1], status).toBe('warn');
    }
  });

  it('does not throw on a word it has never heard of', () => {
    // A rolling deploy, a partial rollback, or an API a version ahead. The old
    // code indexed a map and destructured the result, so any of these threw
    // while rendering the one screen that shows a password.
    for (const status of ['sent', 'delivered', 'QUEUED', 'pending', '']) {
      expect(() => mailOutcome(status), status).not.toThrow();
    }
  });

  it('does not throw when the field is missing altogether', () => {
    expect(() => mailOutcome(undefined)).not.toThrow();
    expect(() => mailOutcome(null)).not.toThrow();
  });

  it('falls back to saying nobody was told, never to claiming delivery', () => {
    // The direction of the error is the whole point. Guessing "queued" would
    // tell an operator their client has been emailed when nothing was sent,
    // and the operator would stop there — leaving the client waiting for a
    // message that is never coming.
    for (const status of ['sent', 'delivered', undefined, '', 'anything']) {
      expect(mailOutcome(status), String(status)).toEqual(NOBODY_WAS_TOLD);
      expect(mailOutcome(status)[1], String(status)).toBe('warn');
    }
  });
});
