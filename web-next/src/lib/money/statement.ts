// Reading a bank statement a person has pasted in.
//
// # Why paste, rather than a file picker
//
// The API takes the lines as JSON; there is no upload route and inventing one
// would be inventing a backend. What a bank actually sends is a CSV, and what
// a person actually does with it is open it, tidy it, and copy the rows. So
// the screen takes the rows, and this reads them.
//
// # Fixed columns, and no guessing
//
// `date, description, reference, amount`, with the reference optional. Not a
// heuristic that finds the date column and the money column on its own: banks
// differ, and a heuristic that is right nine times out of ten is a heuristic
// that files a March charge in April on the tenth. The screen says the order
// above the box, which is a thing a person can follow.
//
// # ISO dates only, deliberately
//
// `03/04/2026` is the third of April in Dhaka and the fourth of March in
// California, and nothing in a pasted line says which. Every other date in
// this product is `YYYY-MM-DD`, so this refuses anything else per row rather
// than picking one reading and being silently wrong for half the world.
//
// # The sign is the bank's
//
// Positive is money ARRIVING in the account, which is what the API stores and
// what a bank's own CSV means by a credit. The screen says so; this does not
// try to work it out.

import Decimal from 'decimal.js';

import type { Key } from '@/lib/i18n/locale';

export interface ParsedLine {
  value_date: string;
  description: string;
  reference: string;
  /** A decimal STRING, exactly as typed. Never a number. */
  amount: string;
}

export interface RowProblem {
  /** 1-based, as the person sees it in their spreadsheet. */
  row: number;
  key: Key;
  text: string;
}

export interface ParseResult {
  lines: ParsedLine[];
  problems: RowProblem[];
}

const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/;

/** A decimal, or nothing. Trailing separators are not a number yet. */
function decimalOf(raw: string): Decimal | null {
  const value = raw.replace(/,/g, '').trim();
  if (value === '' || /[.+-]$/.test(value)) return null;
  try {
    const d = new Decimal(value);
    return d.isFinite() ? d : null;
  } catch {
    return null;
  }
}

/** Splits one pasted row. Tabs win, because a spreadsheet paste uses them. */
function fields(row: string): string[] {
  const parts = row.includes('\t') ? row.split('\t') : row.split(',');
  return parts.map((p) => p.trim().replace(/^"|"$/g, ''));
}

/**
 * Reads pasted rows into statement lines.
 *
 * Every row is either a line or a problem naming its own row number, so a
 * paste of two hundred rows with one bad date says which one rather than
 * refusing the lot. A blank row is neither: spreadsheets end with one.
 */
export function parseStatement(text: string): ParseResult {
  const lines: ParsedLine[] = [];
  const problems: RowProblem[] = [];

  const rows = text.split(/\r?\n/);
  rows.forEach((raw, i) => {
    const row = i + 1;
    if (raw.trim() === '') return;

    const cells = fields(raw);
    if (cells.length < 3) {
      problems.push({ row, key: 'nx.rec.rowTooFew', text: raw.trim() });
      return;
    }

    const [date, description, ...rest] = cells;
    // The amount is the last cell; anything between the description and it is
    // the reference. Four columns is the documented shape, and a fifth is
    // more likely a stray comma inside a description than a new field.
    const amountRaw = rest[rest.length - 1] ?? '';
    const reference = rest.slice(0, -1).join(' ').trim();

    if (!ISO_DATE.test(date ?? '')) {
      problems.push({ row, key: 'nx.rec.rowBadDate', text: date ?? '' });
      return;
    }
    const amount = decimalOf(amountRaw);
    if (amount === null) {
      problems.push({ row, key: 'nx.rec.rowBadAmount', text: amountRaw });
      return;
    }
    // The column refuses zero: `bank_statement_line_non_zero`. Said here so a
    // person fixes one row rather than reading a refusal about all of them.
    if (amount.isZero()) {
      problems.push({ row, key: 'nx.rec.rowZero', text: amountRaw });
      return;
    }
    if ((description ?? '').trim() === '') {
      problems.push({ row, key: 'nx.rec.rowNoDescription', text: raw.trim() });
      return;
    }

    lines.push({
      value_date: date as string,
      description: (description as string).trim(),
      reference,
      amount: amount.toFixed(2),
    });
  });

  return { lines, problems };
}

/** What the pasted lines come to. */
export function totalOf(lines: readonly ParsedLine[]): string {
  let total = new Decimal(0);
  for (const line of lines) total = total.plus(new Decimal(line.amount));
  return total.toFixed(2);
}

/**
 * Where the opening balance plus these lines lands.
 *
 * Shown beside the closing balance the person typed, because the API refuses
 * a statement whose lines do not reach its own closing figure — "Check that
 * every line was imported" — and that is a truncated paste nine times in ten.
 * Finding it while pasting beats finding it in a 400.
 */
export function closesAt(opening: string, lines: readonly ParsedLine[]): string {
  const start = decimalOf(opening) ?? new Decimal(0);
  return start.plus(new Decimal(totalOf(lines))).toFixed(2);
}

/** Whether the statement is internally consistent, as the API requires. */
export function addsUp(
  opening: string,
  lines: readonly ParsedLine[],
  closing: string,
): boolean {
  const stated = decimalOf(closing);
  if (stated === null || lines.length === 0) return false;
  return stated.equals(new Decimal(closesAt(opening, lines)));
}

/**
 * A statement line as it comes back, with whatever claimed it.
 *
 * `matched_to` is the journal entry number rather than an id, because that is
 * what somebody checks against the books.
 */
export interface StatementLine {
  id: string;
  value_date: string;
  description: string;
  reference?: string;
  /** Signed from the BANK's point of view: positive is money arriving. */
  amount: string;
  matched_to?: string;
  /** `automatic` or `manual`. A rule's guess and a person's decision are
   *  not the same claim, and undoing one is not undoing the other. */
  match_kind?: string;
  matched_by?: string;
}

/** A line in the books that no statement line has claimed. */
export interface LedgerLine {
  id: string;
  entry_date: string;
  entry_no: string;
  memo?: string;
  amount: string;
  source_type?: string;
}

export interface Statement {
  id: string;
  account: string;
  currency: string;
  starts_on: string;
  ends_on: string;
  opening_balance: string;
  closing_balance: string;
  /**
   * `draft` or `reconciled` — whether anybody has signed it off.
   *
   * NOT the same question as `reconciled` below, and the two can disagree: a
   * statement signed off in March is still signed off in April, and the
   * arithmetic underneath it is recomputed from today's books. So the badge
   * comes from here and the figure comes from there.
   */
  status: string;
  reference?: string;
  reconciled_by?: string;
  reconciled_at?: string;
  /** What the books say the account held at `ends_on`. */
  ledger_balance: string;
  /**
   * The closing balance, less the ledger balance, less what neither side has
   * matched. Zero means the two exception lists explain the whole gap and
   * nothing else does — which is the claim the sign-off makes.
   */
  difference: string;
  /** Whether that difference is currently zero. */
  reconciled: boolean;
  lines?: StatementLine[];
  /** What the books hold and the bank has not seen. C11's exception report. */
  unmatched_in_books?: LedgerLine[];
}

/** Whether a statement has been signed off, which is a person's act. */
export function isSignedOff(statement: { status: string }): boolean {
  return statement.status === 'reconciled';
}

/**
 * The statement lines nobody has matched.
 *
 * Charges and interest the books do not have — the other half of the exception
 * report, and the half that usually turns into a journal entry.
 */
export function unmatchedLines(
  statement: Pick<Statement, 'lines'>,
): StatementLine[] {
  return (statement.lines ?? []).filter((l) => !l.matched_to);
}

/**
 * A ledger line worth suggesting for a statement line.
 *
 * The same rule the server auto-matches on, run over what is left: the exact
 * amount, within three days. Deliberately narrow — a looser rule pairs a
 * payment with the following week's identical one, and the person doing the
 * matching would have to un-do it.
 */
export const MATCH_WINDOW_DAYS = 3;

export function likelyMatches(
  line: StatementLine,
  ledger: readonly LedgerLine[],
): LedgerLine[] {
  const amount = decimalOf(line.amount);
  if (amount === null) return [];
  return ledger.filter((l) => {
    const theirs = decimalOf(l.amount);
    if (theirs === null || !theirs.equals(amount)) return false;
    return daysApart(line.value_date, l.entry_date) <= MATCH_WINDOW_DAYS;
  });
}

/** Whole days between two ISO dates, as a magnitude. */
export function daysApart(a: string, b: string): number {
  const left = Date.parse(`${a}T00:00:00Z`);
  const right = Date.parse(`${b}T00:00:00Z`);
  if (Number.isNaN(left) || Number.isNaN(right)) return Number.POSITIVE_INFINITY;
  return Math.abs(left - right) / 86_400_000;
}
