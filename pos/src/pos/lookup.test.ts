// Finding the sale a customer is holding a receipt for.
//
// # What went wrong
//
// The returns screen asks the cashier to "scan the receipt or type the sale
// reference" and sent whatever it got straight to
// `GET /api/v1/pos/sales/{invoiceID}/returnable`.
//
// That route takes `sales_invoice.id`, a UUID minted server-side inside
// Finalize. The till never sees it: it generates the DOCUMENT uuid, queues the
// sale under it, prints the first eight characters of it on the receipt, and
// the push response reports applied or failed against that document uuid and
// nothing else. So the eight characters failed as a malformed UUID and the
// whole document uuid came back 404 — on every sale, at every terminal.
//
// Found by driving the packaged Windows application (e2e/tauri.mjs).
//
// These pin the two things that were wrong: the reference goes to the lookup,
// and everything after it uses the id the lookup returned.

import { describe, expect, it } from 'vitest';

import { fetchReturnable, lookUpSale, submitReturn } from '@rawsyst/shared/pos/returns';
import { submitExchange } from './exchange';

/** Records what was asked for, and answers as the server would. */
function recording(answers: Record<string, unknown> = {}) {
  const calls: Array<{ method: string; path: string; body?: unknown }> = [];
  const client = {
    send: (method: string, path: string, body?: unknown) => {
      calls.push({ method, path, body });
      for (const [prefix, answer] of Object.entries(answers)) {
        if (path.startsWith(prefix)) return Promise.resolve(answer);
      }
      return Promise.resolve({});
    },
  } as never;
  return { client, calls };
}

const match = {
  id: '11111111-1111-4111-8111-111111111111',
  uuid: '22222222-2222-4222-8222-222222222222',
  doc_type: 'simplified',
  human_number: 'SIM-000123',
  state: 'issued',
  issue_date: '2026-08-28',
  currency: 'SAR',
  total_inclusive: '230.00',
};

describe('looking a sale up from its receipt', () => {
  it('asks the server to resolve the reference rather than assuming it', async () => {
    const { client, calls } = recording({ '/api/v1/pos/sales/lookup': match });

    const found = await lookUpSale(client, '22222222');

    expect(calls).toHaveLength(1);
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.path).toBe(
      '/api/v1/pos/sales/lookup?reference=22222222',
    );
    // The id the till could not otherwise know.
    expect(found.id).toBe(match.id);
  });

  it('escapes a reference so an invoice number cannot break the query', async () => {
    const { client, calls } = recording({ '/api/v1/pos/sales/lookup': match });

    await lookUpSale(client, 'SIM 000123&company_id=other');

    expect(calls[0]!.path).toBe(
      '/api/v1/pos/sales/lookup?reference=SIM%20000123%26company_id%3Dother',
    );
  });

  it('reads the lines with the id the lookup returned, not the reference', async () => {
    const { client, calls } = recording({
      '/api/v1/pos/sales/lookup': match,
      [`/api/v1/pos/sales/${match.id}/returnable`]: { lines: [] },
    });

    const found = await lookUpSale(client, '22222222');
    await fetchReturnable(client, found.id);

    expect(calls[1]!.path).toBe(
      `/api/v1/pos/sales/${match.id}/returnable`,
    );
    // The shape of the original bug, as an assertion: the document uuid is
    // what the receipt carries and is not what this route takes.
    expect(calls[1]!.path).not.toContain(match.uuid);
  });

  it('refunds against the invoice id, never the scanned reference', async () => {
    const { client, calls } = recording();

    await submitReturn(client, {
      creditNoteUuid: 'cn-1',
      originalInvoiceId: match.id,
      reason: 'wrong size',
      lines: [{ lineId: 'l1', qty: '1' }],
      refunds: [{ method: 'cash', amount: '115.00' }],
    });

    const body = calls[0]!.body as { original_invoice_id: string };
    expect(body.original_invoice_id).toBe(match.id);
    expect(body.original_invoice_id).not.toBe(match.uuid);
  });

  it('exchanges against the invoice id too', async () => {
    const { client, calls } = recording();

    await submitExchange(client, {
      creditNoteUuid: 'cn-1',
      invoiceUuid: 'inv-1',
      originalInvoiceId: match.id,
      reason: 'wrong size',
      returning: [{ lineId: 'l1', qty: '1' }],
      replacement: [
        {
          variantId: 'v2',
          description: 'Abaya · L · Black',
          qty: '1',
          unitPrice: '230.00',
          taxTreatment: 'standard',
        },
      ],
      settlement: [{ method: 'cash', amount: '115.00' }],
    });

    const body = calls[0]!.body as { original_invoice_id: string };
    expect(body.original_invoice_id).toBe(match.id);
  });
});
