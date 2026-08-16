// The buying screens' judgement calls.
//
// Each of these has a failure mode that costs money rather than merely looking
// wrong: a receiving form that pre-fills the wrong quantity puts the wrong
// stock on the shelf, and a screen that offers to pay a held bill routes around
// the control B5.2 calls the single most effective one there is.

import { describe, expect, it } from 'vitest';

import {
  matchSummary,
  payable,
  receivedFraction,
  receivingDefaults,
} from './purchasing';
import type { Bill, MatchLine, OrderLine } from '../api/purchasing';

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
