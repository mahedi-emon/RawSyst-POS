// Splitting one payment across several invoices.
//
// Money, so decimal.js: a payment is a figure somebody will reconcile against a
// bank statement, and `0.1 + 0.2` in binary floating point is not `0.3`.

import Decimal from 'decimal.js';

/** A number a person is part-way through typing is zero, not NaN. */
function num(value: string): Decimal {
  if (value.trim() === '') return new Decimal(0);
  try {
    return new Decimal(value);
  } catch {
    return new Decimal(0);
  }
}

/** What this payment comes to, across every invoice it settles. */
export function sumOf(amounts: Iterable<string>): string {
  let total = new Decimal(0);
  for (const amount of amounts) total = total.plus(num(amount));
  return total.toFixed(2);
}

/**
 * Whether an allocation exceeds what is owed on that invoice.
 *
 * Caught here as well as at the server because it is the one mistake a person
 * makes with a keyboard rather than with intent -- a digit too many -- and the
 * whole payment is refused for it. Saying so beside the box is the difference
 * between fixing one number and retyping five.
 *
 * Under-paying is NOT flagged: a part payment is an ordinary thing to make.
 */
export function overAllocated(amount: string, outstanding: string): boolean {
  if (amount.trim() === '') return false;
  return num(amount).greaterThan(num(outstanding));
}
