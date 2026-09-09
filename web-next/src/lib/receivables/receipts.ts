// Money taken from customers, and taking one back.
//
// # The mirror of the payables side, and refused for the same reasons
//
// A receipt allocated to the wrong invoice is a fact about money that arrived.
// Design 02 §2 and C9.1: facts are not edited. The original stays and a NEW
// receipt reverses it, posted through the same rule with the sides flipped, so
// cash goes back out and the receivable is reinstated. The customer's statement
// then shows both, which is how they can see how they got back to owing it.
//
// # Why the reversal is read from the entry rather than rebuilt
//
// A receipt that settled a foreign-currency invoice carries a realised exchange
// gain or loss whose size depended on two rates on two days. Rebuilding the
// entry from the rule at TODAY's date would not recover it, and reversing one
// leg and not the other would leave the gain standing while the receipt it
// arose from was undone. The service reads the original entry and flips it;
// nothing on this side has an opinion about the accounting.
//
// # Unapplied is not the invoice allocation
//
// A receipt's amount IS the sum of its invoice allocations by construction, so
// that figure is always zero and says nothing. What can be exhausted is the
// INSTALMENT side, and `unapplied` is what is left to mark a schedule off with.

/** One receipt, as `GET /receivables/receipts` answers it. */
export interface ListedReceipt {
  id: string;
  receipt_number: string;
  customer_id: string;
  customer: string;
  received_on: string;
  method: string;
  reference?: string;
  amount: string;
  /** What is left on it to collect an instalment against. */
  unapplied: string;
  currency: string;
  /** This document exists to undo another receipt. */
  reversal: boolean;
  /** Another receipt has undone this one. */
  reversed: boolean;
}

/** Why a receipt cannot be reversed, or null when it can. */
export type ReceiptBlock = 'is_a_reversal' | 'already_reversed' | null;

export function receiptBlock(r: ListedReceipt): ReceiptBlock {
  // A reversal first, for the same reason as on the payables side: it is the
  // thing somebody has to understand before any other sentence makes sense.
  if (r.reversal) return 'is_a_reversal';
  if (r.reversed) return 'already_reversed';
  return null;
}

export function canReverseReceipt(r: ListedReceipt): boolean {
  return receiptBlock(r) === null;
}

/** How a receipt reads in a list. */
export type ReceiptState = 'live' | 'reversed' | 'reversal';

export function receiptState(r: ListedReceipt): ReceiptState {
  if (r.reversal) return 'reversal';
  if (r.reversed) return 'reversed';
  return 'live';
}

/**
 * Whether part of this receipt has been spent settling an instalment.
 *
 * Worth showing beside a reversal, because reversing a receipt an instalment
 * schedule has already been collected against is the case somebody should look
 * at twice before pressing the button. The server allows it — the collection is
 * a separate document and stays — and a screen that said nothing would be
 * hiding the one fact that makes the decision hard.
 */
export function partlySpent(r: ListedReceipt): boolean {
  if (r.reversal || r.reversed) return false;
  return r.unapplied !== r.amount;
}
