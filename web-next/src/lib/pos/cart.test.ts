import { describe, expect, it } from 'vitest';

import {
  addScanned,
  balanceOf,
  exceedsStock,
  lineNet,
  removeLine,
  setQty,
  tenderedTotal,
  totalsFor,
  type CartLine,
} from './cart';

const line = (over: Partial<CartLine> = {}): CartLine => ({
  variantId: 'v1',
  sku: 'M-ABY-BLK-L',
  description: 'Abaya, black, large',
  qty: '1',
  unitPrice: '449.00',
  lineDiscount: '0',
  taxTreatment: 'standard',
  ...over,
});

describe('the till adds up the way the invoice does', () => {
  it('does not accumulate float error across many lines', () => {
    // Ten lines of 0.10 and 0.20 is 3.00. Added as float64 it is
    // 2.9999999999999996, and a till that showed that would be handing over an
    // invoice it disagrees with.
    const lines = Array.from({ length: 10 }, (_, i) =>
      line({ variantId: `v${i}`, unitPrice: i % 2 === 0 ? '0.10' : '0.20' }),
    );
    expect(totalsFor(lines, '0').net).toBe('1.50');
  });

  it('multiplies quantity by price exactly', () => {
    expect(lineNet(line({ qty: '3', unitPrice: '33.33' })).toString()).toBe('99.99');
  });

  it('sums gross, discount and net so they reconcile', () => {
    const t = totalsFor(
      [
        line({ variantId: 'a', qty: '2', unitPrice: '100.00', lineDiscount: '10.00' }),
        line({ variantId: 'b', qty: '1', unitPrice: '50.00' }),
      ],
      '5.00',
    );
    expect(t.gross).toBe('250.00');
    expect(t.discount).toBe('15.00');
    expect(t.net).toBe('235.00');
    expect(t.units).toBe('3');
    expect(t.lineCount).toBe(2);
  });

  it('never shows a negative total while a discount is being typed', () => {
    // Mid-keystroke a cashier can have typed 5000 into a discount box on a
    // 50.00 sale. The eventual refusal is correct; a negative total on the
    // customer-facing figure on the way there is not.
    const t = totalsFor([line({ unitPrice: '50.00' })], '5000');
    expect(t.net).toBe('0.00');
  });

  it('honours a currency with three decimal places', () => {
    const t = totalsFor([line({ qty: '3', unitPrice: '1.234' })], '0', 3);
    expect(t.net).toBe('3.702');
  });
});

describe('tendering', () => {
  const tenders = [{ amount: '100.00' }, { amount: '50.50' }];

  it('adds tenders exactly', () => {
    expect(tenderedTotal(tenders)).toBe('150.50');
  });

  it('reports what is left to pay as positive', () => {
    expect(balanceOf('235.00', tenders)).toBe('84.50');
  });

  it('reports change as negative, leaving the wording to the screen', () => {
    expect(balanceOf('100.00', tenders)).toBe('-50.50');
  });

  it('reports an exact tender as zero', () => {
    expect(balanceOf('150.50', tenders)).toBe('0.00');
  });
});

describe('scanning', () => {
  it('merges a repeat scan into a quantity rather than a second line', () => {
    let lines = addScanned([], { variantId: 'v1', sku: 'A', description: 'A', unitPrice: '10.00', taxTreatment: 'standard' });
    lines = addScanned(lines, { variantId: 'v1', sku: 'A', description: 'A', unitPrice: '10.00', taxTreatment: 'standard' });
    lines = addScanned(lines, { variantId: 'v1', sku: 'A', description: 'A', unitPrice: '10.00', taxTreatment: 'standard' });
    expect(lines).toHaveLength(1);
    expect(lines[0]?.qty).toBe('3');
  });

  it('keeps a promotion line separate', () => {
    // The campaign was quoted for a specific quantity. Growing it silently
    // would redeem something the server never priced.
    const promo = line({ variantId: 'v1', promotionId: 'p1', qty: '1' });
    const lines = addScanned([promo], {
      variantId: 'v1',
      sku: 'A',
      description: 'A',
      unitPrice: '10.00',
      taxTreatment: 'standard',
    });
    expect(lines).toHaveLength(2);
    expect(lines[0]?.promotionId).toBe('p1');
    expect(lines[1]?.promotionId).toBeUndefined();
  });

  it('starts a new line for a different variant', () => {
    let lines = addScanned([], { variantId: 'v1', sku: 'A', description: 'A', unitPrice: '10.00', taxTreatment: 'standard' });
    lines = addScanned(lines, { variantId: 'v2', sku: 'B', description: 'B', unitPrice: '20.00', taxTreatment: 'standard' });
    expect(lines).toHaveLength(2);
  });
});

describe('editing lines', () => {
  it('sets a quantity', () => {
    const lines = setQty([line()], 'v1', '7');
    expect(lines[0]?.qty).toBe('7');
  });

  it('removes a line', () => {
    expect(removeLine([line(), line({ variantId: 'v2' })], 'v1')).toHaveLength(1);
  });
});

describe('stock awareness', () => {
  it('flags a quantity beyond what the shop holds', () => {
    expect(exceedsStock(line({ qty: '5', onHand: '3' }))).toBe(true);
    expect(exceedsStock(line({ qty: '3', onHand: '3' }))).toBe(false);
  });

  it('says nothing when the till does not know the stock', () => {
    // Absent is not zero. A till that has not been told the level must not
    // block a sale by assuming there is none.
    expect(exceedsStock(line({ qty: '5' }))).toBe(false);
  });
});

describe('a line always carries the tax treatment the server demands', () => {
  it('keeps it on a line built by scanning', () => {
    // Live validation: POST /pos/sales answers 400 "Choose a tax treatment for
    // this product." without it, and GET /catalog/scan does not return one --
    // the catalogue snapshot is the only source. A line that reached the tender
    // screen without it would fail at the worst possible moment.
    const lines = addScanned([], {
      variantId: 'v1',
      sku: 'ABAYA-BLK-L',
      description: 'Abaya, Black',
      unitPrice: '125.0000',
      taxTreatment: 'standard',
    });
    expect(lines[0]?.taxTreatment).toBe('standard');
  });

  it('keeps it when a repeat scan merges into an existing line', () => {
    let lines = addScanned([], {
      variantId: 'v1',
      sku: 'A',
      description: 'A',
      unitPrice: '10.00',
      taxTreatment: 'zero_rated',
    });
    lines = addScanned(lines, {
      variantId: 'v1',
      sku: 'A',
      description: 'A',
      unitPrice: '10.00',
      taxTreatment: 'zero_rated',
    });
    expect(lines).toHaveLength(1);
    expect(lines[0]?.qty).toBe('2');
    expect(lines[0]?.taxTreatment).toBe('zero_rated');
  });
});
