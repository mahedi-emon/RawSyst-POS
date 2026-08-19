// Customers, and collecting what they owe.
//
// The mirror of api/purchasing.ts. Nothing here computes a balance, an ageing
// bucket or whether a customer has headroom left: every one of those is derived
// on the server from the invoices and the receipts, because a figure computed
// twice is a figure that eventually disagrees with itself. This module states
// what happened and reads the answer.

import type { Client } from './client';

// --- Customers -----------------------------------------------------------

export type CustomerType = 'retail' | 'wholesale' | 'vip';

export interface Customer {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  customer_type: CustomerType;
  phone?: string;
  email?: string;
  vat_number?: string;
  address?: string;
  payment_terms_days: number;
  /** Absent means no credit account at all, which is different from a limit of
   *  zero. A customer with no limit cannot buy on account. */
  credit_limit?: string;
  notes?: string;
  is_active: boolean;

  /** What they owe right now, derived from their invoices and receipts. */
  balance: string;
  /** What is left of the limit. Empty when there is no limit to have a
   *  remainder of, never negative. */
  available?: string;
}

export interface CustomerBody {
  code?: string;
  name: string;
  name_ar?: string;
  customer_type: CustomerType;
  phone?: string;
  email?: string;
  vat_number?: string;
  address?: string;
  payment_terms_days: number;
  credit_limit?: string;
  notes?: string;
}

/**
 * @param companyId which company's customers. EMPTY means "resolve it from the
 * registered terminal" — a till has no company to name, its authority comes
 * from the device in its token, and the server's customerScope handles both.
 */
export function listCustomers(
  client: Client,
  companyId: string,
  search = '',
  includeInactive = false,
): Promise<Customer[]> {
  const query = new URLSearchParams();
  if (companyId) query.set('company_id', companyId);
  if (search) query.set('search', search);
  if (includeInactive) query.set('include_inactive', 'true');
  return client
    .send<{ data: Customer[] }>('GET', `/api/v1/customers?${query}`)
    .then((b) => b.data ?? []);
}

export function readCustomer(
  client: Client,
  companyId: string,
  customerId: string,
): Promise<Customer> {
  return client.send<Customer>(
    'GET',
    `/api/v1/customers/${customerId}?company_id=${companyId}`,
  );
}

export function createCustomer(
  client: Client,
  companyId: string,
  body: CustomerBody,
): Promise<Customer> {
  return client.send<Customer>(
    'POST',
    `/api/v1/customers?company_id=${companyId}`,
    body,
  );
}

export function updateCustomer(
  client: Client,
  companyId: string,
  customerId: string,
  body: CustomerBody,
): Promise<Customer> {
  return client.send<Customer>(
    'PUT',
    `/api/v1/customers/${customerId}?company_id=${companyId}`,
    body,
  );
}

/**
 * Its own call, because it carries its own permission.
 *
 * Deciding how much a customer may owe is a different act from recording their
 * phone number, so the server gates it on `customers.set_credit_limit` rather
 * than on `customers.manage`. An empty string removes the account entirely.
 */
export function setCreditLimit(
  client: Client,
  companyId: string,
  customerId: string,
  creditLimit: string,
): Promise<Customer> {
  return client.send<Customer>(
    'POST',
    `/api/v1/customers/${customerId}/credit-limit?company_id=${companyId}`,
    { credit_limit: creditLimit },
  );
}

export function setCustomerActive(
  client: Client,
  companyId: string,
  customerId: string,
  active: boolean,
): Promise<Customer> {
  return client.send<Customer>(
    'POST',
    `/api/v1/customers/${customerId}/active?company_id=${companyId}`,
    { active },
  );
}

// --- The ledger ----------------------------------------------------------

export interface LedgerRow {
  date: string;
  kind: 'sale' | 'credit' | 'receipt';
  reference: string;
  charged?: string;
  received?: string;
  /** The running total AFTER this row, so a customer can see how they got here
   *  and not only where they are. */
  balance: string;
  due_date?: string;
}

export interface Ledger {
  customer: Customer;
  rows: LedgerRow[];
  closing: string;
  base_currency: string;
}

export function readLedger(
  client: Client,
  companyId: string,
  customerId: string,
): Promise<Ledger> {
  return client.send<Ledger>(
    'GET',
    `/api/v1/customers/${customerId}/ledger?company_id=${companyId}`,
  );
}

// --- Open invoices and receipts ------------------------------------------

export interface OpenInvoice {
  invoice_id: string;
  human_number?: string;
  issue_date: string;
  due_date: string;
  on_account: string;
  /** What came back off the account through a return, shown apart from money
   *  received because a customer disputing a balance needs the difference. */
  credited: string;
  received: string;
  outstanding: string;
}

export function listOpenInvoices(
  client: Client,
  companyId: string,
  customerId: string,
): Promise<OpenInvoice[]> {
  return client
    .send<{ data: OpenInvoice[] }>(
      'GET',
      `/api/v1/customers/${customerId}/open-invoices?company_id=${companyId}`,
    )
    .then((b) => b.data ?? []);
}

export interface SettledInvoice {
  invoice_id: string;
  human_number?: string;
  amount: string;
  outstanding: string;
}

export interface Receipt {
  id: string;
  receipt_number: string;
  customer_id: string;
  customer: string;
  received_on: string;
  method: string;
  reference?: string;
  amount: string;
  currency: string;
  settled: SettledInvoice[];
  /** True when the server recognised this uuid and did NOT take the money a
   *  second time. */
  already_taken: boolean;
}

export interface ReceiptBody {
  /** Assigned by the client BEFORE the call. A network failure after the server
   *  committed would otherwise have a cashier take the same payment twice. */
  uuid: string;
  customer_id: string;
  method: string;
  reference?: string;
  received_on?: string;
  allocations: { invoice_id: string; amount: string }[];
}

export function takePayment(
  client: Client,
  companyId: string,
  body: ReceiptBody,
): Promise<Receipt> {
  return client.send<Receipt>(
    'POST',
    `/api/v1/receivables/receipts?company_id=${companyId}`,
    body,
  );
}

// --- Ageing --------------------------------------------------------------

export interface AgeingRow {
  customer_id: string;
  customer: string;
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

export function readAgeing(
  client: Client,
  companyId: string,
  asOf = '',
): Promise<Ageing> {
  const query = new URLSearchParams({ company_id: companyId });
  if (asOf) query.set('as_of', asOf);
  return client.send<Ageing>('GET', `/api/v1/receivables/ageing?${query}`);
}
