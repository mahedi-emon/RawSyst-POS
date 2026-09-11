// The expiry rule, which two screens read and neither owns.
//
// The boundaries are the point. A subscription that ends today is not expired,
// one that ended yesterday is, and getting either wrong shows an operator a red
// badge against a client who is fine — or, worse, no badge against one who is
// not.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { daysUntil, expiryTone } from './subscription';

/** A date that many whole days from the fixed "now" below. */
function inDays(n: number): string {
  const d = new Date('2026-09-11T00:00:00Z');
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

describe('the expiry rule', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    // Midday, deliberately. A rule written against midnight can round
    // correctly at midnight and be a day out for the rest of the day.
    vi.setSystemTime(new Date('2026-09-11T12:00:00Z'));
  });
  afterEach(() => vi.useRealTimers());

  it('counts whole days in each direction', () => {
    expect(daysUntil(inDays(0))).toBe(0);
    expect(daysUntil(inDays(1))).toBe(1);
    expect(daysUntil(inDays(-1))).toBe(-1);
    expect(daysUntil(inDays(365))).toBe(365);
  });

  it('marks a subscription critical only once the date has passed', () => {
    // Ends today: still running. A client is not cut off on the morning of
    // their last paid day.
    expect(expiryTone({ expiresOn: inDays(0), cycle: 'monthly' })).toBe('caution');
    expect(expiryTone({ expiresOn: inDays(-1), cycle: 'monthly' })).toBe('critical');
    expect(expiryTone({ expiresOn: inDays(-400), cycle: 'yearly' })).toBe('critical');
  });

  it('warns inside thirty days, which is while something can still be done', () => {
    expect(expiryTone({ expiresOn: inDays(30), cycle: 'yearly' })).toBe('caution');
    expect(expiryTone({ expiresOn: inDays(31), cycle: 'yearly' })).toBeUndefined();
    expect(expiryTone({ expiresOn: inDays(200), cycle: 'yearly' })).toBeUndefined();
  });

  it('leaves a lifetime subscription alone', () => {
    // No end date is what makes it one, so an absent date is not a gap here.
    expect(expiryTone({ cycle: 'lifetime' })).toBeUndefined();
    expect(expiryTone({ expiresOn: undefined, cycle: 'lifetime' })).toBeUndefined();
  });

  it('flags a cycled plan that has no end recorded', () => {
    // Not the same as a lifetime plan, and the difference matters: nothing can
    // ever find this one expired, so it would sit there for ever looking fine.
    expect(expiryTone({ cycle: 'monthly' })).toBe('caution');
    expect(expiryTone({ cycle: 'yearly' })).toBe('caution');
    expect(expiryTone({})).toBe('caution');
  });

  it('reads a date as whole days rather than as a local midnight', () => {
    // The bug this prevents: parsing "2026-09-12" as local midnight in a
    // timezone ahead of UTC makes tomorrow read as today, and every boundary
    // above moves a day in the direction that expires somebody early.
    vi.setSystemTime(new Date('2026-09-11T23:30:00Z'));
    expect(daysUntil('2026-09-12')).toBe(0);
    expect(expiryTone({ expiresOn: '2026-09-12', cycle: 'monthly' })).toBe('caution');
  });
});
