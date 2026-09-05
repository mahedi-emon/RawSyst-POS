// Sending goods back to a supplier.
//
// # Two figures that are not the same figure
//
// The supplier is claimed the price they BILLED: the bill's unit cost at the
// bill's tax rate. That is their document and it is what they will argue with.
//
// The stock leaves at what the VALUATION says those units were worth, which is
// the costing method's answer and is often different — the shop paid freight on
// the delivery, or has bought the same item since at another price, or is on
// FIFO and the oldest layer is cheaper. The difference is a real gain or loss
// and the server books it to cost variance.
//
// So this file computes ONE of the two: the claim, which is arithmetic on the
// bill's own numbers and which somebody filling the form needs to see as they
// type. The other is the server's and is shown when it answers.
//
// # What may go back is the server's answer, always
//
// `qty_returnable` is cumulative across every earlier return, and those are
// rows a browser may never have seen. A screen that subtracted for itself would
// eventually claim the same pallet twice.

import Decimal from 'decimal.js';

/** One bill line and what is left of it. */
export interface Returnable {
  bill_line_id: string;
  line_no: number;
  description: string;
  variant_id?: string;
  qty_billed: string;
  qty_returned: string;
  qty_returnable: string;
  unit_cost: string;
  tax_treatment: string;
  /** A FRACTION, as everywhere: "0.150000" is fifteen per cent. */
  tax_rate: string;
}

export interface PurchaseReturnLine {
  bill_line_id: string;
  variant_id?: string;
  line_no: number;
  description: string;
  qty: string;
  unit_cost: string;
  tax_treatment: string;
  tax_rate: string;
  net_amount: string;
  tax_amount: string;
  gross_amount: string;
  /** What these units were carrying in stock. Not the same as `net_amount`. */
  stock_value: string;
}

export interface PurchaseReturn {
  id: string;
  return_no: string;
  bill_id: string;
  bill_ref?: string;
  supplier: string;
  warehouse?: string;
  returned_on: string;
  reason: string;
  currency: string;
  subtotal_net: string;
  tax_total: string;
  total_inclusive: string;
  stock_value: string;
  /** The claim less what the stock was carrying, signed. */
  variance: string;
  created_by?: string;
  lines?: PurchaseReturnLine[];
  already_returned: boolean;
}

/** A number somebody is part-way through typing is not a number yet. */
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

export interface Claim {
  net: string;
  tax: string;
  total: string;
  /** How many lines carry a quantity. Zero means there is nothing to send. */
  lines: number;
}

/**
 * What the supplier will be claimed, at their own prices.
 *
 * Rounded per line before summing, the way the server does it: rounding the
 * total instead would differ by a unit on a long claim, and the figure on the
 * screen has to be the figure on the debit note.
 */
export function claimFor(
  lines: readonly Returnable[],
  chosen: Readonly<Record<string, string>>,
  places = 2,
): Claim {
  let net = new Decimal(0);
  let tax = new Decimal(0);
  let count = 0;

  for (const line of lines) {
    const qty = decimalOf(chosen[line.bill_line_id]);
    if (!qty.greaterThan(0)) continue;
    count += 1;
    const lineNet = qty.times(decimalOf(line.unit_cost)).toDecimalPlaces(4);
    const lineTax = lineNet.times(decimalOf(line.tax_rate)).toDecimalPlaces(4);
    net = net.plus(lineNet);
    tax = tax.plus(lineTax);
  }

  return {
    net: net.toDecimalPlaces(places, Decimal.ROUND_HALF_UP).toFixed(places),
    tax: tax.toDecimalPlaces(places, Decimal.ROUND_HALF_UP).toFixed(places),
    total: net
      .plus(tax)
      .toDecimalPlaces(places, Decimal.ROUND_HALF_UP)
      .toFixed(places),
    lines: count,
  };
}

/** Whether a quantity is more than the server says may go back. */
export function overReturnable(line: Returnable, entered: string): boolean {
  return decimalOf(entered).greaterThan(decimalOf(line.qty_returnable));
}

/** The lines to send, from the quantities somebody typed. */
export function claimLines(
  chosen: Readonly<Record<string, string>>,
): { bill_line_id: string; qty: string }[] {
  return Object.entries(chosen)
    .filter(([, qty]) => decimalOf(qty).greaterThan(0))
    .map(([bill_line_id, qty]) => ({ bill_line_id, qty }));
}

/** Whether a line still has anything left to send back. */
export function anythingLeft(line: Returnable): boolean {
  return decimalOf(line.qty_returnable).greaterThan(0);
}

export type Blocked =
  | { ok: true }
  | { ok: false; reason: 'nothing_chosen' | 'too_many' | 'no_reason' };

/**
 * Whether the claim can be sent.
 *
 * The same three refusals the server gives, said before the round trip. The
 * reason matters most: the server asks for one because an unexplained return
 * is how the value of a pallet goes missing between a clerk and a driver, and
 * finding that out after typing a page of quantities is a page retyped.
 */
export function readiness(
  lines: readonly Returnable[],
  chosen: Readonly<Record<string, string>>,
  reason: string,
): Blocked {
  if (claimFor(lines, chosen).lines === 0) {
    return { ok: false, reason: 'nothing_chosen' };
  }
  if (lines.some((l) => overReturnable(l, chosen[l.bill_line_id] ?? ''))) {
    return { ok: false, reason: 'too_many' };
  }
  if (reason.trim().length < 3) return { ok: false, reason: 'no_reason' };
  return { ok: true };
}
