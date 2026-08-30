import { describe, expect, it } from 'vitest';

import type { Order, OrderLine } from '../api/orders';
import {
  cancellable,
  lineTotal,
  previewTotals,
  readyToRaise,
  type DraftLine,
  documentsFor,
  nextState,
  needsAttention,
  outstanding,
  stepOf,
} from './orders';

function order(over: Partial<Order> = {}): Order {
  return {
    id: 'o1',
    order_no: 'SO-000001',
    state: 'quotation',
    channel: 'store',
    currency: 'SAR',
    subtotal: '100.00',
    discount: '0.00',
    total: '100.00',
    created_at: '2026-08-30T09:00:00Z',
    ...over,
  };
}

function line(over: Partial<OrderLine> = {}): OrderLine {
  return {
    id: 'l1',
    line_no: 1,
    variant_id: 'v1',
    sku: 'SKU1',
    product: 'Shirt',
    qty: '6',
    unit_price: '10.00',
    discount: '0.00',
    line_total: '60.00',
    qty_picked: '0',
    qty_delivered: '0',
    ...over,
  };
}

describe('where an order has got to', () => {
  it('walks B11 lifecycle in order', () => {
    expect(stepOf('quotation')).toBe(0);
    expect(stepOf('confirmed')).toBe(1);
    expect(stepOf('completed')).toBe(5);
  });

  // A cancelled order did not reach a step, it left the sequence. Drawing it at
  // the step it happened to be on says the opposite of what happened.
  it('puts a cancelled order outside the sequence', () => {
    expect(stepOf('cancelled')).toBe(-1);
  });
});

describe('what pressing next would do', () => {
  it('moves one step', () => {
    expect(nextState('quotation')).toBe('confirmed');
    expect(nextState('packed')).toBe('delivered');
  });

  // Completing an order is something only invoicing does. A button promising to
  // complete it that came back refused every time is a button people stop
  // believing.
  it('offers nothing on a delivered order', () => {
    expect(nextState('delivered')).toBeNull();
  });

  it('offers nothing at either end', () => {
    expect(nextState('completed')).toBeNull();
    expect(nextState('cancelled')).toBeNull();
  });
});

describe('whether an order can still be abandoned', () => {
  it('can be, right up to invoicing', () => {
    expect(cancellable('quotation')).toBe(true);
    expect(cancellable('delivered')).toBe(true);
  });

  // An invoice is corrected by a credit note, never by cancelling the order
  // behind it.
  it('cannot be once it has been invoiced', () => {
    expect(cancellable('completed')).toBe(false);
  });

  it('cannot be twice', () => {
    expect(cancellable('cancelled')).toBe(false);
  });
});

// Offering all three from the moment an order is a quotation prints two empty
// pages.
describe('which documents are worth printing', () => {
  it('offers none on a quotation', () => {
    expect(documentsFor(order())).toEqual([]);
  });

  it('offers the picking slip once it is confirmed', () => {
    expect(documentsFor(order({ state: 'confirmed', lines: [line()] }))).toEqual([
      'picking',
    ]);
  });

  it('offers all three once something has been picked', () => {
    expect(
      documentsFor(
        order({ state: 'processing', lines: [line({ qty_picked: '4' })] }),
      ),
    ).toEqual(['picking', 'packing', 'delivery']);
  });

  it('offers none on a cancelled order', () => {
    expect(documentsFor(order({ state: 'cancelled', lines: [line()] }))).toEqual(
      [],
    );
  });
});

// A picker works from this number. It must not be arrived at by floating-point
// subtraction.
describe('what is left to pick', () => {
  it('is the difference', () => {
    expect(outstanding(line({ qty: '6', qty_picked: '4' }))).toBe('2');
  });

  it('is nothing when the line is complete', () => {
    expect(outstanding(line({ qty: '6', qty_picked: '6' }))).toBe('0');
  });

  it('holds a fractional quantity exactly', () => {
    // 0.75 less 0.375 is 0.375, and a float would offer 0.37499999999999994 to
    // somebody counting fabric off a roll.
    expect(outstanding(line({ qty: '0.75', qty_picked: '0.375' }))).toBe('0.375');
  });

  it('falls back to the ordered quantity rather than to nonsense', () => {
    expect(outstanding(line({ qty: '6', qty_picked: 'four' }))).toBe('6');
  });
});

describe('which orders the working list should show', () => {
  it('is everything somebody still has to act on', () => {
    expect(needsAttention(order({ state: 'confirmed' }))).toBe(true);
    expect(needsAttention(order({ state: 'delivered' }))).toBe(true);
  });

  it('is not the finished ones', () => {
    expect(needsAttention(order({ state: 'completed' }))).toBe(false);
    expect(needsAttention(order({ state: 'cancelled' }))).toBe(false);
  });
});

function draft(over: Partial<DraftLine> = {}): DraftLine {
  return { variantId: 'v1', description: '', qty: '2', unitPrice: '10.00', discount: '0', ...over };
}

describe('what the quotation being typed comes to', () => {
  it('adds the lines up', () => {
    expect(previewTotals([draft(), draft({ qty: '3', unitPrice: '5.50' })])).toEqual({
      subtotal: '36.50',
      discount: '0.00',
      total: '36.50',
    });
  });

  it('takes the discounts off the total, not off the subtotal', () => {
    // Both figures are printed on the quotation, so a discount folded into the
    // subtotal would leave the customer unable to see what they were given.
    expect(previewTotals([draft({ qty: '4', unitPrice: '25.00', discount: '10.00' })])).toEqual({
      subtotal: '100.00',
      discount: '10.00',
      total: '90.00',
    });
  });

  it('is worth nothing rather than NaN on an empty line', () => {
    // Somebody who has cleared the quantity to retype it must not see the
    // running total blank out or read NaN back.
    expect(previewTotals([draft({ qty: '' })]).total).toBe('0.00');
    expect(previewTotals([draft({ qty: '.' })]).total).toBe('0.00');
    expect(previewTotals([draft({ unitPrice: '' })]).total).toBe('0.00');
  });

  it('reads a quantity mid-decimal as what has been typed so far', () => {
    expect(previewTotals([draft({ qty: '1.' })]).total).toBe('10.00');
  });

  it('prices a fractional quantity without a float', () => {
    // 0.15 × 33.33 is 4.9995, which rounds to 5.00 — and 0.1 + 0.2 arithmetic
    // would put it on the wrong side.
    expect(lineTotal(draft({ qty: '0.15', unitPrice: '33.33' }))).toBe('5.00');
  });

  it('holds an amount too large for a double', () => {
    expect(lineTotal(draft({ qty: '1', unitPrice: '99999999999999.99' }))).toBe(
      '99999999999999.99',
    );
  });
});

describe('whether the quotation is worth sending', () => {
  it('is not, with nothing on it', () => {
    expect(readyToRaise([])).toBe(false);
    expect(readyToRaise([draft({ variantId: '' })])).toBe(false);
  });

  it('is not, when every line is for none of something', () => {
    expect(readyToRaise([draft({ qty: '0' })])).toBe(false);
  });

  it('is, once one line asks for something', () => {
    expect(readyToRaise([draft({ variantId: '' }), draft()])).toBe(true);
  });
});
