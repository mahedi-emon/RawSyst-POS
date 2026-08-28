// Decisions the expenses screen makes, as functions.
//
// Separated for the reason the rest of this codebase separates them: a
// decision inside a component can only be checked by rendering the component,
// and these are the ones worth checking directly.

import type { ExpenseSummary } from '../api/expenses';

/** The current month, from the first to today.
 *
 * "This month so far" rather than the whole calendar month: an owner asking
 * where the money is going on the 8th means the eight days that have happened,
 * and a period running to the 31st reports the same total under a heading that
 * implies more is coming. */
export function monthToDate(now: Date = new Date()): { from: string; to: string } {
  const iso = (d: Date) => d.toISOString().slice(0, 10);
  const first = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1));
  return { from: iso(first), to: iso(now) };
}

/** Whether either half of the VAT split is worth showing.
 *
 * `absorbed` is VAT the shop paid and will not get back, on a category E2.3
 * restricts. It is a real cost that looks like tax, so it is called out
 * whenever there is any — and never when there is none, because a line reading
 * "0.00 of VAT you cannot reclaim" teaches a reader to skip the row. */
export function splitOf(summary: {
  tax_recoverable: string;
  tax_absorbed: string;
}): { recoverable: boolean; absorbed: boolean } {
  return {
    recoverable: !isZeroAmount(summary.tax_recoverable),
    absorbed: !isZeroAmount(summary.tax_absorbed),
  };
}

/** True for "0", "0.00", "-0.0000" and anything else that means nothing.
 *
 * String comparison rather than Number(): the amounts are decimal strings on
 * purpose, and putting one through a float to ask whether it is zero is the
 * habit this codebase avoids everywhere else. */
export function isZeroAmount(amount: string | null | undefined): boolean {
  if (!amount) return true;
  return /^-?0*\.?0*$/.test(amount.trim());
}

/** What the shop actually bore, which is not the same as what it paid.
 *
 * The gross includes VAT that will be reclaimed, and that is not a cost. The
 * cost is the gross less the recoverable part — which is exactly what the
 * category breakdown sums, so this is how a reader checks the two agree. */
export function borneBy(summary: ExpenseSummary): string {
  return subtract(summary.total, summary.tax_recoverable);
}

/** Decimal subtraction on strings, to two places.
 *
 * Done in integer minor units rather than with floats: 1150.00 - 150.00 is
 * 999.9999999999999 in IEEE 754, and a figure wrong in the last place is wrong
 * on a screen an owner reconciles against their bank. */
function subtract(a: string, b: string): string {
  const minor = (s: string) => {
    const [whole = '0', frac = ''] = String(s ?? '0').trim().split('.');
    const cents = (frac + '00').slice(0, 2);
    const sign = whole.startsWith('-') ? -1 : 1;
    return sign * (Math.abs(Number(whole)) * 100 + Number(cents));
  };
  const value = minor(a) - minor(b);
  const sign = value < 0 ? '-' : '';
  const abs = Math.abs(value);
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`;
}
