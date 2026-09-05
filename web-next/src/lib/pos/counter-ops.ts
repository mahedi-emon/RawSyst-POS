// The till, after the selling: shifts, promotions, points and store credit.
//
// # A blind close is defeated by showing the expected figure
//
// The whole point of counting a drawer without being told the target is that
// the variance means something. A cashier who can see what the system expects
// can make the drawer agree with it, and then the variance reads zero on every
// shift and the one signal the practice produces is gone. That is why the
// register and the X report sit behind `report.view` while the till itself has
// `sales.receive_payment` — and why nothing on this side ever computes an
// expected figure from the sales it can see.
//
// # A drawer nobody has counted is not a drawer counted at nothing
//
// `counted_cash`, `expected_cash` and `variance` are ABSENT on an open session,
// not zero. `counted()` asks whether they arrived. A zero would send a
// supervisor to every till in the shop.
//
// # A scheme that does not exist is not a scheme set to zero
//
// `GET /loyalty/program` reports `exists: false` with empty rates. A form full
// of defaults that are not in force reads as a scheme somebody has configured,
// and a shop would hand out points nobody can spend.

/** One shift, as a supervisor reads it the morning after. */
export interface Shift {
  id: string;
  session_no: number;
  state: string;

  store_id: string;
  store: string;
  device_id: string;
  device: string;
  opened_by: string;
  closed_by?: string;

  opened_at: string;
  closed_at?: string;
  opening_float: string;

  /** All three absent while the session is still open. */
  counted_cash?: string;
  expected_cash?: string;
  variance?: string;

  blind_close: boolean;
}

/** Whether a drawer has actually been counted. */
export function counted(shift: Shift): boolean {
  return shift.counted_cash !== undefined;
}

/** How a shift's variance reads. */
export type VarianceState = 'over' | 'short' | 'exact' | 'uncounted';

/**
 * What the variance says about a drawer.
 *
 * Over and short are kept apart rather than shown as one number with a sign.
 * They are different problems: a shortfall is money missing, a surplus is money
 * that should not be there — a sale rung up wrong, or change not given. A
 * manager reads them differently and a minus sign is easy to miss.
 */
export function varianceState(shift: Shift): VarianceState {
  if (!counted(shift)) return 'uncounted';
  const raw = (shift.variance ?? '').trim();
  if (raw === '') return 'uncounted';
  const n = Number(raw);
  if (!Number.isFinite(n)) return 'uncounted';
  if (n > 0) return 'over';
  if (n < 0) return 'short';
  return 'exact';
}

/** A shift that a supervisor should go and look at. */
export function needsLooking(shift: Shift): boolean {
  const state = varianceState(shift);
  return state === 'over' || state === 'short';
}

/** What the shifts in view come to, so the list has a bottom line. */
export function shiftTotals(shifts: Shift[]): {
  counted: number;
  open: number;
  outBy: string;
} {
  let out = 0;
  let countedShifts = 0;
  let open = 0;
  for (const s of shifts) {
    if (s.state === 'open') open += 1;
    if (!counted(s)) continue;
    countedShifts += 1;
    const n = Number(s.variance ?? '');
    if (Number.isFinite(n)) out += n;
  }
  return { counted: countedShifts, open, outBy: out.toFixed(2) };
}

/** A promotion in force, or waiting, or finished. */
export interface Promotion {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  kind: string;

  value?: string;
  buy_qty?: string;
  get_qty?: string;

  category_id?: string;
  brand_id?: string;
  variant_id?: string;
  customer_type?: string;
  /** What the scope amounts to, in words, from the server. */
  applies_to: string;

  starts_on?: string;
  ends_on?: string;
  store_id?: string;
  min_purchase?: string;

  coupon_code?: string;
  max_uses?: number;
  max_uses_per_customer?: number;

  is_active: boolean;
  priority: number;

  /** What it has cost so far, on the row rather than behind a report. */
  times_used: number;
  discount_given: string;
  sales_generated: string;
  currency: string;
}

/** The four shapes a promotion can take. The server's own vocabulary. */
export const PROMOTION_KINDS = [
  'percentage',
  'amount',
  'buy_x_get_y',
  'bundle_price',
] as const;
export type PromotionKind = (typeof PROMOTION_KINDS)[number];

/** Where a promotion is in its life. */
export type PromotionState = 'off' | 'waiting' | 'running' | 'finished';

/**
 * What a promotion is doing today.
 *
 * Four states, not two. "Inactive" covers a campaign somebody switched off, one
 * that has not started and one that finished last month, and an owner asking
 * why a discount is not applying needs to know which. Dates are compared as
 * calendar days from the local date, never from a timestamp — a promotion that
 * ends today ends at the end of today.
 */
export function promotionState(p: Promotion, today = localDay()): PromotionState {
  if (!p.is_active) return 'off';
  if (p.starts_on && p.starts_on > today) return 'waiting';
  if (p.ends_on && p.ends_on < today) return 'finished';
  return 'running';
}

/** Today, as an ISO calendar day in the reader's own timezone. */
export function localDay(now: Date = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(
    now.getDate(),
  ).padStart(2, '0')}`;
}

/**
 * Whether a promotion has run out of uses.
 *
 * Only when a limit was set. A campaign with no cap is never exhausted, and
 * `max_uses` absent must not read as a cap of zero.
 */
export function exhausted(p: Promotion): boolean {
  return p.max_uses !== undefined && p.times_used >= p.max_uses;
}

/** The loyalty scheme, or the absence of one. */
export interface LoyaltyProgram {
  is_active: boolean;
  spend_per_point: string;
  point_value: string;
  expiry_months?: number;
  tiers: LoyaltyTier[] | null;
  currency: string;
  /** Whether a scheme has been set up at all. */
  exists: boolean;
  /** What the shop owes in points, as money. */
  owed: string;
  points_outstanding: number;
}

export interface LoyaltyTier {
  key: string;
  name: string;
  name_ar?: string;
  min_spend: string;
  discount_percent?: string;
}

/** One customer's standing in the scheme. */
export interface LoyaltyMember {
  customer_id: string;
  customer: string;
  points: number;
  worth: string;
  tier?: string;
  next_tier?: string;
  to_next_tier?: string;
  discount_percent?: string;
  lifetime_spend: string;
  visits: number;
  last_purchase?: string;
  currency: string;
  segment: string;
}

/**
 * Whether the scheme is actually earning anybody points.
 *
 * Set up AND switched on. A scheme that exists and is inactive is one somebody
 * paused, and the screen says so — different from never having had one.
 */
export function schemeRunning(p: LoyaltyProgram): boolean {
  return p.exists && p.is_active;
}

/** Store credit held by one customer. */
export interface WalletRow {
  customer_id: string;
  customer?: string;
  balance: string;
  currency: string;
}

/**
 * What the business owes in store credit, over every wallet.
 *
 * Added here rather than asked of the server because the list IS the answer:
 * a total from a second query could disagree with the rows above it, which on
 * a liability is the worst kind of disagreement.
 */
export function creditOwed(rows: WalletRow[]): string {
  let total = 0;
  for (const r of rows) {
    const n = Number((r.balance ?? '').trim());
    if (Number.isFinite(n)) total += n;
  }
  return total.toFixed(2);
}
