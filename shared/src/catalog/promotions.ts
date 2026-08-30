// What a promotion says, and whether it is running.

import type { Promotion } from '../api/promotions';
import type { Key } from '../i18n/strings';

/** Whether a campaign is on, waiting, or over.
 *
 *  Three states rather than the `is_active` flag alone, because a campaign that
 *  is switched on and starts next month is not running, and a list that showed
 *  it as running would have somebody at a till wondering why the discount is
 *  not coming off.
 *
 *  Compared as ISO date STRINGS. They sort correctly, and building a Date per
 *  row to ask which side of today it falls on is work for nothing on a list
 *  that may be long. */
export type Liveness = 'running' | 'scheduled' | 'finished';

export function isLive(p: Promotion, today = isoToday()): Liveness {
  if (!p.is_active) return 'finished';
  if (p.ends_on && p.ends_on < today) return 'finished';
  if (p.starts_on && p.starts_on > today) return 'scheduled';
  return 'running';
}

function isoToday(): string {
  return new Date().toISOString().slice(0, 10);
}

/** What a promotion offers, as a sentence.
 *
 *  The four mechanisms read very differently — "10% off" and "3 for 100" are
 *  not the same shape of statement — so each has its own phrasing in the
 *  catalogue rather than a generic "kind: value" rendering that would be
 *  unreadable in all three languages at once. */
export function describeKind(
  p: Promotion,
  t: (k: Key, vars?: Record<string, string>) => string,
): string {
  switch (p.kind) {
    case 'percentage':
      return t('promo.says.percentage', { value: p.value ?? '' });
    case 'amount':
      return t('promo.says.amount', { value: p.value ?? '' });
    case 'buy_x_get_y':
      return t('promo.says.buy_x_get_y', {
        buy: p.buy_qty ?? '',
        get: p.get_qty ?? '',
      });
    case 'bundle_price':
      return t('promo.says.bundle_price', {
        qty: p.buy_qty ?? '',
        price: p.value ?? '',
      });
    default:
      return p.kind;
  }
}
