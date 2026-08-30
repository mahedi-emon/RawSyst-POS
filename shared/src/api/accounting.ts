// The books: statements, the calendar they are kept against, and the trail of
// who touched them.
//
// # Two account names, and the screen chooses
//
// Every statement line carries `name` and `name_ar`. The server sends both and
// the screen picks, which is how the catalogue already carries a product's
// Arabic name. The alternative — the server choosing from a request header —
// puts the reader's language into a cache key and into every report that is
// generated rather than viewed.
//
// # `balanced` is reported, not enforced
//
// A trial balance whose entire purpose is to reveal an imbalance must show the
// imbalance rather than refuse to render. So `difference` and `balanced` come
// back as data and the screen says so loudly; it does not treat an unbalanced
// set of books as an error state.

import type { Client } from './client';

/** One account on a trial balance. */
export interface TrialBalanceRow {
  account_id: string;
  code: string;
  name: string;
  type: string;
  debit: string;
  credit: string;
}

export interface TrialBalance {
  as_of: string;
  /** The currency the books are kept in. A statement without one is a
   *  page of numbers, and this product sells into three markets. */
  base_currency: string;
  rows: TrialBalanceRow[];
  total_debit: string;
  total_credit: string;
  /** Must be zero. Shown when it is not, because that is the one thing this
   *  statement exists to reveal. */
  difference: string;
  balanced: boolean;
}

/** One line of a statement. */
export interface StatementLine {
  account_id: string;
  code: string;
  name: string;
  name_ar?: string;
  amount: string;
}

export interface ProfitAndLoss {
  from: string;
  to: string;
  /** The currency the books are kept in. A statement without one is a
   *  page of numbers, and this product sells into three markets. */
  base_currency: string;
  revenue: StatementLine[];
  revenue_total: string;
  /** Separated from other expenses because gross profit is the number a
   *  retailer actually manages. */
  cost_of_sales: StatementLine[];
  cost_of_sales_total: string;
  gross_profit: string;
  expenses: StatementLine[];
  expenses_total: string;
  net_profit: string;
}

export interface BalanceSheet {
  as_of: string;
  /** The currency the books are kept in. A statement without one is a
   *  page of numbers, and this product sells into three markets. */
  base_currency: string;
  assets: StatementLine[];
  assets_total: string;
  liabilities: StatementLine[];
  liabilities_total: string;
  equity: StatementLine[];
  equity_total: string;
  /** Profit earned since the books began that has not yet been closed into
   *  retained earnings. Without it the sheet is short by exactly the year's
   *  profit on every day of the year. */
  current_earnings: string;
  equity_and_liabilities: string;
  difference: string;
  balanced: boolean;
}

export interface CashFlowLine {
  code: string;
  name: string;
  amount: string;
}

export interface CashFlow {
  from: string;
  to: string;
  /** The currency the books are kept in. A statement without one is a
   *  page of numbers, and this product sells into three markets. */
  base_currency: string;
  opening: string;
  closing: string;
  in: CashFlowLine[];
  out: CashFlowLine[];
  net_total: string;
  /** Stated so nobody mistakes this for an IAS 7 indirect statement. */
  method: string;
}

/** One month of the books. */
export interface Period {
  id: string;
  fiscal_year: number;
  period_no: number;
  starts_on: string;
  ends_on: string;
  state: 'open' | 'closed' | 'locked';
  closed_by?: string;
  closed_at?: string;
  reopened_by?: string;
  reopened_at?: string;
  reopen_reason?: string;
  /** How many journal entries the period holds. Closing an empty month and
   *  closing one with four thousand entries in it are different decisions. */
  entries: number;
  is_current?: boolean;
}

export interface FiscalYear {
  fiscal_year: number;
  periods: Period[];
}

export interface FiscalCalendar {
  years: FiscalYear[];
  /** 1–12. The screen needs it to say "April 2026 to March 2027" rather than
   *  leaving a reader to infer the shape of the year from the dates. */
  fiscal_year_start_month: number;
}

/** One thing somebody did. */
export interface AuditRecord {
  occurred_at: string;
  actor?: string;
  action: string;
  entity_type: string;
  entity_id?: string;
  ip?: string;
  device?: string;
  /** The raw JSON the writer recorded. Shown as-is: this is evidence, and a
   *  summary would mean the reader sees a rendering rather than the record. */
  before?: unknown;
  after?: unknown;
}

export interface AuditTrail {
  data: AuditRecord[];
  /** The verbs that appear in this tenant's log at all, so a filter offers
   *  what is there rather than a fixed list that goes stale. */
  actions: string[];
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

const query = (q: Record<string, string | undefined>) => {
  const parts = Object.entries(q)
    .filter(([, v]) => v !== undefined && v !== '')
    .map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`);
  return parts.length ? '?' + parts.join('&') : '';
};

// --- statements -----------------------------------------------------------

export function trialBalance(
  client: Client,
  companyId: string,
  asOf: string,
): Promise<TrialBalance> {
  return client.send<TrialBalance>(
    'GET',
    scoped('/api/v1/reports/trial-balance' + query({ as_of: asOf }), companyId),
  );
}

export function profitAndLoss(
  client: Client,
  companyId: string,
  from: string,
  to: string,
): Promise<ProfitAndLoss> {
  return client.send<ProfitAndLoss>(
    'GET',
    scoped('/api/v1/reports/profit-and-loss' + query({ from, to }), companyId),
  );
}

export function balanceSheet(
  client: Client,
  companyId: string,
  asOf: string,
): Promise<BalanceSheet> {
  return client.send<BalanceSheet>(
    'GET',
    scoped('/api/v1/reports/balance-sheet' + query({ as_of: asOf }), companyId),
  );
}

export function cashFlow(
  client: Client,
  companyId: string,
  from: string,
  to: string,
): Promise<CashFlow> {
  return client.send<CashFlow>(
    'GET',
    scoped('/api/v1/reports/cash-flow' + query({ from, to }), companyId),
  );
}

// --- the calendar ---------------------------------------------------------

export function fiscalCalendar(
  client: Client,
  companyId: string,
): Promise<FiscalCalendar> {
  return client.send<FiscalCalendar>(
    'GET',
    scoped('/api/v1/accounting/periods', companyId),
  );
}

export function openFiscalYear(
  client: Client,
  companyId: string,
  year: number,
): Promise<{ fiscal_year: number; periods_created: number }> {
  return client.send<{ fiscal_year: number; periods_created: number }>(
    'POST',
    scoped('/api/v1/accounting/periods', companyId),
    { fiscal_year: year },
  );
}

export function closePeriod(
  client: Client,
  companyId: string,
  id: string,
): Promise<Period> {
  return client.send<Period>(
    'POST',
    scoped(`/api/v1/accounting/periods/${id}/close`, companyId),
  );
}

/** Reopening needs a written reason — C10 makes it mandatory, and the database
 *  refuses one shorter than ten characters. Reopening changes figures somebody
 *  has already reported. */
export function reopenPeriod(
  client: Client,
  companyId: string,
  id: string,
  reason: string,
): Promise<Period> {
  return client.send<Period>(
    'POST',
    scoped(`/api/v1/accounting/periods/${id}/reopen`, companyId),
    { reason },
  );
}

/** What closing a year did. */
export interface YearEnd {
  fiscal_year: number;
  closed_on: string;
  revenue_closed: string;
  expenses_closed: string;
  /** Revenue less expenses — what moved to Retained Earnings. Negative for a
   *  loss, which is a fact rather than a failure. */
  profit_to_retained_earnings: string;
  accounts_closed: number;
  entry_no?: string;
  currency: string;
  already_closed?: boolean;
}

/** Runs C10's year-end routine: empties revenue and expense into Retained
 *  Earnings and locks every month of the year beyond reopening.
 *
 *  Refused unless every month is closed and the books balance. */
export function closeFiscalYear(
  client: Client,
  companyId: string,
  year: number,
): Promise<YearEnd> {
  return client.send<YearEnd>(
    'POST',
    scoped('/api/v1/accounting/year-end', companyId),
    { fiscal_year: year },
  );
}

// --- the trail ------------------------------------------------------------

/** The trail is tenant-wide, not company-scoped: `audit_log` carries no company
 *  dimension, because creating a company is an action that does not belong to
 *  one and neither does signing in. */
export function auditTrail(
  client: Client,
  q: { action?: string; from?: string; to?: string } = {},
): Promise<AuditTrail> {
  return client.send<AuditTrail>('GET', '/api/v1/audit' + query(q));
}
