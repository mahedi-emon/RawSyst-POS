// Swapping goods, from the till's side.
//
// One request, because the two halves must not be separable. A terminal that
// issued the credit note and then failed to place the sale would have given
// the goods away; one that placed the sale and failed to credit would have
// charged twice. The server does both in a single transaction, and the till's
// job is to describe what is happening rather than to arrange it.
//
// # The till does not compute the difference
//
// It shows one, so the cashier can tell the customer what to expect before
// committing. That figure is an estimate from the cached price and the
// returnable amounts the server already gave us, and it is labelled as such.
// The authority is the server: it prices the replacement from the registry
// rate at the transaction date and the credit from the original invoice, and
// it refuses the exchange outright if what the till offered does not match.
//
// A till permitted to state its own settlement could quietly undercharge, and
// nothing downstream would notice — the invoice would balance, the journal
// would balance, and the shop would simply be poorer.
//
// # Needs the network, like returns
//
// For the same reason: how much of the original invoice has already been given
// back cannot be known here. See returns.ts.

import type { Client } from '../api/client';
import type { CachedVariant } from '../offline/catalogue';
import type { ReturnableLine, ReturnSelection } from './returns';

/** A tender settling the difference, in whichever direction it is owed. */
export interface SettlementTender {
  method: string;
  amount: string;
  reference?: string;
}

export interface ExchangeResult {
  credit_note: { credit_note_id: string; human_number?: string };
  replacement: { invoice_id: string; human_number?: string };
  /** The offsetting portion, which moved no money. */
  credit_applied: string;
  /** Positive when the customer paid, negative when the shop paid out. */
  difference: string;
  customer_paid: boolean;
}

/**
 * Swaps goods.
 *
 * Both UUIDs are generated here, before the call, which is what makes a retry
 * safe: a network failure after the server committed would otherwise have the
 * cashier press the button again and issue a second credit note against the
 * same goods. They must differ — the server refuses a pair that shares one,
 * because the two documents would then be indistinguishable to every
 * idempotency check downstream.
 *
 * No warehouse and no company are sent. Where the stock returns to and where
 * the replacement comes from are both resolved from the registered device,
 * exactly as on a sale.
 */
export function submitExchange(
  client: Client,
  input: {
    creditNoteUuid: string;
    invoiceUuid: string;
    originalInvoiceId: string;
    reason: string;
    returning: ReturnSelection[];
    replacement: Array<{
      variantId: string;
      description: string;
      qty: string;
      unitPrice: string;
      taxTreatment: string;
    }>;
    settlement: SettlementTender[];
  },
): Promise<ExchangeResult> {
  return client.send<ExchangeResult>('POST', '/api/v1/pos/exchanges', {
    credit_note_uuid: input.creditNoteUuid,
    invoice_uuid: input.invoiceUuid,
    original_invoice_id: input.originalInvoiceId,
    issued_at: new Date().toISOString(),
    reason: input.reason,
    returning: input.returning.map((l) => ({
      line_id: l.lineId,
      qty: l.qty,
    })),
    replacement: {
      doc_type: 'simplified',
      lines: input.replacement.map((l) => ({
        variant_id: l.variantId,
        description: l.description,
        qty: l.qty,
        unit_price: l.unitPrice,
        tax_treatment: l.taxTreatment,
      })),
      // No tenders. How an exchange settles is the server's arithmetic, and
      // sending any here would be sending a number it is going to discard.
    },
    settlement: input.settlement,
  });
}

/** One item going out in exchange. */
export interface ReplacementLine {
  variantId: string;
  description: string;
  qty: string;
  unitPrice: string;
  taxTreatment: string;
}

/** Turns a scanned variant into a replacement line. */
export function replacementFrom(v: CachedVariant, qty = '1'): ReplacementLine {
  return {
    variantId: v.id,
    description: v.name || v.sku,
    qty,
    unitPrice: v.price,
    taxTreatment: v.taxTreatment || 'standard',
  };
}

/**
 * What the exchange looks like it will settle at.
 *
 * SHOWN, not sent. The cashier needs to tell the customer whether to reach for
 * their wallet before the button is pressed, and a screen that said nothing
 * until the server answered would be unusable at a counter.
 *
 * Positive means the customer pays; negative means the shop pays out. Computed
 * in minor units throughout — a preview that disagreed with the server by a
 * hallala because of a float would be worse than no preview, since the cashier
 * would have quoted it aloud.
 */
export function previewDifference(
  returnable: ReturnableLine[],
  returning: ReturnSelection[],
  replacement: ReplacementLine[],
): { credit: string; owed: string; customerPays: boolean } {
  let creditCents = 0;
  for (const pick of returning) {
    const line = returnable.find((l) => l.line_id === pick.lineId);
    if (!line) continue;

    const takingBack = Number(pick.qty);
    const available = Number(line.qty_returnable);
    if (!Number.isFinite(takingBack) || takingBack <= 0 || available <= 0) {
      continue;
    }
    // Proportional to the line's own returnable amount, not to quantity times
    // unit price. The two differ whenever an invoice-level discount was
    // allocated across the lines.
    const share = Math.min(takingBack, available) / available;
    creditCents += Math.round(minor(line.gross_returnable) * share);
  }

  let replacementCents = 0;
  for (const line of replacement) {
    const qty = Number(line.qty);
    if (!Number.isFinite(qty) || qty <= 0) continue;
    replacementCents += Math.round(minor(line.unitPrice) * qty);
  }

  const difference = replacementCents - creditCents;
  return {
    credit: major(creditCents),
    owed: major(Math.abs(difference)),
    customerPays: difference > 0,
  };
}

/** Whether the exchange is complete enough to send. */
export function readyToExchange(
  returning: ReturnSelection[],
  replacement: ReplacementLine[],
  reason: string,
): boolean {
  // Both halves must be present. Nothing coming back is a sale and nothing
  // going out is a return; both have their own screens, and the server refuses
  // either through this one.
  return (
    returning.length > 0 &&
    replacement.length > 0 &&
    // C14 requires a reason on every return, and an exchange contains one.
    reason.trim().length >= 3
  );
}

function minor(amount: string): number {
  const negative = amount.trimStart().startsWith('-');
  const [whole = '0', frac = ''] = amount.replace('-', '').split('.');
  const cents = Number(whole) * 100 + Number((frac + '00').slice(0, 2));
  return negative ? -cents : cents;
}

function major(cents: number): string {
  const sign = cents < 0 ? '-' : '';
  const abs = Math.abs(cents);
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`;
}
