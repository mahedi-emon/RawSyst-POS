// Buying stock, and paying for it.
//
// Blueprint B5's chain: PO → GRN → Bill → three-way match → Payment. Nothing
// here computes anything — the match runs on the server, the journal is posted
// by the same rules the sale side uses, and stock moves through the same
// costing engine. This module states what happened and reads the answer.

import type { Client } from './client';

// --- Suppliers -----------------------------------------------------------

export interface Supplier {
  id: string;
  code: string;
  legal_name: string;
  name_ar?: string;
  contact_name?: string;
  email?: string;
  phone?: string;
  vat_number?: string;
  cr_number?: string;
  country?: string;
  payment_terms_days: number;
  credit_limit?: string;
  notes?: string;
  is_active: boolean;
  /** What is owed right now. On the list because a buyer choosing a supplier
   *  needs to see they are already behind on that account. */
  outstanding: string;
}

export function listSuppliers(
  client: Client,
  companyId: string,
  search = '',
  includeInactive = false,
): Promise<Supplier[]> {
  const query = new URLSearchParams({ company_id: companyId });
  if (search) query.set('search', search);
  if (includeInactive) query.set('include_inactive', 'true');
  return client
    .send<{ data: Supplier[] }>('GET', `/api/v1/purchasing/suppliers?${query}`)
    .then((b) => b.data ?? []);
}

export function createSupplier(
  client: Client,
  companyId: string,
  body: {
    code: string;
    legal_name: string;
    payment_terms_days: number;
    vat_number?: string;
    phone?: string;
    email?: string;
  },
): Promise<Supplier> {
  return client.send<Supplier>(
    'POST',
    `/api/v1/purchasing/suppliers?company_id=${companyId}`,
    body,
  );
}

// --- Purchase orders -----------------------------------------------------

export interface OrderLine {
  id: string;
  line_no: number;
  variant_id: string;
  description: string;
  qty_ordered: string;
  qty_received: string;
  qty_outstanding: string;
  qty_billed: string;
  unit_cost: string;
  tax_treatment: string;
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
  lines?: OrderLine[];
}

export function listOrders(
  client: Client,
  companyId: string,
  status = '',
): Promise<Order[]> {
  const query = new URLSearchParams({ company_id: companyId });
  if (status) query.set('status', status);
  return client
    .send<{ data: Order[] }>('GET', `/api/v1/purchasing/orders?${query}`)
    .then((b) => b.data ?? []);
}

export function readOrder(
  client: Client,
  companyId: string,
  poId: string,
): Promise<Order> {
  return client.send<Order>(
    'GET',
    `/api/v1/purchasing/orders/${poId}?company_id=${companyId}`,
  );
}

export function createOrder(
  client: Client,
  companyId: string,
  body: {
    supplier_id: string;
    warehouse_id: string;
    expected_on?: string;
    notes?: string;
    lines: Array<{
      variant_id: string;
      description: string;
      qty: string;
      unit_cost: string;
      tax_rate: string;
    }>;
  },
): Promise<Order> {
  return client.send<Order>(
    'POST',
    `/api/v1/purchasing/orders?company_id=${companyId}`,
    body,
  );
}

export function issueOrder(
  client: Client,
  companyId: string,
  poId: string,
): Promise<Order> {
  return client.send<Order>(
    'POST',
    `/api/v1/purchasing/orders/${poId}/issue?company_id=${companyId}`,
  );
}

// --- Receiving -----------------------------------------------------------

export interface Receipt {
  id: string;
  grn_number: string;
  po_id: string;
  po_number: string;
  received_on: string;
  order_status: string;
  already_received: boolean;
  lines: Array<{
    po_line_id: string;
    description: string;
    qty_received: string;
    qty_rejected: string;
    unit_cost: string;
    value: string;
  }>;
}

/**
 * Records a delivery, which is the only thing that puts purchased stock on the
 * shelf — B5 forbids a purchase order doing it.
 *
 * The UUID is generated before the call. A network failure after the server
 * committed would otherwise have a storeman press Receive again and receive the
 * same delivery twice.
 */
export function receiveGoods(
  client: Client,
  companyId: string,
  body: {
    po_id: string;
    delivery_note_ref?: string;
    notes?: string;
    /** Freight, duty and handling. Spread across the lines and carried into
     *  the cost layers, so a later sale reports the margin it actually earned. */
    landed_cost?: string;
    /** Recoverable, and deliberately separate: E2.5 puts duty in inventory
     *  cost and import VAT in a receivable, and adding them together
     *  overstates stock while understating the reclaim. */
    import_vat?: string;
    landed_cost_basis?: string;
    lines: Array<{
      po_line_id: string;
      qty_received: string;
      qty_rejected?: string;
      reject_reason?: string;
    }>;
  },
): Promise<Receipt> {
  return client.send<Receipt>(
    'POST',
    `/api/v1/purchasing/receipts?company_id=${companyId}`,
    { uuid: crypto.randomUUID(), ...body },
  );
}

// --- Bills ---------------------------------------------------------------

export interface MatchLine {
  dimension: string;
  description?: string;
  ordered?: string;
  received?: string;
  billed?: string;
  variance: string;
  variance_pct?: string;
  outcome: 'pass' | 'within_tolerance' | 'breach';
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
  match?: MatchLine[];
  /** False on a blocked bill: recorded, but deliberately kept out of the
   *  ledger until somebody accepts the discrepancy. */
  posted: boolean;
  already_recorded: boolean;
}

export function listBills(
  client: Client,
  companyId: string,
  status = '',
): Promise<Bill[]> {
  const query = new URLSearchParams({ company_id: companyId });
  if (status) query.set('status', status);
  return client
    .send<{ data: Bill[] }>('GET', `/api/v1/purchasing/bills?${query}`)
    .then((b) => b.data ?? []);
}

export function readBill(
  client: Client,
  companyId: string,
  billId: string,
): Promise<Bill> {
  return client.send<Bill>(
    'GET',
    `/api/v1/purchasing/bills/${billId}?company_id=${companyId}`,
  );
}

export function recordBill(
  client: Client,
  companyId: string,
  body: {
    supplier_id: string;
    po_id?: string;
    supplier_ref: string;
    bill_date?: string;
    lines: Array<{
      po_line_id?: string;
      description: string;
      qty: string;
      unit_cost: string;
      tax_rate: string;
    }>;
  },
): Promise<Bill> {
  return client.send<Bill>(
    'POST',
    `/api/v1/purchasing/bills?company_id=${companyId}`,
    { uuid: crypto.randomUUID(), ...body },
  );
}

export function approveBill(
  client: Client,
  companyId: string,
  billId: string,
  reason: string,
): Promise<Bill> {
  return client.send<Bill>(
    'POST',
    `/api/v1/purchasing/bills/${billId}/approve?company_id=${companyId}`,
    { reason },
  );
}

// --- Payment -------------------------------------------------------------

export interface Payment {
  id: string;
  payment_number: string;
  supplier: string;
  paid_on: string;
  method: string;
  amount: string;
  currency: string;
  already_paid: boolean;
  settled: Array<{
    bill_id: string;
    supplier_ref: string;
    amount: string;
    outstanding: string;
    status: string;
  }>;
}

export function paySupplier(
  client: Client,
  companyId: string,
  body: {
    supplier_id: string;
    method: string;
    reference?: string;
    allocations: Array<{ bill_id: string; amount: string }>;
  },
): Promise<Payment> {
  return client.send<Payment>(
    'POST',
    `/api/v1/purchasing/payments?company_id=${companyId}`,
    { uuid: crypto.randomUUID(), ...body },
  );
}

// --- Ageing --------------------------------------------------------------

export interface AgeingRow {
  supplier_id: string;
  supplier: string;
  not_due: string;
  days_0_30: string;
  days_31_60: string;
  days_61_90: string;
  days_90_plus: string;
  total: string;
}

export interface Ageing {
  as_of: string;
  rows: AgeingRow[];
  total: string;
  base_currency: string;
}

export function fetchAgeing(
  client: Client,
  companyId: string,
): Promise<Ageing> {
  return client.send<Ageing>(
    'GET',
    `/api/v1/purchasing/ageing?company_id=${companyId}`,
  );
}

// --- Warehouses ----------------------------------------------------------

export interface Warehouse {
  id: string;
  code: string;
  name: string;
  /** The branch it belongs to, where a company has several. */
  store?: string;
}

export function listWarehouses(
  client: Client,
  companyId: string,
): Promise<Warehouse[]> {
  return client
    .send<{ data: Warehouse[] }>(
      'GET',
      `/api/v1/purchasing/warehouses?company_id=${companyId}`,
    )
    .then((b) => b.data ?? []);
}

/** Rewrites a DRAFT order.
 *
 * Draft only. An issued order is a commitment the supplier can hold the shop
 * to, and the server refuses to change one — the same reasoning that forbids
 * editing a finalized invoice. */
export function updateOrder(
  client: Client,
  companyId: string,
  poId: string,
  body: {
    supplier_id: string;
    warehouse_id: string;
    expected_on?: string;
    notes?: string;
    lines: Array<{
      variant_id: string;
      description: string;
      qty: string;
      unit_cost: string;
      tax_rate: string;
    }>;
  },
): Promise<Order> {
  return client.send<Order>(
    'PUT',
    `/api/v1/purchasing/orders/${poId}?company_id=${companyId}`,
    body,
  );
}

/** Corrects a supplier's details.
 *
 * The code is absent on purpose: it appears on purchase orders already issued,
 * and renaming it would silently change what those documents refer to. The
 * server ignores one if sent. */
export function updateSupplier(
  client: Client,
  companyId: string,
  supplierId: string,
  body: {
    legal_name: string;
    payment_terms_days: number;
    vat_number?: string;
    phone?: string;
    email?: string;
  },
): Promise<Supplier> {
  return client.send<Supplier>(
    'PUT',
    `/api/v1/purchasing/suppliers/${supplierId}?company_id=${companyId}`,
    body,
  );
}

/** Takes a supplier off the buyer's lists, or puts them back.
 *
 * Never a delete: orders, receipts, bills and payments all refer to them, and a
 * row that vanished would leave that history pointing at nothing. Refused by the
 * server while money is still owed. */
export function setSupplierActive(
  client: Client,
  companyId: string,
  supplierId: string,
  isActive: boolean,
): Promise<Supplier> {
  return client.send<Supplier>(
    'POST',
    `/api/v1/purchasing/suppliers/${supplierId}/active?company_id=${companyId}`,
    { is_active: isActive },
  );
}
