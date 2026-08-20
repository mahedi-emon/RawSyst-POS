// The buying screens' judgement calls.
//
// Each of these has a failure mode that costs money rather than merely looking
// wrong: a receiving form that pre-fills the wrong quantity puts the wrong
// stock on the shelf, and a screen that offers to pay a held bill routes around
// the control B5.2 calls the single most effective one there is.

import { describe, expect, it } from 'vitest';

import {
  canReverseSupplierPayment,
  matchSummary,
  payable,
  paymentKind,
  receiptNotice,
  receivedFraction,
  receivingDefaults,
} from './purchasing';
import type { Bill, MatchLine, OrderLine, Receipt } from '../api/purchasing';

function receipt(over: Partial<Receipt> = {}): Receipt {
  return {
    id: 'g1',
    grn_number: 'GRN-0007',
    po_id: 'p1',
    po_number: 'PO-0003',
    received_on: '2026-08-16',
    order_status: 'received',
    already_received: false,
    cost_correction: '0.00',
    units_recosted: '0',
    lines: [],
    ...over,
  };
}

function line(over: Partial<OrderLine> = {}): OrderLine {
  return {
    id: 'l1',
    line_no: 1,
    variant_id: 'v1',
    description: 'Abaya',
    qty_ordered: '10.0000',
    qty_received: '0.0000',
    qty_outstanding: '10.0000',
    qty_billed: '0.0000',
    unit_cost: '100.0000',
    tax_treatment: 'standard',
    net_amount: '1000.0000',
    tax_amount: '150.0000',
    gross_amount: '1150.0000',
    ...over,
  };
}

function bill(over: Partial<Bill> = {}): Bill {
  return {
    id: 'b1',
    supplier_id: 's1',
    supplier: 'Acme',
    supplier_ref: 'INV-1',
    bill_date: '2026-08-16',
    due_date: '2026-09-15',
    currency: 'SAR',
    subtotal_net: '1000.00',
    tax_total: '150.00',
    total_inclusive: '1150.00',
    amount_paid: '0.00',
    outstanding: '1150.00',
    status: 'matched',
    posted: true,
    already_recorded: false,
    ...over,
  };
}

describe('pre-filling a receiving form', () => {
  it('offers what is still outstanding', () => {
    // The common case by a long way is a delivery that matches, so the
    // storeman confirms rather than transcribes.
    const defaults = receivingDefaults([line()]);
    expect(defaults).toEqual({ l1: '10' });
  });

  it('offers only the remainder on a part-delivered line', () => {
    const defaults = receivingDefaults([
      line({ qty_received: '4.0000', qty_outstanding: '6.0000' }),
    ]);
    expect(defaults).toEqual({ l1: '6' });
  });

  it('leaves a completed line blank rather than filling it with zero', () => {
    // A zero in a quantity box invites someone to overtype it, and receiving
    // more against a complete line is exactly what the blank prevents.
    const defaults = receivingDefaults([
      line({ qty_received: '10.0000', qty_outstanding: '0.0000' }),
    ]);
    expect(defaults).toEqual({});
  });

  it('trims the scale the database returns', () => {
    const defaults = receivingDefaults([line({ qty_outstanding: '2.5000' })]);
    // A genuine fraction survives; half a metre of fabric is a real quantity.
    expect(defaults.l1).toBe('2.5');
  });

  it('handles an order with no lines', () => {
    expect(receivingDefaults([])).toEqual({});
  });
});

describe('deciding whether a bill can be paid', () => {
  it('refuses a held bill', () => {
    // The control has to bite in the UI as well as on the server, or the
    // screen is teaching people that the block is advisory.
    expect(payable(bill({ status: 'blocked' }))).toBe(false);
  });

  it('allows one that passed the match', () => {
    expect(payable(bill({ status: 'matched' }))).toBe(true);
  });

  it('allows one whose discrepancy was approved', () => {
    // Somebody put their name to it, so the money may move.
    expect(payable(bill({ status: 'approved' }))).toBe(true);
  });

  it('refuses a bill with nothing left on it', () => {
    expect(
      payable(bill({ status: 'paid', amount_paid: '1150.00', outstanding: '0.00' })),
    ).toBe(false);
  });

  it('refuses a draft or a cancelled bill', () => {
    expect(payable(bill({ status: 'draft' }))).toBe(false);
    expect(payable(bill({ status: 'cancelled' }))).toBe(false);
  });
});

describe('summarising the match', () => {
  const pass: MatchLine = { dimension: 'qty', variance: '0', outcome: 'pass' };

  it('says plainly when everything agrees', () => {
    const s = matchSummary([pass, { ...pass, dimension: 'price' }]);
    expect(s.outcome).toBe('pass');
    expect(s.breaches).toBe(0);
    expect(s.message).toMatch(/agrees/i);
  });

  it('counts the breaches', () => {
    const s = matchSummary([
      pass,
      { dimension: 'qty', variance: '2', outcome: 'breach' },
      { dimension: 'price', variance: '10', outcome: 'breach' },
    ]);
    expect(s.outcome).toBe('breach');
    expect(s.breaches).toBe(2);
    expect(s.message).toMatch(/2 checks/);
  });

  it('reads naturally for a single breach', () => {
    const s = matchSummary([{ dimension: 'qty', variance: '2', outcome: 'breach' }]);
    expect(s.message).toMatch(/^One check/);
  });

  it('reports within-tolerance honestly rather than as a clean pass', () => {
    // A supplier drifting upward inside tolerance month after month is
    // something real, and a green tick would hide it.
    const s = matchSummary([
      pass,
      { dimension: 'price', variance: '5', outcome: 'within_tolerance' },
    ]);
    expect(s.outcome).toBe('within_tolerance');
    expect(s.message).toMatch(/tolerance/i);
  });

  it('lets a breach outrank a tolerance', () => {
    const s = matchSummary([
      { dimension: 'price', variance: '5', outcome: 'within_tolerance' },
      { dimension: 'qty', variance: '2', outcome: 'breach' },
    ]);
    expect(s.outcome).toBe('breach');
  });

  it('treats an unmatched bill as passing', () => {
    // A bill with no order behind it has nothing to disagree with. The server
    // records that as a pass with an explanation.
    expect(matchSummary([]).outcome).toBe('pass');
  });
});

describe('how much of an order has arrived', () => {
  it('reads nothing, half and all', () => {
    expect(receivedFraction([line()])).toBe(0);
    expect(receivedFraction([line({ qty_received: '5.0000' })])).toBe(0.5);
    expect(receivedFraction([line({ qty_received: '10.0000' })])).toBe(1);
  });

  it('sums across lines', () => {
    const fraction = receivedFraction([
      line({ id: 'a', qty_ordered: '10', qty_received: '10' }),
      line({ id: 'b', qty_ordered: '10', qty_received: '0' }),
    ]);
    expect(fraction).toBe(0.5);
  });

  it('clamps over-receiving rather than overflowing the bar', () => {
    // A supplier sending a bonus case is good news, and a bar rendering past
    // its own track looks like a bug.
    expect(receivedFraction([line({ qty_received: '12.0000' })])).toBe(1);
  });

  it('reports nothing for an empty order rather than dividing by zero', () => {
    expect(receivedFraction([])).toBe(0);
    expect(receivedFraction([line({ qty_ordered: '0', qty_received: '0' })])).toBe(0);
  });
});

describe('what a storeman is told after a delivery', () => {
  it('says the stock landed and stops there when nothing was corrected', () => {
    const notice = receiptNotice(receipt());
    expect(notice).toBe('Recorded as GRN-0007. Stock has been updated.');
    // Nothing about costs. The overwhelmingly common delivery corrects nothing,
    // and an explanation of a correction that did not happen is noise.
    expect(notice).not.toContain('cost of goods sold');
  });

  it('recognises a delivery it has already recorded', () => {
    expect(receiptNotice(receipt({ already_received: true }))).toBe(
      'That delivery was already recorded as GRN-0007.',
    );
  });

  it('explains a correction that raised cost of goods sold', () => {
    // C13: two units sold before the delivery were costed at an estimate, and
    // the goods turned out dearer. Cost of goods sold on a sale already reported
    // moves, and a figure changing on last week's numbers unannounced is how
    // people stop trusting a system.
    const notice = receiptNotice(
      receipt({ cost_correction: '80.00', units_recosted: '2' }),
    );
    expect(notice).toContain('Recorded as GRN-0007.');
    expect(notice).toContain('2 units');
    expect(notice).toContain('cost of goods sold rose by 80.00');
  });

  it('explains a correction the other way without a double negative', () => {
    const notice = receiptNotice(
      receipt({ cost_correction: '-60.00', units_recosted: '2' }),
    );
    expect(notice).toContain('cost of goods sold fell by 60.00');
    // money() renders a negative in parentheses, which after "fell by" would
    // read as a correction in the opposite direction to the one described.
    expect(notice).not.toContain('(');
  });

  it('counts a single unit in the singular', () => {
    const notice = receiptNotice(
      receipt({ cost_correction: '12.50', units_recosted: '1' }),
    );
    expect(notice).toContain('1 unit sold');
    expect(notice).not.toContain('1 units');
  });

  it('trims the trailing zeroes a numeric quantity arrives with', () => {
    const notice = receiptNotice(
      receipt({ cost_correction: '80.00', units_recosted: '2.0000' }),
    );
    expect(notice).toContain('2 units');
    expect(notice).not.toContain('2.0000');
  });

  it('groups a large correction so it can be read at a glance', () => {
    const notice = receiptNotice(
      receipt({ cost_correction: '12345.60', units_recosted: '400' }),
    );
    expect(notice).toContain('12,345.60');
  });

  it('says nothing about costs when the figure is not a number', () => {
    // A server that sent something unexpected must not produce "rose by NaN" on
    // a screen a storeman is meant to act on.
    expect(receiptNotice(receipt({ cost_correction: 'oops' }))).toBe(
      'Recorded as GRN-0007. Stock has been updated.',
    );
    expect(receiptNotice(receipt({ cost_correction: '' }))).toBe(
      'Recorded as GRN-0007. Stock has been updated.',
    );
  });
});

describe('reversing a supplier payment', () => {
  // The AP mirror of the receipt-reversal rules. A button that always refuses
  // teaches a buyer to distrust the rest of them, so the ones the server would
  // turn down are not drawn.

  it('offers the action on a live payment', () => {
    expect(canReverseSupplierPayment({ id: 'p1' })).toBe(true);
  });

  it('does not offer it on a payment already reversed', () => {
    expect(canReverseSupplierPayment({ id: 'p1', reversed: true })).toBe(false);
  });

  it('does not offer it on a reversal', () => {
    // Undoing a reversal means paying again, which leaves both facts on the
    // record. Reversing one would let a clerk walk a balance anywhere.
    expect(canReverseSupplierPayment({ id: 'p2', reverses_id: 'p1' })).toBe(false);
  });

  it('does not offer it on something with no identifier', () => {
    expect(canReverseSupplierPayment({})).toBe(false);
  });

  it('tells the three kinds of row apart', () => {
    // What a buyer needs at a glance: money that stayed out, money that came
    // back, and the document that brought it back.
    expect(paymentKind({})).toBe('payment');
    expect(paymentKind({ reversed: true })).toBe('reversed');
    expect(paymentKind({ reverses_id: 'p1' })).toBe('reversal');
  });

  it('reads a reversal as a reversal even if it were somehow marked reversed', () => {
    // The server refuses to reverse a reversal, so this cannot arise — but if
    // the flags ever disagreed, being a reversal is the more specific fact.
    expect(paymentKind({ reverses_id: 'p1', reversed: true })).toBe('reversal');
  });
});
