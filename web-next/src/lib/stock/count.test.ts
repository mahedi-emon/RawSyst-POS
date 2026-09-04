import { describe, expect, it } from 'vitest';

import { differenceOf } from './count';

describe('how far off the system was', () => {
  it('is what was counted less what the system believed', () => {
    expect(differenceOf('70', '72')).toBe('-2');
    expect(differenceOf('75', '72')).toBe('3');
  });

  it('is zero when they agree, which is the answer nobody acts on', () => {
    expect(differenceOf('72', '72')).toBe('0');
  });

  it('says nothing at all when nothing was counted', () => {
    // The one that matters. Blank is not zero: counting nothing and counting
    // none of something are different claims, and treating the first as the
    // second writes off everything on the shelf.
    expect(differenceOf('', '72')).toBeNull();
    expect(differenceOf('   ', '72')).toBeNull();
  });

  it('says nothing while a number is half typed', () => {
    for (const partial of ['-', '.', '1.', 'abc']) {
      expect(differenceOf(partial, '72')).toBeNull();
    }
  });

  it('counts a shelf that is genuinely empty as a real difference', () => {
    // Zero IS a claim: the shelf was checked and there was nothing on it.
    expect(differenceOf('0', '72')).toBe('-72');
  });

  it('does not go through a float', () => {
    // 0.3 - 0.1 is 0.19999999999999998 in binary floating point, and this
    // difference becomes a journal entry.
    expect(differenceOf('0.3', '0.1')).toBe('0.2');
  });

  it('handles a system quantity the server sent with trailing zeros', () => {
    // On-hand comes back as "72.0000" from some routes and "72" from others.
    expect(differenceOf('70', '72.0000')).toBe('-2');
  });

  it('treats a missing system quantity as nothing on the shelf', () => {
    expect(differenceOf('5', '')).toBe('5');
  });

  it('keeps a fractional count, for the things sold by weight', () => {
    expect(differenceOf('2.5', '3')).toBe('-0.5');
  });
});
