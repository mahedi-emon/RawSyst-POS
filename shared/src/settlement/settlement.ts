// What the settlement screen decides, separated from what it draws.
//
// One decision here costs money if it is wrong: the fee implied by a deposit.
// The person entering it has a bank statement in front of them, types the
// figure that landed, and the difference between that and what was taken is
// what the acquirer kept. If the screen computes that difference differently
// from the server the accountant sees one number and the ledger records
// another, and they find out at the year end.
//
// So the arithmetic is BigInt minor units, the same as the receivables screens
// and for the same reason: float64 cannot hold 0.15, and a fee that drifts by a
// fraction of a halala is a fee that eventually disagrees with the statement it
// was read from.

import { major, minor } from '../receivables/receivables';
import type { PendingTender } from '../api/settlement';

/** What the selected payments come to, as the server will compute it. */
export function grossOf(
  pending: PendingTender[],
  selected: ReadonlySet<string>,
): string {
  let total = 0n;
  for (const t of pending) {
    if (selected.has(t.tender_id)) total += minor(t.amount);
  }
  return major(total);
}

export type DepositCheck =
  | { kind: 'nothing_selected'; message: string }
  | { kind: 'no_amount'; message: string }
  | { kind: 'exceeds'; message: string }
  | { kind: 'no_fee'; gross: string; fee: string; message: string }
  | { kind: 'ready'; gross: string; fee: string; message: string };

/**
 * Whether a deposit can be recorded, and what it implies.
 *
 * The `no_fee` case is called out rather than folded into `ready` because it is
 * usually a mistake: an acquirer that charged nothing is possible but rare, and
 * far more often the person has typed the gross into the deposit box. Saying so
 * is cheaper than a journal entry that has to be reversed.
 *
 * An over-large deposit is refused here as well as by the server. The server is
 * the authority; this exists so the refusal arrives while the figure is still
 * on screen and can be corrected, rather than as an error after a submit.
 */
export function checkDeposit(
  pending: PendingTender[],
  selected: ReadonlySet<string>,
  netAmount: string,
): DepositCheck {
  if (selected.size === 0) {
    return {
      kind: 'nothing_selected',
      message: 'Tick the payments this deposit covered.',
    };
  }

  const gross = grossOf(pending, selected);
  const net = minor(netAmount);
  if (net <= 0n) {
    return {
      kind: 'no_amount',
      message: 'Enter the amount that landed in the bank.',
    };
  }

  const fee = minor(gross) - net;
  if (fee < 0n) {
    return {
      kind: 'exceeds',
      message:
        `This deposit is more than the ${gross} of payments it covers. ` +
        'An acquirer paying more than was taken is a separate event, not a fee.',
    };
  }

  const count = selected.size === 1 ? '1 payment' : `${selected.size} payments`;
  if (fee === 0n) {
    return {
      kind: 'no_fee',
      gross,
      fee: '0.00',
      message:
        `${count} totalling ${gross}, deposited in full. ` +
        'Check the statement — a deposit with no fee on it is unusual.',
    };
  }

  return {
    kind: 'ready',
    gross,
    fee: major(fee),
    message: `${count} totalling ${gross}, less a fee of ${major(fee)}.`,
  };
}

/** Whether the form may be submitted. */
export function canRecord(check: DepositCheck): boolean {
  return check.kind === 'ready' || check.kind === 'no_fee';
}

/**
 * Groups what is outstanding by payment method.
 *
 * Because that is how it will be deposited. Mada and an international card
 * settle on different days into different batches, and a list sorted only by
 * date makes the person pick them apart by eye — which is where a payment gets
 * included in the wrong deposit.
 */
export interface MethodGroup {
  method: string;
  count: number;
  total: string;
  tenders: PendingTender[];
}

export function byMethod(pending: PendingTender[]): MethodGroup[] {
  const groups = new Map<string, PendingTender[]>();
  for (const t of pending) {
    const held = groups.get(t.method);
    if (held) held.push(t);
    else groups.set(t.method, [t]);
  }

  return [...groups.entries()]
    .map(([method, tenders]) => ({
      method,
      count: tenders.length,
      total: major(tenders.reduce((sum, t) => sum + minor(t.amount), 0n)),
      tenders,
    }))
    // Largest first: the biggest pile of unsettled money is the one somebody
    // came to this screen about. Ties keep the order the payments arrived in
    // — a comparator that never returns 0 is inconsistent, and Array.sort is
    // entitled to reorder equal elements however it likes when given one.
    .sort((a, b) => {
      const left = minor(a.total);
      const right = minor(b.total);
      if (left === right) return 0;
      return right > left ? 1 : -1;
    });
}

/** What the whole outstanding position comes to. */
export function outstandingTotal(pending: PendingTender[]): string {
  return major(pending.reduce((sum, t) => sum + minor(t.amount), 0n));
}
