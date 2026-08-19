import { describe, expect, it } from 'vitest';

import { codeLength, formatCode } from './PairingScreen';

// The code is typed once, under time pressure, on a machine that may be a
// touchscreen. These are the ways somebody actually enters it.

describe('typing an enrolment code', () => {
  it('inserts the dash so nobody has to find it on a touch keyboard', () => {
    expect(formatCode('ABCD1')).toBe('ABCD-1');
    expect(formatCode('ABCD1234')).toBe('ABCD-1234');
  });

  it('accepts a code read aloud and typed in lower case', () => {
    expect(formatCode('k7qp4m2x')).toBe('K7QP-4M2X');
  });

  it('accepts one pasted with its dash already in it', () => {
    expect(formatCode('K7QP-4M2X')).toBe('K7QP-4M2X');
  });

  it('accepts one pasted with spaces', () => {
    expect(formatCode('K7QP 4M2X')).toBe('K7QP-4M2X');
  });

  it('stops at eight characters rather than letting a paste overrun', () => {
    expect(formatCode('K7QP4M2XZZZZ')).toBe('K7QP-4M2X');
  });

  it('shows nothing for an empty box rather than a lone dash', () => {
    expect(formatCode('')).toBe('');
    expect(formatCode('---')).toBe('');
  });

  it('does not map characters onto one another', () => {
    // The alphabet already omits the pairs people confuse. Silently correcting
    // anything else would let two different codes become the same code.
    expect(formatCode('0OIL1')).toBe('0OIL-1');
  });

  it('counts what was typed, not what is displayed', () => {
    expect(codeLength('K7QP-4M2X')).toBe(8);
    expect(codeLength('K7QP-')).toBe(4);
    expect(codeLength('')).toBe(0);
  });
});
