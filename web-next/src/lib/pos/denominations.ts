// The notes and coins a drawer actually holds.
//
// # Why this is not a number pad
//
// A cashier closing a till does not know the total. They know they have four
// 500s, eleven 100s and a handful of coins, and the total is what falls out of
// counting them. A single "enter the amount" box asks somebody to do arithmetic
// under time pressure at the end of a shift, and an arithmetic slip becomes a
// variance that somebody then has to investigate.
//
// Searching 21st.dev for this found number pads and animated currency tickers
// and nothing that counts denominations -- and an animated rolling figure on a
// number being reconciled would be worse than no help at all. So it is written
// here, from the currencies the product sells into.
//
// # Values are strings
//
// Every denomination is a decimal string and the multiplication is done with
// `decimal.js`, for the same reason nothing else in this product touches money
// with a float: 0.05 × 3 is 0.15000000000000002, and a drawer that is two
// hundredths out because of that is a variance nobody can explain.

import { Decimal } from 'decimal.js';

export interface Denomination {
  /** Face value, as a decimal string. */
  value: string;
  /** How it is written on the note or coin. */
  label: string;
  kind: 'note' | 'coin';
}

/**
 * Per currency, largest first.
 *
 * Largest first because that is the order a drawer is counted in: the notes are
 * banded and lifted out before anybody reaches the coin tray.
 */
const BY_CURRENCY: Readonly<Record<string, readonly Denomination[]>> = {
  SAR: [
    { value: '500', label: '500', kind: 'note' },
    { value: '200', label: '200', kind: 'note' },
    { value: '100', label: '100', kind: 'note' },
    { value: '50', label: '50', kind: 'note' },
    { value: '10', label: '10', kind: 'note' },
    { value: '5', label: '5', kind: 'note' },
    { value: '1', label: '1', kind: 'coin' },
    { value: '0.50', label: '50h', kind: 'coin' },
    { value: '0.25', label: '25h', kind: 'coin' },
    { value: '0.10', label: '10h', kind: 'coin' },
    { value: '0.05', label: '5h', kind: 'coin' },
  ],
  BDT: [
    { value: '1000', label: '1000', kind: 'note' },
    { value: '500', label: '500', kind: 'note' },
    { value: '200', label: '200', kind: 'note' },
    { value: '100', label: '100', kind: 'note' },
    { value: '50', label: '50', kind: 'note' },
    { value: '20', label: '20', kind: 'note' },
    { value: '10', label: '10', kind: 'note' },
    { value: '5', label: '5', kind: 'coin' },
    { value: '2', label: '2', kind: 'coin' },
    { value: '1', label: '1', kind: 'coin' },
  ],
  USD: [
    { value: '100', label: '100', kind: 'note' },
    { value: '50', label: '50', kind: 'note' },
    { value: '20', label: '20', kind: 'note' },
    { value: '10', label: '10', kind: 'note' },
    { value: '5', label: '5', kind: 'note' },
    { value: '1', label: '1', kind: 'note' },
    { value: '0.25', label: '25c', kind: 'coin' },
    { value: '0.10', label: '10c', kind: 'coin' },
    { value: '0.05', label: '5c', kind: 'coin' },
    { value: '0.01', label: '1c', kind: 'coin' },
  ],
};

/**
 * What this currency is counted in, or nothing.
 *
 * A currency the product has no denominations for gets no counting grid and
 * falls back to a single total, rather than being offered a Saudi drawer.
 * Guessing a foreign country's notes is worse than not offering the aid.
 */
export function denominationsFor(currency: string): readonly Denomination[] {
  return BY_CURRENCY[currency.toUpperCase()] ?? [];
}

/** Sums a count per denomination into one decimal string. */
export function totalOf(
  counts: Readonly<Record<string, string>>,
  currency: string,
  places = 2,
): string {
  let total = new Decimal(0);
  for (const d of denominationsFor(currency)) {
    const n = counts[d.value];
    if (!n) continue;
    const parsed = Number.parseInt(n, 10);
    if (!Number.isFinite(parsed) || parsed <= 0) continue;
    total = total.plus(new Decimal(d.value).times(parsed));
  }
  return total.toDecimalPlaces(places, Decimal.ROUND_HALF_UP).toFixed(places);
}

/**
 * The difference between what was counted and what the till expected.
 *
 * Positive is over, negative is short. Returned signed so the screen can say
 * which -- "over" and "short" are the words a shop uses and they are not
 * interchangeable, however small the figure.
 */
export function varianceOf(
  counted: string,
  expected: string,
  places = 2,
): string {
  return new Decimal(counted || '0')
    .minus(new Decimal(expected || '0'))
    .toDecimalPlaces(places, Decimal.ROUND_HALF_UP)
    .toFixed(places);
}
