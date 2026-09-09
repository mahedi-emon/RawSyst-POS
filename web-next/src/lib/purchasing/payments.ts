// What has been paid to suppliers, and what can still be taken back.
//
// # Posted history is never edited
//
// Design 02 §111 is absolute: "Corrections happen only by posting a reversing
// entry with reverses_id set. There is no code path — and no database
// permission — that edits posted history." So a payment sent to the wrong
// supplier is not corrected; a NEW payment reverses it, both stay on the
// record, and the payables ledger shows how the balance got back to where it
// started.
//
// # Three refusals, stated before the button rather than after it
//
// The service refuses a reversal for exactly three reasons, and a screen that
// offered the control anyway would teach the rule by failing. Each one is a
// different sentence because each has a different remedy: a reversal cannot be
// reversed (record a new payment), a payment can only be reversed once (look at
// the reversal that already exists), and a payment that never reached the
// journal has nothing to undo (it is not a correction, it is a defect).
//
// # One reversal per original, in full
//
// Enforced by a unique index on `reverses_id`. A clerk who split a payment
// across three bills and got one wrong reverses the whole thing and pays again.
// Partial reversal would be a new business rule and this does not invent one.

/** One supplier payment, as `GET /purchasing/payments` answers it. */
export interface ListedPayment {
  id: string;
  payment_number: string;
  supplier_id: string;
  supplier: string;
  paid_on: string;
  method: string;
  reference?: string;
  amount: string;
  currency: string;
  /** This document exists to undo another. */
  reversal: boolean;
  /** Something else has undone this one. */
  reversed: boolean;
  /** The supplier references of the bills it settled. */
  bills: string[];
  /** Whether it reached the journal. A payment that did not cannot be reversed. */
  posted: boolean;
}

/** Why a payment cannot be reversed, or null when it can. */
export type ReversalBlock =
  | 'is_a_reversal'
  | 'already_reversed'
  | 'never_posted'
  | null;

export function reversalBlock(p: ListedPayment): ReversalBlock {
  // Order matters: a reversal that has itself been reversed should read as a
  // reversal, because that is the thing somebody has to understand before the
  // second sentence makes any sense.
  if (p.reversal) return 'is_a_reversal';
  if (p.reversed) return 'already_reversed';
  if (!p.posted) return 'never_posted';
  return null;
}

export function canReverse(p: ListedPayment): boolean {
  return reversalBlock(p) === null;
}

/**
 * How a payment reads in a list: live, undone, or the undoing.
 *
 * All three are shown. A reversed payment is not one anybody may reverse
 * again, and hiding it would leave a figure in the payables ledger that nothing
 * on screen explains.
 */
export type PaymentState = 'live' | 'reversed' | 'reversal' | 'unposted';

export function paymentState(p: ListedPayment): PaymentState {
  if (p.reversal) return 'reversal';
  if (p.reversed) return 'reversed';
  if (!p.posted) return 'unposted';
  return 'live';
}

/**
 * What the bills this payment settled are called, for a row.
 *
 * The supplier's own invoice numbers, which is what a buyer recognises. A
 * payment against no bill is possible in the data and not through the create
 * route, which refuses an empty allocation — so an empty list is worth showing
 * as such rather than as a blank cell.
 */
export function billsSettled(p: ListedPayment): string {
  return p.bills.length > 0 ? p.bills.join(', ') : '';
}
