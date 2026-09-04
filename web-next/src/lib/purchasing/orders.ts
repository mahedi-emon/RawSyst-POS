// The shapes the purchasing screens read, and the words for the codes in them.
//
// Kept together because three screens share them -- the list, one order, and
// receiving against one -- and a status spelled two ways in two places is how
// a badge ends up saying something the row does not.

import type { Key } from '@/lib/i18n/locale';
import type { Tone } from '@/components/ui/panel';

export interface OrderLine {
  id: string;
  line_no: number;
  variant_id: string;
  description: string;
  qty_ordered: string;
  qty_received: string;
  /** Never negative: over-receiving reports zero outstanding, not -2. */
  qty_outstanding: string;
  qty_billed: string;
  unit_cost: string;
  /** Empty on orders raised before migration 0125. */
  tax_treatment: string;
  /** A FRACTION -- "0.150000" is fifteen per cent, as everywhere else. */
  tax_rate: string;
  /**
   * Whether booking this line in must name the supplier lot.
   *
   * Added by migration 0126. Before it, a receiving screen could only find
   * out which lines needed one by submitting the delivery and reading the
   * error -- which means typing a whole pallet and then being told.
   */
  tracks_batches: boolean;
  net_amount: string;
  tax_amount: string;
  gross_amount: string;
}

export interface Order {
  id: string;
  po_number: string;
  supplier_id: string;
  supplier: string;
  warehouse_id: string;
  status: string;
  ordered_on: string;
  expected_on?: string;
  currency: string;
  subtotal_net: string;
  tax_total: string;
  total_inclusive: string;
  notes?: string;
  /** Only on `GET /purchasing/orders/{id}`; the list does not carry them. */
  lines?: OrderLine[];
}

/**
 * The six the table allows, in the order an order passes through them.
 *
 * `po_status_valid` is draft, issued, receiving, received, closed, cancelled --
 * so there is no seventh to guess at, and anything else means the backend has
 * changed under this screen.
 */
export const ORDER_STATUS: Record<string, { key: Key; tone: Tone }> = {
  draft: { key: 'nx.po.stDraft', tone: 'neutral' },
  issued: { key: 'nx.po.stIssued', tone: 'info' },
  receiving: { key: 'nx.po.stReceiving', tone: 'caution' },
  received: { key: 'nx.po.stReceived', tone: 'positive' },
  closed: { key: 'nx.po.stClosed', tone: 'neutral' },
  cancelled: { key: 'nx.po.stCancelled', tone: 'critical' },
};

/** The order the filter offers them in, which is the order they happen in. */
export const ORDER_STATUSES = [
  'draft',
  'issued',
  'receiving',
  'received',
  'closed',
  'cancelled',
] as const;

/** How a line is taxed. Empty means an order raised before 0125 stored it. */
export const TAX_TREATMENT: Record<string, Key> = {
  standard: 'nx.po.txStandard',
  zero_rated: 'nx.po.txZero',
  exempt: 'nx.po.txExempt',
};

/**
 * Whether an order can still be changed.
 *
 * `PUT /purchasing/orders/{id}` refuses anything but a draft -- "an issued
 * order is a commitment the supplier can hold you to" -- so the screen offers
 * no edit rather than one that comes back 400.
 */
export function isEditable(status: string): boolean {
  return status === 'draft';
}

/** Whether goods can still be booked in against it. */
export function canReceive(status: string): boolean {
  return status === 'issued' || status === 'receiving';
}
