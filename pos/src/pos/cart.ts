// The cart, and its arithmetic.
//
// # Money is a string here too
//
// Every amount below is a decimal string handled by decimal.js, never a
// JavaScript number. The reason is the same one the server has: float64 cannot
// represent 0.15 exactly, and a till that computed a total in floats would
// eventually print a receipt that disagreed with the invoice by a hallala. The
// customer holds the receipt.
//
// # The server is still the authority
//
// These figures exist so a cashier can see a running total and take payment.
// The invoice's real numbers come back from the server, which recomputes
// everything from the registry's VAT rate at the transaction date. Where the
// two disagree, the server is right and the screen updates — the till never
// argues.

import Decimal from 'decimal.js';

// Two decimals, half-up, matching the server's moneyScale and ZATCA's rounding.
Decimal.set({ rounding: Decimal.ROUND_HALF_UP });

export interface CartLine {
  variantId: string;
  sku: string;
  description: string;
  /** Decimal strings throughout. */
  unitPrice: string;
  qty: string;
  lineDiscount: string;
  taxTreatment: string;
}

export interface CartTotals {
  subtotalNet: string;
  taxTotal: string;
  discountTotal: string;
  totalInclusive: string;
}

/** A tender the cashier has taken. */
export interface CartTender {
  method: string;
  amount: string;
  reference?: string;
}

export function emptyLine(v: {
  variantId: string;
  sku: string;
  description: string;
  unitPrice: string;
  taxTreatment: string;
}): CartLine {
  return { ...v, qty: '1', lineDiscount: '0' };
}

/**
 * Totals the cart, VAT-inclusive.
 *
 * Saudi retail prices include tax, so the net is back-calculated rather than
 * the tax being added on. Doing it the other way round would show a total a
 * penny away from the shelf price, which is the sort of difference a customer
 * notices and a cashier cannot explain.
 */
export function totalCart(lines: CartLine[], taxRate: string): CartTotals {
  const rate = new Decimal(taxRate);

  let net = new Decimal(0);
  let tax = new Decimal(0);
  let discount = new Decimal(0);

  for (const line of lines) {
    const gross = new Decimal(line.unitPrice)
      .times(line.qty)
      .minus(line.lineDiscount);

    // Per line, rounded per line — the same order the server uses, so the two
    // arrive at the same figure rather than differing by accumulated rounding.
    const lineNet = taxable(line)
      ? gross.dividedBy(rate.plus(1)).toDecimalPlaces(2)
      : gross.toDecimalPlaces(2);
    const lineTax = gross.toDecimalPlaces(2).minus(lineNet);

    net = net.plus(lineNet);
    tax = tax.plus(lineTax);
    discount = discount.plus(line.lineDiscount);
  }

  return {
    subtotalNet: net.toFixed(2),
    taxTotal: tax.toFixed(2),
    discountTotal: discount.toFixed(2),
    totalInclusive: net.plus(tax).toFixed(2),
  };
}

/** Zero-rated and exempt lines carry a value but no tax. */
function taxable(line: CartLine): boolean {
  return line.taxTreatment === 'standard';
}

/** What is still owed after the tenders taken so far. */
export function outstanding(total: string, tenders: CartTender[]): string {
  const paid = tenders.reduce(
    (sum, t) => sum.plus(new Decimal(t.amount)),
    new Decimal(0),
  );
  return new Decimal(total).minus(paid).toFixed(2);
}

/**
 * Whether the tenders settle the sale exactly.
 *
 * Exactly, not "at least". An overpayment is change owed, which is a tender of
 * its own — treating it as revenue overstates takings and the VAT on them, and
 * the server refuses the sale on the same grounds.
 */
export function settled(total: string, tenders: CartTender[]): boolean {
  return new Decimal(outstanding(total, tenders)).isZero();
}
