import { describe, expect, it } from 'vitest';

import type { FiscalCalendar, Period } from '../api/accounting';
import {
  closablePeriod,
  isOff,
  monthToDate,
  nextYearToOpen,
  periodsTouching,
  reopenable,
  settlednessOf,
} from './accounting';

function period(over: Partial<Period> = {}): Period {
  return {
    id: 'p1',
    fiscal_year: 2026,
    period_no: 1,
    starts_on: '2026-01-01',
    ends_on: '2026-01-31',
    state: 'open',
    entries: 0,
    ...over,
  };
}

function calendar(periods: Period[]): FiscalCalendar {
  const years = new Map<number, Period[]>();
  for (const p of periods) {
    years.set(p.fiscal_year, [...(years.get(p.fiscal_year) ?? []), p]);
  }
  return {
    fiscal_year_start_month: 1,
    years: [...years.entries()].map(([fiscal_year, ps]) => ({
      fiscal_year,
      periods: ps,
    })),
  };
}

// The judgement this module exists for. A profit-and-loss covering a month that
// is still open will be a different number tomorrow, and a person about to send
// one to a bank should be told that before they send it.
describe('how settled the figures over a range are', () => {
  const jan = period({ id: 'jan', period_no: 1, starts_on: '2026-01-01', ends_on: '2026-01-31' });
  const feb = period({ id: 'feb', period_no: 2, starts_on: '2026-02-01', ends_on: '2026-02-28' });

  it('is settled when every month it touches is closed', () => {
    const c = calendar([
      { ...jan, state: 'closed' },
      { ...feb, state: 'locked' },
    ]);
    expect(settlednessOf(c, { from: '2026-01-01', to: '2026-02-28' })).toBe('settled');
  });

  it('is open when none of them are', () => {
    expect(
      settlednessOf(calendar([jan, feb]), { from: '2026-01-01', to: '2026-02-28' }),
    ).toBe('open');
  });

  it('says so when a range straddles a closed month and an open one', () => {
    const c = calendar([{ ...jan, state: 'closed' }, feb]);
    expect(settlednessOf(c, { from: '2026-01-01', to: '2026-02-28' })).toBe('partly');
  });

  // Saying "open" while the answer is still arriving would be a claim the
  // screen cannot support.
  it('says nothing at all before the calendar has loaded', () => {
    expect(settlednessOf(null, { from: '2026-01-01', to: '2026-01-31' })).toBe('unknown');
  });

  it('says nothing for a range no period covers', () => {
    expect(
      settlednessOf(calendar([jan]), { from: '2030-01-01', to: '2030-01-31' }),
    ).toBe('unknown');
  });
});

describe('which periods a range touches', () => {
  const jan = period({ id: 'jan', starts_on: '2026-01-01', ends_on: '2026-01-31' });
  const feb = period({ id: 'feb', period_no: 2, starts_on: '2026-02-01', ends_on: '2026-02-28' });
  const mar = period({ id: 'mar', period_no: 3, starts_on: '2026-03-01', ends_on: '2026-03-31' });
  const c = calendar([jan, feb, mar]);

  it('includes a month the range only clips', () => {
    const hit = periodsTouching(c, { from: '2026-01-28', to: '2026-02-03' });
    expect(hit.map((p) => p.id)).toEqual(['jan', 'feb']);
  });

  it('includes a month the range sits entirely inside', () => {
    const hit = periodsTouching(c, { from: '2026-02-10', to: '2026-02-11' });
    expect(hit.map((p) => p.id)).toEqual(['feb']);
  });

  it('excludes a month that ends the day before the range starts', () => {
    const hit = periodsTouching(c, { from: '2026-02-01', to: '2026-02-28' });
    expect(hit.map((p) => p.id)).toEqual(['feb']);
  });
});

// Periods close in order. Offering Close on every open month would mean most
// presses come back refused, and a screen whose buttons usually fail is one
// people stop believing.
describe('which period may be closed', () => {
  it('is the earliest one still open', () => {
    const c = calendar([
      period({ id: 'jan', state: 'closed', starts_on: '2026-01-01', ends_on: '2026-01-31' }),
      period({ id: 'feb', period_no: 2, starts_on: '2026-02-01', ends_on: '2026-02-28' }),
      period({ id: 'mar', period_no: 3, starts_on: '2026-03-01', ends_on: '2026-03-31' }),
    ]);
    expect(closablePeriod(c)).toBe('feb');
  });

  it('is nothing when every period is closed', () => {
    const c = calendar([period({ id: 'jan', state: 'closed' })]);
    expect(closablePeriod(c)).toBeNull();
  });

  it('is nothing before the calendar has loaded', () => {
    expect(closablePeriod(null)).toBeNull();
  });
});

describe('which period may be reopened', () => {
  it('is a closed one', () => {
    expect(reopenable(period({ state: 'closed' }))).toBe(true);
  });

  // Locked means the year-end routine has closed revenue and expense into
  // retained earnings. Putting a transaction back would leave those entries
  // wrong with nothing saying so.
  it('is never a locked one', () => {
    expect(reopenable(period({ state: 'locked' }))).toBe(false);
  });

  it('is never one that is already open', () => {
    expect(reopenable(period({ state: 'open' }))).toBe(false);
  });
});

describe('the next year to offer', () => {
  it('is one past the latest there is', () => {
    const c = calendar([
      period({ fiscal_year: 2026 }),
      period({ id: 'p2', fiscal_year: 2027 }),
    ]);
    expect(nextYearToOpen(c)).toBe(2028);
  });

  it('falls back to this year when there is no calendar at all', () => {
    expect(nextYearToOpen(calendar([]))).toBe(new Date().getUTCFullYear());
    expect(nextYearToOpen(null)).toBe(new Date().getUTCFullYear());
  });
});

// A trial balance that does not balance must SAY so. Asking the question of a
// float is the one operation guaranteed to sometimes lie about it.
describe('whether a figure that must be zero is not', () => {
  it('accepts every spelling of nothing', () => {
    expect(isOff('0')).toBe(false);
    expect(isOff('0.00')).toBe(false);
    expect(isOff('-0.00')).toBe(false);
    expect(isOff('')).toBe(false);
  });

  it('catches a difference in the last minor unit, either way', () => {
    expect(isOff('0.01')).toBe(true);
    expect(isOff('-0.01')).toBe(true);
    expect(isOff('1,234.00')).toBe(true);
  });
});

describe('the range a statement opens on', () => {
  it('is this month so far', () => {
    const r = monthToDate(new Date('2026-08-30T09:00:00Z'));
    expect(r).toEqual({ from: '2026-08-01', to: '2026-08-30' });
  });

  it('handles the first of the month', () => {
    const r = monthToDate(new Date('2026-08-01T00:30:00Z'));
    expect(r).toEqual({ from: '2026-08-01', to: '2026-08-01' });
  });
});
