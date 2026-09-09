// One investor's capital account.
//
// # What the backend sends, and what it does not
//
// `GET /investors/{id}/statement` answers the movements inside a period, the
// balance the account stood at before it, and the totals in and out. It does
// NOT send a running balance per row, and it does not send an export — there is
// no export route for a statement, so this screen has no download button and
// does not pretend to.
//
// # The running balance is arithmetic, not a new fact
//
// Adding the opening to each movement in turn is the same sum a paper capital
// account does, and it is done here rather than asked for because the server
// already sent both halves. It is done with `Decimal` and never with a float:
// `Number('0.1') + Number('0.2')` is `0.30000000000000004`, and a statement is
// exactly where somebody would notice.
//
// # Oldest first, which is not how the route answers
//
// The route orders newest first, because that is what a register wants. A
// STATEMENT is read from the opening balance downwards — that is the only order
// in which a running balance means anything — so the rows are reversed here and
// the reversal is stated rather than left as a surprise.
//
// # Contributions add, withdrawals subtract, and nothing else exists
//
// `investment_direction_valid` admits exactly those two. A direction this file
// has not heard of is treated as a contribution and reported, rather than
// silently dropped from a balance somebody is about to sign.

import Decimal from 'decimal.js';

/** One movement, exactly as the route answers it. */
export interface Movement {
  id: string;
  /** `contribution` or `withdrawal`. */
  direction: string;
  amount: string;
  moved_on: string;
  /** The cash or bank account the money moved through. Empty if it is gone. */
  account?: string;
  reference?: string;
  note?: string;
  currency: string;
}

/** The statement, as `GET /investors/{id}/statement` answers it. */
export interface InvestorStatement {
  investor_id: string;
  investor: string;
  currency: string;
  /** The window that was applied. Empty when unbounded. */
  from?: string;
  to?: string;
  opening: string;
  contributed: string;
  withdrawn: string;
  closing: string;
  movements: Movement[];
}

/** One row of the statement, with the balance after it. */
export interface StatementRow extends Movement {
  /** What the account stood at once this movement had been recorded. */
  balance: string;
  /** True for a withdrawal, so a screen can show a sign as well as a colour. */
  outward: boolean;
}

export function isWithdrawal(m: { direction: string }): boolean {
  return m.direction === 'withdrawal';
}

/**
 * The movements in reading order, each with the balance after it.
 *
 * The last row's balance is the closing balance the server sent, and a screen
 * that finds otherwise has found a real disagreement — see `closesWhereItSays`.
 */
export function withRunningBalance(s: InvestorStatement): StatementRow[] {
  let balance = new Decimal(s.opening || '0');
  // Reversed: the route answers newest first and a running balance only reads
  // downwards from the opening.
  return [...s.movements].reverse().map((m) => {
    const amount = new Decimal(m.amount || '0');
    balance = isWithdrawal(m) ? balance.minus(amount) : balance.plus(amount);
    return { ...m, balance: balance.toFixed(2), outward: isWithdrawal(m) };
  });
}

/**
 * Whether the rows arrive at the closing balance the server states.
 *
 * They should, because both come from the same rows. When they do not, one of
 * two things has happened: a movement was recorded between the two queries
 * inside the request, or a direction exists that this file does not know how to
 * apply. Either way a statement whose own arithmetic disagrees must say so
 * rather than print two figures and let the reader find it.
 */
export function closesWhereItSays(s: InvestorStatement): boolean {
  const rows = withRunningBalance(s);
  const last = rows.length > 0 ? rows[rows.length - 1]!.balance : null;
  const reached = last ?? new Decimal(s.opening || '0').toFixed(2);
  return new Decimal(reached).equals(new Decimal(s.closing || '0'));
}

/** The net movement over the window: what went in less what came out. */
export function movedInPeriod(s: InvestorStatement): string {
  return new Decimal(s.contributed || '0')
    .minus(new Decimal(s.withdrawn || '0'))
    .toFixed(2);
}

/**
 * A period, as the two date boxes hold it.
 *
 * Both ends optional and both inclusive, matching the route. An end before its
 * start is refused by the server, and this says so first so nobody presses a
 * button to be told.
 */
export interface Period {
  from: string;
  to: string;
}

export type PeriodProblem = 'backwards' | 'unreadable' | null;

const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/;

export function periodProblem(p: Period): PeriodProblem {
  for (const value of [p.from, p.to]) {
    if (value !== '' && !ISO_DATE.test(value)) return 'unreadable';
  }
  if (p.from !== '' && p.to !== '' && p.to < p.from) return 'backwards';
  return null;
}

/** The query the statement route takes. Empty ends are left off, not sent blank. */
export function periodQuery(p: Period): Record<string, string> {
  const query: Record<string, string> = {};
  if (p.from) query.from = p.from;
  if (p.to) query.to = p.to;
  return query;
}
