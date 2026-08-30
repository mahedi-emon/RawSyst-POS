// Stock operations (blueprint B4).
//
// # Quantities are strings, like money
//
// A quantity here can be fractional — 2.5 kg of coffee, 0.75 m of fabric — and
// it is compared against a `numeric(18,4)` the database holds exactly. Parsing
// it into a JavaScript number to display it and back to send it is two chances
// to lose the last digit on a figure that decides whether a shelf is short.
//
// So quantities cross this boundary as strings and are never arithmetic in the
// browser. The one place the screen does arithmetic — showing a count's
// variance before it is posted — is a hint, and the server computes the figure
// that is actually posted.
//
// # The server decides what stock is worth, always
//
// No call here sends a cost. A person recording damage says how many were
// damaged; what those units were worth comes from the costing engine, for the
// same reason a till does not get to say what a sale cost. Gross profit has to
// be a measurement, and a measurement cannot come from the party being
// measured.

import type { Client } from './client';

/** A place stock can be. */
export interface StockLocation {
  id: string;
  code: string;
  name: string;
  /** `shop_floor`, `store_room` or `central`. The system's own `transit`
   *  location is never returned: it is not somewhere a person may choose. */
  kind: 'shop_floor' | 'store_room' | 'central';
  store?: string;
  is_active: boolean;
  /** Whether anything is on hand here. The screen needs it to explain, before
   *  the press rather than after, why a location cannot be retired. */
  holds_stock: boolean;
}

/** One product's position at one location. */
export interface StockLine {
  variant_id: string;
  sku: string;
  product: string;
  barcode?: string;
  location: string;
  on_hand: string;
  reorder_level?: string;
  below_minimum?: boolean;
}

/** One thing that happened to stock. */
export interface StockMovement {
  occurred_at: string;
  product: string;
  sku: string;
  location: string;
  /** One of the reasons migration 0020 fixed: sale, return, grn, adjustment,
   *  transfer_in, transfer_out, wastage, opening, count, internal_use. */
  reason: string;
  delta: string;
  value?: string;
  document?: string;
  note?: string;
}

/** One product's part of a voucher. */
export interface AdjustmentLine {
  variant_id: string;
  sku: string;
  product: string;
  system_qty: string;
  counted_qty?: string;
  delta?: string;
  value?: string;
  /** The system quantity changed between the sheet being opened and posted —
   *  somebody sold one mid-count. B4's flagged discrepancy, pointed at the
   *  discrepancy that is not one. */
  moved_while_counting?: boolean;
}

/** A correction, a write-off, or a count. */
export interface Adjustment {
  id: string;
  adjustment_no: string;
  kind: 'adjustment' | 'wastage' | 'count';
  reason: string;
  note?: string;
  status: 'draft' | 'posted' | 'cancelled';
  location: string;
  /** Signed: negative for value destroyed, positive for value found, zero for a
   *  count that came out exactly right. */
  value: string;
  currency: string;
  created_by?: string;
  created_at: string;
  posted_at?: string;
  lines?: AdjustmentLine[];
  already_recorded?: boolean;
}

/** One product's part of a transfer. */
export interface TransferLine {
  variant_id: string;
  sku: string;
  product: string;
  qty_requested: string;
  qty_dispatched?: string;
  qty_received?: string;
  value?: string;
  short?: string;
}

/** Stock moving between two of the company's own rooms. */
export interface Transfer {
  id: string;
  transfer_no: string;
  status: 'requested' | 'approved' | 'dispatched' | 'received' | 'cancelled';
  note?: string;
  from: string;
  to: string;
  requested_by?: string;
  requested_at: string;
  approved_by?: string;
  approved_at?: string;
  dispatched_at?: string;
  received_by?: string;
  received_at?: string;
  value?: string;
  currency: string;
  /** Dispatched but never confirmed. Non-zero means stock is unaccounted for
   *  and still sitting in transit. */
  short_by?: string;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

const query = (q: Record<string, string | boolean | undefined>) => {
  const parts = Object.entries(q)
    .filter(([, v]) => v !== undefined && v !== '' && v !== false)
    .map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`);
  return parts.length ? '?' + parts.join('&') : '';
};

// --- locations ------------------------------------------------------------

/** A branch a location can belong to. */
export interface Branch {
  id: string;
  code: string;
  name: string;
}

/** The locations, and the branches a new one can be attached to.
 *
 *  One reply rather than two requests. The other list of stores in this product
 *  sits behind `devices.view`, and a storeman naming the room they work in
 *  should not have to hold a permission for the terminals screen. */
export interface Places {
  data: StockLocation[];
  branches: Branch[];
}

export function listStockLocations(
  client: Client,
  companyId: string,
  includeRetired = false,
): Promise<Places> {
  return client.send<Places>(
    'GET',
    scoped('/api/v1/stock/locations' + query({ include_retired: includeRetired }), companyId),
  );
}

export function createStockLocation(
  client: Client,
  companyId: string,
  body: { code: string; name: string; kind: string; store_id?: string },
): Promise<StockLocation> {
  return client.send<StockLocation>(
    'POST',
    scoped('/api/v1/stock/locations', companyId),
    body,
  );
}

export function renameStockLocation(
  client: Client,
  companyId: string,
  id: string,
  name: string,
): Promise<void> {
  return client.send<void>(
    'PUT',
    scoped(`/api/v1/stock/locations/${id}`, companyId),
    { name },
  );
}

export function setStockLocationActive(
  client: Client,
  companyId: string,
  id: string,
  isActive: boolean,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/stock/locations/${id}/active`, companyId),
    { is_active: isActive },
  );
}

// --- levels and history ---------------------------------------------------

export function listStockOnHand(
  client: Client,
  companyId: string,
  q: { location_id?: string; q?: string; low?: boolean } = {},
): Promise<{ data: StockLine[] }> {
  return client.send<{ data: StockLine[] }>(
    'GET',
    scoped('/api/v1/stock/on-hand' + query(q), companyId),
  );
}

export function listStockMovements(
  client: Client,
  companyId: string,
  q: { location_id?: string; q?: string } = {},
): Promise<{ data: StockMovement[] }> {
  return client.send<{ data: StockMovement[] }>(
    'GET',
    scoped('/api/v1/stock/movements' + query(q), companyId),
  );
}

// --- adjustments and wastage ----------------------------------------------

export function listAdjustments(
  client: Client,
  companyId: string,
  q: { kind?: string; status?: string; location_id?: string } = {},
): Promise<{ data: Adjustment[] }> {
  return client.send<{ data: Adjustment[] }>(
    'GET',
    scoped('/api/v1/stock/adjustments' + query(q), companyId),
  );
}

export function readAdjustment(
  client: Client,
  companyId: string,
  id: string,
): Promise<Adjustment> {
  return client.send<Adjustment>(
    'GET',
    scoped(`/api/v1/stock/adjustments/${id}`, companyId),
  );
}

export function recordAdjustment(
  client: Client,
  companyId: string,
  body: {
    /** Assigned by the caller, so a retry after a lost response returns the
     *  original rather than writing the stock off twice. */
    uuid: string;
    location_id: string;
    kind: 'adjustment' | 'wastage';
    reason: string;
    note: string;
    lines: Array<{ variant_id: string; delta: string }>;
  },
): Promise<Adjustment> {
  return client.send<Adjustment>(
    'POST',
    scoped('/api/v1/stock/adjustments', companyId),
    body,
  );
}

// --- the physical count ---------------------------------------------------

export function openStockCount(
  client: Client,
  companyId: string,
  body: {
    location_id: string;
    category_id?: string;
    variant_ids?: string[];
    note?: string;
  },
): Promise<Adjustment> {
  return client.send<Adjustment>(
    'POST',
    scoped('/api/v1/stock/counts', companyId),
    body,
  );
}

export function saveStockCount(
  client: Client,
  companyId: string,
  id: string,
  lines: Array<{ variant_id: string; counted_qty: string }>,
): Promise<void> {
  return client.send<void>(
    'PUT',
    scoped(`/api/v1/stock/counts/${id}`, companyId),
    { lines },
  );
}

export function postStockCount(
  client: Client,
  companyId: string,
  id: string,
): Promise<Adjustment> {
  return client.send<Adjustment>(
    'POST',
    scoped(`/api/v1/stock/counts/${id}/post`, companyId),
  );
}

export function cancelStockCount(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/stock/counts/${id}/cancel`, companyId),
  );
}

// --- transfers ------------------------------------------------------------

export function listTransfers(
  client: Client,
  companyId: string,
  status = '',
): Promise<{ data: Transfer[] }> {
  return client.send<{ data: Transfer[] }>(
    'GET',
    scoped('/api/v1/stock/transfers' + query({ status }), companyId),
  );
}

export function readTransfer(
  client: Client,
  companyId: string,
  id: string,
): Promise<Transfer & { lines?: TransferLine[] }> {
  return client.send<Transfer & { lines?: TransferLine[] }>(
    'GET',
    scoped(`/api/v1/stock/transfers/${id}`, companyId),
  );
}

export function requestTransfer(
  client: Client,
  companyId: string,
  body: {
    from_location_id: string;
    to_location_id: string;
    note?: string;
    lines: Array<{ variant_id: string; qty: string }>;
  },
): Promise<Transfer> {
  return client.send<Transfer>(
    'POST',
    scoped('/api/v1/stock/transfers', companyId),
    body,
  );
}

/** The four steps B4 specifies, as four calls.
 *
 *  `dispatch` and `receive` take an optional list of quantities: a storeman who
 *  found four where six were asked for, and a branch confirming three of the
 *  four that were sent. Sending none means "all of it, as the document says",
 *  which is the ordinary case. */
export function advanceTransfer(
  client: Client,
  companyId: string,
  id: string,
  step: 'approve' | 'dispatch' | 'receive' | 'cancel',
  lines: Array<{ variant_id: string; qty: string }> = [],
): Promise<Transfer> {
  return client.send<Transfer>(
    'POST',
    scoped(`/api/v1/stock/transfers/${id}/${step}`, companyId),
    step === 'dispatch' || step === 'receive' ? { lines } : undefined,
  );
}
