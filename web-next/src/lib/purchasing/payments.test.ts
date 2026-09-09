import { describe, expect, it } from 'vitest';

import {
  billsSettled,
  canReverse,
  paymentState,
  reversalBlock,
  type ListedPayment,
} from './payments';

const payment = (over: Partial<ListedPayment> = {}): ListedPayment => ({
  id: 'p1',
  payment_number: 'PAY-000117',
  supplier_id: 's1',
  supplier: 'Acme Textiles',
  paid_on: '2026-08-16',
  method: 'bank_transfer',
  amount: '1150.00',
  currency: 'SAR',
  reversal: false,
  reversed: false,
  bills: ['INV-3f2a'],
  posted: true,
  ...over,
});

describe('whether a payment can still be taken back', () => {
  it('lets a live, posted, un-reversed payment be reversed', () => {
    expect(canReverse(payment())).toBe(true);
    expect(reversalBlock(payment())).toBeNull();
  });

  it('refuses to reverse a reversal', () => {
    // Reversing a reversal would let a clerk walk a balance anywhere by
    // alternating documents. Undoing a reversal means paying again, which
    // leaves both facts on the record.
    expect(reversalBlock(payment({ reversal: true }))).toBe('is_a_reversal');
  });

  it('refuses a second reversal of the same payment', () => {
    // One reversing document per original, enforced by a unique index.
    expect(reversalBlock(payment({ reversed: true }))).toBe('already_reversed');
  });

  it('refuses a payment that never reached the journal', () => {
    expect(reversalBlock(payment({ posted: false }))).toBe('never_posted');
  });

  it('calls a reversed reversal a reversal, because that is the first thing to understand', () => {
    expect(reversalBlock(payment({ reversal: true, reversed: true }))).toBe(
      'is_a_reversal',
    );
  });
});

describe('how a payment reads in the ledger', () => {
  it('keeps the three kinds of row apart', () => {
    expect(paymentState(payment())).toBe('live');
    expect(paymentState(payment({ reversed: true }))).toBe('reversed');
    expect(paymentState(payment({ reversal: true }))).toBe('reversal');
    expect(paymentState(payment({ posted: false }))).toBe('unposted');
  });
});

describe('the bills a payment settled', () => {
  it('names them the way the supplier does', () => {
    expect(billsSettled(payment({ bills: ['INV-1', 'INV-2'] }))).toBe('INV-1, INV-2');
  });

  it('says nothing rather than an empty string dressed as a list', () => {
    expect(billsSettled(payment({ bills: [] }))).toBe('');
  });
});
