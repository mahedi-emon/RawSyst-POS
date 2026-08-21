// One finalised invoice, UI spec §5.
//
// The mirror of the other api modules: the server owns the document and this
// reads it. Money and quantities arrive as decimal STRINGS and stay strings —
// re-deriving a total here would eventually disagree with the paper the
// customer is holding, and on a tax invoice that is not a rounding curiosity.
//
// # There is no update, and there never will be
//
// A finalised tax invoice is immutable. The API offers no PUT and no DELETE for
// one, and the screen explains why rather than hiding a disabled button. The
// correction path is a credit note, which is a new document of its own.

import type { Client } from './client';

export interface InvoiceLine {
  line_no: number;
  variant_id: string | null;
  description: string;
  description_ar: string | null;
  qty: string;
  unit_price: string;
  line_discount: string;
  /** This line's share of an invoice-level discount, allocated at sale time
   *  and stored — never reconstructed later, which is where drift comes from. */
  invoice_discount_alloc: string;
  tax_treatment: string;
  tax_rate: string;
  tax_amount: string;
  net_amount: string;
  gross_amount: string;
}

export interface InvoiceTender {
  tender_no: number;
  method: string;
  amount: string;
  reference: string | null;
  settlement_status: string;
}

/** The invoice's position on its terminal's ZATCA chain. Nothing here is
 *  secret — a counter and a hash. The key that turns them into a stamp never
 *  leaves the terminal's keystore. */
export interface InvoiceChain {
  icv: number;
  pih: string;
  invoice_hash: string;
  schema_version: string;
  /** Base64 TLV. Null until the terminal has signed and handed the document
   *  back, which is the ordinary state while the format is unverified. */
  qr_tlv: string | null;
  submitted_at: string | null;
  response_code: number | null;
  reject_reason: string | null;
}

export interface InvoiceCustomer {
  id: string;
  name: string;
}

export interface InvoiceAuditEntry {
  action: string;
  /** Denormalised server-side so it survives the user being deleted. */
  actor_label: string | null;
  occurred_at: string;
  device_label: string | null;
}

export interface Invoice {
  id: string;
  uuid: string;
  doc_type: string;
  human_number: string | null;
  state: string;

  issue_date: string;
  currency: string;

  subtotal_net: string;
  discount_total: string;
  tax_total: string;
  total_inclusive: string;

  lines: InvoiceLine[];
  tenders: InvoiceTender[];

  zatca: InvoiceChain | null;
  customer: InvoiceCustomer | null;
  audit: InvoiceAuditEntry[];

  /** Set on a credit or debit note: the invoice it corrects. A note without
   *  one has no meaning. */
  parent_invoice_id: string | null;
}

export function fetchInvoice(client: Client, invoiceId: string): Promise<Invoice> {
  return client.send<Invoice>('GET', `/api/v1/pos/sales/${invoiceId}`);
}

/**
 * Records that a copy was printed.
 *
 * Reprinting is not reissuing: no new document, no new number, no new chain
 * position. The only thing that changes is that the audit trail now says a copy
 * went out and who asked for it, which is the whole point of logging it.
 */
export function reprintInvoice(client: Client, invoiceId: string): Promise<void> {
  return client.send<void>('POST', `/api/v1/pos/sales/${invoiceId}/reprint`, {});
}
