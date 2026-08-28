// Cash expenses (blueprint C3, design 02 rule 5).
//
// The module that answers the sentence C3 opens with: "the Owner must be able
// to see, in one click, exactly where every riyal is going."
//
// # Two figures that look alike and are not
//
// `tax_recoverable` is VAT the shop will get back from the tax authority.
// `tax_absorbed` is VAT it will not, on a category E2.3 restricts —
// entertainment, some vehicles, fuel — which is charged to the expense instead.
// The second is a real cost that looks like tax, and a screen that showed only
// "VAT" would make a shop think its fuel cost less than it did.
//
// # Money is a string, as everywhere
//
// The net amount is typed by a person and the tax is computed by the server
// from the registry rate for the expense DATE. A client that computed its own
// tax would be deciding what the VAT return claims.

import type { Client } from './client';

/** A category expenses are booked to. */
export interface ExpenseHead {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  account_id: string;
  account_code: string;
  account_name: string;
  /** Whether VAT on this category can be reclaimed. The one field here that is
   *  a tax position rather than a label, which is why changing it needs its own
   *  permission. */
  input_vat_recoverable: boolean;
  is_active: boolean;
  /** What has been booked to this category, so a list can be read without a
   *  second request. */
  spent: string;
}

/** One expense account a category may post to. */
export interface ExpenseAccount {
  id: string;
  code: string;
  name: string;
}

/** One category's share of an expense. */
export interface ExpenseLine {
  head_id: string;
  head: string;
  description?: string;
  net_amount: string;
  tax_treatment: string;
  tax_rate: string;
  tax_amount: string;
  tax_recoverable: string;
  /** VAT this category cannot reclaim, charged to the expense instead. */
  tax_absorbed: string;
  /** What the expense account was debited: net plus whatever was absorbed. The
   *  figure a "where is my money going" report sums. */
  charge_amount: string;
}

/** A recorded payment. */
export interface Expense {
  id: string;
  expense_no: string;
  expense_date: string;
  reference?: string;
  description?: string;
  paid_from: 'cash' | 'bank';
  store?: string;
  supplier?: string;
  currency: string;

  subtotal_net: string;
  tax_total: string;
  tax_recoverable: string;
  tax_absorbed: string;
  total_inclusive: string;

  lines?: ExpenseLine[];
  already_recorded?: boolean;
}

/** One category's share of a period. */
export interface HeadSpend {
  head_id: string;
  head: string;
  amount: string;
  /** Percentage of the period, so the answer needs no arithmetic to read. */
  share: string;
}

/** What a period cost, broken down the way C3.1 asks to see it. */
export interface ExpenseSummary {
  from: string;
  to: string;
  total: string;
  tax_recoverable: string;
  tax_absorbed: string;
  by_head: HeadSpend[];
  count: number;
  expenses: Expense[];
}

export interface ExpenseBody {
  uuid: string;
  expense_date: string;
  store_id?: string;
  supplier_id?: string;
  reference?: string;
  description?: string;
  paid_from: 'cash' | 'bank';
  lines: Array<{
    head_id: string;
    description?: string;
    /** Net, never gross. The server computes the tax. */
    net_amount: string;
    tax_treatment?: string;
  }>;
}

export interface ExpenseHeadBody {
  code: string;
  name: string;
  name_ar?: string;
  account_id: string;
  /** Required, never defaulted. Defaulting either way is wrong: false silently
   *  stops a shop reclaiming VAT it is entitled to, true silently claims VAT on
   *  entertainment. */
  input_vat_recoverable: boolean;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

export function listExpenses(
  client: Client,
  companyId: string,
  query: { from?: string; to?: string; head_id?: string; store_id?: string } = {},
): Promise<ExpenseSummary> {
  const params = Object.entries(query)
    .filter(([, v]) => v)
    .map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`)
    .join('&');
  return client.send<ExpenseSummary>(
    'GET',
    scoped('/api/v1/expenses' + (params ? '?' + params : ''), companyId),
  );
}

export function recordExpense(
  client: Client,
  companyId: string,
  body: ExpenseBody,
): Promise<Expense> {
  return client.send<Expense>('POST', scoped('/api/v1/expenses', companyId), body);
}

export function readExpense(
  client: Client,
  companyId: string,
  id: string,
): Promise<Expense> {
  return client.send<Expense>('GET', scoped(`/api/v1/expenses/${id}`, companyId));
}

export function listExpenseHeads(
  client: Client,
  companyId: string,
  includeRetired = false,
): Promise<ExpenseHead[]> {
  const path = includeRetired
    ? '/api/v1/expenses/heads?include_retired=true'
    : '/api/v1/expenses/heads';
  return client
    .send<{ data: ExpenseHead[] }>('GET', scoped(path, companyId))
    .then((b) => b.data ?? []);
}

export function listExpenseAccounts(
  client: Client,
  companyId: string,
): Promise<ExpenseAccount[]> {
  return client
    .send<{ data: ExpenseAccount[] }>(
      'GET',
      scoped('/api/v1/expenses/accounts', companyId),
    )
    .then((b) => b.data ?? []);
}

export function createExpenseHead(
  client: Client,
  companyId: string,
  body: ExpenseHeadBody,
): Promise<ExpenseHead> {
  return client.send<ExpenseHead>(
    'POST',
    scoped('/api/v1/expenses/heads', companyId),
    body,
  );
}

export function updateExpenseHead(
  client: Client,
  companyId: string,
  id: string,
  body: ExpenseHeadBody,
): Promise<ExpenseHead> {
  return client.send<ExpenseHead>(
    'PUT',
    scoped(`/api/v1/expenses/heads/${id}`, companyId),
    body,
  );
}

export function setExpenseHeadActive(
  client: Client,
  companyId: string,
  id: string,
  active: boolean,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/expenses/heads/${id}/active`, companyId),
    { active },
  );
}
