// D2's analytics: what sold, what is not moving, and what to order.
//
// # A blank figure is a question this shop cannot answer yet
//
// Four of the thirteen KPIs come back as an empty string on a young business:
// inventory turnover needs a period of purchase history, repeat-customer share
// and lifetime value need customers who have come back. The server sends `""`
// rather than `0`, and the difference is the whole point — a shop told its
// repeat-customer rate is 0% concludes nobody returns, when the truth is that
// nobody has been given time to.
//
// So `stated()` asks whether a figure arrived, never whether it is non-zero,
// and every screen renders an unstated one as a dash with a reason.
//
// # The forecast says what it is
//
// `basis` comes back as "sales over the last 90 days, repeated". It is shown,
// verbatim, next to the number. An owner ordering stock against a forecast has
// to know it is arithmetic on the past and not a prediction — and a forecast
// that hides its method is one people trust more than it deserves.
//
// # Nothing here is stored
//
// Every figure is a question about facts that already exist. Materialising them
// would create a second copy of the shop's numbers, free to drift from the
// ledger they came from.

/** D2's thirteen figures, in one answer so they cannot disagree. */
export interface KPIs {
  from: string;
  to: string;
  currency: string;

  revenue: string;
  gross_profit: string;
  gross_margin_pct: string;
  orders: number;
  average_order_value: string;
  units_per_transaction: string;
  discount_ratio_pct: string;
  return_rate_pct: string;

  /** Empty when the shop has not traded long enough to have one. */
  inventory_turnover: string;
  repeat_customer_pct: string;
  customer_lifetime_value: string;

  sales_per_store: string;
  sales_per_employee: string;
}

/**
 * Whether the server actually stated a figure.
 *
 * Presence, not value. `"0.00"` is a real answer — a month with no returns has
 * a return rate of zero — and treating it as unstated would hide a fact.
 * `""` is the server saying it cannot answer yet.
 */
export function stated(value: string | undefined | null): boolean {
  return typeof value === 'string' && value.trim() !== '';
}

/** One product, measured. Fast and dead stock are the same query, sorted. */
export interface Mover {
  variant_id: string;
  sku: string;
  product: string;
  sold_qty: string;
  revenue: string;
  profit: string;
  on_hand: string;
  velocity: string;
  days_cover?: number;
  reorder_on?: string;
  /** -1 when it has never sold. Not 0, which would mean "sold today". */
  days_since_sold: number;
  currency: string;
}

/**
 * Whether a line is stock that is not moving.
 *
 * `days_since_sold === -1` means it has never sold at all, which is the worst
 * case and the one an owner most wants to see. Anything sitting on the shelf
 * with no sale in the period counts too.
 */
export function isDead(m: Mover): boolean {
  return m.days_since_sold === -1 || Number(m.sold_qty) === 0;
}

/** The two ways the same measurement is read. */
export const MOVER_VIEWS = ['fast', 'dead'] as const;
export type MoverView = (typeof MOVER_VIEWS)[number];

/**
 * The movers, sorted for the question being asked.
 *
 * Fast is by revenue, because "what is selling" means "what is earning" — units
 * sold puts a pile of cheap items above the thing that pays the rent. Dead is
 * by what is tied up in it, for the same reason in reverse.
 */
export function moversFor(rows: Mover[], view: MoverView): Mover[] {
  const alive = rows.filter((m) => !isDead(m));
  const dead = rows.filter(isDead);
  if (view === 'dead') {
    return [...dead].sort((a, b) => Number(b.on_hand) - Number(a.on_hand));
  }
  return [...alive].sort((a, b) => Number(b.revenue) - Number(a.revenue));
}

/** What the next month is expected to need. */
export interface ForecastLine {
  variant_id: string;
  sku: string;
  product: string;
  window_days: number;
  sold_in_window: string;
  velocity: string;
  forecast_days: number;
  expected_demand: string;
  on_hand: string;
  shortfall: string;
  /** The server's own words about what this is. Shown as written. */
  basis: string;
}

/**
 * Whether a line is short, and by enough to act on.
 *
 * The shortfall is the server's arithmetic; this only asks whether it is worth
 * putting in front of somebody. A shortfall of zero is a line that is fine.
 */
export function isShort(line: ForecastLine): boolean {
  const n = Number(line.shortfall);
  return Number.isFinite(n) && n > 0;
}

/** Profit by category, with credit notes subtracted rather than excluded. */
export interface ProfitLine {
  id: string;
  label: string;
  revenue: string;
  cost: string;
  profit: string;
  margin_pct: string;
  units: string;
  currency: string;
}

/** How the workforce is made up, and how much of it is local. */
export interface Workforce {
  total: number;
  saudi: number;
  non_saudi: number;
  saudi_share: string;
  expiring_soon: number;
  expired: number;
  by_department: { department: string; total: number; saudi: number }[];
}

/** A report somebody keeps, and possibly schedules. */
export interface SavedReport {
  id: string;
  name: string;
  kind: string;
  /** A relative phrase, never two dates. */
  period: string;
  store_id?: string;
  warehouse_id?: string;
  account_id?: string;
  cadence?: string;
  day_of_week?: number;
  day_of_month?: number;
  recipients?: string;
  last_run_at?: string;
  last_run_error?: string;
  is_active: boolean;
  /** What the relative period resolves to today, so the screen can show it. */
  from: string;
  to: string;
}

/** The reports that can be kept. The server's own vocabulary. */
export const SAVED_KINDS = [
  'trial_balance',
  'profit_and_loss',
  'balance_sheet',
  'cash_flow',
  'sales',
  'expenses',
  'stock',
  'vat_return',
  'receivables',
  'payables',
  'movers',
  'compliance',
] as const;
export type SavedKind = (typeof SAVED_KINDS)[number];

/**
 * The windows a saved report can cover.
 *
 * Relative, never two dates. "Last month" run in October means September and
 * the same report run in November means October — storing dates would make a
 * saved report a snapshot, and a schedule built on them would email the same
 * figures for ever.
 */
export const SAVED_PERIODS = [
  'today',
  'this_week',
  'this_month',
  'last_month',
  'this_quarter',
  'last_quarter',
  'this_year',
  'last_year',
] as const;
export type SavedPeriod = (typeof SAVED_PERIODS)[number];

/** How often a kept report is sent. Nothing is sent without a schedule. */
export const CADENCES = ['daily', 'weekly', 'monthly'] as const;
export type Cadence = (typeof CADENCES)[number];

/** Why a saved report cannot be kept yet. */
export type SavedProblem =
  | 'no_name'
  | 'no_recipients'
  | 'no_day'
  | 'no_date'
  | 'none';

/**
 * Whether a saved report is ready to keep.
 *
 * The schedule rules are the database's, mirrored: a cadence with nobody to
 * send to "runs every week and reaches nobody", a weekly one needs a day, a
 * monthly one needs a date — and that date stops at 28, because a schedule set
 * for the 31st skips February and a shop that asked for monthly figures
 * quietly gets eleven.
 */
export function savedProblem(draft: {
  name: string;
  cadence: string;
  recipients: string;
  dayOfWeek: string;
  dayOfMonth: string;
}): SavedProblem {
  if (draft.name.trim() === '') return 'no_name';
  if (draft.cadence === '') return 'none';
  if (draft.recipients.trim() === '') return 'no_recipients';
  if (draft.cadence === 'weekly') {
    const d = wholeNumber(draft.dayOfWeek);
    if (d === null || d < 0 || d > 6) return 'no_day';
  }
  if (draft.cadence === 'monthly') {
    const d = wholeNumber(draft.dayOfMonth);
    if (d === null || d < 1 || d > 28) return 'no_date';
  }
  return 'none';
}

/**
 * A whole number, or null when nothing was chosen.
 *
 * `Number('')` is 0, not NaN — so a weekly schedule with no day picked passed
 * every range check as Sunday, and would have been saved to send on a day
 * nobody chose. Caught by a test that expected the empty case to be refused.
 * Zero itself is a real answer here, which is exactly why blank cannot be it.
 */
function wholeNumber(raw: string): number | null {
  const text = raw.trim();
  if (text === '') return null;
  const n = Number(text);
  return Number.isInteger(n) ? n : null;
}

/** The kinds the export route can take away, in its own spelling. */
const EXPORTABLE: Record<string, string> = {
  trial_balance: 'trial-balance',
  profit_and_loss: 'profit-and-loss',
  balance_sheet: 'balance-sheet',
  cash_flow: 'cash-flow',
  vat_return: 'vat-return',
  sales: 'sales',
  expenses: 'expenses',
  stock: 'stock',
};

/**
 * The export path for a saved report's kind, or null.
 *
 * The two vocabularies differ — the saved-report table writes `trial_balance`
 * and the export route takes `trial-balance` — and four savable kinds have no
 * export at all. Returning null rather than guessing a path means the screen
 * shows no button instead of one that 400s.
 */
export function exportKindOf(kind: string): string | null {
  return EXPORTABLE[kind] ?? null;
}
