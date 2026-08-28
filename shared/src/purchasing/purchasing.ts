// The buying screens' own decisions, separated from their rendering.
//
// Three things here decide something rather than lay something out: what to
// pre-fill a receiving form with, how a three-way match result is summarised
// for a buyer, and whether a bill can be paid. Each has a failure mode that
// costs money rather than looking wrong, so each lives here where it can be
// tested.

import type { Translate } from '../i18n/strings';

import type { Bill, MatchLine, OrderLine, Receipt } from '../api/purchasing';
import { trimQuantity } from '../dashboard/drilldown';
import { money } from '../ui/format';

/**
 * What to pre-fill the receiving form with.
 *
 * Whatever is still outstanding, because the overwhelmingly common case is a
 * delivery that matches the order — pre-filling means a storeman confirms
 * rather than transcribes, and transcription is where receiving errors come
 * from.
 *
 * A fully received line is left blank rather than filled with zero. A zero in
 * a quantity box invites someone to overtype it, and receiving more against a
 * complete line is exactly the mistake the blank prevents.
 */
export function receivingDefaults(lines: OrderLine[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of lines) {
    if (Number(line.qty_outstanding) > 0) {
      out[line.id] = trimQuantity(line.qty_outstanding);
    }
  }
  return out;
}

/**
 * What to tell the storeman once a delivery is recorded.
 *
 * Ordinarily "stock has been updated" is the whole story. It is not when the
 * delivery corrected the cost of a sale that went below zero: that moves cost of
 * goods sold on a sale already reported, and a figure changing on last week's
 * numbers with no explanation is how people stop trusting a system.
 *
 * So the correction is stated, with its direction in words rather than as a
 * sign. "Cost of goods sold rose by 80.00" is readable by the person standing at
 * the loading bay; "correction: +80.00" needs an accountant to interpret, and
 * the person receiving goods is not one.
 *
 * A currency is deliberately not shown. The receiving screen is inside one
 * company and its own base currency, and a code here would be the only one on
 * the screen.
 */
export function receiptNotice(receipt: Receipt): string {
  if (receipt.already_received) {
    return `That delivery was already recorded as ${receipt.grn_number}.`;
  }

  const recorded = `Recorded as ${receipt.grn_number}. Stock has been updated.`;

  const correction = Number(receipt.cost_correction);
  if (!Number.isFinite(correction) || correction === 0) return recorded;

  const units = trimQuantity(receipt.units_recosted);
  const items = units === '1' ? '1 unit' : `${units} units`;
  const direction = correction > 0 ? 'rose' : 'fell';
  // Formatted from the absolute value: the direction is already in the verb, and
  // "fell by (20.00)" reads as a double negative.
  const amount = money(Math.abs(correction).toFixed(2));

  return (
    `${recorded} ${items} sold before this delivery had been costed at an ` +
    `estimate, so cost of goods sold ${direction} by ${amount}.`
  );
}

/**
 * Whether a bill can be paid from this screen.
 *
 * A blocked bill cannot, and that is the entire point of the three-way match:
 * B5.2 calls it the single most effective control against supplier overbilling,
 * and a control that the UI routes around is decoration. The server refuses it
 * regardless — this only decides whether to offer the button.
 */
export function payable(bill: Bill): boolean {
  if (bill.status === 'blocked') return false;
  if (bill.status === 'draft' || bill.status === 'cancelled') return false;
  return Number(bill.outstanding) > 0;
}

/** A one-line summary of what the match found. */
export function matchSummary(
  lines: MatchLine[],
  // The reader's language, when the caller has one. A parameter rather than a
  // hook so this stays a pure function; English when it is absent.
  translate?: Translate,
): {
  outcome: 'pass' | 'within_tolerance' | 'breach';
  breaches: number;
  message: string;
} {
  const breaches = lines.filter((l) => l.outcome === 'breach');
  if (breaches.length > 0) {
    return {
      outcome: 'breach',
      breaches: breaches.length,
      message:
        breaches.length === 1
          ? (translate?.('match.oneDisagrees') ??
            'One check does not agree with the order or the delivery.')
          : (translate?.('match.nDisagree', { count: breaches.length }) ??
            `${breaches.length} checks do not agree with the order or the delivery.`),
    };
  }

  // Within tolerance is reported honestly rather than folded into "agrees".
  // A buyer seeing a supplier drift upward inside tolerance month after month
  // is seeing something real, and a green tick would hide it.
  if (lines.some((l) => l.outcome === 'within_tolerance')) {
    return {
      outcome: 'within_tolerance',
      breaches: 0,
      message:
        translate?.('match.withinTolerance') ??
        'Small differences, inside the tolerance you have set.',
    };
  }

  return {
    outcome: 'pass',
    breaches: 0,
    message:
      translate?.('match.allAgree') ??
      'Everything agrees with the order and the delivery.',
  };
}

/**
 * How much of an order has arrived, as a fraction for a progress reading.
 *
 * Clamped at 1. Over-receiving is a real event — a supplier sends a bonus
 * case — and a bar rendering past its own track looks like a bug rather than
 * like good news.
 */
export function receivedFraction(lines: OrderLine[]): number {
  let ordered = 0;
  let received = 0;
  for (const line of lines) {
    ordered += Number(line.qty_ordered) || 0;
    received += Number(line.qty_received) || 0;
  }
  if (ordered <= 0) return 0;
  return Math.min(received / ordered, 1);
}

/**
 * Whether a supplier payment can be reversed from the screen.
 *
 * The AP mirror of `canReversePayment`. Only a live payment: a reversal is not
 * itself reversible — undoing one means paying again, which leaves both facts
 * on the record — and a payment already undone stays visible but stops offering
 * the action, because a second attempt is refused by the server anyway and a
 * button that always refuses teaches a buyer to distrust the rest of them.
 */
export function canReverseSupplierPayment(payment: {
  id?: string;
  reverses_id?: string;
  reversed?: boolean;
}): boolean {
  return Boolean(payment.id) && !payment.reverses_id && !payment.reversed;
}

/** How a payment row reads, once reversals are in the list.
 *
 * Three states, because a buyer looking at a supplier's history needs to tell
 * them apart at a glance: money that went out and stayed out, money that went
 * out and came back, and the document that brought it back. */
export function paymentKind(payment: {
  reverses_id?: string;
  reversed?: boolean;
}): 'payment' | 'reversed' | 'reversal' {
  if (payment.reverses_id) return 'reversal';
  return payment.reversed ? 'reversed' : 'payment';
}
