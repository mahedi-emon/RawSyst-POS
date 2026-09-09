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
  offersWorthTaking,
  offerTotal,
  quoteBasket,
  withOffers,
  type PricedLine,
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

// ---------------------------------------------------------------------------
// Asking the server what the campaigns would give
// ---------------------------------------------------------------------------

describe('offers the server would give this cart', () => {
  const lines: CartLine[] = [
    {
      variantId: 'v1',
      sku: 'SHIRT-BLK-M',
      description: 'Shirt',
      qty: '2',
      unitPrice: '100.00',
      lineDiscount: '0',
      taxTreatment: 'standard',
    },
    {
      variantId: 'v2',
      sku: 'ABAYA-BLK-L',
      description: 'Abaya',
      qty: '1',
      unitPrice: '300.00',
      lineDiscount: '0',
      taxTreatment: 'standard',
    },
  ];

  it('sends the price on the line, not the catalogue price', () => {
    // A cashier may have overridden it, and a campaign has to be measured
    // against what is actually being charged.
    const basket = quoteBasket(lines, { storeId: 's1' }) as {
      lines: { variant_id: string; unit_price: string }[];
    };
    expect(basket.lines).toEqual([
      { variant_id: 'v1', qty: '2', unit_price: '100.00' },
      { variant_id: 'v2', qty: '1', unit_price: '300.00' },
    ]);
  });

  it('ignores the lines no campaign touched', () => {
    // Telling a cashier that eleven of thirteen lines are unchanged is telling
    // them nothing, with a customer waiting.
    const quoted: PricedLine[] = [
      { variant_id: 'v1', promotion_id: 'p1', promotion: 'Winter', discount: '20.00', line_total: '180.00' },
      { variant_id: 'v2', discount: '0', line_total: '300.00' },
    ];
    expect(offersWorthTaking(lines, quoted).map((o) => o.variant_id)).toEqual(['v1']);
  });

  it('ignores a campaign already on the line', () => {
    // Re-applying the same one would take the discount twice.
    const already = [{ ...lines[0]!, promotionId: 'p1', lineDiscount: '20.00' }, lines[1]!];
    const quoted: PricedLine[] = [
      { variant_id: 'v1', promotion_id: 'p1', discount: '20.00', line_total: '180.00' },
    ];
    expect(offersWorthTaking(already, quoted)).toEqual([]);
  });

  it('ignores a promotion that came to nothing', () => {
    const quoted: PricedLine[] = [
      { variant_id: 'v1', promotion_id: 'p1', discount: '0.00', line_total: '200.00' },
    ];
    expect(offersWorthTaking(lines, quoted)).toEqual([]);
  });

  it('adds the offers up for the sentence above the button', () => {
    const offers: PricedLine[] = [
      { variant_id: 'v1', promotion_id: 'p1', discount: '20.00', line_total: '180.00' },
      { variant_id: 'v2', promotion_id: 'p2', discount: '15.50', line_total: '284.50' },
    ];
    expect(offerTotal(offers)).toBe('35.50');
  });

  it('writes the discount and the campaign onto the line together', () => {
    // The sale records the redemption from the id, so a discount without one
    // would take money off a campaign nobody can show was applied.
    const offers: PricedLine[] = [
      { variant_id: 'v1', promotion_id: 'p1', discount: '20.00', line_total: '180.00' },
    ];
    const after = withOffers(lines, offers);
    expect(after[0]?.lineDiscount).toBe('20.00');
    expect(after[0]?.promotionId).toBe('p1');
    // And leaves the untouched line exactly as it was.
    expect(after[1]).toEqual(lines[1]);
  });
});
