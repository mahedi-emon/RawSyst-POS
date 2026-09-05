import { describe, expect, it } from 'vitest';

import {
  exportKindOf,
  isDead,
  isShort,
  moversFor,
  savedProblem,
  stated,
  type ForecastLine,
  type Mover,
} from './analytics';

function mover(over: Partial<Mover> = {}): Mover {
  return {
    variant_id: 'v1',
    sku: 'SKU',
    product: 'A product',
    sold_qty: '10',
    revenue: '100.00',
    profit: '40.00',
    on_hand: '5',
    velocity: '0.1',
    days_since_sold: 0,
    currency: 'SAR',
    ...over,
  };
}

describe('stated', () => {
  it('is false for a figure the shop cannot answer yet', () => {
    // Inventory turnover on a business that opened last week. The server sends
    // "" rather than 0, and a shop told its repeat-customer rate is 0%
    // concludes nobody returns.
    expect(stated('')).toBe(false);
    expect(stated('   ')).toBe(false);
    expect(stated(undefined)).toBe(false);
  });

  it('is TRUE for a genuine zero', () => {
    // A month with no returns has a return rate of zero, and hiding it would
    // hide a fact.
    expect(stated('0.0')).toBe(true);
    expect(stated('0')).toBe(true);
  });
});

describe('isDead', () => {
  it('catches something that has never sold', () => {
    // -1, not 0. Zero would mean "sold today".
    expect(isDead(mover({ days_since_sold: -1, sold_qty: '0' }))).toBe(true);
  });

  it('catches something that did not sell in the period', () => {
    expect(isDead(mover({ sold_qty: '0', days_since_sold: 200 }))).toBe(true);
  });

  it('leaves something that sold alone', () => {
    expect(isDead(mover())).toBe(false);
  });
});

describe('moversFor', () => {
  const rows = [
    mover({ variant_id: 'cheap', sold_qty: '500', revenue: '250.00' }),
    mover({ variant_id: 'dear', sold_qty: '3', revenue: '9000.00' }),
    mover({
      variant_id: 'dead-big',
      sold_qty: '0',
      revenue: '0.00',
      on_hand: '80',
      days_since_sold: -1,
    }),
    mover({
      variant_id: 'dead-small',
      sold_qty: '0',
      revenue: '0.00',
      on_hand: '2',
      days_since_sold: -1,
    }),
  ];

  it('reads fast movers by what they earn, not by units', () => {
    // A pile of cheap items above the thing that pays the rent answers a
    // different question from the one being asked.
    expect(moversFor(rows, 'fast').map((m) => m.variant_id)).toEqual([
      'dear',
      'cheap',
    ]);
  });

  it('reads dead stock by what is tied up in it', () => {
    expect(moversFor(rows, 'dead').map((m) => m.variant_id)).toEqual([
      'dead-big',
      'dead-small',
    ]);
  });

  it('never shows the same line in both', () => {
    const fast = new Set(moversFor(rows, 'fast').map((m) => m.variant_id));
    for (const m of moversFor(rows, 'dead')) {
      expect(fast.has(m.variant_id)).toBe(false);
    }
  });
});

describe('isShort', () => {
  function line(shortfall: string): ForecastLine {
    return {
      variant_id: 'v1',
      sku: 'SKU',
      product: 'A product',
      window_days: 90,
      sold_in_window: '60',
      velocity: '0.6667',
      forecast_days: 30,
      expected_demand: '21',
      on_hand: '74',
      shortfall,
      basis: 'sales over the last 90 days, repeated',
    };
  }

  it('is short when the server says there is a gap', () => {
    expect(isShort(line('12'))).toBe(true);
  });

  it('is not short at zero', () => {
    expect(isShort(line('0'))).toBe(false);
  });

  it('is not short on a figure that is not a number', () => {
    expect(isShort(line(''))).toBe(false);
  });
});

describe('savedProblem', () => {
  const base = {
    name: 'Monthly figures',
    cadence: '',
    recipients: '',
    dayOfWeek: '',
    dayOfMonth: '',
  };

  it('passes a report kept by hand', () => {
    expect(savedProblem(base)).toBe('none');
  });

  it('needs a name', () => {
    expect(savedProblem({ ...base, name: '  ' })).toBe('no_name');
  });

  it('refuses a schedule with nobody to send to', () => {
    // "A schedule with nobody to send it to runs every week and reaches
    // nobody" — the server's own words.
    expect(savedProblem({ ...base, cadence: 'weekly', dayOfWeek: '1' })).toBe(
      'no_recipients',
    );
  });

  it('needs a day for a weekly schedule', () => {
    expect(
      savedProblem({
        ...base,
        cadence: 'weekly',
        recipients: 'a@example.test',
      }),
    ).toBe('no_day');
  });

  it('stops a monthly schedule at the 28th', () => {
    // A schedule set for the 31st skips February entirely, and a shop that
    // asked for monthly figures quietly gets eleven of them.
    expect(
      savedProblem({
        ...base,
        cadence: 'monthly',
        recipients: 'a@example.test',
        dayOfMonth: '31',
      }),
    ).toBe('no_date');
    expect(
      savedProblem({
        ...base,
        cadence: 'monthly',
        recipients: 'a@example.test',
        dayOfMonth: '28',
      }),
    ).toBe('none');
  });

  it('takes Sunday as a valid weekly day', () => {
    // Zero is a day, and a check written as `!d` would refuse it.
    expect(
      savedProblem({
        ...base,
        cadence: 'weekly',
        recipients: 'a@example.test',
        dayOfWeek: '0',
      }),
    ).toBe('none');
  });
});

describe('exportKindOf', () => {
  it('translates between the two vocabularies', () => {
    // The saved-report table writes trial_balance; the export route takes
    // trial-balance.
    expect(exportKindOf('trial_balance')).toBe('trial-balance');
    expect(exportKindOf('vat_return')).toBe('vat-return');
  });

  it('is null for a kind that cannot be exported', () => {
    // Rather than guessing a path, so the screen shows no button instead of
    // one that answers 400.
    expect(exportKindOf('receivables')).toBeNull();
    expect(exportKindOf('compliance')).toBeNull();
    expect(exportKindOf('movers')).toBeNull();
  });
});
