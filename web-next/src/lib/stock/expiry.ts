// When a lot stops being sellable.
//
// A date that has passed and a date that is close both need somebody, and they
// need different somebodies: one needs the stock pulled off the shelf today,
// the other needs it sold this month. So they are separate states rather than
// one "attention" flag.

export type { Batch } from './stock';

export type ExpiryState = 'none' | 'expired' | 'soon' | 'fine';

/**
 * Thirty days.
 *
 * Not a rule the backend states, and deliberately not presented as one: the
 * screen uses it to decide what to draw attention to, and nothing is refused or
 * written because of it. A shop with a different shelf life reads the date
 * itself, which is always on the row beside the badge.
 */
const SOON_DAYS = 30;

/**
 * Compared at day resolution, in the viewer's own timezone.
 *
 * An expiry date is a calendar date rather than an instant — a lot is good
 * until the end of the day printed on it — so comparing timestamps would mark
 * a batch expired for anybody west of the shop.
 */
export function expiryState(expiresOn: string | undefined, today = new Date()): ExpiryState {
  if (!expiresOn) return 'none';

  const parts = expiresOn.slice(0, 10).split('-').map(Number);
  if (parts.length !== 3 || parts.some((n) => !Number.isFinite(n))) return 'none';

  const [y, m, d] = parts as [number, number, number];
  const expiry = Date.UTC(y, m - 1, d);
  const now = Date.UTC(today.getFullYear(), today.getMonth(), today.getDate());

  const days = Math.round((expiry - now) / 86_400_000);
  if (days < 0) return 'expired';
  if (days <= SOON_DAYS) return 'soon';
  return 'fine';
}
