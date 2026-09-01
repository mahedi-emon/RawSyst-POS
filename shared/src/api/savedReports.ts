// Saved and scheduled reports (blueprint D1), and the Saudization reading (E6).
//
// A saved report is a saved SHAPE — which report, over what relative window,
// filtered to which branch — not a saved answer. Running it recomputes from the
// ledger, which is why the window is a phrase like "last month" rather than two
// dates: a schedule built on stored dates would email the same figures forever.

import type { Client } from './client';

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

/** The reports the product computes and can therefore keep. */
export type SavedKind =
  | 'trial_balance'
  | 'profit_and_loss'
  | 'balance_sheet'
  | 'cash_flow'
  | 'sales'
  | 'expenses'
  | 'stock'
  | 'vat_return'
  | 'receivables'
  | 'payables'
  | 'movers'
  | 'compliance';

export type SavedPeriod =
  | 'today'
  | 'this_week'
  | 'this_month'
  | 'last_month'
  | 'this_quarter'
  | 'last_quarter'
  | 'this_year'
  | 'last_year';

export interface SavedReport {
  id?: string;
  name: string;
  kind: SavedKind;
  period: SavedPeriod;

  store_id?: string;
  warehouse_id?: string;
  account_id?: string;

  cadence?: 'daily' | 'weekly' | 'monthly' | '';
  day_of_week?: number;
  day_of_month?: number;
  recipients?: string;

  last_run_at?: string;
  last_run_error?: string;
  is_active: boolean;

  /** What the relative period resolves to today, so the screen can show it. */
  from?: string;
  to?: string;
}

export function listSavedReports(
  client: Client,
  companyId: string,
): Promise<{ data: SavedReport[] }> {
  return client.send('GET', scoped('/api/v1/reports/saved', companyId));
}

export function saveReport(
  client: Client,
  companyId: string,
  body: SavedReport,
): Promise<{ report: SavedReport }> {
  return client.send('PUT', scoped('/api/v1/reports/saved', companyId), body);
}

export function removeSavedReport(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send(
    'DELETE',
    scoped(`/api/v1/reports/saved/${id}`, companyId),
  );
}

export interface WorkforceLine {
  department: string;
  total: number;
  saudi: number;
}

export interface Workforce {
  total: number;
  saudi: number;
  non_saudi: number;
  /** Percentage of the workforce that is Saudi, to two places. */
  saudi_share: string;
  expiring_soon: number;
  expired: number;
  by_department: WorkforceLine[];
}

export function workforce(
  client: Client,
  companyId: string,
): Promise<{ workforce: Workforce }> {
  return client.send('GET', scoped('/api/v1/reports/workforce', companyId));
}
