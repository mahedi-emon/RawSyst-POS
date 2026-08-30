import { describe, expect, it } from 'vitest';

import type { AgeingRow, Customer, LedgerRow, OpenInvoice } from '../api/receivables';
import {
  ageingTone,
  ageingTotals,
  allocateOldestFirst,
  canReversePayment,
  checkAllocation,
  creditStanding,
  major,
  minor,
  worstBucket,
} from './receivables';

const customer = (over: Partial<Customer> = {}): Customer => ({
  id: 'c1',
  code: 'CUST1',
  name: 'Al Noor Trading',
  customer_type: 'wholesale',
  payment_terms_days: 30,
  is_active: true,
  balance: '0.00',
  currency: 'SAR',
  ...over,
});

const invoice = (over: Partial<OpenInvoice> = {}): OpenInvoice => ({
  invoice_id: 'i1',
  issue_date: '2026-08-01',
  due_date: '2026-08-31',
  on_account: '100.00',
  credited: '0.00',
  received: '0.00',
  outstanding: '100.00',
  ...over,
});

describe('money in minor units', () => {
  it('holds the amounts a float cannot', () => {
    // 0.15 has no exact float64 representation, which is why every boundary in
    // this system carries money as a string.
    expect(minor('0.15')).toBe(15n);
    expect(major(minor('0.15'))).toBe('0.15');
  });

  it('survives amounts past what a float can count in whole units', () => {
    // Number.MAX_SAFE_INTEGER is about 9e15, so 90 trillion in halalas is
    // already past where a float starts skipping integers.
    const huge = '99999999999999.99';
    expect(major(minor(huge))).toBe(huge);
  });

  it('reads a missing amount as nothing rather than NaN', () => {
    expect(minor('')).toBe(0n);
    expect(minor(undefined as unknown as string)).toBe(0n);
  });

  it('keeps a negative negative', () => {
    expect(major(minor('-42.50'))).toBe('-42.50');
  });

  it('pads a single decimal place', () => {
    expect(major(minor('7.5'))).toBe('7.50');
  });
});

describe('what a credit account means', () => {
  it('says plainly when there is no account at all', () => {
    // Not the same as a limit of zero. A record somebody typed in a hurry must
    // not read as unlimited trust.
    const standing = creditStanding(customer());
    expect(standing.kind).toBe('none');
    expect(standing.message).toMatch(/paid at the till/i);
  });

  it('reports the headroom when there is room', () => {
    const standing = creditStanding(
      customer({ credit_limit: '5000.00', balance: '1000.00', available: '4000.00' }),
    );
    expect(standing.kind).toBe('clear');
    expect(standing.message).toContain('4000.00');
  });

  it('warns when a tenth or less of the limit is left', () => {
    const standing = creditStanding(
      customer({ credit_limit: '5000.00', balance: '4600.00', available: '400.00' }),
    );
    expect(standing.kind).toBe('near_limit');
  });

  it('scales the warning to the limit rather than to a fixed amount', () => {
    // 400 left is tight on a 5,000 limit and comfortable on a 500,000 one.
    const big = creditStanding(
      customer({ credit_limit: '500000.00', balance: '499600.00', available: '400.00' }),
    );
    expect(big.kind).toBe('near_limit');

    const roomy = creditStanding(
      customer({ credit_limit: '5000.00', balance: '500.00', available: '4500.00' }),
    );
    expect(roomy.kind).toBe('clear');
  });

  it('says nothing further can go on the account at the limit', () => {
    expect(
      creditStanding(
        customer({ credit_limit: '1000.00', balance: '1000.00', available: '0.00' }),
      ).kind,
    ).toBe('at_limit');
  });

  it('does not divide by a zero limit', () => {
    const standing = creditStanding(
      customer({ credit_limit: '0.00', balance: '0.00', available: '0.00' }),
    );
    expect(standing.kind).toBe('at_limit');
  });
});

describe('pre-filling a receipt', () => {
  const invoices = [
    invoice({ invoice_id: 'newest', due_date: '2026-10-31', outstanding: '100.00' }),
    invoice({ invoice_id: 'oldest', due_date: '2026-08-31', outstanding: '60.00' }),
    invoice({ invoice_id: 'middle', due_date: '2026-09-30', outstanding: '50.00' }),
  ];

  it('settles the oldest first', () => {
    const { allocations } = allocateOldestFirst(invoices, '80.00');
    expect(allocations).toEqual({ oldest: '60.00', middle: '20.00' });
  });

  it('leaves nothing on an invoice it did not reach', () => {
    const { allocations } = allocateOldestFirst(invoices, '30.00');
    expect(allocations).toEqual({ oldest: '30.00' });
  });

  it('says what it could not place rather than dropping it', () => {
    // A customer handing over more than they owe must not have the excess
    // silently vanish from the form.
    const { allocations, unallocated } = allocateOldestFirst(invoices, '250.00');
    expect(allocations).toEqual({ oldest: '60.00', middle: '50.00', newest: '100.00' });
    expect(unallocated).toBe('40.00');
  });

  it('places nothing when nothing was received', () => {
    expect(allocateOldestFirst(invoices, '0.00').allocations).toEqual({});
  });

  it('skips an invoice with nothing left on it', () => {
    const settled = [invoice({ invoice_id: 'done', outstanding: '0.00' })];
    expect(allocateOldestFirst(settled, '50.00').allocations).toEqual({});
  });
});

describe('checking an allocation before sending it', () => {
  const invoices = [invoice({ invoice_id: 'i1', outstanding: '100.00' })];

  it('accepts one that settles exactly', () => {
    expect(checkAllocation(invoices, { i1: '100.00' })).toEqual({
      kind: 'ok',
      total: '100.00',
    });
  });

  it('refuses more than an invoice owes, and names it', () => {
    const problem = checkAllocation(
      [invoice({ invoice_id: 'i1', human_number: 'INV-0007', outstanding: '100.00' })],
      { i1: '150.00' },
    );
    expect(problem.kind).toBe('over_invoice');
    if (problem.kind === 'over_invoice') {
      expect(problem.invoice).toBe('INV-0007');
      expect(problem.outstanding).toBe('100.00');
    }
  });

  it('treats an all-zero allocation as nothing to send', () => {
    expect(checkAllocation(invoices, { i1: '0.00' })).toEqual({ kind: 'nothing' });
  });

  it('adds up a payment split across invoices', () => {
    const two = [
      invoice({ invoice_id: 'a', outstanding: '100.00' }),
      invoice({ invoice_id: 'b', outstanding: '100.00' }),
    ];
    expect(checkAllocation(two, { a: '40.00', b: '35.50' })).toEqual({
      kind: 'ok',
      total: '75.50',
    });
  });

  it('ignores an allocation to an invoice that is not open', () => {
    expect(checkAllocation(invoices, { gone: '50.00' })).toEqual({ kind: 'nothing' });
  });
});

describe('reading an ageing row', () => {
  const row = (over: Partial<AgeingRow> = {}): AgeingRow => ({
    customer_id: 'c1',
    customer: 'Al Noor',
    not_due: '0.00',
    days_0_30: '0.00',
    days_31_60: '0.00',
    days_61_90: '0.00',
    days_90_plus: '0.00',
    total: '0.00',
    ...over,
  });

  it('takes the worst bucket, not the biggest', () => {
    // 10 at 90+ is worse news than 5,000 not yet due.
    expect(
      worstBucket(row({ not_due: '5000.00', days_90_plus: '10.00' })),
    ).toBe('90_plus');
  });

  it('reports nothing when the row is empty', () => {
    expect(worstBucket(row())).toBe('none');
  });

  it('does not colour an invoice that is merely not yet due', () => {
    expect(ageingTone(worstBucket(row({ not_due: '900.00' })))).toBe('neutral');
  });

  it('escalates past sixty days', () => {
    expect(ageingTone('61_90')).toBe('danger');
    expect(ageingTone('31_60')).toBe('warning');
  });

  it('foots the table', () => {
    const totals = ageingTotals([
      row({ days_0_30: '10.50', total: '10.50' }),
      row({ days_0_30: '0.25', days_90_plus: '100.00', total: '100.25' }),
    ]);
    expect(totals.days_0_30).toBe('10.75');
    expect(totals.days_90_plus).toBe('100.00');
    expect(totals.total).toBe('110.75');
  });

  it('foots an empty table as zero rather than as nothing', () => {
    expect(ageingTotals([]).total).toBe('0.00');
  });
});

describe('which statement rows can be reversed', () => {
  const row = (over: Partial<LedgerRow> = {}): LedgerRow => ({
    date: '2026-08-16',
    kind: 'receipt',
    reference: 'RCT-2026-000001',
    received: '115.00',
    balance: '0.00',
    source_id: 'r1',
    ...over,
  });

  it('offers a reverse on a live payment', () => {
    expect(canReversePayment(row())).toBe(true);
  });

  it('does not offer a reverse on a sale, a credit or a reversal', () => {
    // A sale is put right by a credit note; a reversal is already the
    // correcting document. Offering either would invent a second way to edit
    // history.
    expect(canReversePayment(row({ kind: 'sale', source_id: 'i1' }))).toBe(false);
    expect(canReversePayment(row({ kind: 'credit', source_id: 'c1' }))).toBe(false);
    expect(canReversePayment(row({ kind: 'reversal', reverses_id: 'r1' }))).toBe(
      false,
    );
  });

  it('does not offer a reverse on a payment that has already been put right', () => {
    expect(canReversePayment(row({ reversed: true }))).toBe(false);
  });

  it('does not offer a reverse when the row has no source id', () => {
    expect(canReversePayment(row({ source_id: undefined }))).toBe(false);
  });
});
