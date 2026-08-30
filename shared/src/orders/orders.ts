// What an order screen decides, away from the JSX.

import type { Order, OrderLine, OrderState } from '../api/orders';

/** The lifecycle B11 draws, in order. */
export const LIFECYCLE: readonly OrderState[] = [
  'quotation',
  'confirmed',
  'processing',
  'packed',
  'delivered',
  'completed',
] as const;

/** How far along an order is, as an index into the lifecycle.
 *
 *  A cancelled order returns −1: it did not reach a step, it left the sequence,
 *  and drawing it at the step it happened to be on when somebody stopped it
 *  would say the opposite of what happened. */
export function stepOf(state: OrderState): number {
  if (state === 'cancelled') return -1;
  return LIFECYCLE.indexOf(state);
}

/** What pressing "next" would move an order to, or null when nothing would.
 *
 *  Null on `delivered` as well as on the two ends, because completing an order
 *  is something only invoicing does — a button that promised to complete it and
 *  came back refused every time is a button people stop believing. */
export function nextState(state: OrderState): OrderState | null {
  const at = stepOf(state);
  if (at < 0) return null;
  const next = LIFECYCLE[at + 1];
  if (!next || next === 'completed') return null;
  return next;
}

/** Whether an order can still be abandoned. */
export function cancellable(state: OrderState): boolean {
  return state !== 'completed' && state !== 'cancelled';
}

/** Which of B11's three documents makes sense to print right now.
 *
 *  A picking slip before anything is picked, a packing slip once something is,
 *  and a delivery note the same. Offering all three from the moment an order is
 *  a quotation would print two empty pages.
 */
export function documentsFor(order: Order): Array<'picking' | 'packing' | 'delivery'> {
  if (order.state === 'quotation' || order.state === 'cancelled') return [];

  const anythingPicked = (order.lines ?? []).some((l) => !isZero(l.qty_picked));
  if (!anythingPicked) return ['picking'];
  return ['picking', 'packing', 'delivery'];
}

/** What is left to pick on a line, as a string.
 *
 *  Subtracted as scaled integers rather than as floats: a line for 0.75 metres
 *  of fabric, half picked, must not report 0.37499999999999994 remaining on a
 *  slip somebody works from. */
export function outstanding(line: OrderLine): string {
  const want = scaled(line.qty);
  const got = scaled(line.qty_picked);
  if (want === null || got === null) return line.qty;
  return unscaled(want - got);
}

const SCALE = 4;

export function scaled(qty: string): bigint | null {
  const s = (qty ?? '').trim();
  if (!/^-?\d*(\.\d*)?$/.test(s) || s === '' || s === '-' || s === '.') {
    return null;
  }
  const negative = s.startsWith('-');
  const [whole = '0', fraction = ''] = (negative ? s.slice(1) : s).split('.');
  const value =
    BigInt(whole || '0') * BigInt(10 ** SCALE) +
    BigInt((fraction + '0'.repeat(SCALE)).slice(0, SCALE) || '0');
  return negative ? -value : value;
}

export function unscaled(value: bigint): string {
  const negative = value < 0n;
  const abs = negative ? -value : value;
  const unit = BigInt(10 ** SCALE);
  const whole = abs / unit;
  const fraction = (abs % unit).toString().padStart(SCALE, '0').replace(/0+$/, '');
  const text = fraction ? `${whole}.${fraction}` : whole.toString();
  return negative ? '-' + text : text;
}

export function isZero(qty: string): boolean {
  const v = scaled(qty);
  return v === null || v === 0n;
}

/** Whether an order still needs somebody to do something.
 *
 *  Used to decide what the working list shows by default. A shop with four
 *  hundred completed orders opening the screen wants the six that are waiting. */
export function needsAttention(order: Order): boolean {
  return order.state !== 'completed' && order.state !== 'cancelled';
}

/** A line being typed. Every value is a string, exactly as it came off the
 *  keyboard and exactly as it will be sent. */
export interface DraftLine {
  variantId: string;
  description: string;
  qty: string;
  unitPrice: string;
  discount: string;
}

export interface Totals {
  subtotal: string;
  discount: string;
  total: string;
}

/** What the quotation being typed comes to, for the person typing it.
 *
 *  The server recomputes all three from the lines and is the authority. This
 *  exists so somebody quoting a customer over the phone can read a number off
 *  the screen, and it is therefore done in minor units: a preview that
 *  disagreed with the saved quotation by a hallala would be worse than no
 *  preview, because it would teach them the figures here are approximate. */
export function previewTotals(lines: DraftLine[]): Totals {
  let subtotal = 0n;
  let discount = 0n;
  for (const line of lines) {
    subtotal += lineSubtotal(line);
    discount += toMinor(line.discount);
  }
  return {
    subtotal: toMajor(subtotal),
    discount: toMajor(discount),
    total: toMajor(subtotal - discount),
  };
}

/** One line, priced and discounted, as the server will state it. */
export function lineTotal(line: DraftLine): string {
  return toMajor(lineSubtotal(line) - toMinor(line.discount));
}

function lineSubtotal(line: DraftLine): bigint {
  const qty = scaled(line.qty);
  if (qty === null) return 0n;
  const price = toMinor(line.unitPrice);
  const unit = BigInt(10 ** SCALE);
  const product = qty * price;
  // Half up, matching the server's decimal rounding rather than JavaScript's
  // treatment of a negative through Math.round.
  const rounded =
    (product + (product < 0n ? -unit / 2n : unit / 2n)) / unit;
  return rounded;
}

function toMinor(amount: string): bigint {
  const trimmed = (amount ?? '').trim();
  if (trimmed === '' || !/^-?\d*(\.\d*)?$/.test(trimmed) || !/\d/.test(trimmed)) {
    return 0n;
  }
  const negative = trimmed.startsWith('-');
  const [whole = '0', frac = ''] = trimmed.replace('-', '').split('.');
  const cents = BigInt(whole || '0') * 100n + BigInt((frac + '00').slice(0, 2) || '0');
  return negative ? -cents : cents;
}

function toMajor(cents: bigint): string {
  const negative = cents < 0n;
  const abs = negative ? -cents : cents;
  return `${negative ? '-' : ''}${abs / 100n}.${String(abs % 100n).padStart(2, '0')}`;
}

/** Whether the quotation is worth sending. */
export function readyToRaise(lines: DraftLine[]): boolean {
  return lines.some((l) => l.variantId !== '' && (scaled(l.qty) ?? 0n) > 0n);
}
