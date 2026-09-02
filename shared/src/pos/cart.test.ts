// Cart arithmetic.
//
// The till's figures must match what the server computes, because the customer
// is handed a receipt printed from one and an invoice recorded from the other.
// These cases are the ones where a naive implementation drifts.

import { describe, expect, it } from 'vitest';

import {
  emptyLine,
  outstanding,
  settled,
  totalCart,
  type CartLine,
} from './cart';

const RATE = '0.15';

function line(over: Partial<CartLine> = {}): CartLine {
  return {
    ...emptyLine({
      variantId: 'v1',
      sku: 'ABAYA-BLK-L',
      description: 'Abaya',
      unitPrice: '115.00',
      taxTreatment: 'standard',
    }),
    ...over,
  };
}

describe('VAT-inclusive pricing', () => {
  it('back-calculates the net from the shelf price', () => {
    // Saudi retail prices include tax. Adding tax on top would show a total a
    // penny away from the shelf price, which a customer notices.
    const t = totalCart([line()], RATE);
    expect(t.subtotalNet).toBe('100.00');
    expect(t.taxTotal).toBe('15.00');
    expect(t.totalInclusive).toBe('115.00');
  });

  it('keeps the total equal to the shelf price across quantities', () => {
    const t = totalCart([line({ qty: '3' })], RATE);
    expect(t.totalInclusive).toBe('345.00');
    expect(t.subtotalNet).toBe('300.00');
  });

  it('handles a price whose net does not divide cleanly', () => {
    // 1000.01 inclusive is 869.5739… net. Rounded per line, as the server does.
    const t = totalCart([line({ unitPrice: '1000.01' })], RATE);
    expect(t.subtotalNet).toBe('869.57');
    expect(t.taxTotal).toBe('130.44');
    expect(t.totalInclusive).toBe('1000.01');
  });

  it('rounds per line rather than on the total', () => {
    // Three lines that each round. Totalling first and rounding once would
    // give a different answer from the server, which rounds per line.
    const lines = [
      line({ unitPrice: '1000.01' }),
      line({ unitPrice: '1000.01' }),
      line({ unitPrice: '1000.01' }),
    ];
    const t = totalCart(lines, RATE);
    expect(t.subtotalNet).toBe('2608.71');
    expect(t.totalInclusive).toBe('3000.03');
  });
});

describe('tax treatments', () => {
  it('gives a zero-rated line a value but no tax', () => {
    // Different from exempt, and different again from a line worth nothing.
    const t = totalCart([line({ taxTreatment: 'zero_rated', unitPrice: '50.00' })], RATE);
    expect(t.subtotalNet).toBe('50.00');
    expect(t.taxTotal).toBe('0.00');
  });

  it('mixes treatments on one sale', () => {
    const t = totalCart(
      [line(), line({ taxTreatment: 'zero_rated', unitPrice: '50.00' })],
      RATE,
    );
    expect(t.subtotalNet).toBe('150.00');
    expect(t.taxTotal).toBe('15.00');
    expect(t.totalInclusive).toBe('165.00');
  });
});

describe('discounts', () => {
  it('reduces the taxable amount rather than being taken off afterwards', () => {
    // Tax is charged on what the customer actually pays. Discounting after tax
    // would overstate the VAT owed on the sale.
    const t = totalCart([line({ lineDiscount: '15.00' })], RATE);
    expect(t.totalInclusive).toBe('100.00');
    expect(t.taxTotal).toBe('13.04');
  });
});

describe('settlement', () => {
  it('is settled only when the payments match exactly', () => {
    const tenders = [{ method: 'cash', amount: '115.00' }];
    expect(settled('115.00', tenders)).toBe(true);
    expect(outstanding('115.00', tenders)).toBe('0.00');
  });

  it('refuses an overpayment as unsettled', () => {
    // Change owed is a tender of its own. Treating an overpayment as revenue
    // overstates takings and the VAT on them, and the server refuses it too.
    expect(settled('115.00', [{ method: 'cash', amount: '120.00' }])).toBe(false);
    expect(outstanding('115.00', [{ method: 'cash', amount: '120.00' }])).toBe('-5.00');
  });

  it('adds up a split payment', () => {
    const tenders = [
      { method: 'cash', amount: '200.00' },
      { method: 'mada', amount: '300.00' },
      { method: 'tabby', amount: '500.00' },
    ];
    expect(settled('1000.00', tenders)).toBe(true);
  });

  it('shows what is still owed part-way through a split', () => {
    expect(outstanding('1000.00', [{ method: 'cash', amount: '200.00' }])).toBe(
      '800.00',
    );
  });
});

describe('precision', () => {
  it('does not drift across many lines', () => {
    // The case a float64 implementation fails: 0.15 has no exact binary
    // representation, so repeated addition accumulates error.
    const lines = Array.from({ length: 100 }, () => line({ unitPrice: '0.07' }));
    const t = totalCart(lines, RATE);
    expect(t.totalInclusive).toBe('7.00');
  });
});
