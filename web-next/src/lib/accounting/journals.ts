// Hand-written journal entries, and the chart they name accounts from.
//
// # A journal balances or it is refused
//
// Debits equal credits, exactly, and the server says the difference rather than
// the two totals: *"debits come to X and credits to Y, a difference of Z"* —
// because the number a person has to find is the difference, not the pair.
//
// This computes the same figure while they type. Not to replace the refusal but
// to prevent it: an entry that does not balance is the ordinary state of a
// journal half-written, and finding out on submit is finding out too late.
//
// # A line is a debit or a credit, never both and never neither
//
// The server refuses both — *"a negative debit is a credit"* — so the form
// carries two boxes and this reports a line that has filled in the wrong number
// of them.

import Decimal from 'decimal.js';

/** One line of the chart of accounts. */
export interface Account {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  type: string;
  parent_id?: string;
  /** A header groups its children and may not hold an entry. */
  is_postable: boolean;
  /** A sub-ledger must reconcile to this exactly (C9.3). */
  is_control: boolean;
  control_of?: string;
  currency?: string;
  is_active: boolean;
  /** The posting rules' name for it, where one is mapped. */
  role?: string;
  /** Debits less credits, signed and not normalised by type. */
  balance: string;
}

export interface JournalLine {
  account_id: string;
  account_code: string;
  account_name: string;
  debit: string;
  credit: string;
  memo?: string;
}

export interface Journal {
  id: string;
  journal_no: string;
  journal_entry_id: string;
  entry_no: number;
  entry_date: string;
  reason: string;
  memo?: string;
  currency: string;
  total: string;
  lines: JournalLine[];
  /** Set on an entry that reverses another. */
  reverses_id?: string;
  /** Set on an entry that has been reversed. */
  reversed_by?: string;
  created_by?: string;
  created_at: string;
}

/** A line as somebody is typing it. */
export interface DraftLine {
  accountID: string;
  debit: string;
  credit: string;
  memo: string;
}

export function blankLine(): DraftLine {
  return { accountID: '', debit: '', credit: '', memo: '' };
}

/** A number a person is part-way through typing is not a number yet. */
function decimalOf(raw: string | undefined): Decimal {
  const value = (raw ?? '').trim();
  if (value === '' || /[.+-]$/.test(value)) return new Decimal(0);
  try {
    const d = new Decimal(value);
    return d.isFinite() ? d : new Decimal(0);
  } catch {
    return new Decimal(0);
  }
}

export interface Balance {
  debits: string;
  credits: string;
  /** Debits less credits, signed. Zero when the entry balances. */
  difference: string;
  balanced: boolean;
  /** Lines carrying an account and exactly one amount. */
  usable: number;
}

/**
 * What the entry comes to as it stands.
 *
 * The difference is what the screen shows, because it is the figure somebody
 * has to close — two totals leave them doing the subtraction the server has
 * already done for them.
 */
export function balanceOf(lines: readonly DraftLine[], places = 2): Balance {
  let debits = new Decimal(0);
  let credits = new Decimal(0);
  let usable = 0;

  for (const line of lines) {
    const debit = decimalOf(line.debit);
    const credit = decimalOf(line.credit);
    debits = debits.plus(debit);
    credits = credits.plus(credit);
    // Exactly one side, and an account to put it on.
    if (line.accountID !== '' && debit.greaterThan(0) !== credit.greaterThan(0)) {
      usable += 1;
    }
  }

  const difference = debits.minus(credits);
  return {
    debits: debits.toDecimalPlaces(places, Decimal.ROUND_HALF_UP).toFixed(places),
    credits: credits.toDecimalPlaces(places, Decimal.ROUND_HALF_UP).toFixed(places),
    difference: difference
      .toDecimalPlaces(places, Decimal.ROUND_HALF_UP)
      .toFixed(places),
    balanced: difference.isZero() && debits.greaterThan(0),
    usable,
  };
}

/** Whether one line is the shape the server accepts. */
export function lineProblem(
  line: DraftLine,
): 'none' | 'no_account' | 'no_amount' | 'both_sides' | 'negative' {
  const debit = decimalOf(line.debit);
  const credit = decimalOf(line.credit);
  const touched =
    line.accountID !== '' || line.debit.trim() !== '' || line.credit.trim() !== '';
  if (!touched) return 'none';
  if (debit.isNegative() || credit.isNegative()) return 'negative';
  if (line.accountID === '') return 'no_account';
  if (debit.greaterThan(0) && credit.greaterThan(0)) return 'both_sides';
  if (!debit.greaterThan(0) && !credit.greaterThan(0)) return 'no_amount';
  return 'none';
}

/** Whether a line has anything on it at all. Blank rows are dropped, not sent. */
export function isBlank(line: DraftLine): boolean {
  return (
    line.accountID === '' &&
    line.debit.trim() === '' &&
    line.credit.trim() === '' &&
    line.memo.trim() === ''
  );
}

/** The lines to send: the ones with something on them, in order. */
export function postableLines(
  lines: readonly DraftLine[],
): { account_id: string; debit?: string; credit?: string; memo?: string }[] {
  return lines.filter((l) => !isBlank(l)).map((l) => ({
    account_id: l.accountID,
    // Only the side that carries a figure. Sending "0" on the other would be
    // sending both, which the server refuses as ambiguous.
    ...(decimalOf(l.debit).greaterThan(0) ? { debit: l.debit.trim() } : {}),
    ...(decimalOf(l.credit).greaterThan(0) ? { credit: l.credit.trim() } : {}),
    ...(l.memo.trim() !== '' ? { memo: l.memo.trim() } : {}),
  }));
}

export type Readiness =
  | { ok: true }
  | {
      ok: false;
      reason: 'no_reason' | 'too_few' | 'line_problem' | 'unbalanced' | 'nothing';
    };

/**
 * Whether the entry can be posted.
 *
 * The same refusals the server gives, in the same order, said before the round
 * trip. C10 requires a reason on every manual journal and the service says why:
 * it is *"what somebody reading the ledger a year from now has to go on."*
 */
export function readiness(
  lines: readonly DraftLine[],
  reason: string,
): Readiness {
  if (reason.trim() === '') return { ok: false, reason: 'no_reason' };

  const real = lines.filter((l) => !isBlank(l));
  if (real.length < 2) return { ok: false, reason: 'too_few' };
  if (real.some((l) => lineProblem(l) !== 'none')) {
    return { ok: false, reason: 'line_problem' };
  }

  const balance = balanceOf(real);
  if (balance.debits === '0.00') return { ok: false, reason: 'nothing' };
  if (!balance.balanced) return { ok: false, reason: 'unbalanced' };
  return { ok: true };
}

/**
 * The accounts a journal line may name.
 *
 * Postable and active only. A header groups its children and holds nothing;
 * offering one is offering the mistake that makes a chart of accounts stop
 * adding up.
 */
export function postableAccounts(accounts: readonly Account[]): Account[] {
  return accounts.filter((a) => a.is_postable && a.is_active);
}

/** The five headings a chart is read under, in the order a balance sheet uses. */
export const ACCOUNT_TYPES = [
  'asset',
  'liability',
  'equity',
  'revenue',
  'expense',
] as const;

export type AccountType = (typeof ACCOUNT_TYPES)[number];
