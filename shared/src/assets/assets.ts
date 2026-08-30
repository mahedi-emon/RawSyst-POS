// The judgements the asset screens make, away from the JSX.

import type { Asset } from '../api/assets';
import { monthName } from '../ui/format';
import type { Locale } from '../i18n/strings';

/** Today, as the date inputs want it. */
export function today(now = new Date()): string {
  return now.toISOString().slice(0, 10);
}

/** The next month anybody owes depreciation for, or null when nobody does.
 *
 *  # Why it comes from the register rather than from the clock
 *
 *  The obvious implementation is "last month". It is wrong for a company that
 *  has not run depreciation since March: the button would charge August, skip
 *  four months for ever, and the asset would reach the end of its life with
 *  four months of cost never charged to anything.
 *
 *  So it is the EARLIEST month any asset is waiting for. A company four months
 *  behind presses the button four times and each entry lands in the month it
 *  belongs to, which is the whole reason the routine works a month at a time.
 *
 *  Null when every asset is up to date, and the button is then not offered at
 *  all — a control that does nothing is worse than an absent one.
 */
export function monthAfter(assets: Asset[]): string | null {
  let earliest: string | null = null;

  for (const a of assets) {
    if (a.status !== 'in_use' || a.months_due <= 0) continue;

    // The month after the last one charged, or the month it was acquired when
    // nothing has been charged yet.
    const from = a.depreciated_to
      ? nextMonthOf(a.depreciated_to)
      : firstOfMonth(a.acquired_on);

    if (earliest === null || from < earliest) earliest = from;
  }
  return earliest;
}

/** A month as a person reads it, in their own language. */
export function monthLabelOf(iso: string, locale: Locale = 'en'): string {
  const [year, month] = iso.split('-');
  const n = Number(month);
  if (!year || !n) return iso;
  return `${monthName(n, locale)} ${year}`;
}

function firstOfMonth(iso: string): string {
  return iso.slice(0, 7) + '-01';
}

function nextMonthOf(iso: string): string {
  const [y, m] = iso.split('-').map(Number);
  if (!y || !m) return iso;
  const year = m === 12 ? y + 1 : y;
  const month = m === 12 ? 1 : m + 1;
  return `${year}-${String(month).padStart(2, '0')}-01`;
}

/** What a disposal will land as, worked out on the screen so a person can see
 *  which way it goes before they commit.
 *
 *  The SERVER computes the figure that is actually posted, from what it has
 *  really depreciated. This is a preview and nothing more — the same
 *  relationship the count sheet's variance has to the one that posts. */
export type DisposalOutcome = 'gain' | 'loss' | 'even';

export function disposalOutcome(
  bookValue: string,
  proceeds: string,
): DisposalOutcome {
  const book = minor(bookValue);
  const got = minor(proceeds);
  if (book === null || got === null) return 'even';
  if (got > book) return 'gain';
  if (got < book) return 'loss';
  return 'even';
}

/** A money string as an integer number of minor units.
 *
 *  Two decimals, scaled, so the comparison above is exact. Turning a book value
 *  into a float to ask whether it equals the proceeds is the one operation
 *  guaranteed to sometimes say a sale at exactly book value made a gain of a
 *  hundred-millionth. */
function minor(amount: string): number | null {
  const s = (amount ?? '').trim().replace(/,/g, '');
  if (!/^-?\d*(\.\d*)?$/.test(s) || s === '' || s === '-' || s === '.') {
    return null;
  }
  const negative = s.startsWith('-');
  const [whole = '0', fraction = ''] = (negative ? s.slice(1) : s).split('.');
  const value =
    Number(whole || '0') * 100 + Number((fraction + '00').slice(0, 2) || '0');
  return negative ? -value : value;
}
