// Swapping goods: what comes back, what goes out, and who owes the difference.
//
// # An exchange is a return and a sale, not a third kind of document
//
// The service is explicit: "There is no 'exchange document' because inventing
// one would mean inventing its treatment, and E1 does not have one to invent."
// A credit note against the original invoice, and a new invoice for the goods
// leaving the shop, in one transaction — because either alone leaves the books
// wrong.
//
// # Only the difference moves real money
//
// "A customer swapping a 100 item for a 150 one hands over 50. They do not hand
// over 150 and receive 100 back, and the books must not say they did: the
// drawer would then be expected to hold cash that never moved through it, and
// the blind Z-count at close would show a variance with no cause."
//
// So the offsetting portion goes through a clearing account and never appears
// as a tender. What the cashier collects, or hands back, is the difference and
// nothing else — and the server states that figure and requires it EXACTLY:
// "an overpayment is change owed, and treating it as part of the sale
// overstates takings and the VAT on them."
//
// # This file's figures are the till's estimate, and the server's are the truth
//
// The credit is worked out here pro-rata from what `returnable` reports, which
// matches the server for a whole line and for any line without an allocated
// discount. A partial return of a discounted line can differ by a rounding
// unit, and when it does the server refuses with the exact amount it settles
// at. The screen shows that sentence rather than arguing with it: a till that
// insisted on its own figure would be asserting what an exchange settles for,
// which is precisely the authority it does not have.

import { Decimal } from 'decimal.js';

import type { CartLine } from './cart';
import { lineNet } from './cart';

/** One line of the original invoice, as `GET /pos/sales/{id}/returnable` says. */
export interface ReturnableLine {
  line_id: string;
  line_no: number;
  variant_id?: string;
  description: string;
  qty_sold: string;
  qty_returned: string;
  qty_returnable: string;
  unit_price: string;
  tax_treatment: string;
  /** Tax-inclusive, for the whole returnable quantity. */
  gross_returnable: string;
}

/**
 * A number a person is part-way through typing is not a number yet.
 *
 * Every comparison below asks `greaterThan(0)` rather than `isPositive()`.
 * decimal.js reads the SIGN, and zero's sign is positive -- `new Decimal(0)
 * .isPositive()` is `true`. Written the obvious way, an empty quantity box
 * counted as a line to return, a returnable quantity of nothing was divided
 * into a total and produced NaN on the screen, and "there is nothing coming
 * back, so this is a sale rather than an exchange" never fired at all.
 */
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

/**
 * What the chosen quantities are worth, at the invoice's own prices.
 *
 * Pro-rata on each line's returnable gross, so the tax and any discount already
 * allocated to it come back in the same proportion. Tax-inclusive, because that
 * is what the settlement is measured in.
 */
export function creditFor(
  lines: readonly ReturnableLine[],
  chosen: Readonly<Record<string, string>>,
  places = 2,
): string {
  let total = new Decimal(0);
  for (const line of lines) {
    const returning = decimalOf(chosen[line.line_id]);
    if (!returning.greaterThan(0)) continue;
    const returnable = decimalOf(line.qty_returnable);
    if (!returnable.greaterThan(0)) continue;
    const per = decimalOf(line.gross_returnable).dividedBy(returnable);
    total = total.plus(per.times(returning));
  }
  return total.toDecimalPlaces(places, Decimal.ROUND_HALF_UP).toFixed(places);
}

/**
 * What the replacement comes to.
 *
 * Prices are tax-INCLUSIVE, which is why this is comparable with the credit
 * without the till knowing a single tax rate. The server splits the total into
 * net and tax on the way back; the till never does.
 */
export function replacementTotal(
  lines: readonly CartLine[],
  places = 2,
): string {
  let total = new Decimal(0);
  for (const line of lines) total = total.plus(lineNet(line));
  return Decimal.max(total, new Decimal(0))
    .toDecimalPlaces(places, Decimal.ROUND_HALF_UP)
    .toFixed(places);
}

/** Which way the money goes, and how much of it. */
export type Direction = 'customer_pays' | 'shop_pays' | 'even';

export interface Settlement {
  direction: Direction;
  /** Always positive, or "0.00". What actually changes hands. */
  amount: string;
  /** Signed: positive when the replacement costs more. */
  difference: string;
  /** The offsetting portion, which moves through the clearing account. */
  offset: string;
}

export function settlementOf(
  credit: string,
  replacement: string,
  places = 2,
): Settlement {
  const back = decimalOf(credit);
  const out = decimalOf(replacement);
  const difference = out.minus(back).toDecimalPlaces(places, Decimal.ROUND_HALF_UP);
  const offset = Decimal.min(back, out).toDecimalPlaces(places, Decimal.ROUND_HALF_UP);

  return {
    direction: difference.isZero()
      ? 'even'
      : difference.greaterThan(0)
        ? 'customer_pays'
        : 'shop_pays',
    amount: difference.abs().toFixed(places),
    difference: difference.toFixed(places),
    offset: offset.toFixed(places),
  };
}

/**
 * Whether an exchange can be made at all.
 *
 * The same two refusals the server gives, said before the round trip rather
 * than after it: "There is nothing coming back, so this is a sale rather than
 * an exchange" and "There is nothing going out, so this is a return rather than
 * an exchange." Both are true statements about what the person should do
 * instead, which is why they are worth repeating here.
 */
export type Readiness =
  | { ok: true }
  | { ok: false; reason: 'nothing_back' | 'nothing_out' | 'no_reason' | 'settlement' };

export function readiness(
  credit: string,
  replacement: string,
  reason: string,
  offered: readonly { amount: string }[],
): Readiness {
  if (!decimalOf(credit).greaterThan(0)) return { ok: false, reason: 'nothing_back' };
  if (!decimalOf(replacement).greaterThan(0)) return { ok: false, reason: 'nothing_out' };
  // C14 requires a reason on every return, and an exchange contains one:
  // "Unexplained returns are how refund fraud is concealed."
  if (reason.trim().length < 3) return { ok: false, reason: 'no_reason' };
  if (!settles(offered, settlementOf(credit, replacement))) {
    return { ok: false, reason: 'settlement' };
  }
  return { ok: true };
}

/**
 * Whether what has been offered meets the difference exactly.
 *
 * Exactly, not "at least". The server says why an overpayment cannot simply be
 * absorbed, and an even swap takes no tender at all.
 */
export function settles(
  offered: readonly { amount: string }[],
  settlement: Settlement,
): boolean {
  let total = new Decimal(0);
  for (const tender of offered) total = total.plus(decimalOf(tender.amount));
  return total.equals(new Decimal(settlement.amount));
}

/** The lines to send, from the quantities somebody typed. */
export function returningLines(
  chosen: Readonly<Record<string, string>>,
): { line_id: string; qty: string }[] {
  return Object.entries(chosen)
    .filter(([, qty]) => decimalOf(qty).greaterThan(0))
    .map(([line_id, qty]) => ({ line_id, qty }));
}

/** What a quantity box may not exceed. The till never works this out itself. */
export function overReturnable(line: ReturnableLine, entered: string): boolean {
  return decimalOf(entered).greaterThan(decimalOf(line.qty_returnable));
}
