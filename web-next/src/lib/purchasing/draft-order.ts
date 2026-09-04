// What a draft purchase order comes to, before the server has seen it.
//
// The figure on this screen is what somebody decides to commit the shop to, so
// it has to agree with what `CreateOrder` will compute:
//
//   net   = qty x unit_cost, rounded to 4
//   tax   = net x rate,      rounded to 4
//   gross = net + tax
//
// Four decimals, not two, because that is what `po_line` stores and what the
// order comes back with. Rounding to two here would show a buyer one number and
// send them another.
//
// decimal.js throughout, like the cart. `0.1 + 0.2` is not `0.3` in binary
// floating point, and a purchase order is a document a supplier can hold you
// to.

import Decimal from 'decimal.js';

export interface Draft {
  variant_id: string;
  description: string;
  /** As typed. Empty is not zero -- it is "not said yet". */
  qty: string;
  unit_cost: string;
  /**
   * As typed, in PER CENT.
   *
   * The API takes a fraction and the screen converts on the way out. Held as a
   * percentage here because that is what a buyer reads off a supplier's
   * invoice, and because 15 sent as a rate meant 1500% until the boundary
   * started refusing it.
   */
  tax_percent: string;
}

export interface Totals {
  net: string;
  tax: string;
  gross: string;
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

export function lineTotals(line: Draft): Totals {
  const net = num(line.qty).times(num(line.unit_cost)).toDecimalPlaces(4);
  // Per cent in, fraction out -- the same conversion the screen sends.
  const rate = num(line.tax_percent).dividedBy(100);
  const tax = net.times(rate).toDecimalPlaces(4);
  return {
    net: net.toFixed(4),
    tax: tax.toFixed(4),
    gross: net.plus(tax).toFixed(4),
  };
}

export function orderTotals(lines: readonly Draft[]): Totals {
  // Summed from the ROUNDED lines, because that is the order CreateOrder does
  // it in: each line is rounded and stored, and the header is the sum of what
  // was stored. Rounding once at the end would give a total that disagrees
  // with the lines printed under it.
  let net = new Decimal(0);
  let tax = new Decimal(0);
  for (const line of lines) {
    const sums = lineTotals(line);
    net = net.plus(sums.net);
    tax = tax.plus(sums.tax);
  }
  return {
    net: net.toFixed(4),
    tax: tax.toFixed(4),
    gross: net.plus(tax).toFixed(4),
  };
}
