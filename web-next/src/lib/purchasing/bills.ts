// Supplier invoices, and the evidence of what the match found.

import type { Tone } from '@/components/ui/panel';
import type { Key } from '@/lib/i18n/locale';

/** One comparison the match made between invoice, order and delivery. */
export interface MatchLine {
  dimension: string;
  description?: string;
  ordered?: string;
  received?: string;
  /**
   * How much of what arrived earlier invoices already claimed.
   *
   * Reported beside `received` rather than netted into it, because somebody
   * looking at a held-back invoice needs both facts: the goods did arrive, and
   * they have already been invoiced once.
   */
  previously_billed?: string;
  billed?: string;
  variance: string;
  variance_pct?: string;
  outcome: string;
  /** Plain English from the server, saying what the numbers mean. */
  detail?: string;
}

export interface Bill {
  id: string;
  supplier_id: string;
  supplier: string;
  supplier_ref: string;
  po_id?: string;
  po_number?: string;
  bill_date: string;
  due_date: string;
  currency: string;
  subtotal_net: string;
  tax_total: string;
  total_inclusive: string;
  amount_paid: string;
  outstanding: string;
  status: string;
  /** Kept rather than recomputed, so the control can be audited. */
  match?: MatchLine[];
  /** False for a held-back bill: recorded, and deliberately not in the ledger. */
  posted: boolean;
  already_recorded?: boolean;
}

/** The six `bill_status_valid` allows, in the order a bill passes through. */
export const BILL_STATUS: Record<string, { key: Key; tone: Tone }> = {
  draft: { key: 'nx.bill.stDraft', tone: 'neutral' },
  matched: { key: 'nx.bill.stMatched', tone: 'positive' },
  blocked: { key: 'nx.bill.stBlocked', tone: 'critical' },
  approved: { key: 'nx.bill.stApproved', tone: 'info' },
  paid: { key: 'nx.bill.stPaid', tone: 'positive' },
  cancelled: { key: 'nx.bill.stCancelled', tone: 'neutral' },
};

export const BILL_STATUSES = [
  'draft',
  'matched',
  'blocked',
  'approved',
  'paid',
  'cancelled',
] as const;

/** The four things the match compares. */
export const MATCH_DIMENSION: Record<string, Key> = {
  qty: 'nx.bill.dimQty',
  price: 'nx.bill.dimPrice',
  tax: 'nx.bill.dimTax',
  total: 'nx.bill.dimTotal',
};

/**
 * Only a blocked bill can be approved.
 *
 * `ApproveBill` answers 409 for anything else — "that bill is matched, so there
 * is nothing to approve" — so the screen offers no button rather than one that
 * gets refused.
 */
export function canAccept(status: string): boolean {
  return status === 'blocked';
}
