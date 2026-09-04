import { describe, expect, it } from 'vitest';

import { denominationsFor, totalOf, varianceOf } from './denominations';

describe('a drawer is counted, not calculated', () => {
  it('sums a Saudi drawer exactly, coins included', () => {
    // 4x500 + 11x100 + 3x0.05. The last term is the one that matters:
    // 0.05 * 3 through a float is 0.15000000000000002, and a till two
    // hundredths out is a variance somebody has to investigate.
    const counted = totalOf({ '500': '4', '100': '11', '0.05': '3' }, 'SAR');
    expect(counted).toBe('3100.15');
  });

  it('sums a Bangladeshi drawer', () => {
    expect(totalOf({ '1000': '3', '500': '2', '20': '7' }, 'BDT')).toBe('4140.00');
  });

  it('sums American coins without float drift', () => {
    // Four quarters, three dimes, two nickels, one cent.
    expect(totalOf({ '0.25': '4', '0.10': '3', '0.05': '2', '0.01': '1' }, 'USD')).toBe(
      '1.41',
    );
  });

  it('ignores blanks, zeroes and anything that is not a count', () => {
    expect(
      totalOf({ '500': '', '100': '0', '50': '-2', '10': 'abc', '5': '3' }, 'SAR'),
    ).toBe('15.00');
  });

  it('is zero when nothing has been counted', () => {
    expect(totalOf({}, 'SAR')).toBe('0.00');
  });
});

describe('the variance says over or short, and by how much', () => {
  it('reports a drawer with too much in it as positive', () => {
    expect(varianceOf('1000.50', '1000.00')).toBe('0.50');
  });

  it('reports a drawer that is light as negative', () => {
    expect(varianceOf('980.00', '1000.00')).toBe('-20.00');
  });

  it('reports an exact drawer as zero, not as nearly zero', () => {
    expect(varianceOf('1000.00', '1000.00')).toBe('0.00');
  });

  it('treats a missing expected figure as zero rather than failing', () => {
    // On a blind close the server withholds the expected figure until the
    // count is committed, so the screen has nothing to compare against and
    // must not crash trying.
    expect(varianceOf('500.00', '')).toBe('500.00');
  });
});

describe('currencies the product has no drawer for', () => {
  it('offers no counting grid rather than a guessed one', () => {
    // Guessing another country's notes would put denominations on screen that
    // do not exist there. The screen falls back to a single total instead.
    expect(denominationsFor('EUR')).toEqual([]);
    expect(totalOf({ '500': '4' }, 'EUR')).toBe('0.00');
  });

  it('knows the three markets the product sells into', () => {
    expect(denominationsFor('SAR').length).toBeGreaterThan(0);
    expect(denominationsFor('BDT').length).toBeGreaterThan(0);
    expect(denominationsFor('usd').length).toBeGreaterThan(0);
  });

  it('lists largest first, which is the order a drawer is counted in', () => {
    const sar = denominationsFor('SAR');
    expect(sar[0]?.value).toBe('500');
    expect(sar[sar.length - 1]?.value).toBe('0.05');
  });
});
