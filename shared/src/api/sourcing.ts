// Requisitions, RFQs, supplier quotes and the award (blueprint B5, B5.1).
//
// # The comparison carries no winner
//
// `Comparison.lowest_quote_id` names the cheapest quote that could actually be
// accepted today, and that is all it is: a convenience for the eye. B5.1 wants
// a person to choose and to say why, because lead time and payment terms
// routinely outweigh price — so nothing here ranks, recommends, or pre-selects.

import type { Client } from './client';

export type RequisitionStatus =
  | 'draft'
  | 'submitted'
  | 'approved'
  | 'rejected'
  | 'sourcing'
  | 'ordered'
  | 'cancelled';

export interface RequisitionLine {
  id: string;
  line_no: number;
  variant_id?: string;
  sku?: string;
  description: string;
  qty_requested: string;
  note?: string;
}

export interface Requisition {
  id: string;
  requisition_no: string;
  status: RequisitionStatus;
  needed_by?: string;
  justification?: string;

  store_id?: string;
  store_name?: string;
  warehouse_id?: string;

  requested_by?: string;
  requested_at: string;
  decided_by?: string;
  decided_at?: string;
  decision_note?: string;

  lines?: RequisitionLine[];
}

export type RFQStatus =
  | 'draft'
  | 'issued'
  | 'comparing'
  | 'awarded'
  | 'cancelled';

export interface RFQLine {
  id: string;
  line_no: number;
  variant_id: string;
  sku?: string;
  description: string;
  qty: string;
}

export interface RFQInvited {
  supplier_id: string;
  supplier_name: string;
  invited_at: string;
  /** Asked and answered. Distinct from `declined_at`, because "asked and never
   *  replied" is information a buyer needs about a supplier. */
  quoted: boolean;
  declined_at?: string;
  decline_reason?: string;
}

export interface RFQ {
  id: string;
  rfq_number: string;
  status: RFQStatus;
  requisition_id?: string;
  warehouse_id: string;
  closes_on?: string;
  currency: string;
  notes?: string;

  awarded_quote_id?: string;
  award_reason?: string;
  awarded_by?: string;
  awarded_at?: string;
  cancel_reason?: string;

  created_at: string;
  issued_at?: string;

  lines?: RFQLine[];
  invited?: RFQInvited[];
}

export interface QuoteLine {
  id: string;
  rfq_line_id: string;
  line_no: number;
  variant_id: string;
  sku?: string;
  description: string;
  qty: string;
  unit_cost: string;
  tax_treatment: string;
  net_amount: string;
  tax_amount: string;
  gross_amount: string;
}

export interface Quote {
  id: string;
  rfq_id: string;
  supplier_id: string;
  supplier_name: string;
  quote_number?: string;
  revision: number;
  status: 'received' | 'superseded' | 'awarded' | 'rejected';

  received_on: string;
  valid_until?: string;
  /** Derived by comparing the validity date to today, never stored: a quote
   *  does not become a different document at midnight. */
  expired: boolean;

  currency: string;
  lead_time_days?: number;
  payment_terms_days?: number;
  quality_note?: string;

  subtotal_net: string;
  tax_total: string;
  total_inclusive: string;
  notes?: string;

  lines?: QuoteLine[];
}

export interface Comparison {
  rfq: RFQ;
  quotes: Quote[];
  lowest_quote_id?: string;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

const query = (q: Record<string, string | undefined>) => {
  const parts = Object.entries(q)
    .filter(([, v]) => v !== undefined && v !== '')
    .map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`);
  return parts.length ? '?' + parts.join('&') : '';
};

// --- Requisitions --------------------------------------------------------

export function listRequisitions(
  client: Client,
  companyId: string,
  q: { status?: string } = {},
): Promise<{ requisitions: Requisition[] }> {
  return client.send<{ requisitions: Requisition[] }>(
    'GET',
    scoped('/api/v1/purchasing/requisitions' + query(q), companyId),
  );
}

export function readRequisition(
  client: Client,
  companyId: string,
  id: string,
): Promise<Requisition> {
  return client.send<Requisition>(
    'GET',
    scoped(`/api/v1/purchasing/requisitions/${id}`, companyId),
  );
}

export function raiseRequisition(
  client: Client,
  companyId: string,
  body: {
    store_id?: string;
    warehouse_id?: string;
    needed_by?: string;
    justification?: string;
    lines: Array<{
      variant_id?: string;
      description?: string;
      qty: string;
      note?: string;
    }>;
  },
): Promise<Requisition> {
  return client.send<Requisition>(
    'POST',
    scoped('/api/v1/purchasing/requisitions', companyId),
    body,
  );
}

export function decideRequisition(
  client: Client,
  companyId: string,
  id: string,
  approve: boolean,
  note?: string,
): Promise<Requisition> {
  return client.send<Requisition>(
    'POST',
    scoped(`/api/v1/purchasing/requisitions/${id}/decision`, companyId),
    { approve, note },
  );
}

// --- RFQ -----------------------------------------------------------------

export function listRFQs(
  client: Client,
  companyId: string,
  q: { status?: string } = {},
): Promise<{ rfqs: RFQ[] }> {
  return client.send<{ rfqs: RFQ[] }>(
    'GET',
    scoped('/api/v1/purchasing/rfqs' + query(q), companyId),
  );
}

export function raiseRFQ(
  client: Client,
  companyId: string,
  body: {
    requisition_id?: string;
    warehouse_id: string;
    closes_on?: string;
    notes?: string;
    supplier_ids: string[];
    lines: Array<{ variant_id: string; description?: string; qty: string }>;
  },
): Promise<RFQ> {
  return client.send<RFQ>(
    'POST',
    scoped('/api/v1/purchasing/rfqs', companyId),
    body,
  );
}

export function compareQuotes(
  client: Client,
  companyId: string,
  rfqId: string,
): Promise<Comparison> {
  return client.send<Comparison>(
    'GET',
    scoped(`/api/v1/purchasing/rfqs/${rfqId}/comparison`, companyId),
  );
}

export function recordQuote(
  client: Client,
  companyId: string,
  rfqId: string,
  body: {
    supplier_id: string;
    quote_number?: string;
    received_on?: string;
    valid_until?: string;
    lead_time_days?: number;
    payment_terms_days?: number;
    quality_note?: string;
    notes?: string;
    lines: Array<{
      rfq_line_id: string;
      qty: string;
      unit_cost: string;
      tax_treatment?: string;
      tax_rate?: string;
      note?: string;
    }>;
  },
): Promise<Quote> {
  return client.send<Quote>(
    'POST',
    scoped(`/api/v1/purchasing/rfqs/${rfqId}/quotes`, companyId),
    body,
  );
}

export function declineToQuote(
  client: Client,
  companyId: string,
  rfqId: string,
  supplierId: string,
  reason?: string,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/purchasing/rfqs/${rfqId}/declines`, companyId),
    { supplier_id: supplierId, reason },
  );
}

/** Picks the winner and raises the purchase order. The reason is mandatory:
 *  it is the record an audit reads, and cheapest does not always win. */
export function awardQuote(
  client: Client,
  companyId: string,
  rfqId: string,
  quoteId: string,
  reason: string,
): Promise<{ rfq: RFQ; order: { id: string; po_number: string } }> {
  return client.send(
    'POST',
    scoped(`/api/v1/purchasing/rfqs/${rfqId}/award`, companyId),
    { quote_id: quoteId, reason },
  );
}

export function cancelRFQ(
  client: Client,
  companyId: string,
  rfqId: string,
  reason: string,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/purchasing/rfqs/${rfqId}/cancel`, companyId),
    { reason },
  );
}

/** B5.1's archive: what this supplier has quoted before, won or lost. */
export function supplierQuoteHistory(
  client: Client,
  companyId: string,
  supplierId: string,
): Promise<{ quotes: Quote[] }> {
  return client.send<{ quotes: Quote[] }>(
    'GET',
    scoped(`/api/v1/purchasing/suppliers/${supplierId}/quotes`, companyId),
  );
}
