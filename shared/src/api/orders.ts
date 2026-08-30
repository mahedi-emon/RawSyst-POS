// Quotations, sales orders and the warehouse documents (blueprint B11, B12).
//
// # The delivery note has no prices, in the type
//
// `OrderDocument` carries no price fields at all. Not optional ones the screen
// declines to render — absent, so nothing can put them back by accident. B11
// says the delivery challan is "itemized without pricing", and that paper is
// handled by a driver, a courier and whoever signs for the goods.

import type { Client } from './client';

/** The lifecycle B11 draws, plus the two ends. */
export type OrderState =
  | 'quotation'
  | 'confirmed'
  | 'processing'
  | 'packed'
  | 'delivered'
  | 'completed'
  | 'cancelled';

export interface OrderLine {
  id: string;
  line_no: number;
  variant_id: string;
  sku: string;
  product: string;
  description?: string;
  qty: string;
  unit_price: string;
  discount: string;
  line_total: string;
  qty_picked: string;
  qty_delivered: string;
}

export interface Order {
  id: string;
  order_no: string;
  state: OrderState;
  channel: string;
  region?: string;
  currency: string;

  customer_id?: string;
  customer?: string;
  store?: string;

  valid_until?: string;
  deliver_to?: string;
  deliver_phone?: string;
  notes?: string;

  subtotal: string;
  discount: string;
  total: string;

  invoice_id?: string;
  invoice_no?: string;

  created_by?: string;
  created_at: string;
  cancel_reason?: string;

  /** A quotation whose validity date has passed. Derived by the server, not
   *  stored — a quote does not become a different row at midnight. */
  expired?: boolean;

  lines?: OrderLine[];
}

/** A picking slip, a packing slip or a delivery note. No prices anywhere. */
export interface OrderDocument {
  kind: 'picking' | 'packing' | 'delivery';
  order_no: string;
  customer?: string;
  deliver_to?: string;
  deliver_phone?: string;
  store?: string;
  printed_at: string;
  note?: string;
  lines: Array<{
    line_no: number;
    sku: string;
    barcode?: string;
    product: string;
    description?: string;
    location?: string;
    qty: string;
  }>;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

const query = (q: Record<string, string | boolean | undefined>) => {
  const parts = Object.entries(q)
    .filter(([, v]) => v !== undefined && v !== '' && v !== false)
    .map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`);
  return parts.length ? '?' + parts.join('&') : '';
};

export function listOrders(
  client: Client,
  companyId: string,
  q: { state?: string; channel?: string; open?: boolean; customer_id?: string } = {},
): Promise<{ data: Order[] }> {
  return client.send<{ data: Order[] }>(
    'GET',
    scoped('/api/v1/orders' + query(q), companyId),
  );
}

export function readOrder(
  client: Client,
  companyId: string,
  id: string,
): Promise<Order> {
  return client.send<Order>('GET', scoped(`/api/v1/orders/${id}`, companyId));
}

/** Always raised as a quotation. Confirming is the customer's decision. */
export function raiseOrder(
  client: Client,
  companyId: string,
  body: {
    customer_id?: string;
    store_id?: string;
    channel?: string;
    region?: string;
    valid_until?: string;
    deliver_to?: string;
    deliver_phone?: string;
    notes?: string;
    lines: Array<{
      variant_id: string;
      description?: string;
      qty: string;
      unit_price: string;
      discount?: string;
    }>;
  },
): Promise<Order> {
  return client.send<Order>('POST', scoped('/api/v1/orders', companyId), body);
}

/** One step along the lifecycle. Forward only. */
export function advanceOrder(
  client: Client,
  companyId: string,
  id: string,
): Promise<Order> {
  return client.send<Order>(
    'POST',
    scoped(`/api/v1/orders/${id}/advance`, companyId),
  );
}

export function cancelOrder(
  client: Client,
  companyId: string,
  id: string,
  reason: string,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/orders/${id}/cancel`, companyId),
    { reason },
  );
}

export function recordPicked(
  client: Client,
  companyId: string,
  id: string,
  lines: Array<{ line_id: string; qty: string }>,
): Promise<Order> {
  return client.send<Order>(
    'POST',
    scoped(`/api/v1/orders/${id}/pick`, companyId),
    { lines },
  );
}

export function recordDelivered(
  client: Client,
  companyId: string,
  id: string,
  lines: Array<{ line_id: string; qty: string }>,
): Promise<Order> {
  return client.send<Order>(
    'POST',
    scoped(`/api/v1/orders/${id}/deliver`, companyId),
    { lines },
  );
}

export function orderDocument(
  client: Client,
  companyId: string,
  id: string,
  kind: 'picking' | 'packing' | 'delivery',
): Promise<OrderDocument> {
  return client.send<OrderDocument>(
    'GET',
    scoped(`/api/v1/orders/${id}/documents/${kind}`, companyId),
  );
}
