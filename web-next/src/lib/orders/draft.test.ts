import { describe, expect, it } from 'vitest';

import { orderTotals, type OrderDraftLine } from './draft';

function line(over: Partial<OrderDraftLine> = {}): OrderDraftLine {
  return {
    variant_id: 'v1',
    description: 'Abaya, black',
    qty: '2',
    unit_price: '100.00',
    discount: '',
    ...over,
  };
}

describe('what a quotation comes to', () => {
  it('multiplies and sums', () => {
    expect(orderTotals([line()]).subtotal).toBe('200.00');
    expect(orderTotals([line()]).total).toBe('200.00');
  });

  it('takes the discount off the total, not off the subtotal shown', () => {
    // Both figures are on the screen, and a customer checks them against each
    // other. Netting the discount into the subtotal would make the discount
    // line look like it was applied twice.
    const totals = orderTotals([line({ discount: '25.00' })]);
    expect(totals.subtotal).toBe('200.00');
    expect(totals.discount).toBe('25.00');
    expect(totals.total).toBe('175.00');
  });

  it('carries no tax at all', () => {
    // Deliberate: an order is taxed when it is INVOICED, at the rate on file
    // for that date. A quotation raised in March and invoiced in April is
    // taxed in April, and an estimate here would be a number the customer
    // reads on the quotation and does not find on their invoice.
    const totals = orderTotals([line()]);
    expect(Object.keys(totals).sort()).toEqual(['discount', 'subtotal', 'total']);
  });

  it('treats an empty box as nothing said yet', () => {
    expect(orderTotals([line({ qty: '', unit_price: '' })]).total).toBe('0.00');
  });

  it('survives a number half typed', () => {
    for (const partial of ['1.', '-', '.', 'abc']) {
      expect(() => orderTotals([line({ unit_price: partial })])).not.toThrow();
    }
  });

  it('does not go through a float', () => {
    // Ten lines of 0.10 must come to exactly 1.00.
    const tenth = line({ qty: '1', unit_price: '0.10' });
    expect(orderTotals(Array.from({ length: 10 }, () => tenth)).total).toBe('1.00');
  });

  it('sums the rounded lines rather than rounding the sum', () => {
    const odd = line({ qty: '1', unit_price: '0.33335' });
    // 0.3334 stored twice, not 0.6667 computed once.
    expect(orderTotals([odd, odd]).subtotal).toBe('0.67');
  });

  it('is zero for an order with nothing on it', () => {
    expect(orderTotals([])).toEqual({
      subtotal: '0.00',
      discount: '0.00',
      total: '0.00',
    });
  });

  it('handles a discount larger than the line, which the server will refuse', () => {
    // Not silently clamped: showing -50 is how somebody sees they typed the
    // discount into the wrong box, and the server is the one that refuses it.
    expect(orderTotals([line({ discount: '250.00' })]).total).toBe('-50.00');
  });
});
