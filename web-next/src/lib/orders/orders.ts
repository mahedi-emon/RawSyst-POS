// An order, from the quotation a customer has not agreed to yet through to the
// invoice that closes it.
//
// # One path forward, and no way back
//
// `forward` in the Go is a map rather than a switch, "so the whole graph is one
// thing a reader can see, and so Advance cannot grow a branch that quietly
// allows a step backwards". The same map is here, for the same reason: a screen
// that could offer a step the server refuses is a screen that has its own
// opinion about the workflow.
//
// # It always starts as a quotation
//
// The route note: "confirming is the customer's decision, and a route that
// could skip it would put 'the customer agreed' in the hands of whoever typed
// the order." So there is no "create confirmed" anywhere in this module.

import type { Tone } from '@/components/ui/panel';
import type { Key } from '@/lib/i18n/locale';

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
  state: string;
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
  /** Derived from the date rather than stored: a quote does not become a
   *  different row at midnight. */
  expired?: boolean;
  lines?: OrderLine[];
}

export const ORDER_STATE: Record<string, { key: Key; tone: Tone }> = {
  quotation: { key: 'nx.ord.stQuotation', tone: 'neutral' },
  confirmed: { key: 'nx.ord.stConfirmed', tone: 'info' },
  processing: { key: 'nx.ord.stProcessing', tone: 'info' },
  packed: { key: 'nx.ord.stPacked', tone: 'caution' },
  delivered: { key: 'nx.ord.stDelivered', tone: 'positive' },
  completed: { key: 'nx.ord.stCompleted', tone: 'positive' },
  cancelled: { key: 'nx.ord.stCancelled', tone: 'critical' },
};

export const ORDER_STATES = [
  'quotation',
  'confirmed',
  'processing',
  'packed',
  'delivered',
  'completed',
  'cancelled',
] as const;

/**
 * The one path through the states, mirroring `forward` in the Go.
 *
 * Nothing after `delivered` advances: completing an order is what invoicing it
 * does, and cancelling is its own route.
 */
const FORWARD: Record<string, string> = {
  quotation: 'confirmed',
  confirmed: 'processing',
  processing: 'packed',
  packed: 'delivered',
};

/** The state this order would move to, or null when nothing advances it. */
export function nextState(state: string): string | null {
  return FORWARD[state] ?? null;
}

/** Picking happens once it is being worked on, and before it goes out. */
export function canPick(state: string): boolean {
  return state === 'processing' || state === 'packed';
}

/** Delivering is what turns packed into delivered. */
export function canDeliver(state: string): boolean {
  return state === 'packed';
}

/**
 * Invoicing is the last step, and it needs the goods to have gone.
 *
 * "Bills a delivered order and completes it: B11's last step, which had no
 * route at all, so an order could never leave delivered."
 */
export function canInvoice(state: string): boolean {
  return state === 'delivered';
}

/** Anything that has not gone out or been settled can still be called off. */
export function canCancel(state: string): boolean {
  return state !== 'completed' && state !== 'cancelled' && state !== 'delivered';
}
