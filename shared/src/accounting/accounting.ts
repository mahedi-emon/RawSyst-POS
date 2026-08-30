// The judgements the accounting screens make, away from the JSX.
//
// Two of them decide what a reader is told about how much to trust a figure,
// which is the whole reason this section exists rather than four report pages.

import type { FiscalCalendar, Period } from '../api/accounting';

/** A date range. */
export interface Range {
  from: string;
  to: string;
}

/** The current month so far, which is what a statement opens on. */
export function monthToDate(today = new Date()): Range {
  const first = new Date(
    Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1),
  );
  return { from: iso(first), to: iso(today) };
}

/** The whole of a given period. */
export function rangeOf(period: Period): Range {
  return { from: period.starts_on, to: period.ends_on };
}

function iso(d: Date): string {
  return d.toISOString().slice(0, 10);
}

/** How settled the figures over a range are.
 *
 *  Not decoration. A profit-and-loss for a month that is still open will be a
 *  different number tomorrow, and a person who prints one and sends it to a
 *  bank should know that before they do rather than afterwards.
 *
 *    settled — every period the range touches is closed or locked
 *    partly  — some are closed and some are not
 *    open    — none are closed, which is the ordinary case for this month
 *
 *  `unknown` when the calendar has not loaded: saying "open" while the answer
 *  is still arriving would be a claim the screen cannot support. */
export type Settledness = 'settled' | 'partly' | 'open' | 'unknown';

export function settlednessOf(
  calendar: FiscalCalendar | null,
  range: Range,
): Settledness {
  if (!calendar) return 'unknown';

  const touched = periodsTouching(calendar, range);
  if (touched.length === 0) return 'unknown';

  const closed = touched.filter((p) => p.state !== 'open').length;
  if (closed === touched.length) return 'settled';
  if (closed === 0) return 'open';
  return 'partly';
}

/** Every period whose dates overlap the range at all. */
export function periodsTouching(
  calendar: FiscalCalendar,
  range: Range,
): Period[] {
  const out: Period[] = [];
  for (const year of calendar.years) {
    for (const p of year.periods) {
      // Two ranges overlap unless one ends before the other starts. Compared
      // as ISO date STRINGS, which sort correctly and avoid constructing a
      // Date per period in a loop that runs over every month a shop has ever
      // traded.
      if (p.starts_on <= range.to && p.ends_on >= range.from) {
        out.push(p);
      }
    }
  }
  return out;
}

/** Whether a period can be closed right now.
 *
 *  Periods close in order, so the only one a person may close is the earliest
 *  that is still open. Offering a Close button on every open month would mean
 *  most presses come back refused, and a screen whose buttons usually fail is
 *  a screen people stop believing. */
export function closablePeriod(calendar: FiscalCalendar | null): string | null {
  if (!calendar) return null;
  const open = allPeriods(calendar)
    .filter((p) => p.state === 'open')
    .sort(byDate);
  return open.length > 0 ? open[0]!.id : null;
}

/** Whether a period can be reopened.
 *
 *  Closed, and not locked: locking means the year-end routine has closed
 *  revenue and expense into retained earnings, and putting a transaction back
 *  would leave those entries wrong with nothing saying so. */
export function reopenable(period: Period): boolean {
  return period.state === 'closed';
}

export function allPeriods(calendar: FiscalCalendar): Period[] {
  return calendar.years.flatMap((y) => y.periods);
}

function byDate(a: Period, b: Period): number {
  return a.starts_on < b.starts_on ? -1 : a.starts_on > b.starts_on ? 1 : 0;
}

/** The next fiscal year a person would want to open: one past the latest
 *  there is. */
export function nextYearToOpen(calendar: FiscalCalendar | null): number {
  const thisYear = new Date().getUTCFullYear();
  if (!calendar || calendar.years.length === 0) return thisYear;
  return Math.max(...calendar.years.map((y) => y.fiscal_year)) + 1;
}

/** Whether a figure that must be zero is not.
 *
 *  Used for the trial balance's difference and the balance sheet's. Compared as
 *  a string with the sign and the separators taken out, because the value is a
 *  decimal the server computed exactly and turning it into a float to ask
 *  whether it is zero is the one operation guaranteed to sometimes lie. */
export function isOff(difference: string): boolean {
  return /[1-9]/.test(difference ?? '');
}
