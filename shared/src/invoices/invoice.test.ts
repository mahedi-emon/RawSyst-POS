import { describe, expect, it } from 'vitest';

import type { Invoice, InvoiceLine } from '../api/invoice';
import {
  auditActionName,
  chainStatus,
  documentTitle,
  hasAnyDiscount,
  isCreditNote,
  lineDiscount,
  orderedAudit,
  paymentSummary,
  settlementNote,
  stamp,
  taxTreatmentName,
} from './invoice';

function line(over: Partial<InvoiceLine> = {}): InvoiceLine {
  return {
    line_no: 1,
    variant_id: null,
    description: 'Executive Abaya',
    description_ar: null,
    qty: '1',
    unit_price: '449.00',
    line_discount: '0',
    invoice_discount_alloc: '0',
    tax_treatment: 'standard',
    tax_rate: '15',
    tax_amount: '67.35',
    net_amount: '449.00',
    gross_amount: '516.35',
    ...over,
  };
}

function invoice(over: Partial<Invoice> = {}): Invoice {
  return {
    id: 'i1',
    uuid: '11111111-2222-3333-4444-555555555555',
    doc_type: 'simplified',
    human_number: 'INV-RYD-000123',
    state: 'signed_pending_report',
    issue_date: '2026-08-15',
    currency: 'SAR',
    subtotal_net: '449.00',
    discount_total: '0',
    tax_total: '67.35',
    total_inclusive: '516.35',
    lines: [line()],
    tenders: [
      {
        tender_no: 1,
        method: 'cash',
        amount: '516.35',
        reference: null,
        settlement_status: 'settled',
      },
    ],
    zatca: null,
    customer: null,
    audit: [],
    parent_invoice_id: null,
    ...over,
  };
}

describe('naming the document', () => {
  it('titles each type the way a shop says it', () => {
    expect(documentTitle('standard')).toBe('Tax Invoice');
    expect(documentTitle('simplified')).toBe('Simplified Tax Invoice');
    expect(documentTitle('credit_note')).toBe('Credit Note');
  });

  it('shows an unknown type as itself rather than guessing', () => {
    // A doc type added server-side must not render as something reassuring.
    expect(documentTitle('proforma_quote')).toBe('proforma quote');
  });

  it('knows which way the money went', () => {
    expect(isCreditNote(invoice())).toBe(false);
    expect(isCreditNote(invoice({ doc_type: 'credit_note' }))).toBe(true);
  });
});

describe('what is paid and what is left', () => {
  it('settles when the tenders cover the total', () => {
    expect(paymentSummary(invoice())).toEqual({
      paid: '516.35',
      outstanding: '0.00',
      settled: true,
    });
  });

  it('adds split tenders without drifting', () => {
    // BigInt minor units. Number('0.1') + Number('0.2') is 0.30000000000000004,
    // and an invoice reporting one hallala outstanding forever is a support
    // call nobody can close.
    const split = invoice({
      total_inclusive: '0.30',
      tenders: [
        { tender_no: 1, method: 'cash', amount: '0.10', reference: null, settlement_status: 'settled' },
        { tender_no: 2, method: 'mada', amount: '0.20', reference: null, settlement_status: 'settled' },
      ],
    });
    expect(paymentSummary(split)).toEqual({
      paid: '0.30',
      outstanding: '0.00',
      settled: true,
    });
  });

  it('reports what a part-paid invoice still owes', () => {
    // The case somebody opens this screen to check.
    const partial = invoice({
      total_inclusive: '516.35',
      tenders: [
        { tender_no: 1, method: 'cash', amount: '200.00', reference: null, settlement_status: 'settled' },
      ],
    });
    expect(paymentSummary(partial)).toEqual({
      paid: '200.00',
      outstanding: '316.35',
      settled: false,
    });
  });

  it('never reports a negative outstanding', () => {
    // Over-tendering is change given, which the till already handled. Showing
    // it as a debt the shop owes would be wrong in the other direction.
    const over = invoice({
      total_inclusive: '100.00',
      tenders: [
        { tender_no: 1, method: 'cash', amount: '150.00', reference: null, settlement_status: 'settled' },
      ],
    });
    expect(over.tenders.length).toBe(1);
    expect(paymentSummary(over).outstanding).toBe('0.00');
    expect(paymentSummary(over).settled).toBe(true);
  });

  it('treats a document with no tenders as wholly outstanding', () => {
    const unpaid = invoice({ tenders: [] });
    expect(paymentSummary(unpaid)).toEqual({
      paid: '0.00',
      outstanding: '516.35',
      settled: false,
    });
  });
});

describe('discounts', () => {
  it('adds a line discount to its share of the invoice discount', () => {
    // A customer asking "why is this not 449" does not care which of the two
    // it was, so they are shown as one figure.
    expect(lineDiscount(line({ line_discount: '20.00', invoice_discount_alloc: '5.50' })))
      .toBe('25.50');
  });

  it('shows the column only when something is in it', () => {
    // A column of dashes costs width and earns nothing.
    expect(hasAnyDiscount(invoice())).toBe(false);
    expect(hasAnyDiscount(invoice({ lines: [line({ line_discount: '5.00' })] }))).toBe(true);
    expect(
      hasAnyDiscount(invoice({ lines: [line({ invoice_discount_alloc: '0.01' })] })),
    ).toBe(true);
  });
});

describe('reading the codes in English', () => {
  it('names tax treatments', () => {
    expect(taxTreatmentName('standard')).toBe('Standard');
    expect(taxTreatmentName('zero_rated')).toBe('Zero-rated');
    expect(taxTreatmentName('out_of_scope')).toBe('Out of scope');
  });

  it('says nothing about a settlement that went as expected', () => {
    // A column saying "settled" on every row tells nobody anything.
    expect(settlementNote('settled')).toBeNull();
    expect(settlementNote('pending')).toBe('Awaiting settlement');
    expect(settlementNote('charged_back')).toBe('Charged back');
  });

  it('names audited actions, and shows unknown ones as themselves', () => {
    expect(auditActionName('invoice_reprinted')).toBe('Reprinted');
    expect(auditActionName('invoice_issued')).toBe('Issued');
    expect(auditActionName('something_new')).toBe('something new');
  });
});

describe('the ZATCA panel', () => {
  it('reports no chain position on a document that has none', () => {
    // A draft consumes no counter, which is correct rather than missing.
    expect(chainStatus(invoice())).toBe('none');
  });

  it('separates having a position from having been signed', () => {
    // The distinction the panel exists to make honestly. A QR that does not
    // scan is worse than no QR, so a positioned-but-unsigned invoice must not
    // render an empty code.
    const positioned = invoice({
      zatca: {
        icv: 42,
        pih: 'abc',
        invoice_hash: 'def',
        schema_version: '2.0',
        qr_tlv: null,
        submitted_at: null,
        response_code: null,
        reject_reason: null,
      },
    });
    expect(chainStatus(positioned)).toBe('positioned');

    const signed = invoice({ zatca: { ...positioned.zatca!, qr_tlv: 'AQVTZWxsZXI=' } });
    expect(chainStatus(signed)).toBe('signed');
  });
});

describe('the audit trail', () => {
  it('orders newest first, whatever order it arrived in', () => {
    const entries = [
      { action: 'invoice_issued', actor_label: 'Fatima', occurred_at: '2026-08-15T10:00:00+03:00', device_label: null },
      { action: 'invoice_reprinted', actor_label: 'Sara', occurred_at: '2026-08-16T09:00:00+03:00', device_label: null },
    ];
    expect(orderedAudit(entries).map((e) => e.action))
      .toEqual(['invoice_reprinted', 'invoice_issued']);
  });

  it('does not mutate what it was given', () => {
    const entries = [
      { action: 'a', actor_label: null, occurred_at: '2026-08-15T10:00:00+03:00', device_label: null },
      { action: 'b', actor_label: null, occurred_at: '2026-08-16T10:00:00+03:00', device_label: null },
    ];
    orderedAudit(entries);
    expect(entries.map((e) => e.action)).toEqual(['a', 'b']);
  });
});

describe('timestamps', () => {
  it('reads the same in every market this product serves', () => {
    // Deliberately not locale-formatted: 8/16 and 16/8 mean different things
    // in different places, and "16 Aug" means one thing everywhere.
    expect(stamp('2026-08-16T14:32:00+03:00')).toBe('16 Aug 14:32');
    expect(stamp('2026-01-02T09:05:00Z')).toBe('2 Jan 09:05');
  });

  it('handles a date with no time', () => {
    expect(stamp('2026-08-16')).toBe('16 Aug');
  });

  it('shows something unparseable as itself rather than as a wrong date', () => {
    expect(stamp('not a date')).toBe('not a date');
  });
});
