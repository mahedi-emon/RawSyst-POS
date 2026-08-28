// The Owner Dashboard's data.
//
// One request. Not because a request is expensive, but because nine of them
// arrive at nine different moments: the screen renders nine times, reflows
// under the owner's eyes, and — worse — shows figures taken from nine
// different instants, so it can appear not to balance when nothing is wrong.
//
// Nothing here computes. Every figure below was produced by the same posting
// engine the trial balance reads, so a number an owner disputes traces back to
// journal entries rather than to browser code nobody can audit.

import type { Client } from './client';

export interface TrendPoint {
  date: string;
  total: string;
}

export interface SalesToday {
  total: string;
  yesterday: string;
  /** Absent when yesterday was zero — "up 100%" from nothing is an artefact. */
  change_pct: string | null;
  invoice_count: number;
  trend: TrendPoint[];
}

export interface ProfitToday {
  revenue: string;
  cost: string;
  gross: string;
  margin_pct: string | null;
}

export interface StatementLine {
  account_id: string;
  code: string;
  name: string;
  /** The same account in Arabic, where the shop has one. An account name is a
   *  shop's own word for its own thing, so it cannot live in the catalogue;
   *  both names are sent and the screen picks. */
  name_ar?: string;
  amount: string;
}

export interface ExpensesToday {
  total: string;
  by_account: StatementLine[];
}

export interface MoneyPosition {
  cash: string;
  bank: string;
  /** Taken but still with the acquirer (C12). Its own figure on purpose. */
  unsettled: string;
  receivable: string;
  store_credit: string;
  /** Goods on the shelves that no supplier has invoiced yet. Money the shop is
   *  going to owe and has not been asked for. */
  accrued_purchases: string;
  total: string;
}

export interface InventoryNow {
  value: string;
  low_stock: number;
  out_of_stock: number;
  variant_count: number;
}

export interface TenderSlice {
  method: string;
  total: string;
  count: number;
}

export interface Attention {
  severity: 'critical' | 'warning' | 'notice';
  kind: string;
  title: string;
  detail: string;
  count: number;
  link: string;
}

export interface Overview {
  date: string;
  base_currency: string;
  sales: SalesToday;
  profit: ProfitToday;
  expenses: ExpensesToday;
  money: MoneyPosition;
  inventory: InventoryNow;
  tenders: TenderSlice[];
  attention: Attention[];
  /** Modules that do not exist yet. Named so the screen can say so rather than
   *  showing a zero an owner would read as "I owe nobody". */
  unbuilt: string[];
}

export function fetchOverview(
  client: Client,
  companyId: string,
  date?: string,
): Promise<Overview> {
  const query = new URLSearchParams({ company_id: companyId });
  if (date) query.set('date', date);
  return client.send<Overview>('GET', `/api/v1/dashboard/overview?${query}`);
}
