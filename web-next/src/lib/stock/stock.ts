// Stock as it moves, and the four documents that move it.
//
// Every figure here is a quantity rather than money, with one exception: an
// adjustment carries the VALUE of what was written off, because that is the
// number a manager reads when they are deciding whether somebody is writing too
// much off. The route note is explicit that reading what was written off is
// gated on `inventory.view` rather than on the verb that does the writing,
// "because gating it behind the verb that DOES the writing would hide it from
// exactly the person checking".

import type { Tone } from '@/components/ui/panel';
import type { Key } from '@/lib/i18n/locale';

/** Somewhere stock can be. */
export interface StockLocation {
  id: string;
  code: string;
  name: string;
  kind: string;
  store?: string;
  is_active: boolean;
  /** False for a location that exists but holds nothing yet. */
  holds_stock: boolean;
}

/** A branch. `/stock/locations` returns these beside the locations. */
export interface Branch {
  id: string;
  code: string;
  name: string;
}

/** One thing that happened to stock. The ledger, not a balance. */
export interface Movement {
  occurred_at: string;
  product: string;
  sku: string;
  location: string;
  /** Why it moved: `grn`, `sale`, `adjustment`, `transfer_out`... */
  reason: string;
  /** Signed. Negative left, positive arrived. */
  delta: string;
  value: string;
}

export interface AdjustmentLine {
  variant_id: string;
  sku: string;
  product: string;
  /** What the system believed was there when the document was raised. */
  system_qty: string;
  /** Empty on an open count until somebody has counted that line. */
  delta: string;
  value: string;
}

export interface Adjustment {
  id: string;
  adjustment_no: string;
  /** `adjustment`, `wastage` or `count`. */
  kind: string;
  reason: string;
  note?: string;
  status: string;
  location: string;
  value: string;
  currency: string;
  created_by?: string;
  created_at: string;
  posted_at?: string;
  lines?: AdjustmentLine[];
}

export interface TransferLine {
  variant_id: string;
  sku: string;
  product: string;
  qty_requested: string;
  qty_dispatched?: string;
  qty_received?: string;
}

export interface Transfer {
  id: string;
  transfer_no: string;
  status: string;
  note?: string;
  from: string;
  to: string;
  requested_by?: string;
  requested_at: string;
  approved_by?: string;
  currency: string;
  lines?: TransferLine[];
}

export interface Batch {
  id?: string;
  variant_id: string;
  sku?: string;
  product?: string;
  batch_no: string;
  manufactured_on?: string;
  expires_on?: string;
  qty_remaining: string;
  location?: string;
}

/** On hand, held for somebody, and what is genuinely free to sell. */
export interface Availability {
  variant_id: string;
  on_hand: string;
  reserved: string;
  available_to_sell: string;
}

/**
 * Why stock was adjusted.
 *
 * `kind` says what sort of document it is and `reason` says why. Both come from
 * the backend, and an unknown one is shown as written rather than guessed at.
 */
export const ADJUSTMENT_KIND: Record<string, { key: Key; tone: Tone }> = {
  adjustment: { key: 'nx.adj.kAdjustment', tone: 'info' },
  wastage: { key: 'nx.adj.kWastage', tone: 'critical' },
  count: { key: 'nx.adj.kCount', tone: 'neutral' },
};

export const ADJUSTMENT_STATUS: Record<string, { key: Key; tone: Tone }> = {
  draft: { key: 'nx.adj.stDraft', tone: 'neutral' },
  posted: { key: 'nx.adj.stPosted', tone: 'positive' },
  cancelled: { key: 'nx.adj.stCancelled', tone: 'neutral' },
};

/**
 * The reasons a shop writes stock off.
 *
 * B4 requires a mandatory reason on a write-off, and a free box would give
 * "damaged", "Damaged" and "brokn" in one report. Offered as a list, with the
 * note beside it for what the list cannot say.
 */
export const ADJUSTMENT_REASONS: ReadonlyArray<{ value: string; key: Key }> = [
  { value: 'damaged', key: 'nx.adj.rDamaged' },
  { value: 'expired', key: 'nx.adj.rExpired' },
  { value: 'lost', key: 'nx.adj.rLost' },
  { value: 'theft', key: 'nx.adj.rTheft' },
  { value: 'found', key: 'nx.adj.rFound' },
  { value: 'correction', key: 'nx.adj.rCorrection' },
];

export const TRANSFER_STATUS: Record<string, { key: Key; tone: Tone }> = {
  requested: { key: 'nx.trf.stRequested', tone: 'info' },
  approved: { key: 'nx.trf.stApproved', tone: 'info' },
  in_transit: { key: 'nx.trf.stInTransit', tone: 'caution' },
  received: { key: 'nx.trf.stReceived', tone: 'positive' },
  cancelled: { key: 'nx.trf.stCancelled', tone: 'neutral' },
};

/** What each location kind is called. `transit` is the system's own. */
export const LOCATION_KIND: Record<string, Key> = {
  shop_floor: 'nx.loc.kShopFloor',
  store_room: 'nx.loc.kStoreRoom',
  central: 'nx.loc.kCentral',
  transit: 'nx.loc.kTransit',
};

/** Whether a movement added stock or took it away. */
export function isIncoming(delta: string): boolean {
  return !delta.trim().startsWith('-');
}

/** The next step a transfer is waiting for, or null when it is finished. */
export function nextStep(status: string): 'approve' | 'dispatch' | 'receive' | null {
  switch (status) {
    case 'requested':
      return 'approve';
    case 'approved':
      return 'dispatch';
    case 'in_transit':
      return 'receive';
    default:
      return null;
  }
}
