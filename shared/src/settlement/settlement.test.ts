import { describe, expect, it } from 'vitest';

import {
  byMethod,
  canRecord,
  checkDeposit,
  grossOf,
  outstandingTotal,
} from './settlement';
import type { PendingTender } from '../api/settlement';

function tender(id: string, amount: string, method = 'mada'): PendingTender {
  return {
    tender_id: id,
    invoice_id: 'i-' + id,
    invoice_number: 'INV-' + id,
    issued_at: '2026-08-15T10:00:00Z',
    method,
    amount,
  };
}

const pending = [
  tender('a', '33.33'),
  tender('b', '66.67'),
  tender('c', '100.00', 'visa'),
];

describe('what the selected payments come to', () => {
  it('adds only what is ticked', () => {
    expect(grossOf(pending, new Set(['a', 'b']))).toBe('100.00');
    expect(grossOf(pending, new Set(['a', 'b', 'c']))).toBe('200.00');
  });

  it('is zero when nothing is ticked', () => {
    expect(grossOf(pending, new Set())).toBe('0.00');
  });

  it('does not drift on amounts a float cannot hold', () => {
    // 0.15 and 0.30 are the classic pair. Three of them come to 0.45 exactly,
    // and in float64 they do not.
    const cents = [tender('x', '0.15'), tender('y', '0.15'), tender('z', '0.15')];
    expect(grossOf(cents, new Set(['x', 'y', 'z']))).toBe('0.45');
  });
});

describe('whether a deposit can be recorded', () => {
  it('asks for a selection before anything else', () => {
    const check = checkDeposit(pending, new Set(), '100.00');
    expect(check.kind).toBe('nothing_selected');
    expect(canRecord(check)).toBe(false);
  });

  it('asks for the amount that landed', () => {
    const check = checkDeposit(pending, new Set(['a']), '');
    expect(check.kind).toBe('no_amount');
    expect(canRecord(check)).toBe(false);
  });

  it('implies the fee from the gross and the deposit', () => {
    const check = checkDeposit(pending, new Set(['a', 'b']), '97.00');
    expect(check.kind).toBe('ready');
    if (check.kind !== 'ready') return;
    expect(check.gross).toBe('100.00');
    expect(check.fee).toBe('3.00');
    expect(check.message).toContain('2 payments');
    expect(canRecord(check)).toBe(true);
  });

  it('refuses a deposit larger than the payments it covers', () => {
    // The server refuses this too. Doing it here as well means the correction
    // happens while the figure is still on screen.
    const check = checkDeposit(pending, new Set(['a']), '50.00');
    expect(check.kind).toBe('exceeds');
    expect(check.message).toContain('separate event');
    expect(canRecord(check)).toBe(false);
  });

  it('lets a fee-free deposit through, but says it is unusual', () => {
    // Possible, and far more often somebody has typed the gross into the
    // deposit box. Saying so is cheaper than reversing a journal entry.
    const check = checkDeposit(pending, new Set(['c']), '100.00');
    expect(check.kind).toBe('no_fee');
    expect(canRecord(check)).toBe(true);
    if (check.kind !== 'no_fee') return;
    expect(check.fee).toBe('0.00');
    expect(check.message).toContain('unusual');
  });

  it('counts one payment as one', () => {
    const check = checkDeposit(pending, new Set(['a']), '33.00');
    expect(check.message).toContain('1 payment');
    expect(check.message).not.toContain('1 payments');
  });
});

describe('grouping what is outstanding', () => {
  it('groups by method, because that is how it settles', () => {
    // Mada and an international card land on different days in different
    // batches. A list sorted only by date makes somebody pick them apart by
    // eye, which is where a payment joins the wrong deposit.
    const [mada, visa] = byMethod(pending);
    expect(byMethod(pending).map((g) => g.method)).toEqual(['mada', 'visa']);
    expect(mada?.total).toBe('100.00');
    expect(mada?.count).toBe(2);
    expect(visa?.total).toBe('100.00');
  });

  it('puts the largest pile first', () => {
    const groups = byMethod([
      tender('a', '10.00', 'visa'),
      tender('b', '500.00', 'mada'),
    ]);
    expect(groups[0]?.method).toBe('mada');
  });

  it('copes with nothing outstanding', () => {
    expect(byMethod([])).toEqual([]);
    expect(outstandingTotal([])).toBe('0.00');
  });

  it('totals the whole position', () => {
    expect(outstandingTotal(pending)).toBe('200.00');
  });
});
