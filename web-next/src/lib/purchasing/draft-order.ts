// What a draft purchase order comes to, before the server has seen it.
//
// # Net only, and deliberately
//
// The tax is not computed here, because this screen does not know the rate and
// should not guess it. `CreateOrder` resolves it from the regulatory register
// at the order date — the same rule the sale path and the expenses path follow,
// and for the reason the expenses service states outright: a client that could
// state its own VAT rate could state what the return claims.
//
// So the buyer picks a TREATMENT, which is a fact they know — did this supplier
// charge, or is it zero-rated, or exempt — and the server says how much. The
// summary on the screen says "before tax" and means it; the full breakdown is
// on the order the moment it is raised.
//
// Showing an estimated tax here would be worse than showing none: it would be
// a number the buyer reads, remembers, and then finds different on the order.
//
// # Four decimals, and no floats
//
// `po_line` stores four and the order comes back with four; rounding to two
// here would show a buyer one number and send them another. decimal.js because
// `0.1 + 0.2` is not `0.3` in binary floating point, and a purchase order is a
// document a supplier can hold you to.

import Decimal from 'decimal.js';

export interface Draft {
  variant_id: string;
  description: string;
  /** As typed. Empty is not zero -- it is "not said yet". */
  qty: string;
  unit_cost: string;
  /**
   * How this line is taxed: `standard`, `zero_rated`, `exempt`.
   *
   * The caller's to state, because only they know whether the supplier
   * charged. Checked against what the country allows on the order date, and
   * priced from the register — a treatment the country does not use is
   * refused, naming the country.
   */
  tax_treatment: string;
}

export interface Totals {
  net: string;
}

/** A number a person is part-way through typing is zero, not NaN. */
function num(value: string): Decimal {
  if (value.trim() === '') return new Decimal(0);
  try {
    return new Decimal(value);
  } catch {
    // "1." and "-" occur on the way to a real number; treating them as zero
    // keeps the total from flickering to NaN under somebody's hands.
    return new Decimal(0);
  }
}

export function lineNet(line: Draft): string {
  return num(line.qty).times(num(line.unit_cost)).toDecimalPlaces(4).toFixed(4);
}

export function orderNet(lines: readonly Draft[]): string {
  // Summed from the ROUNDED lines, because that is the order CreateOrder does
  // it in: each line is rounded and stored, and the header is the sum of what
  // was stored. Rounding once at the end would give a total that disagrees
  // with the lines printed under it.
  let net = new Decimal(0);
  for (const line of lines) net = net.plus(lineNet(line));
  return net.toFixed(4);
}
