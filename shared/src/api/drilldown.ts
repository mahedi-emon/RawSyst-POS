// What is behind each figure on the dashboard.
//
// Blueprint A8: one-click drill-through on every widget, because a KPI you
// cannot open is trivia. An owner who sees a number they did not expect has one
// useful next question — which transactions made it.
//
// Nothing here computes. Each list arrives already summed by the same posting
// engine that produced the tile, and a backend test asserts the two agree: a
// detail screen filtered slightly differently from its summary is worse than no
// detail screen, because it makes an owner believe the summary is wrong.

import type { Client } from './client';
import type { StatementLine } from './dashboard';

// --- Sales ---------------------------------------------------------------

export interface SaleRow {
  id: string;
  human_number?: string;
  doc_type: string;
  /** The ZATCA lifecycle position, so an owner scanning the day can see at a
   *  glance which invoice has not reported. */
  state: string;
  issued_at: string;
  total_inclusive: string;
  tax_total: string;
  /** "Cash", or "Cash + Mada" on a split. */
  tenders: string;
  line_count: number;
  store_name?: string;
  is_credit_note: boolean;
}

export interface SalesDetail {
  date: string;
  rows: SaleRow[];
  /** Over the whole day, not the returned page, so a paged list still tells an
   *  owner what the day came to. */
  sales_total: string;
  refund_total: string;
  net_total: string;
  tax_total: string;
  invoice_count: number;
  refund_count: number;
  has_more: boolean;
  base_currency: string;
}

export function fetchSales(
  client: Client,
  companyId: string,
  date: string,
): Promise<SalesDetail> {
  return client.send<SalesDetail>(
    'GET',
    `/api/v1/dashboard/sales?company_id=${companyId}&date=${date}`,
  );
}

// --- Expenses ------------------------------------------------------------

export interface ExpenseEntry {
  entry_id: string;
  entry_no: string;
  date: string;
  memo: string;
  account_id: string;
  account: string;
  code: string;
  amount: string;
  /** What caused the posting. An owner asking "what is this expense" usually
   *  means what created it, not which account it landed in. */
  source_type?: string;
}

export interface ExpensesDetail {
  date: string;
  entries: ExpenseEntry[];
  by_account: StatementLine[];
  total: string;
  base_currency: string;
  account_id?: string;
}

export function fetchExpenses(
  client: Client,
  companyId: string,
  date: string,
  accountId?: string,
): Promise<ExpensesDetail> {
  const query = new URLSearchParams({ company_id: companyId, date });
  if (accountId) query.set('account_id', accountId);
  return client.send<ExpensesDetail>('GET', `/api/v1/dashboard/expenses?${query}`);
}

// --- Compliance ----------------------------------------------------------

export interface ComplianceRow {
  invoice_id: string;
  human_number?: string;
  doc_type: string;
  state: string;
  issued_at: string;
  total_inclusive: string;
  /** Position on this terminal's chain. A gap in the sequence is the exact
   *  signal tamper detection looks for. */
  icv: number;
  /** Drives the 12/24/72-hour escalation of design 08 §4. */
  age_hours: number;
  attempts: number;
  last_error?: string;
}

export interface ComplianceQueue {
  rows: ComplianceRow[];
  outstanding: number;
  oldest_hours: number;
  base_currency: string;
  /** False while the P1 verification gate is open. The screen must say so
   *  rather than implying a transient failure somebody can retry away. */
  signing_available: boolean;
}

export function fetchCompliance(
  client: Client,
  companyId: string,
): Promise<ComplianceQueue> {
  return client.send<ComplianceQueue>(
    'GET',
    `/api/v1/dashboard/compliance?company_id=${companyId}`,
  );
}

// --- Stock ---------------------------------------------------------------

export interface StockRow {
  variant_id: string;
  sku: string;
  name: string;
  barcode?: string;
  on_hand: string;
  reorder_level?: string;
  value: string;
}

export interface StockDetail {
  filter: string;
  rows: StockRow[];
  count: number;
  base_currency: string;
}

export function fetchStock(
  client: Client,
  companyId: string,
  filter: 'low' | 'out',
): Promise<StockDetail> {
  return client.send<StockDetail>(
    'GET',
    `/api/v1/dashboard/stock?company_id=${companyId}&filter=${filter}`,
  );
}
