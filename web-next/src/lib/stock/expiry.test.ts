import { describe, expect, it } from 'vitest';

import { expiryState } from './expiry';

const TODAY = new Date(2026, 8, 5); // 5 September 2026, local time

describe('when a lot stops being sellable', () => {
  it('says nothing about a lot with no date', () => {
    expect(expiryState(undefined, TODAY)).toBe('none');
    expect(expiryState('', TODAY)).toBe('none');
  });

  it('is good until the END of the day printed on it', () => {
    // The one that would annoy a shopkeeper: a lot dated today is still
    // sellable today. Comparing instants rather than dates marks it expired
    // from one minute past midnight.
    expect(expiryState('2026-09-05', TODAY)).toBe('soon');
  });

  it('is expired the day after', () => {
    expect(expiryState('2026-09-04', TODAY)).toBe('expired');
  });

  it('draws attention inside thirty days', () => {
    expect(expiryState('2026-09-20', TODAY)).toBe('soon');
    expect(expiryState('2026-10-05', TODAY)).toBe('soon');
  });

  it('leaves a lot with months on it alone', () => {
    expect(expiryState('2026-10-06', TODAY)).toBe('fine');
    expect(expiryState('2027-01-01', TODAY)).toBe('fine');
  });

  it('reads a timestamp as the date in it', () => {
    // Some routes send a date and some send an instant; both mean the same day.
    expect(expiryState('2026-09-04T23:00:00Z', TODAY)).toBe('expired');
    expect(expiryState('2026-09-20T00:00:00Z', TODAY)).toBe('soon');
  });

  it('says nothing rather than guessing at a date it cannot read', () => {
    expect(expiryState('not-a-date', TODAY)).toBe('none');
    expect(expiryState('2026-13', TODAY)).toBe('none');
  });

  it('handles the turn of a year', () => {
    const newYearsEve = new Date(2026, 11, 31);
    expect(expiryState('2027-01-05', newYearsEve)).toBe('soon');
    expect(expiryState('2026-12-30', newYearsEve)).toBe('expired');
  });
});
