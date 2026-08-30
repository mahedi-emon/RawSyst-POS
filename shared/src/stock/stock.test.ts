import { describe, expect, it } from 'vitest';

import {
  isNegative,
  isShort,
  isZero,
  nextStepFor,
  scaled,
  stepOf,
  unscaled,
  variance,
  varianceTone,
} from './stock';

// The reason this module exists rather than a subtraction inline in the JSX.
//
// A quantity is numeric(18,4) on the server. Turning one into a JavaScript
// number to compare it is how a count of exactly the right amount reports a
// variance of a hundred-millionth of a unit — and then a person stops trusting
// the screen, which is worse than a wrong number.
describe('comparing quantities without floating point', () => {
  it('finds no variance where a float would find one', () => {
    // 0.1 + 0.2 === 0.30000000000000004 in binary floating point. Three
    // deliveries of a tenth of a kilo counted as three tenths must be exact.
    expect(variance('0.3', '0.3')).toBe('0');
    expect(varianceTone(variance('0.3', '0.3'))).toBe('flat');
  });

  it('holds four decimal places, which is what the column holds', () => {
    expect(variance('2.0001', '2.0002')).toBe('0.0001');
  });

  it('does not invent precision the figure did not have', () => {
    expect(variance('10', '8')).toBe('-2');
  });

  it('round-trips a quantity through the scaled form', () => {
    for (const q of ['0', '1', '-3', '2.5', '0.0001', '-0.25', '1234.5678']) {
      expect(unscaled(scaled(q)!)).toBe(q);
    }
  });
});

describe('a line nobody has counted', () => {
  // The distinction the whole count workflow rests on. A blank cell is silence
  // about that shelf, and the server treats it as silence too rather than
  // writing the shelf off — a sheet where somebody counted three aisles and
  // went home must not empty the fourth.
  it('has no variance, which is not the same as a variance of zero', () => {
    expect(variance('10', '')).toBeNull();
    expect(variance('10', '0')).toBe('-10');
  });

  it('is still silence while a number is half typed', () => {
    expect(variance('10', '-')).toBeNull();
    expect(variance('10', '1.')).toBe('-9');
  });

  it('refuses text', () => {
    expect(variance('10', 'eight')).toBeNull();
    expect(scaled('1e3')).toBeNull();
  });
});

describe('which way a variance points', () => {
  it('marks a shortfall', () => {
    expect(varianceTone('-2')).toBe('short');
  });

  // Colouring a surplus the same red as a shortfall teaches people to ignore
  // red. It is unexpected, not bad.
  it('does not mark a surplus as a problem', () => {
    expect(varianceTone('2')).toBe('over');
  });

  it('treats nothing found as nothing to say', () => {
    expect(varianceTone('0')).toBe('flat');
    expect(varianceTone(null)).toBe('flat');
  });
});

describe('reading a quantity', () => {
  it('knows zero from blank from a number', () => {
    expect(isZero('0')).toBe(true);
    expect(isZero('')).toBe(true);
    expect(isZero('0.0000')).toBe(true);
    expect(isZero('0.0001')).toBe(false);
  });

  it('knows a negative', () => {
    expect(isNegative('-1')).toBe(true);
    expect(isNegative('1')).toBe(false);
    expect(isNegative('')).toBe(false);
  });
});

describe('where a transfer has got to', () => {
  it('walks the four steps B4 specifies', () => {
    expect(stepOf('requested')).toBe(0);
    expect(stepOf('approved')).toBe(1);
    expect(stepOf('dispatched')).toBe(2);
    expect(stepOf('received')).toBe(3);
  });

  // A cancelled transfer did not reach a step, it left the sequence. Drawing it
  // at the step it happened to be on says the opposite of what happened.
  it('puts a cancelled transfer outside the sequence', () => {
    expect(stepOf('cancelled')).toBe(-1);
  });
});

describe('what this person can do next', () => {
  const keeper = { transfer: true, approve: false };
  const manager = { transfer: true, approve: true };

  // The control B4 asks for. A keeper who could approve their own request would
  // make the manager's step theatre.
  it('offers approval only to somebody who may approve', () => {
    expect(nextStepFor('requested', keeper)).toBeNull();
    expect(nextStepFor('requested', manager)).toBe('approve');
  });

  it('offers the dispatch and the receipt to whoever moves stock', () => {
    expect(nextStepFor('approved', keeper)).toBe('dispatch');
    expect(nextStepFor('dispatched', keeper)).toBe('receive');
  });

  it('offers nothing on a transfer that is over', () => {
    expect(nextStepFor('received', manager)).toBeNull();
    expect(nextStepFor('cancelled', manager)).toBeNull();
  });
});

describe('stock that never arrived', () => {
  it('is reported when a branch confirmed less than was sent', () => {
    expect(isShort('1')).toBe(true);
  });

  it('is not reported for a transfer that arrived whole', () => {
    expect(isShort(undefined)).toBe(false);
    expect(isShort('0')).toBe(false);
  });
});
