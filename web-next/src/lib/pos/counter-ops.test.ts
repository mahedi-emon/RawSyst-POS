import { describe, expect, it } from 'vitest';

import {
  counted,
  creditOwed,
  exhausted,
  needsLooking,
  promotionState,
  schemeRunning,
  shiftTotals,
  varianceState,
  type LoyaltyProgram,
  type Promotion,
  type Shift,
} from './counter-ops';

function shift(over: Partial<Shift> = {}): Shift {
  return {
    id: 's1',
    session_no: 1,
    state: 'closed',
    store_id: 'st1',
    store: 'Main Branch',
    device_id: 'd1',
    device: 'Till 1',
    opened_by: 'A Cashier',
    opened_at: '2026-09-04T09:00:00+03:00',
    opening_float: '200.00',
    counted_cash: '1000.00',
    expected_cash: '1000.00',
    variance: '0.00',
    blind_close: true,
    ...over,
  };
}

describe('counted and varianceState', () => {
  it('knows an open drawer has not been counted', () => {
    const open = shift({
      state: 'open',
      counted_cash: undefined,
      expected_cash: undefined,
      variance: undefined,
    });
    expect(counted(open)).toBe(false);
    expect(varianceState(open)).toBe('uncounted');
    // A zero here would send a supervisor to every till in the shop.
    expect(needsLooking(open)).toBe(false);
  });

  it('keeps over and short apart', () => {
    // Different problems: a shortfall is money missing, a surplus is money
    // that should not be there. A minus sign is easy to miss.
    expect(varianceState(shift({ variance: '15.00' }))).toBe('over');
    expect(varianceState(shift({ variance: '-15.00' }))).toBe('short');
    expect(varianceState(shift({ variance: '0.00' }))).toBe('exact');
  });

  it('sends a supervisor to a drawer that is out either way', () => {
    expect(needsLooking(shift({ variance: '15.00' }))).toBe(true);
    expect(needsLooking(shift({ variance: '-0.50' }))).toBe(true);
    expect(needsLooking(shift())).toBe(false);
  });
});

describe('shiftTotals', () => {
  it('adds only what was counted', () => {
    const total = shiftTotals([
      shift({ variance: '10.00' }),
      shift({ variance: '-4.00' }),
      shift({
        state: 'open',
        counted_cash: undefined,
        variance: undefined,
      }),
    ]);
    expect(total.counted).toBe(2);
    expect(total.open).toBe(1);
    expect(total.outBy).toBe('6.00');
  });

  it('is zero across an empty list', () => {
    expect(shiftTotals([])).toEqual({ counted: 0, open: 0, outBy: '0.00' });
  });
});

function promotion(over: Partial<Promotion> = {}): Promotion {
  return {
    id: 'p1',
    code: 'SUMMER',
    name: 'Summer sale',
    kind: 'percentage',
    value: '10',
    applies_to: 'Everything',
    is_active: true,
    priority: 1,
    times_used: 0,
    discount_given: '0.00',
    sales_generated: '0.00',
    currency: 'SAR',
    ...over,
  };
}

describe('promotionState', () => {
  const today = '2026-09-05';

  it('is running with no dates on it', () => {
    expect(promotionState(promotion(), today)).toBe('running');
  });

  it('keeps switched-off apart from not-yet and finished', () => {
    // "Inactive" would cover all three, and an owner asking why a discount is
    // not applying needs to know which.
    expect(promotionState(promotion({ is_active: false }), today)).toBe('off');
    expect(promotionState(promotion({ starts_on: '2026-10-01' }), today)).toBe(
      'waiting',
    );
    expect(promotionState(promotion({ ends_on: '2026-08-31' }), today)).toBe(
      'finished',
    );
  });

  it('lets a promotion run on the day it ends', () => {
    // It ends at the end of today, not at the start of it.
    expect(promotionState(promotion({ ends_on: today }), today)).toBe('running');
  });

  it('lets a promotion start on the day it starts', () => {
    expect(promotionState(promotion({ starts_on: today }), today)).toBe('running');
  });
});

describe('exhausted', () => {
  it('is false when no cap was set', () => {
    // A campaign with no limit is never exhausted, and an absent max_uses
    // must not read as a cap of zero.
    expect(exhausted(promotion({ times_used: 900 }))).toBe(false);
  });

  it('is true once the cap is reached', () => {
    expect(exhausted(promotion({ max_uses: 100, times_used: 100 }))).toBe(true);
    expect(exhausted(promotion({ max_uses: 100, times_used: 99 }))).toBe(false);
  });
});

describe('schemeRunning', () => {
  function program(over: Partial<LoyaltyProgram> = {}): LoyaltyProgram {
    return {
      is_active: false,
      spend_per_point: '',
      point_value: '',
      tiers: null,
      currency: 'SAR',
      exists: false,
      owed: '0.00',
      points_outstanding: 0,
      ...over,
    };
  }

  it('is false when no scheme was ever set up', () => {
    expect(schemeRunning(program())).toBe(false);
  });

  it('is false for a scheme somebody paused', () => {
    // Different from never having had one, and the screen says which.
    expect(schemeRunning(program({ exists: true, is_active: false }))).toBe(false);
  });

  it('is true only when it exists and is on', () => {
    expect(schemeRunning(program({ exists: true, is_active: true }))).toBe(true);
  });
});

describe('creditOwed', () => {
  it('adds the balances', () => {
    expect(
      creditOwed([
        { customer_id: 'a', balance: '50.00', currency: 'SAR' },
        { customer_id: 'b', balance: '25.50', currency: 'SAR' },
      ]),
    ).toBe('75.50');
  });

  it('is nothing across an empty list', () => {
    expect(creditOwed([])).toBe('0.00');
  });

  it('skips a balance that is not a number rather than becoming NaN', () => {
    expect(
      creditOwed([
        { customer_id: 'a', balance: '50.00', currency: 'SAR' },
        { customer_id: 'b', balance: '', currency: 'SAR' },
      ]),
    ).toBe('50.00');
  });
});
