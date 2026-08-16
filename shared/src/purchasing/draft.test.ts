// The order form's running total.
//
// It is a preview, and the server is the authority — but a preview that
// disagreed with the saved order by a hallala would be worse than showing none,
// because it teaches the buyer that the numbers on this screen are approximate.
// They are not supposed to be, so this is tested against the cases where a
// float would drift and where the rounding order matters.

import { describe, expect, it } from 'vitest';

import {
  billableQty,
  lineTotals,
  orderTotals,
  readyToSave,
  type DraftLine,
} from './draft';

function line(over: Partial<DraftLine> = {}): DraftLine {
  return {
    variantId: 'v1',
    description: 'Abaya',
    qty: '1',
    unitCost: '100.00',
    taxRate: '0.15',
    ...over,
  };
}

describe('one line', () => {
  it('multiplies and taxes', () => {
    expect(lineTotals(line({ qty: '10', unitCost: '100.00' }))).toEqual({
      net: '1000.00',
      tax: '150.00',
      gross: '1150.00',
    });
  });

  it('never goes through a float', () => {
    // The canonical case: 0.1 * 3 in binary floating point is
    // 0.30000000000000004.
    expect(lineTotals(line({ qty: '3', unitCost: '0.10' })).net).toBe('0.30');
    // And a rate that cannot be represented exactly either.
    expect(lineTotals(line({ qty: '1', unitCost: '0.15' })).net).toBe('0.15');
  });

  it('handles a fractional quantity', () => {
    // Two and a half metres of fabric at 40 is 100.
    expect(lineTotals(line({ qty: '2.5', unitCost: '40.00' })).net).toBe('100.00');
  });

  it('is worth nothing while it is still being typed', () => {
    // Somebody midway through entering "1." must not see the running total
    // blank out or show NaN.
    for (const qty of ['', '.', '-', 'abc']) {
      expect(lineTotals(line({ qty }))).toEqual({
        net: '0.00',
        tax: '0.00',
        gross: '0.00',
      });
    }
  });

  it('treats an empty cost as zero rather than as invalid', () => {
    // A buyer often picks the item before they know the price. The line is
    // real; it is simply worth nothing yet.
    expect(lineTotals(line({ qty: '5', unitCost: '' })).net).toBe('0.00');
  });

  it('applies a zero rate without inventing tax', () => {
    expect(lineTotals(line({ qty: '10', unitCost: '100.00', taxRate: '0' }))).toEqual({
      net: '1000.00',
      tax: '0.00',
      gross: '1000.00',
    });
  });
});

describe('the whole order', () => {
  it('sums the lines', () => {
    expect(
      orderTotals([
        line({ qty: '10', unitCost: '100.00' }),
        line({ qty: '2', unitCost: '50.00' }),
      ]),
    ).toEqual({ net: '1100.00', tax: '165.00', gross: '1265.00' });
  });

  it('rounds each line before summing, as the server does', () => {
    // Three lines whose tax each rounds up. Rounding the SUM instead would
    // give 0.44; the server rounds per line and gets 0.45, and a preview that
    // was more accurate than the thing it has to agree with is still wrong.
    const totals = orderTotals([
      line({ qty: '1', unitCost: '0.99' }),
      line({ qty: '1', unitCost: '0.99' }),
      line({ qty: '1', unitCost: '0.99' }),
    ]);
    expect(totals.net).toBe('2.97');
    expect(totals.tax).toBe('0.45');
    expect(totals.gross).toBe('3.42');
  });

  it('ignores half-typed lines without breaking the total', () => {
    expect(
      orderTotals([
        line({ qty: '10', unitCost: '100.00' }),
        line({ qty: '', unitCost: '' }),
      ]).gross,
    ).toBe('1150.00');
  });

  it('is zero for an empty order', () => {
    expect(orderTotals([])).toEqual({ net: '0.00', tax: '0.00', gross: '0.00' });
  });

  it('survives an amount beyond what a float holds exactly', () => {
    const totals = orderTotals([line({ qty: '1', unitCost: '90071992547409.93' })]);
    expect(totals.net).toBe('90071992547409.93');
  });
});

describe('whether the order is worth saving', () => {
  it('needs a supplier, a destination and one real line', () => {
    expect(readyToSave('s1', 'w1', [line()])).toBe(true);
  });

  it('refuses without a supplier or a warehouse', () => {
    // Both are authorisation-relevant: the supplier is who the shop is
    // committing to, and the warehouse is whose stockroom the goods land in.
    expect(readyToSave('', 'w1', [line()])).toBe(false);
    expect(readyToSave('s1', '', [line()])).toBe(false);
  });

  it('refuses an order of nothing', () => {
    expect(readyToSave('s1', 'w1', [])).toBe(false);
    // A row that exists but has no item chosen does not count.
    expect(readyToSave('s1', 'w1', [line({ variantId: '' })])).toBe(false);
    expect(readyToSave('s1', 'w1', [line({ qty: '0' })])).toBe(false);
  });

  it('accepts an order where only some rows are finished', () => {
    // A buyer adds three rows and fills two. Saving should not be blocked by
    // the blank one, which is dropped on submit.
    expect(
      readyToSave('s1', 'w1', [line(), line({ variantId: '', qty: '' })]),
    ).toBe(true);
  });
});

describe('what to prefill a bill line with', () => {
  it('offers what arrived, not what was ordered', () => {
    // A supplier who delivered five of eight will normally invoice for five.
    // Prefilling eight would have the buyer accept a manufactured discrepancy
    // without reading it.
    expect(billableQty({ qty_received: '5.0000', qty_billed: '0.0000' })).toBe('5');
  });

  it('subtracts what has already been billed', () => {
    // A second invoice against the same delivery covers the remainder only.
    expect(billableQty({ qty_received: '8.0000', qty_billed: '5.0000' })).toBe('3');
  });

  it('offers nothing where nothing has arrived', () => {
    // Billing for goods that have not come is the fraud case the match exists
    // to catch. The form must not put the number there itself.
    expect(billableQty({ qty_received: '0.0000', qty_billed: '0.0000' })).toBe('0');
  });

  it('offers nothing on a line already fully billed', () => {
    expect(billableQty({ qty_received: '5.0000', qty_billed: '5.0000' })).toBe('0');
  });

  it('never offers a negative, however the counts came out', () => {
    // Over-billing already happened, or goods were returned. Either way "-2"
    // is arithmetic rather than an answer to what should be invoiced now.
    expect(billableQty({ qty_received: '3.0000', qty_billed: '5.0000' })).toBe('0');
  });

  it('keeps a genuine fraction and drops the column scale', () => {
    expect(billableQty({ qty_received: '2.5000', qty_billed: '0' })).toBe('2.5');
    expect(billableQty({ qty_received: '120.0000', qty_billed: '0' })).toBe('120');
  });

  it('treats unreadable counts as nothing rather than NaN', () => {
    expect(billableQty({ qty_received: '', qty_billed: '' })).toBe('0');
    expect(billableQty({ qty_received: 'x', qty_billed: '0' })).toBe('0');
  });
});
