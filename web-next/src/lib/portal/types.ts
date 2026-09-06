// What the portal routes answer.
//
// Read off `internal/portal/customer.go` and `internal/portal/supplier.go`.
// Every money figure is a string and stays one: the amounts a customer is
// shown are the amounts on their invoice, and a float would eventually
// disagree with the paper.

/** The customer's own summary — `GET /portal/me`, under `me`. */
export interface Me {
  name: string;
  phone?: string;
  email?: string;
  currency: string;
  /** What they owe the shop. */
  outstanding: string;
  /** What the shop holds for them. Kept apart from gift cards, because a card
   *  can be given away and a credit cannot. */
  store_credit: string;
  gift_card_balance: string;
  /** False for a shop that runs no scheme, which is not the same as a member
   *  with no points. */
  loyalty_enrolled: boolean;
  points: number;
  tier?: string;
}

export interface PortalInvoice {
  id: string;
  human_number: string;
  issued_at: string;
  total: string;
  paid: string;
  currency: string;
  outstanding: string;
}

export interface PortalOrder {
  id: string;
  order_no: string;
  state: string;
  placed_at: string;
  total: string;
  currency: string;
  /** The delivery, when there is one. A customer does not distinguish order
   *  status from delivery tracking, so the screen does not either. */
  delivery_status?: string;
  driver_name?: string;
  delivered_at?: string;
}

export interface Warranty {
  serial_no: string;
  product?: string;
  status: string;
  sold_on?: string;
  expires_on?: string;
  in_warranty: boolean;
}

export interface PortalAddress {
  id: string;
  label: string;
  line1: string;
  line2?: string;
  city?: string;
  district?: string;
  postcode?: string;
  country?: string;
  phone?: string;
  is_default: boolean;
}

export interface ReturnRequest {
  id: string;
  request_no: string;
  invoice_id?: string;
  invoice_no?: string;
  kind: string;
  reason: string;
  items: string;
  status: string;
  decision_note?: string;
  created_at: string;
  decided_at?: string;
}

/** `GET /portal/supplier/home`, under `home`. */
export interface SupplierHome {
  supplier_name: string;
  contact_name: string;
  currency: string;
  /** Orders waiting for an answer — the figure the portal exists to surface. */
  awaiting_response: number;
  open_orders: number;
  outstanding: string;
  overdue: string;
  open_rfqs: number;
}

export interface SupplierOrderLine {
  line_no: number;
  sku?: string;
  description: string;
  qty_ordered: string;
  qty_received: string;
  unit_cost: string;
  gross_amount: string;
}

export interface SupplierOrder {
  id: string;
  po_number: string;
  status: string;
  ordered_on?: string;
  expected_on?: string;
  currency: string;
  total: string;
  /** Their own answer, when they have given one. */
  response?: string;
  comment?: string;
  promised_on?: string;
  responded_at?: string;
  lines?: SupplierOrderLine[];
}

export interface SupplierBill {
  id: string;
  supplier_ref?: string;
  bill_date: string;
  due_on?: string;
  currency: string;
  gross_total: string;
  paid_total: string;
  outstanding: string;
  status: string;
  overdue: boolean;
}

export interface SupplierRFQ {
  id: string;
  rfq_no: string;
  status: string;
  closes_on?: string;
  note?: string;
  quoted: boolean;
  quoted_on?: string;
  quote_total?: string;
}

/** What a supplier may answer a purchase order with. */
export type OrderResponse = 'accepted' | 'accepted_with_changes' | 'rejected';

export const ORDER_RESPONSES: readonly OrderResponse[] = [
  'accepted',
  'accepted_with_changes',
  'rejected',
];
