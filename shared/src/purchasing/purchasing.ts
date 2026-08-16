// The buying screens' own decisions, separated from their rendering.
//
// Three things here decide something rather than lay something out: what to
// pre-fill a receiving form with, how a three-way match result is summarised
// for a buyer, and whether a bill can be paid. Each has a failure mode that
// costs money rather than looking wrong, so each lives here where it can be
// tested.

import type { Bill, MatchLine, OrderLine } from '../api/purchasing';
import { trimQuantity } from '../dashboard/drilldown';

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
export function matchSummary(lines: MatchLine[]): {
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
          ? 'One check does not agree with the order or the delivery.'
          : `${breaches.length} checks do not agree with the order or the delivery.`,
    };
  }

  // Within tolerance is reported honestly rather than folded into "agrees".
  // A buyer seeing a supplier drift upward inside tolerance month after month
  // is seeing something real, and a green tick would hide it.
  if (lines.some((l) => l.outcome === 'within_tolerance')) {
    return {
      outcome: 'within_tolerance',
      breaches: 0,
      message: 'Small differences, inside the tolerance you have set.',
    };
  }

  return {
    outcome: 'pass',
    breaches: 0,
    message: 'Everything agrees with the order and the delivery.',
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
