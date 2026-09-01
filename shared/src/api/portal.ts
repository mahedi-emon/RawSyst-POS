// The customer self-service portal and the supplier portal (blueprint F2, F3).
//
// # A portal client is not the staff client
//
// `Client` carries a staff session and refreshes it through a cookie. A portal
// caller has neither: they hold a bearer token issued by the portal's own
// sign-in, against one shop, and it is not a credential anywhere else in the
// product.
//
// So these functions take the token explicitly rather than reading it from a
// session context. That is deliberate — the type makes it impossible to call a
// portal route with a staff session by accident, and impossible to call a staff
// route with a portal token.
//
// # The shop is in every URL
//
// A portal belongs to a shop: the sign-in page is the shop's, and a customer of
// one branch of a group is not a customer of another. The tenant and company
// are therefore in the query string of every call, and the server only matches
// a session whose portal user belongs to the company being named.

/** Which shop's portal this is. Comes from the link the shop published. */
export interface PortalShop {
  tenantId: string;
  companyId: string;
}

const at = (shop: PortalShop, path: string) =>
  `${path}${path.includes('?') ? '&' : '?'}tenant_id=${shop.tenantId}` +
  `&company_id=${shop.companyId}`;

/** What went wrong, in the words the server chose. */
export class PortalFailed extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'PortalFailed';
    this.status = status;
  }
}

async function call<T>(
  method: string,
  url: string,
  token: string | null,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers.Authorization = `Bearer ${token}`;

  const res = await fetch(url, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  const payload = text ? (JSON.parse(text) as Record<string, unknown>) : {};
  if (!res.ok) {
    // The server's own sentence, in the reader's language, or nothing. Never a
    // sentence invented here: this module has no locale, and each screen
    // already falls back to its own translated words when the message is
    // empty.
    const err = payload.error as { message?: string } | undefined;
    throw new PortalFailed(res.status, err?.message ?? '');
  }
  return payload as T;
}

// --- signing in -----------------------------------------------------------

/** Asks for a code. Answers the same way whether or not the number is known. */
export function requestCode(shop: PortalShop, phone: string): Promise<void> {
  return call('POST', at(shop, '/api/v1/portal/code'), null, { phone });
}

export function exchangeCode(
  shop: PortalShop,
  phone: string,
  code: string,
): Promise<{ token: string; name: string }> {
  return call('POST', at(shop, '/api/v1/portal/session'), null, {
    phone,
    code,
  });
}

export function supplierSignIn(
  shop: PortalShop,
  email: string,
  password: string,
): Promise<{ token: string; name: string }> {
  return call('POST', at(shop, '/api/v1/portal/supplier/session'), null, {
    email,
    password,
  });
}

export function signOut(shop: PortalShop, token: string): Promise<void> {
  return call('DELETE', at(shop, '/api/v1/portal/session'), token);
}

// --- what a customer sees --------------------------------------------------

export interface PortalMe {
  name: string;
  phone?: string;
  email?: string;
  currency: string;
  outstanding: string;
  store_credit: string;
  gift_card_balance: string;
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
  delivery_status?: string;
  driver_name?: string;
  delivered_at?: string;
}

export interface PortalWarranty {
  serial_no: string;
  product?: string;
  status: string;
  sold_on?: string;
  expires_on?: string;
  in_warranty: boolean;
}

export interface PortalAddress {
  id?: string;
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

export interface PortalReturnRequest {
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
  customer_name?: string;
}

export function portalMe(
  shop: PortalShop,
  token: string,
): Promise<{ me: PortalMe }> {
  return call('GET', at(shop, '/api/v1/portal/me'), token);
}

export function portalInvoices(
  shop: PortalShop,
  token: string,
): Promise<{ data: PortalInvoice[] }> {
  return call('GET', at(shop, '/api/v1/portal/invoices'), token);
}

export function portalOrders(
  shop: PortalShop,
  token: string,
): Promise<{ data: PortalOrder[] }> {
  return call('GET', at(shop, '/api/v1/portal/orders'), token);
}

export function portalWarranty(
  shop: PortalShop,
  token: string,
  serialNo: string,
): Promise<{ warranty: PortalWarranty }> {
  return call(
    'GET',
    at(
      shop,
      `/api/v1/portal/warranty?serial_no=${encodeURIComponent(serialNo)}`,
    ),
    token,
  );
}

export function portalAddresses(
  shop: PortalShop,
  token: string,
): Promise<{ data: PortalAddress[] }> {
  return call('GET', at(shop, '/api/v1/portal/addresses'), token);
}

export function savePortalAddress(
  shop: PortalShop,
  token: string,
  body: PortalAddress,
): Promise<{ data: PortalAddress[] }> {
  return call('PUT', at(shop, '/api/v1/portal/addresses'), token, body);
}

export function removePortalAddress(
  shop: PortalShop,
  token: string,
  id: string,
): Promise<void> {
  return call('DELETE', at(shop, `/api/v1/portal/addresses/${id}`), token);
}

export function portalReturns(
  shop: PortalShop,
  token: string,
): Promise<{ data: PortalReturnRequest[] }> {
  return call('GET', at(shop, '/api/v1/portal/returns'), token);
}

export function askToReturn(
  shop: PortalShop,
  token: string,
  body: {
    invoice_id?: string;
    kind: string;
    reason: string;
    items: string;
  },
): Promise<{ request: PortalReturnRequest }> {
  return call('POST', at(shop, '/api/v1/portal/returns'), token, body);
}

// --- what a supplier sees --------------------------------------------------

export interface SupplierHome {
  supplier_name: string;
  contact_name: string;
  currency: string;
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

export function supplierHome(
  shop: PortalShop,
  token: string,
): Promise<{ home: SupplierHome }> {
  return call('GET', at(shop, '/api/v1/portal/supplier/home'), token);
}

export function supplierOrders(
  shop: PortalShop,
  token: string,
): Promise<{ data: SupplierOrder[] }> {
  return call('GET', at(shop, '/api/v1/portal/supplier/orders'), token);
}

export function supplierOrder(
  shop: PortalShop,
  token: string,
  id: string,
): Promise<{ order: SupplierOrder }> {
  return call('GET', at(shop, `/api/v1/portal/supplier/orders/${id}`), token);
}

export function respondToOrder(
  shop: PortalShop,
  token: string,
  id: string,
  body: { response: string; comment?: string; promised_on?: string },
): Promise<{ order: SupplierOrder }> {
  return call(
    'POST',
    at(shop, `/api/v1/portal/supplier/orders/${id}/respond`),
    token,
    body,
  );
}

export function supplierBills(
  shop: PortalShop,
  token: string,
): Promise<{ data: SupplierBill[] }> {
  return call('GET', at(shop, '/api/v1/portal/supplier/bills'), token);
}

export function supplierRFQs(
  shop: PortalShop,
  token: string,
): Promise<{ data: SupplierRFQ[] }> {
  return call('GET', at(shop, '/api/v1/portal/supplier/rfqs'), token);
}
