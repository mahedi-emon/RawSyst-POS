import { describe, expect, it } from 'vitest';

import { overAllocated, sumOf } from './allocate';

describe('what a payment comes to', () => {
  it('adds the allocations', () => {
    expect(sumOf(['100.00', '250.50'])).toBe('350.50');
  });

  it('does not go through a float', () => {
    // 0.1 + 0.2 is 0.30000000000000004 in binary floating point, and this is a
    // figure somebody reconciles against a bank statement.
    expect(sumOf(['0.10', '0.20'])).toBe('0.30');
    expect(sumOf(Array.from({ length: 10 }, () => '0.10'))).toBe('1.00');
  });

  it('treats an empty box as nothing rather than as broken', () => {
    expect(sumOf(['', '50.00', '   '])).toBe('50.00');
  });

  it('survives a number somebody is part-way through typing', () => {
    for (const partial of ['1.', '-', '.', 'abc']) {
      expect(() => sumOf([partial])).not.toThrow();
    }
  });

  it('is zero when nothing has been allocated', () => {
    expect(sumOf([])).toBe('0.00');
  });
});

describe('paying more than is owed', () => {
  it('catches a digit too many', () => {
    // The mistake somebody makes with a keyboard rather than with intent, and
    // the whole payment is refused for it.
    expect(overAllocated('4467.75', '446.775')).toBe(true);
  });

  it('allows paying exactly what is owed', () => {
    expect(overAllocated('446.7750', '446.7750')).toBe(false);
  });

  it('allows a part payment, which is an ordinary thing to make', () => {
    expect(overAllocated('100.00', '446.7750')).toBe(false);
  });

  it('says nothing about an empty box', () => {
    expect(overAllocated('', '446.7750')).toBe(false);
  });

  it('compares by value, not by how the number was written', () => {
    // "0" and "0.00" and "0.0000" are the same amount, and the server sends
    // all three shapes in different payloads.
    expect(overAllocated('100', '100.0000')).toBe(false);
    expect(overAllocated('100.0000', '100')).toBe(false);
  });
});
