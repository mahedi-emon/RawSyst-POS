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

// --- standing costs ---------------------------------------------------------

/**
 * How often a schedule repeats, as the API stores it.
 *
 * Three, because the service allows three. Its refusal says what to do with
 * everything else: "A schedule repeats weekly, monthly or yearly. Use
 * interval_count for anything else: monthly every 3 is quarterly."
 */
export const FREQUENCY: Record<string, Key> = {
  weekly: 'nx.exp.rWeekly',
  monthly: 'nx.exp.rMonthly',
  yearly: 'nx.exp.rYearly',
};

/** A frequency and how many of them apart, which is the whole schedule. */
export interface Cadence {
  frequency: string;
  interval_count: number;
}

/**
 * What a person picks, against what the API stores.
 *
 * Nobody agrees rent as "monthly, every three". They agree it quarterly. So
 * the form offers the cadences a shop actually signs contracts on and sends
 * the pair each one means, rather than exposing two controls and asking the
 * reader to compose them.
 */
export interface Preset {
  id: string;
  labelKey: Key;
  cadence: Cadence;
}

export const PRESETS: readonly Preset[] = [
  { id: 'weekly', labelKey: 'nx.exp.rWeekly', cadence: { frequency: 'weekly', interval_count: 1 } },
  {
    id: 'fortnightly',
    labelKey: 'nx.exp.rFortnightly',
    cadence: { frequency: 'weekly', interval_count: 2 },
  },
  { id: 'monthly', labelKey: 'nx.exp.rMonthly', cadence: { frequency: 'monthly', interval_count: 1 } },
  {
    id: 'quarterly',
    labelKey: 'nx.exp.rQuarterly',
    cadence: { frequency: 'monthly', interval_count: 3 },
  },
  { id: 'yearly', labelKey: 'nx.exp.rYearly', cadence: { frequency: 'yearly', interval_count: 1 } },
];

/** The preset a stored schedule was made from, when one covers it. */
export function presetFor(cadence: Cadence): Preset | undefined {
  return PRESETS.find(
    (p) =>
      p.cadence.frequency === cadence.frequency &&
      p.cadence.interval_count === cadence.interval_count,
  );
}

const EVERY: Record<string, Key> = {
  weekly: 'nx.exp.rEveryWeeks',
  monthly: 'nx.exp.rEveryMonths',
  yearly: 'nx.exp.rEveryYears',
};

/**
 * A cadence in words.
 *
 * Falls through to "Every {n} months" for an interval no preset covers — one
 * imported, or one an operator set through the API. There is no plural
 * machinery because there is no singular case to reach: an interval of one is
 * always a preset, so the fallback only ever renders a number above one.
 */
export function describeCadence(
  cadence: Cadence,
  t: (key: Key, params?: Record<string, string | number>) => string,
): string {
  const preset = presetFor(cadence);
  if (preset) return t(preset.labelKey);
  const key = EVERY[cadence.frequency];
  if (!key) return cadence.frequency;
  return t(key, { n: cadence.interval_count });
}

/**
 * A standing cost.
 *
 * The schedule posts nothing by itself. `POST /expenses/recurring/generate`
 * turns the ones that have fallen due into ordinary expenses down the same
 * path a person typing one takes, which is why it is gated on
 * `expense.record` rather than on the permission that configures this.
 */
export interface Recurring {
  id: string;
  /** What it is for — "Shop rent", "Internet". */
  name: string;
  head_id: string;
  head?: string;
  store_id?: string;
  supplier_id?: string;
  department_id?: string;
  amount: string;
  currency: string;
  /** `cash` or `bank`, exactly as on an expense. */
  paid_from: string;
  description?: string;
  frequency: string;
  interval_count: number;
  starts_on: string;
  ends_on?: string;
  /**
   * Stored, not derived.
   *
   * "A monthly schedule starting on the 31st has no 31st in February, and
   * every rule for the same day next month is a judgement somebody has to be
   * able to see and override."
   */
  next_due_on: string;
  is_active: boolean;
}

/** Whether a schedule would produce an expense if generation ran today. */
export function isDue(r: Recurring, today: string): boolean {
  if (!r.is_active) return false;
  if (r.ends_on && r.next_due_on > r.ends_on) return false;
  return r.next_due_on <= today;
}

/** What one pass over the schedules did. */
export interface GenerateResult {
  created: number;
  skipped: number;
  /** The expense numbers made. Null rather than empty when none were. */
  expenses: string[] | null;
  /**
   * The schedules that could not be booked, and why.
   *
   * Reported rather than thrown: a closed period on one schedule must not
   * stop the rent being booked on another, so a pass can half succeed and the
   * screen has to be able to say so.
   */
  failed?: string[];
}
