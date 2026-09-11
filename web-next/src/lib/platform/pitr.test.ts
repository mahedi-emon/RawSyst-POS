// What the recovery screen is allowed to say.
//
// The screen shows two things that decide whether somebody acts: whether a
// moment is recoverable, and which word goes on a base backup. Both are read
// off maps and helpers in this module, and both have a comfortable answer and a
// true one. These hold it to the true one.

import { describe, expect, it } from 'vitest';

import {
  BASE_LABEL,
  BASE_TONE,
  CONFIRM_WORD,
  RECOVERY_STATUS_LABEL,
  RECOVERY_STATUS_TONE,
  TARGET_HELP,
  TARGET_KINDS,
  TARGET_LABEL,
  momentToISO,
  needsMoment,
  needsValue,
  nowForInput,
  windowLength,
} from './pitr';

describe('the recovery vocabulary', () => {
  it('gives every target a label and an explanation', () => {
    // The explanation is what an operator reads before choosing between "at a
    // moment" and "just before a moment", and the difference between those two
    // is whether the transaction being recovered from is included.
    for (const kind of TARGET_KINDS) {
      expect(TARGET_LABEL[kind], kind).toBeTruthy();
      expect(TARGET_HELP[kind], kind).toBeTruthy();
    }
  });

  it('asks for a moment only where a moment is meant', () => {
    expect(needsMoment('time')).toBe(true);
    expect(needsMoment('before_time')).toBe(true);
    // `latest` and `immediate` are defined by the archive rather than by a
    // clock. Offering a time box for them would invite somebody to type one
    // and believe it was used.
    expect(needsMoment('latest')).toBe(false);
    expect(needsMoment('immediate')).toBe(false);
    expect(needsValue('lsn')).toBe(true);
    expect(needsValue('name')).toBe(true);
    expect(needsValue('time')).toBe(false);
  });

  it('never gives a stored base backup the green word', () => {
    // The whole distinction this subsystem keeps. A copy that reached the
    // bucket has had nothing proved about it; only one that has been recovered
    // and counted is green.
    expect(BASE_TONE.stored).not.toBe('positive');
    expect(BASE_TONE.verified).toBe('positive');
    expect(BASE_LABEL.stored).not.toBe(BASE_LABEL.verified);

    for (const status of Object.keys(BASE_LABEL)) {
      expect(BASE_TONE[status], status).toBeTruthy();
    }
  });

  it('shows a refused recovery as its own state, not as a failure', () => {
    // A refusal is a recovery that was never attempted — a target outside the
    // window, usually. Colouring it as a failure would send somebody looking
    // for a broken archive.
    expect(RECOVERY_STATUS_TONE.refused).not.toBe(RECOVERY_STATUS_TONE.failed);
    expect(RECOVERY_STATUS_LABEL.refused).toBeTruthy();
    for (const status of Object.keys(RECOVERY_STATUS_LABEL)) {
      expect(RECOVERY_STATUS_TONE[status], status).toBeTruthy();
    }
  });
});

describe('the moment an operator types', () => {
  it('turns a local moment into an instant, and refuses anything else', () => {
    const iso = momentToISO('2026-09-11T14:32');
    expect(iso).toBeTruthy();
    expect(new Date(iso as string).getTime()).not.toBeNaN();

    // A target that silently became the current moment would replay everything
    // and report success, which is the hardest failure here to notice.
    expect(momentToISO('')).toBeNull();
    expect(momentToISO('   ')).toBeNull();
    expect(momentToISO('yesterday afternoon')).toBeNull();
    expect(momentToISO('2026-13-45T99:99')).toBeNull();
  });

  it('offers the box a value it can actually display', () => {
    // `datetime-local` shows nothing at all for a value it cannot parse, which
    // reads as a broken control rather than as a bad default.
    const at = new Date(2026, 8, 11, 14, 32);
    expect(nowForInput(at)).toBe('2026-09-11T14:32');
  });

  it('round-trips what it offers', () => {
    const at = new Date(2026, 8, 11, 14, 32);
    const iso = momentToISO(nowForInput(at));
    expect(iso).toBeTruthy();
    expect(new Date(iso as string).getMinutes()).toBe(32);
  });
});

describe('the recovery window', () => {
  it('measures a window and refuses to invent one', () => {
    const start = '2026-09-04T12:00:00Z';
    const end = '2026-09-11T12:00:00Z';
    expect(windowLength(start, end)).toBe(7 * 24 * 3600 * 1000);

    // A window of "0 days" reads as a working system with nothing in it. An
    // empty figure reads as what it is.
    expect(windowLength(undefined, end)).toBeNull();
    expect(windowLength(start, undefined)).toBeNull();
    expect(windowLength('not a date', end)).toBeNull();
    // Backwards is not a window.
    expect(windowLength(end, start)).toBeNull();
  });
});

describe('the confirmation', () => {
  it('is the token the server compares and not a translated sentence', () => {
    // A translated confirmation is a confirmation the API refuses, which is a
    // button that does nothing without saying why.
    expect(CONFIRM_WORD).toBe('RECOVER');
  });
});
