// How far off the system was.
//
// The figure somebody checking a count actually reads is not what they counted
// — it is the difference — so it is computed beside the box as they type rather
// than after a round trip.
//
// decimal.js because a count posts to the books: the difference becomes a
// journal entry, and `0.1 + 0.2` in binary floating point is not `0.3`.

import Decimal from 'decimal.js';

/**
 * Counted less what the system believed, or null when nothing was counted.
 *
 * **Blank is not zero.** Counting nothing and counting none of something are
 * different claims, and the second is a write-off of everything on the shelf.
 * The server treats an absent line as untouched, and this returns null so the
 * screen can say "not counted yet" rather than showing a difference of minus
 * everything.
 */
export function differenceOf(counted: string, systemQty: string): string | null {
  const typed = counted.trim();
  if (typed === '') return null;
  // Half typed. decimal.js parses "1." as 1, so without this the difference
  // flickers to -71 between the 1 and the 5 of "1.5" -- a number that was
  // never true, shown to somebody who is checking numbers.
  if (/[.+-]$/.test(typed)) return null;
  let a: Decimal;
  let b: Decimal;
  try {
    a = new Decimal(typed);
    b = new Decimal(systemQty || '0');
  } catch {
    // Part-way through typing -- "1." or "-" -- which is not yet a claim.
    return null;
  }
  const diff = a.minus(b);
  // Trailing zeros dropped: a difference of "-2.0000" reads as a precision
  // nobody asked for, and quantities here are whole units far more often than
  // not.
  return diff.toDecimalPlaces(4).toString();
}
