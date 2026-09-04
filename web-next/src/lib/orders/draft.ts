// What a quotation comes to before the server has seen it.
//
// No tax. An order carries none until it is invoiced, and the rate is read from
// the regulatory register at THAT date rather than at this one — a quotation
// raised in March and invoiced in April is taxed in April. Showing an estimate
// here would be a number the customer reads on a quotation and does not find on
// their invoice.
//
// decimal.js, because a quotation is a price somebody is being held to.

import Decimal from 'decimal.js';

export interface OrderDraftLine {
  variant_id: string;
  description: string;
  qty: string;
  unit_price: string;
  /** An absolute amount off this line, not a percentage. */
  discount: string;
}

export interface OrderTotals {
  subtotal: string;
  discount: string;
  total: string;
}

function num(value: string): Decimal {
  if (value.trim() === '') return new Decimal(0);
  try {
    return new Decimal(value);
  } catch {
    return new Decimal(0);
  }
}

export function orderTotals(lines: readonly OrderDraftLine[]): OrderTotals {
  let subtotal = new Decimal(0);
  let discount = new Decimal(0);

  for (const line of lines) {
    // Rounded per line and then summed, which is the order the server does it
    // in: a total that disagrees with the lines printed under it is the
    // complaint a customer is always right to make.
    const gross = num(line.qty).times(num(line.unit_price)).toDecimalPlaces(4);
    const off = num(line.discount).toDecimalPlaces(4);
    subtotal = subtotal.plus(gross);
    discount = discount.plus(off);
  }

  return {
    subtotal: subtotal.toFixed(2),
    discount: discount.toFixed(2),
    total: subtotal.minus(discount).toFixed(2),
  };
}
