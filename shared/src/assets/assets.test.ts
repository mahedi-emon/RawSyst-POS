import { describe, expect, it } from 'vitest';

import type { Asset } from '../api/assets';
import { disposalOutcome, monthAfter, monthLabelOf, today } from './assets';

function asset(over: Partial<Asset> = {}): Asset {
  return {
    id: 'a1',
    asset_no: 'FA-000001',
    name: 'Delivery van',
    category: 'vehicle',
    acquired_on: '2026-01-10',
    cost: '60000.00',
    residual_value: '12000.00',
    useful_life_months: 48,
    currency: 'SAR',
    depreciated: '0.00',
    book_value: '60000.00',
    monthly_charge: '1000.00',
    months_due: 0,
    status: 'in_use',
    ...over,
  };
}

// The judgement that decides whether four months of an asset's cost ever reach
// the profit and loss.
describe('which month depreciation is owed for', () => {
  it('is the month after the last one charged', () => {
    expect(
      monthAfter([asset({ depreciated_to: '2026-03-01', months_due: 4 })]),
    ).toBe('2026-04-01');
  });

  it('rolls over the year end', () => {
    expect(
      monthAfter([asset({ depreciated_to: '2026-12-01', months_due: 1 })]),
    ).toBe('2027-01-01');
  });

  it('is the month it was acquired when nothing has been charged', () => {
    expect(
      monthAfter([asset({ acquired_on: '2026-01-10', months_due: 6 })]),
    ).toBe('2026-01-01');
  });

  // The whole point. "Last month" would skip the four months in between for
  // ever, and the asset would reach the end of its life with four months of
  // cost never charged to anything.
  it('is the EARLIEST month anybody owes, not the most recent', () => {
    const behind = asset({
      id: 'behind',
      depreciated_to: '2026-03-01',
      months_due: 4,
    });
    const uptodate = asset({
      id: 'uptodate',
      depreciated_to: '2026-06-01',
      months_due: 1,
    });
    expect(monthAfter([uptodate, behind])).toBe('2026-04-01');
  });

  it('offers nothing when every asset is up to date', () => {
    expect(monthAfter([asset({ months_due: 0 })])).toBeNull();
    expect(monthAfter([])).toBeNull();
  });

  it('ignores an asset that has been disposed of', () => {
    expect(
      monthAfter([
        asset({ status: 'disposed', months_due: 5, depreciated_to: '2026-01-01' }),
      ]),
    ).toBeNull();
  });
});

describe('naming a month', () => {
  it('reads as a person would say it', () => {
    expect(monthLabelOf('2026-04-01')).toBe('Apr 2026');
  });

  it('falls back to the date it was given rather than to nothing', () => {
    expect(monthLabelOf('not-a-date')).toBe('not-a-date');
  });
});

// A preview, not the posted figure — the server computes that from what it has
// really depreciated. But a preview that lied would be worse than none.
describe('which way a disposal will land', () => {
  it('is a gain above book value', () => {
    expect(disposalOutcome('59000.00', '62000.00')).toBe('gain');
  });

  it('is a loss below it', () => {
    expect(disposalOutcome('59000.00', '50000.00')).toBe('loss');
  });

  // Compared as scaled integers. Asking a float whether 59000.00 equals
  // 59000.00 is safe; asking it about figures that arrived through arithmetic
  // is not, and this comparison has to be exact.
  it('is neither at exactly book value', () => {
    expect(disposalOutcome('59000.00', '59000.00')).toBe('even');
    expect(disposalOutcome('0.30', '0.30')).toBe('even');
  });

  it('catches a difference of one minor unit', () => {
    expect(disposalOutcome('59000.00', '59000.01')).toBe('gain');
    expect(disposalOutcome('59000.00', '58999.99')).toBe('loss');
  });

  it('reads a scrapped asset as a loss of its whole book value', () => {
    expect(disposalOutcome('59000.00', '0')).toBe('loss');
  });

  it('says nothing useful about a half-typed figure rather than guessing', () => {
    expect(disposalOutcome('59000.00', '')).toBe('even');
    expect(disposalOutcome('59000.00', '-')).toBe('even');
  });
});

describe('the date a form opens on', () => {
  it('is today', () => {
    expect(today(new Date('2026-08-30T09:00:00Z'))).toBe('2026-08-30');
  });
});
