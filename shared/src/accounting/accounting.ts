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

/** Today, as the date inputs want it. */
export function today(now = new Date()): string {
  return now.toISOString().slice(0, 10);
}

/** One line read out of a pasted bank statement. */
export interface ParsedLine {
  value_date: string;
  description: string;
  reference?: string;
  amount: string;
}

/** What a paste produced, or what is wrong with it. */
export interface ParsedStatement {
  lines: ParsedLine[];
  /** A sentence naming the first row that could not be read, and why. Null
   *  when everything parsed. */
  problem: string | null;
}

/** Reads a pasted bank statement.
 *
 *  # Why paste rather than a file picker
 *
 *  Every bank exports a different file, and half of them export something
 *  Excel has already opened and re-saved. A person copying rows out of a
 *  spreadsheet is the path that works with all of them, and it is also the one
 *  where a mistake is visible before it is sent.
 *
 *  # What it accepts
 *
 *  Comma or tab separated, which is what a paste from Excel actually produces:
 *
 *      date, description, amount
 *      date, description, reference, amount
 *
 *  The amount is the LAST field, because that is the one column every bank puts
 *  somewhere different and the only one whose position can be inferred from its
 *  content. Separate debit and credit columns are not accepted, deliberately: a
 *  guess about which of two columns a figure belongs in is a guess about the
 *  direction of the money.
 *
 *  # Why a problem is a sentence, not a thrown error
 *
 *  A person pasting forty rows wants to know which one is wrong. So the first
 *  bad row is named with its number and what was wrong with it, and nothing is
 *  sent until it is fixed — rather than importing thirty-nine rows and leaving
 *  a difference nobody can explain.
 */
export function parseStatementCsv(text: string): ParsedStatement {
  const rows = text
    .split(/\r?\n/)
    .map((r) => r.trim())
    .filter((r) => r !== '');

  const lines: ParsedLine[] = [];
  for (let i = 0; i < rows.length; i++) {
    const row = rows[i]!;
    const cells = rejoinParenthesised(
      row.split(/\t|,(?=(?:[^"]*"[^"]*")*[^"]*$)/).map(unquote),
    );

    // A header row, skipped rather than refused: a person pasting from a
    // spreadsheet takes the headings with them nearly every time.
    if (i === 0 && !isDate(cells[0] ?? '')) continue;

    if (cells.length < 3) {
      return { lines: [], problem: rowProblem(i + 1, 'columns') };
    }

    const date = cells[0] ?? '';
    if (!isDate(date)) {
      return { lines: [], problem: rowProblem(i + 1, 'date') };
    }

    const amount = normaliseAmount(cells[cells.length - 1] ?? '');
    if (amount === null) {
      return { lines: [], problem: rowProblem(i + 1, 'amount') };
    }

    lines.push({
      value_date: date,
      description: cells[1] ?? '',
      reference: cells.length > 3 ? cells[2] : undefined,
      amount,
    });
  }

  if (lines.length === 0) {
    return { lines: [], problem: rowProblem(0, 'empty') };
  }
  return { lines, problem: null };
}

/** A problem, keyed so the screen can translate it.
 *
 *  Returned as a key rather than a sentence because this module has no
 *  translator, and a parser that returned English would put a hardcoded string
 *  on an Arabic screen. */
function rowProblem(row: number, what: string): string {
  return `${what}:${row}`;
}

/** Puts an accounting-style negative back together.
 *
 *  A bank that exports `(1,234.56)` without quoting it hands us a row that
 *  splits on the thousands separator inside its own parentheses: the last two
 *  cells come out as `(1` and `234.56)`. Left alone the amount reads as
 *  `234.56)`, the row is refused, and the shape a great many banks actually
 *  produce is the one this parser cannot read.
 *
 *  So trailing cells are rejoined, from the end, until the opening bracket is
 *  found. Bounded to the last few because an unmatched `(` earlier in a
 *  description is somebody's note and not part of an amount.
 */
function rejoinParenthesised(cells: string[]): string[] {
  const last = cells[cells.length - 1];
  if (!last || !last.endsWith(')') || last.startsWith('(')) return cells;

  for (let i = cells.length - 2; i >= 0 && i >= cells.length - 4; i--) {
    if ((cells[i] ?? '').startsWith('(')) {
      return [...cells.slice(0, i), cells.slice(i).join(',')];
    }
  }
  return cells;
}

function unquote(cell: string): string {
  const t = cell.trim();
  if (t.startsWith('"') && t.endsWith('"') && t.length >= 2) {
    return t.slice(1, -1).replace(/""/g, '"').trim();
  }
  return t;
}

function isDate(cell: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(cell.trim());
}

/** A bank's amount as a decimal string, or null if it is not one.
 *
 *  Accepts thousands separators and accounting parentheses, because both come
 *  straight out of a bank's own export: `(1,234.56)` is how a great many of
 *  them write a debit, and reading it as a positive number would put the money
 *  in rather than out. */
function normaliseAmount(cell: string): string | null {
  let t = cell.trim();
  if (t === '') return null;

  let negative = false;
  if (t.startsWith('(') && t.endsWith(')')) {
    negative = true;
    t = t.slice(1, -1).trim();
  }
  if (t.startsWith('-')) {
    negative = !negative;
    t = t.slice(1).trim();
  }
  if (t.startsWith('+')) t = t.slice(1).trim();

  t = t.replace(/[,\s]/g, '');
  if (!/^\d+(\.\d+)?$/.test(t)) return null;
  if (/^0+(\.0+)?$/.test(t)) return null; // a zero line is not a movement

  return (negative ? '-' : '') + t;
}
