// What the business spent, and how much of the tax on it it can reclaim.
//
// # The recoverable split is the reason this is not a simple list
//
// E2.3 restricts input VAT recovery by CATEGORY: entertainment, some vehicles
// and fuel are spent with tax that cannot be reclaimed. Each expense head
// carries `input_vat_recoverable`, and when it is false the tax is absorbed
// into the expense rather than claimed — "so the VAT return is not overstated".
//
// The period summary therefore reports both halves, and so does this screen.
// Showing one number for tax would hide the half that is a real cost.

import type { Key } from '@/lib/i18n/locale';

/** A category of spending, mapped to the ledger account it posts to. */
export interface ExpenseHead {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  account_id: string;
  account_code: string;
  account_name: string;
  account_name_ar?: string;
  /**
   * Whether VAT on this category can be reclaimed.
   *
   * False for the ones E2.3 restricts. All of it or none of it: recovery is
   * restricted by category, never apportioned within one.
   */
  input_vat_recoverable: boolean;
  is_active: boolean;
  /** What has gone through this head in the period being looked at. */
  spent: string;
  currency: string;
}

export interface LedgerAccount {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
}

export interface Department {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  is_active: boolean;
}

export interface ExpenseLine {
  head_id: string;
  head?: string;
  description?: string;
  net_amount: string;
  tax_treatment: string;
  /** A FRACTION, as everywhere: "0.150000" is fifteen per cent. */
  tax_rate: string;
  tax_amount: string;
  /** The half that can be reclaimed. */
  tax_recoverable: string;
  /** The half that cannot, and is therefore part of the cost. */
  tax_absorbed: string;
  /**
   * What actually lands in the expense account.
   *
   * Net plus whatever tax was absorbed — which is the whole point of the
   * split: restricted VAT is not a tax the business reclaims, it is part of
   * what the thing cost.
   */
  charge_amount: string;
}

export interface Expense {
  id: string;
  expense_no: string;
  expense_date: string;
  reference?: string;
  /** Who it was paid to and anything else about it, in one field. */
  description?: string;
  /** `cash` or `bank` — a role, not an account id. */
  paid_from?: string;
  store?: string;
  supplier?: string;
  currency: string;
  subtotal_net: string;
  tax_total: string;
  tax_recoverable: string;
  tax_absorbed: string;
  total_inclusive: string;
  created_by?: string;
  lines?: ExpenseLine[];
}

/** One head's share of a period. */
export interface HeadTotal {
  head_id: string;
  head: string;
  amount: string;
  /** Percentage of the period's spending, as a number in a string. */
  share: string;
}

/**
 * The period, not a page.
 *
 * `GET /expenses` answers with the totals for a date range and the expenses
 * inside it, because "what did we spend last month" is the question and a list
 * alone cannot answer it.
 */
export interface ExpensePeriod {
  from: string;
  to: string;
  total: string;
  tax_recoverable: string;
  tax_absorbed: string;
  by_head: HeadTotal[];
  count: number;
  expenses: Expense[];
}

/** How often a standing cost repeats. */
export const RECURRENCE: Record<string, Key> = {
  weekly: 'nx.exp.rWeekly',
  monthly: 'nx.exp.rMonthly',
  quarterly: 'nx.exp.rQuarterly',
  yearly: 'nx.exp.rYearly',
};

export interface Recurring {
  id: string;
  head_id: string;
  head?: string;
  department?: string;
  supplier?: string;
  amount: string;
  currency: string;
  cadence: string;
  next_due?: string;
  is_active: boolean;
  note?: string;
}
