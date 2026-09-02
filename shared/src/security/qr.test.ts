import { describe, expect, it } from 'vitest';

import { encodeQR, type QRMatrix } from './qr';

/** Reads a module, treating anything off the grid as light. */
function at(m: QRMatrix, r: number, c: number): boolean {
  return m[r]?.[c] === true;
}

/** The side length. */
function side(m: QRMatrix): number {
  return m.length;
}

// What can be checked without writing a decoder, checked.
//
// A QR encoder is one of the few things in this codebase whose output cannot be
// read back by the code that produced it. So the tests here prove the
// STRUCTURE — the parts the standard fixes and a reader depends on to find the
// code at all — rather than asserting a whole matrix nobody can verify by eye.
//
// The enrolment screen shows the typed secret beside the QR for the same
// reason: if the picture were wrong, the person is not stuck.

describe('the QR encoder', () => {
  it('sizes the matrix to the version the payload needs', () => {
    // Version 1 is 21x21 and each version adds four modules a side.
    const small = encodeQR('hello');
    expect(side(small)).toBe(21);
    expect(small[0]?.length).toBe(21);

    // An otpauth URI of the length this product actually produces.
    const uri =
      'otpauth://totp/RawSyst:owner@example.com?algorithm=SHA1&digits=6' +
      '&issuer=RawSyst&period=30&secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';
    const real = encodeQR(uri);
    expect(side(real)).toBeGreaterThanOrEqual(21);
    expect(side(real) % 4).toBe(1);
    expect(side(real)).toBe(real[0]?.length);
  });

  it('draws the three finder patterns a reader looks for', () => {
    const m = encodeQR('hello');
    const n = side(m);

    // The 7x7 eye: a dark ring, a light ring, a 3x3 dark core.
    const eyeAt = (row: number, col: number) => {
      for (let r = 0; r < 7; r++) {
        for (let c = 0; c < 7; c++) {
          const onRing = r === 0 || r === 6 || c === 0 || c === 6;
          const inCore = r >= 2 && r <= 4 && c >= 2 && c <= 4;
          expect(at(m, row + r, col + c)).toBe(onRing || inCore);
        }
      }
    };

    eyeAt(0, 0);
    eyeAt(0, n - 7);
    eyeAt(n - 7, 0);

    // And NOT a fourth eye in the remaining corner, which is how a reader
    // tells the code's orientation. Checked as the whole 7x7 pattern: three
    // dark modules on a diagonal are ordinary data and prove nothing.
    let looksLikeAnEye = true;
    for (let r = 0; r < 7 && looksLikeAnEye; r++) {
      for (let c = 0; c < 7; c++) {
        const onRing = r === 0 || r === 6 || c === 0 || c === 6;
        const inCore = r >= 2 && r <= 4 && c >= 2 && c <= 4;
        if (at(m, n - 7 + r, n - 7 + c) !== (onRing || inCore)) {
          looksLikeAnEye = false;
          break;
        }
      }
    }
    expect(looksLikeAnEye).toBe(false);
  });

  it('lays the timing patterns down the sixth row and column', () => {
    const m = encodeQR('hello');
    for (let i = 8; i < side(m) - 8; i++) {
      expect(at(m, 6, i)).toBe(i % 2 === 0);
      expect(at(m, i, 6)).toBe(i % 2 === 0);
    }
  });

  it('always sets the dark module', () => {
    // One module the standard fixes as dark whatever the data and whatever the
    // mask. A reader uses it to confirm it has the format field the right way
    // round.
    for (const text of ['hello', 'a', 'x'.repeat(100)]) {
      const m = encodeQR(text);
      expect(at(m, side(m) - 8, 8)).toBe(true);
    }
  });

  it('is deterministic', () => {
    // Mask selection scores eight candidates and takes the lowest. A tie
    // broken by map iteration order would give two different pictures for one
    // secret, and the second scan would fail.
    const uri = 'otpauth://totp/RawSyst:a@b.test?secret=ABCDEFGHIJKLMNOP';
    expect(encodeQR(uri)).toEqual(encodeQR(uri));
  });

  it('refuses a payload it cannot hold rather than truncating it', () => {
    // Version 10 at level L holds 271 bytes. A QR that scanned to half a URI
    // would be worse than none: somebody would scan it and believe it worked.
    expect(() => encodeQR('x'.repeat(272))).toThrow();
    expect(() => encodeQR('x'.repeat(271))).not.toThrow();
  });

  it('encodes the payload rather than a constant', () => {
    // Two different secrets must produce two different pictures. The failure
    // this guards against is an encoder that draws a valid, scannable, and
    // completely wrong code.
    const a = encodeQR('otpauth://totp/RawSyst:a@b.test?secret=AAAAAAAAAAAAAAAA');
    const b = encodeQR('otpauth://totp/RawSyst:a@b.test?secret=BBBBBBBBBBBBBBBB');
    expect(a).not.toEqual(b);
  });
});
