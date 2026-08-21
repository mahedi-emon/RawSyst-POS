import { describe, expect, it } from 'vitest';

import type { ShiftReport } from '@rawsyst/shared/api/shift';
import {
  denominationsFor,
  expectedIsWithheld,
  MOVEMENT_REASONS,
  openedAtTime,
  reportVerdict,
  signFor,
  tallyTotal,
  validateAmount,
  validateMovement,
  verdict,
  type Tally,
} from './shift';

const sar = denominationsFor('SAR')!;

function report(over: Partial<ShiftReport> = {}): ShiftReport {
  return {
    session_no: 1,
    state: 'open',
    opened_at: '2026-08-21T09:14:00+03:00',
    opening_float: '200',
    invoice_count: 0,
    gross_sales: '0',
    net_sales: '0',
    tax_total: '0',
    refund_total: '0',
    cash_takings: '0',
    non_cash_takings: '0',
    cash_movements: '0',
    ...over,
  };
}

describe('counting the drawer', () => {
  it('totals a tally exactly, where a float would not', () => {
    // 19 × 0.05 is 0.9500000000000001 in float64. In halalas it is 95.
    expect(tallyTotal({ '0.05': 19 }, sar)).toBe('0.95');

    // The classic: a tenth and a fifth of a riyal.
    expect(tallyTotal({ '0.10': 1, '0.05': 4 }, sar)).toBe('0.30');
  });

  it('adds notes and coins together', () => {
    const tally: Tally = { '500': 2, '100': 3, '50': 1, '1': 7, '0.25': 3 };
    // 1000 + 300 + 50 + 7 + 0.75
    expect(tallyTotal(tally, sar)).toBe('1357.75');
  });

  it('treats an empty pad as nothing counted rather than as an error', () => {
    expect(tallyTotal({}, sar)).toBe('0.00');
  });

  it('ignores blanks, negatives and fractions of a banknote', () => {
    const tally = {
      '100': Number.NaN,
      '50': -3,
      '10': 0,
      // Half a note is not a thing; the count truncates rather than
      // multiplying a fraction into the total.
      '5': 2.9,
    } as unknown as Tally;
    expect(tallyTotal(tally, sar)).toBe('10.00');
  });

  it('offers a pad for each currency the product serves, and none for others', () => {
    for (const currency of ['SAR', 'BDT', 'USD']) {
      expect(denominationsFor(currency)?.length).toBeGreaterThan(0);
    }
    expect(denominationsFor('sar')).not.toBeNull(); // case is not the cashier's problem
    expect(denominationsFor('EUR')).toBeNull();
    expect(denominationsFor('')).toBeNull();
    expect(denominationsFor(null)).toBeNull();
  });

  it('lists every denomination in descending order, so the pad reads like a drawer', () => {
    for (const currency of ['SAR', 'BDT', 'USD']) {
      const pad = denominationsFor(currency)!;
      for (let i = 1; i < pad.length; i++) {
        const previous = Number(pad[i - 1]!.value);
        expect(Number(pad[i]!.value)).toBeLessThan(previous);
      }
    }
  });
});

describe('short, over or exact', () => {
  it('names the direction and shows the gap as a positive number', () => {
    // "Short 5.00" reads at a glance; "-5.00" asks the reader which way round.
    expect(verdict('200.00', '195.00')).toEqual({
      kind: 'short',
      word: 'Short',
      amount: '5.00',
    });
    expect(verdict('200.00', '205.50')).toEqual({
      kind: 'over',
      word: 'Over',
      amount: '5.50',
    });
    expect(verdict('200.00', '200.00')).toEqual({
      kind: 'exact',
      word: 'Exact',
      amount: '0.00',
    });
  });

  it('reads Postgres notation and plain notation as the same figure', () => {
    // "200", "200.00" and "200.0000" all arrive depending on the column.
    expect(verdict('200.0000', '200').kind).toBe('exact');
    expect(verdict('200', '200.00').kind).toBe('exact');
  });

  it('draws no verdict when the expected figure is withheld', () => {
    // The blind close, before the count is committed.
    expect(verdict(undefined, '195.00').kind).toBe('unknown');
    expect(verdict('', '195.00').kind).toBe('unknown');
    expect(verdict('200.00', undefined).kind).toBe('unknown');
  });

  it("takes a closed report's verdict from the server's own variance", () => {
    // Not recomputed from expected and counted: the Z report is a signed
    // record and the screen must not be able to disagree with it.
    const z = report({
      state: 'closed',
      expected_cash: '215',
      counted_cash: '210',
      variance: '-5',
    });
    expect(reportVerdict(z)).toEqual({ kind: 'short', word: 'Short', amount: '5.00' });

    expect(reportVerdict(report({ variance: '0' })).kind).toBe('exact');
    expect(reportVerdict(report({ variance: '12.50' })).kind).toBe('over');
  });

  it('falls back to expected against counted when no variance was stated', () => {
    const open = report({ expected_cash: '200', counted_cash: '190' });
    expect(reportVerdict(open)).toEqual({ kind: 'short', word: 'Short', amount: '10.00' });
  });
});

describe('the blind close', () => {
  it('reports the expected figure as withheld when the server omitted it', () => {
    // The server's omission is the control. Nothing here consults blind_close:
    // inferring it on the client would be guessing at a rule the server
    // already enforces, and would be wrong the moment the two disagreed.
    expect(expectedIsWithheld(report())).toBe(true);
    expect(expectedIsWithheld(report({ expected_cash: '' }))).toBe(true);
    expect(expectedIsWithheld(report({ expected_cash: '200' }))).toBe(false);
  });

  it('yields no verdict from a withheld report, so nothing can be inferred', () => {
    const blind = report({ counted_cash: '195' });
    expect(reportVerdict(blind).kind).toBe('unknown');
    expect(reportVerdict(blind).amount).toBeNull();
  });
});

describe('validation', () => {
  it('accepts an amount and refuses what the server would', () => {
    expect(validateAmount('200', 'Opening float')).toBeNull();
    expect(validateAmount('200.00', 'Opening float')).toBeNull();
    expect(validateAmount('0', 'Opening float')).toBeNull();

    expect(validateAmount('', 'Opening float')).toMatch(/required/);
    expect(validateAmount('two hundred', 'Opening float')).toMatch(/amount/);
    expect(validateAmount('200.000', 'Opening float')).toMatch(/amount/);
    // Negative is refused here as well as by the service: a drawer cannot hold
    // less than nothing.
    expect(validateAmount('-1', 'Opening float')).toMatch(/amount/);
  });

  it('makes a cash movement explain itself', () => {
    expect(validateMovement('100.00', 'to the safe')).toEqual({});

    expect(validateMovement('100.00', '').note).toMatch(/why/);
    expect(validateMovement('100.00', 'ok').note).toMatch(/why/);
    expect(validateMovement('', 'to the safe').amount).toMatch(/how much/);
    expect(validateMovement('lots', 'to the safe').amount).toMatch(/amount/);
    expect(validateMovement('0.00', 'to the safe').amount).toMatch(/nothing/);
  });
});

describe('the direction money moved', () => {
  it('signs an outward reason negative whichever way the cashier typed it', () => {
    // A cashier types "100" and means a hundred left the drawer. Asking them
    // to type the sign is how a shift ends up two hundred out.
    expect(signFor('safe_drop', '100.00')).toBe('-100.00');
    expect(signFor('safe_drop', '-100.00')).toBe('-100.00');
    expect(signFor('petty_cash', '25.50')).toBe('-25.50');
    expect(signFor('supplier_paid', '80')).toBe('-80.00');
  });

  it('signs an inward reason positive', () => {
    expect(signFor('float_in', '100.00')).toBe('100.00');
    expect(signFor('float_in', '-100.00')).toBe('100.00');
    expect(signFor('correction', '5')).toBe('5.00');
  });

  it('offers only the reasons the database allows', () => {
    // Migration 0024's check constraint. A sixth reason invented here would be
    // refused with a constraint violation the cashier cannot act on.
    const allowed = ['float_in', 'safe_drop', 'petty_cash', 'supplier_paid', 'correction'];
    expect(MOVEMENT_REASONS.map((r) => r.value).sort()).toEqual([...allowed].sort());
  });
});

describe('the status line', () => {
  it('shows the clock time a shift opened', () => {
    expect(openedAtTime('2026-08-21T09:14:00+03:00')).toBe('09:14');
    expect(openedAtTime('2026-08-21T21:40:12Z')).toBe('21:40');
  });

  it('says nothing rather than something wrong when the time is unreadable', () => {
    expect(openedAtTime('')).toBe('');
    expect(openedAtTime('2026-08-21')).toBe('');
  });
});
