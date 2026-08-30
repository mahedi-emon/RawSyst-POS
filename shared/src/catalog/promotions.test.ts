import { describe, expect, it } from 'vitest';

import type { Promotion } from '../api/promotions';
import { isLive } from './promotions';

function promo(over: Partial<Promotion> = {}): Promotion {
  return {
    id: 'p1',
    code: 'SUMMER',
    name: 'Summer sale',
    kind: 'percentage',
    value: '10',
    applies_to: '',
    is_active: true,
    priority: 0,
    times_used: 0,
    discount_given: '0.00',
    sales_generated: '0.00',
    currency: 'SAR',
    ...over,
  };
}

// Three states rather than the on/off flag alone. A campaign switched on that
// starts next month is not running, and a list that said it was would have
// somebody at a till wondering why the discount is not coming off.
describe('whether a campaign is running', () => {
  const today = '2026-08-30';

  it('is running when it is on and today is inside its dates', () => {
    expect(
      isLive(promo({ starts_on: '2026-08-01', ends_on: '2026-09-30' }), today),
    ).toBe('running');
  });

  it('is running when it has no dates at all', () => {
    expect(isLive(promo(), today)).toBe('running');
  });

  it('is scheduled when it has not started', () => {
    expect(isLive(promo({ starts_on: '2026-09-01' }), today)).toBe('scheduled');
  });

  it('is finished when it has ended', () => {
    expect(isLive(promo({ ends_on: '2026-08-29' }), today)).toBe('finished');
  });

  // The last day is inclusive. A campaign "until the 30th" that stopped working
  // on the morning of the 30th is the kind of thing a customer argues about at
  // the counter.
  it('is still running on its last day', () => {
    expect(isLive(promo({ ends_on: '2026-08-30' }), today)).toBe('running');
  });

  it('is still running on its first day', () => {
    expect(isLive(promo({ starts_on: '2026-08-30' }), today)).toBe('running');
  });

  // Switched off beats every date.
  it('is finished when it has been stopped, whatever its dates say', () => {
    expect(
      isLive(
        promo({ is_active: false, starts_on: '2026-08-01', ends_on: '2026-09-30' }),
        today,
      ),
    ).toBe('finished');
  });
});
