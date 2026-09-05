import { describe, expect, it } from 'vitest';

import type { CartLine } from './cart';
import {
  creditFor,
  overReturnable,
  readiness,
  replacementTotal,
  returningLines,
  settlementOf,
  settles,
  type ReturnableLine,
} from './exchange';

function returnable(over: Partial<ReturnableLine> = {}): ReturnableLine {
  return {
    line_id: 'l1',
    line_no: 1,
    description: 'Abaya · Black · L',
    qty_sold: '2',
    qty_returned: '0',
    qty_returnable: '2',
    unit_price: '125.00',
    tax_treatment: 'standard',
    gross_returnable: '250.00',
    ...over,
  };
}

function cartLine(over: Partial<CartLine> = {}): CartLine {
  return {
    variantId: 'v1',
    sku: 'SKU-1',
    description: 'Thobe · White · M',
    qty: '1',
    unitPrice: '150.00',
    lineDiscount: '0',
    taxTreatment: 'standard',
    ...over,
  };
}

describe('what is coming back', () => {
  it('values a whole line at its returnable gross', () => {
    expect(creditFor([returnable()], { l1: '2' })).toBe('250.00');
  });

  it('values part of a line pro-rata', () => {
    // Tax and any allocated discount come back in the same proportion, which
    // is the only split the till can make without knowing a rate.
    expect(creditFor([returnable()], { l1: '1' })).toBe('125.00');
  });

  it('ignores a line nobody chose', () => {
    expect(creditFor([returnable()], {})).toBe('0.00');
    expect(creditFor([returnable()], { l1: '0' })).toBe('0.00');
  });

  it('ignores a quantity somebody is half-way through typing', () => {
    // "1." parses as 1 in decimal.js, so the credit would flicker while
    // somebody typed "1.5" and the difference under it would flicker too.
    expect(creditFor([returnable()], { l1: '1.' })).toBe('0.00');
    expect(creditFor([returnable()], { l1: '-' })).toBe('0.00');
  });

  it('adds several lines', () => {
    const lines = [
      returnable(),
      returnable({ line_id: 'l2', qty_returnable: '1', gross_returnable: '40.00' }),
    ];
    expect(creditFor(lines, { l1: '1', l2: '1' })).toBe('165.00');
  });

  it('never divides by a returnable quantity of nothing', () => {
    const line = returnable({ qty_returnable: '0', gross_returnable: '0.00' });
    expect(creditFor([line], { l1: '1' })).toBe('0.00');
  });

  it('says when a quantity is more than may go back', () => {
    // Capped by what the SERVER reports. How much of a line has already been
    // returned lives in credit notes a till that was offline never saw, and
    // the failure mode is refunding the same jacket twice.
    expect(overReturnable(returnable(), '3')).toBe(true);
    expect(overReturnable(returnable(), '2')).toBe(false);
  });

  it('sends only the lines with a quantity on them', () => {
    expect(returningLines({ l1: '2', l2: '0', l3: '' })).toEqual([
      { line_id: 'l1', qty: '2' },
    ]);
  });
});

describe('what is going out', () => {
  it('totals the replacement at tax-inclusive prices', () => {
    // The till knows no tax rate and needs none: prices include tax, so this
    // is directly comparable with the credit.
    expect(replacementTotal([cartLine({ qty: '2' })])).toBe('300.00');
  });

  it('takes a line discount off', () => {
    expect(replacementTotal([cartLine({ lineDiscount: '25.00' })])).toBe('125.00');
  });

  it('does not go below zero mid-keystroke', () => {
    expect(replacementTotal([cartLine({ lineDiscount: '500.00' })])).toBe('0.00');
  });
});

describe('who owes the difference', () => {
  it('has the customer pay when the replacement costs more', () => {
    const s = settlementOf('125.00', '150.00');
    expect(s.direction).toBe('customer_pays');
    expect(s.amount).toBe('25.00');
    expect(s.difference).toBe('25.00');
    // Only the difference is real money; the rest goes through the clearing
    // account and never touches the drawer.
    expect(s.offset).toBe('125.00');
  });

  it('has the shop hand back when it costs less', () => {
    const s = settlementOf('250.00', '150.00');
    expect(s.direction).toBe('shop_pays');
    expect(s.amount).toBe('100.00');
    expect(s.difference).toBe('-100.00');
    expect(s.offset).toBe('150.00');
  });

  it('takes nothing at all on an even swap', () => {
    const s = settlementOf('150.00', '150.00');
    expect(s.direction).toBe('even');
    expect(s.amount).toBe('0.00');
    expect(s.offset).toBe('150.00');
  });
});

describe('settling it', () => {
  const owed = settlementOf('125.00', '150.00');

  it('accepts the difference met exactly', () => {
    expect(settles([{ amount: '25.00' }], owed)).toBe(true);
    expect(settles([{ amount: '20.00' }, { amount: '5.00' }], owed)).toBe(true);
  });

  it('refuses an overpayment, which is change owed', () => {
    // "Not 'at least': an overpayment is change owed, and treating it as part
    // of the sale overstates takings and the VAT on them."
    expect(settles([{ amount: '30.00' }], owed)).toBe(false);
  });

  it('refuses a short payment', () => {
    expect(settles([{ amount: '20.00' }], owed)).toBe(false);
  });

  it('wants nothing tendered on an even swap', () => {
    const even = settlementOf('150.00', '150.00');
    expect(settles([], even)).toBe(true);
    expect(settles([{ amount: '1.00' }], even)).toBe(false);
  });
});

describe('whether the exchange can be made at all', () => {
  it('is ready when both halves and a reason are there', () => {
    expect(readiness('125.00', '150.00', 'Wrong size', [{ amount: '25.00' }])).toEqual({
      ok: true,
    });
  });

  it('says a swap with nothing coming back is a sale', () => {
    expect(readiness('0.00', '150.00', 'Wrong size', [])).toEqual({
      ok: false,
      reason: 'nothing_back',
    });
  });

  it('says a swap with nothing going out is a return', () => {
    expect(readiness('125.00', '0.00', 'Wrong size', [])).toEqual({
      ok: false,
      reason: 'nothing_out',
    });
  });

  it('insists on a reason, because unexplained returns hide fraud', () => {
    expect(readiness('125.00', '150.00', ' ', [{ amount: '25.00' }])).toEqual({
      ok: false,
      reason: 'no_reason',
    });
    expect(readiness('125.00', '150.00', 'ok', [{ amount: '25.00' }])).toEqual({
      ok: false,
      reason: 'no_reason',
    });
  });

  it('insists the difference is met before anything is sent', () => {
    expect(readiness('125.00', '150.00', 'Wrong size', [])).toEqual({
      ok: false,
      reason: 'settlement',
    });
  });

  it('is ready on an even swap with nothing tendered', () => {
    expect(readiness('150.00', '150.00', 'Wrong colour', [])).toEqual({ ok: true });
  });
});
