// Asking for stock, asking suppliers what they would charge, and choosing one.
//
// B5.1 splits this into four permissions on purpose: anybody trusted with stock
// may ASK, approving somebody else's request is a manager's act, running the
// comparison is the buyer's job, and awarding commits the business. "Who chose
// this supplier, and why" is the question the whole module exists to answer,
// and it is worth less if the person who ran the comparison also signed it off.

import type { Tone } from '@/components/ui/panel';
import type { Key } from '@/lib/i18n/locale';

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
  status: string;
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
  /** Only on `GET .../{id}`; the list does not carry them. */
  lines?: RequisitionLine[];
}

export interface RFQLine {
  id: string;
  line_no: number;
  variant_id: string;
  sku?: string;
  description: string;
  qty: string;
}

/** A supplier who was asked, and what came back. */
export interface RFQInvited {
  supplier_id: string;
  supplier_name: string;
  invited_at: string;
  quoted: boolean;
  /**
   * When they said no.
   *
   * Recorded because a missing quote cannot tell you the difference between a
   * supplier who declined and one who never replied, and those are different
   * facts about a supplier.
   */
  declined_at?: string;
  decline_reason?: string;
}

export interface RFQ {
  id: string;
  rfq_number: string;
  status: string;
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
  /** A second reply supersedes the first rather than overwriting it. */
  revision: number;
  status: string;
  received_on: string;
  valid_until?: string;
  /**
   * Derived from the date rather than stored.
   *
   * "A quote does not become a different document at midnight; it simply stops
   * being usable, and a stored flag would need a job to keep true."
   */
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
  /** Live quotes only. A superseded price is not one anybody is offering. */
  quotes: Quote[];
  /**
   * The cheapest live, unexpired quote by total.
   *
   * A convenience for the eye and explicitly NOT a recommendation: B5.1
   * requires a person to choose and to say why, because lead time and payment
   * terms routinely outweigh price. The screen labels it as the lowest total
   * and never as the best.
   */
  lowest_quote_id?: string;
}

/** What a requisition can be. `ordered` means an RFQ from it was awarded. */
export const REQUISITION_STATUS: Record<string, { key: Key; tone: Tone }> = {
  submitted: { key: 'nx.req.stSubmitted', tone: 'info' },
  approved: { key: 'nx.req.stApproved', tone: 'positive' },
  rejected: { key: 'nx.req.stRejected', tone: 'critical' },
  ordered: { key: 'nx.req.stOrdered', tone: 'neutral' },
  cancelled: { key: 'nx.req.stCancelled', tone: 'neutral' },
};

export const RFQ_STATUS: Record<string, { key: Key; tone: Tone }> = {
  draft: { key: 'nx.rfq.stDraft', tone: 'neutral' },
  issued: { key: 'nx.rfq.stIssued', tone: 'info' },
  awarded: { key: 'nx.rfq.stAwarded', tone: 'positive' },
  cancelled: { key: 'nx.rfq.stCancelled', tone: 'neutral' },
};

/** A decision can only be made on a request nobody has decided yet. */
export function awaitingDecision(status: string): boolean {
  return status === 'submitted';
}

/** An RFQ can only be awarded while it is out and unawarded. */
export function canAward(status: string): boolean {
  return status === 'issued';
}

/**
 * Whether this quote can be awarded.
 *
 * An expired one cannot: "the supplier is no longer offering that price, and an
 * order raised against it would be a commitment the shop has no grounds to
 * expect them to honour."
 */
export function canAwardQuote(quote: Quote): boolean {
  return !quote.expired && quote.status === 'received';
}
