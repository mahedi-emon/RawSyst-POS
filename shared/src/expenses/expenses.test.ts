// The decisions the expenses screen makes, checked directly.
//
// Two of these are about money arithmetic on strings, which is the habit this
// codebase keeps everywhere: an amount that goes through a JavaScript number on
// its way to a screen comes out wrong in the last place, and the last place is
// what an owner reconciles against their bank.

import { describe, expect, it } from 'vitest';

import { borneBy, isZeroAmount, monthToDate, splitOf } from './expenses';

describe('the period the screen opens on', () => {
  it('runs from the first of the month to today, not to the end of it', () => {
    // The 8th. An owner asking where the money is going means the eight days
    // that have happened; a period running to the 31st reports the same total
    // under a heading implying more is still to come.
    const period = monthToDate(new Date('2026-08-08T09:00:00Z'));
    expect(period.from).toBe('2026-08-01');
    expect(period.to).toBe('2026-08-08');
  });

  it('handles the first of the month, where both ends are the same day', () => {
    const period = monthToDate(new Date('2026-09-01T00:30:00Z'));
    expect(period.from).toBe('2026-09-01');
    expect(period.to).toBe('2026-09-01');
  });

  it('handles January, where the previous month is in another year', () => {
    const period = monthToDate(new Date('2027-01-05T12:00:00Z'));
    expect(period.from).toBe('2027-01-01');
    expect(period.to).toBe('2027-01-05');
  });
});

describe('whether a figure is worth showing', () => {
  it('treats every spelling of nothing as nothing', () => {
    for (const nothing of ['0', '0.00', '0.0000', '-0.00', '.00', '', undefined]) {
      expect(isZeroAmount(nothing)).toBe(true);
    }
  });

  it('does not mistake a small amount for nothing', () => {
    // The case a naive check gets wrong: 0.01 contains a zero and is not one.
    expect(isZeroAmount('0.01')).toBe(false);
    expect(isZeroAmount('10.00')).toBe(false);
    expect(isZeroAmount('-5.00')).toBe(false);
  });
});

describe('the VAT split the screen calls out', () => {
  it('mentions VAT that cannot be reclaimed, because it is a real cost', () => {
    const split = splitOf({ tax_recoverable: '150.00', tax_absorbed: '60.00' });
    expect(split.absorbed).toBe(true);
  });

  it('says nothing when there is none', () => {
    // A line reading "0.00 of VAT you cannot reclaim" teaches a reader to skip
    // the row, which is how they miss it on the month there IS some.
    const split = splitOf({ tax_recoverable: '150.00', tax_absorbed: '0.00' });
    expect(split.absorbed).toBe(false);
  });
});

describe('what the shop actually bore', () => {
  it('is the gross less the VAT that comes back', () => {
    // 1,000 of rent plus 150 VAT. The shop paid 1,150 and bore 1,000: the VAT
    // is reclaimed and is not a cost.
    expect(
      borneBy({
        total: '1150.00',
        tax_recoverable: '150.00',
        tax_absorbed: '0.00',
      } as never),
    ).toBe('1000.00');
  });

  it('counts VAT that does NOT come back as part of the cost', () => {
    // 400 of fuel plus 60 VAT, on a category E2.3 restricts. Nothing is
    // reclaimed, so the whole 460 was borne.
    expect(
      borneBy({
        total: '460.00',
        tax_recoverable: '0.00',
        tax_absorbed: '60.00',
      } as never),
    ).toBe('460.00');
  });

  it('does not drift on figures a float would round', () => {
    // 1150.00 - 150.00 is 999.9999999999999 in IEEE 754. A figure wrong in the
    // last place is wrong on a screen somebody reconciles against a bank.
    expect(
      borneBy({
        total: '1150.10',
        tax_recoverable: '150.05',
        tax_absorbed: '0.00',
      } as never),
    ).toBe('1000.05');
  });

  it('reports a negative rather than clamping it', () => {
    // Not reachable from a single expense, and reachable from a period once
    // supplier credit notes are recorded against one. Clamping would hide it.
    expect(
      borneBy({
        total: '100.00',
        tax_recoverable: '150.00',
        tax_absorbed: '0.00',
      } as never),
    ).toBe('-50.00');
  });
});
