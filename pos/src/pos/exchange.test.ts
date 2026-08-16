// The till's side of an exchange.
//
// The preview is the risky part: a cashier reads it aloud to the customer
// before the button is pressed, so it has to agree with what the server will
// charge. Where it cannot, it must be labelled as an estimate rather than
// quietly wrong.

import { describe, expect, it, vi } from 'vitest';

import {
  previewDifference,
  readyToExchange,
  replacementFrom,
  submitExchange,
  type ReplacementLine,
} from './exchange';
import type { ReturnableLine, ReturnSelection } from './returns';
import type { CachedVariant } from '../offline/catalogue';

const returnable: ReturnableLine[] = [
  {
    line_id: 'l1',
    line_no: 1,
    description: 'Abaya · M · Black',
    qty_sold: '2',
    qty_returned: '0',
    qty_returnable: '2',
    unit_price: '115.00',
    tax_treatment: 'standard',
    tax_rate: '0.15',
    net_returnable: '200.00',
    tax_returnable: '30.00',
    gross_returnable: '230.00',
  },
];

function replacement(price: string, qty = '1'): ReplacementLine[] {
  return [
    {
      variantId: 'v2',
      description: 'Abaya · L · Black',
      qty,
      unitPrice: price,
      taxTreatment: 'standard',
    },
  ];
}

const takeOneBack: ReturnSelection[] = [{ lineId: 'l1', qty: '1' }];

describe('previewing the difference', () => {
  it('tells the cashier the customer owes money on an upward swap', () => {
    // One of two units back: 115 credit. Replacement 230. Customer pays 115.
    const p = previewDifference(returnable, takeOneBack, replacement('230.00'));

    expect(p.credit).toBe('115.00');
    expect(p.owed).toBe('115.00');
    expect(p.customerPays).toBe(true);
  });

  it('tells the cashier the shop owes money on a downward swap', () => {
    const p = previewDifference(returnable, takeOneBack, replacement('50.00'));

    expect(p.owed).toBe('65.00');
    expect(p.customerPays).toBe(false);
  });

  it('shows nothing owed on an even swap', () => {
    const p = previewDifference(returnable, takeOneBack, replacement('115.00'));

    expect(p.owed).toBe('0.00');
    // Not "customer pays 0.00" — nothing is owed either way.
    expect(p.customerPays).toBe(false);
  });

  it('credits proportionally, not by quantity times unit price', () => {
    // An invoice-level discount was allocated across the line, so its
    // returnable amount is less than 2 x 115. Using the unit price would
    // over-credit — a little every time, and reliably against the shop.
    const discounted: ReturnableLine[] = [
      { ...returnable[0]!, gross_returnable: '200.00' },
    ];
    const p = previewDifference(discounted, takeOneBack, replacement('200.00'));

    expect(p.credit).toBe('100.00');
    expect(p.owed).toBe('100.00');
  });

  it('handles several units of the replacement', () => {
    const p = previewDifference(returnable, takeOneBack, replacement('60.00', '3'));

    expect(p.owed).toBe('65.00');
    expect(p.customerPays).toBe(true);
  });

  it('does not drift on amounts a float would round badly', () => {
    const awkward: ReturnableLine[] = [
      { ...returnable[0]!, qty_returnable: '3', gross_returnable: '0.30' },
    ];
    const p = previewDifference(
      awkward,
      [{ lineId: 'l1', qty: '1' }],
      replacement('0.20'),
    );

    // 0.30 / 3 = 0.10 credit against a 0.20 replacement.
    expect(p.credit).toBe('0.10');
    expect(p.owed).toBe('0.10');
  });

  it('ignores a line that is not on the invoice', () => {
    const p = previewDifference(
      returnable,
      [{ lineId: 'nope', qty: '1' }],
      replacement('50.00'),
    );

    expect(p.credit).toBe('0.00');
    expect(p.owed).toBe('50.00');
  });

  it('ignores an unparseable or negative quantity', () => {
    for (const qty of ['two', '-1', '0']) {
      const p = previewDifference(
        returnable,
        [{ lineId: 'l1', qty }],
        replacement('50.00'),
      );
      expect(p.credit).toBe('0.00');
    }
  });

  it('never credits more than the line has left', () => {
    // Asking for three of two. The preview caps at what is available; the
    // server refuses the exchange outright.
    const p = previewDifference(
      returnable,
      [{ lineId: 'l1', qty: '3' }],
      replacement('230.00'),
    );

    expect(p.credit).toBe('230.00');
  });
});

describe('what may be sent', () => {
  it('needs both halves', () => {
    expect(readyToExchange([], replacement('50.00'), 'wrong size')).toBe(false);
    expect(readyToExchange(takeOneBack, [], 'wrong size')).toBe(false);
    expect(readyToExchange(takeOneBack, replacement('50.00'), 'wrong size')).toBe(
      true,
    );
  });

  it('needs a reason, because C14 requires one on every return', () => {
    expect(readyToExchange(takeOneBack, replacement('50.00'), '')).toBe(false);
    expect(readyToExchange(takeOneBack, replacement('50.00'), '  ')).toBe(false);
    expect(readyToExchange(takeOneBack, replacement('50.00'), 'no')).toBe(false);
    expect(readyToExchange(takeOneBack, replacement('50.00'), 'torn')).toBe(true);
  });
});

describe('the request', () => {
  it('sends no tenders on the replacement', async () => {
    const sent: unknown[] = [];
    const client = {
      send: (_m: string, _p: string, body?: unknown) => {
        sent.push(body);
        return Promise.resolve({});
      },
    } as never;

    await submitExchange(client, {
      creditNoteUuid: 'cn-1',
      invoiceUuid: 'inv-1',
      originalInvoiceId: 'orig-1',
      reason: 'wrong size',
      returning: takeOneBack,
      replacement: replacement('230.00'),
      settlement: [{ method: 'cash', amount: '115.00' }],
    });

    const body = sent[0] as {
      replacement: { lines: unknown[]; tenders?: unknown };
      settlement: unknown[];
      credit_note_uuid: string;
      invoice_uuid: string;
    };

    // How an exchange settles is the server's arithmetic. Sending tenders here
    // would be sending a number it is going to discard — or worse, one it might
    // believe.
    expect(body.replacement.tenders).toBeUndefined();
    expect(body.settlement).toHaveLength(1);

    // Two documents, two identities. The server refuses a pair sharing one.
    expect(body.credit_note_uuid).not.toBe(body.invoice_uuid);
  });

  it('sends no company, warehouse, rate or currency', async () => {
    let captured: Record<string, unknown> = {};
    const client = {
      send: (_m: string, _p: string, body?: unknown) => {
        captured = body as Record<string, unknown>;
        return Promise.resolve({});
      },
    } as never;

    await submitExchange(client, {
      creditNoteUuid: 'cn-1',
      invoiceUuid: 'inv-1',
      originalInvoiceId: 'orig-1',
      reason: 'wrong size',
      returning: takeOneBack,
      replacement: replacement('230.00'),
      settlement: [],
    });

    // All resolved from the registered device and the registry. A till that
    // could name its own warehouse could restore stock into another store's.
    for (const forbidden of [
      'company_id',
      'warehouse_id',
      'tax_rate',
      'currency',
      'device_id',
    ]) {
      expect(captured).not.toHaveProperty(forbidden);
    }
  });

  it('posts to the exchange route', async () => {
    const send = vi.fn().mockResolvedValue({});
    await submitExchange({ send } as never, {
      creditNoteUuid: 'cn-1',
      invoiceUuid: 'inv-1',
      originalInvoiceId: 'orig-1',
      reason: 'wrong size',
      returning: takeOneBack,
      replacement: replacement('230.00'),
      settlement: [],
    });

    expect(send).toHaveBeenCalledWith(
      'POST',
      '/api/v1/pos/exchanges',
      expect.anything(),
    );
  });
});

describe('building a replacement from a scan', () => {
  it('carries the cached price and treatment', () => {
    const v: CachedVariant = {
      id: 'v9',
      productId: 'p9',
      sku: 'SKU-9',
      barcode: '600000009',
      name: 'Abaya',
      nameAr: '',
      attributes: { size: 'L' },
      price: '230.00',
      taxTreatment: 'zero_rated',
      isActive: true,
      updatedAt: '2026-08-16T10:00:00+03:00',
    };

    const line = replacementFrom(v);

    expect(line.variantId).toBe('v9');
    expect(line.unitPrice).toBe('230.00');
    // The server validates the treatment against the country's registry list
    // regardless of what the cache said.
    expect(line.taxTreatment).toBe('zero_rated');
  });

  it('falls back to the SKU when the product has no name', () => {
    const v = {
      id: 'v9',
      productId: 'p9',
      sku: 'SKU-9',
      barcode: '',
      name: '',
      nameAr: '',
      attributes: {},
      price: '10.00',
      taxTreatment: '',
      isActive: true,
      updatedAt: '',
    } satisfies CachedVariant;

    const line = replacementFrom(v);
    expect(line.description).toBe('SKU-9');
    expect(line.taxTreatment).toBe('standard');
  });
});
