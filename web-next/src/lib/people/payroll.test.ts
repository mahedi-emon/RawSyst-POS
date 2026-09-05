import { describe, expect, it } from 'vitest';

import {
  allowed,
  ratePercent,
  runTone,
  SLIP_LINES,
  totalCost,
  type PayrollRun,
} from './payroll';

function run(over: Partial<PayrollRun> = {}): PayrollRun {
  return {
    id: 'r1',
    run_no: 'PAY-00001',
    period: '2026-08',
    status: 'draft',
    currency: 'SAR',
    gross_total: '16532.26',
    deduction_total: '892.54',
    net_total: '15639.72',
    employer_gosi: '1043.25',
    ...over,
  };
}

describe('allowed', () => {
  it('offers approval only on a draft', () => {
    expect(allowed('draft', 'approve')).toBe(true);
    expect(allowed('approved', 'approve')).toBe(false);
    expect(allowed('paid', 'approve')).toBe(false);
  });

  it('needs an approved run before paying', () => {
    // The server's refusal is "That run is draft. Only an approved run can be
    // paid." A button that would collect that answer is not offered.
    expect(allowed('draft', 'pay')).toBe(false);
    expect(allowed('approved', 'pay')).toBe(true);
  });

  it('needs an approved run before a wage file', () => {
    expect(allowed('draft', 'wage-file')).toBe(false);
    expect(allowed('approved', 'wage-file')).toBe(true);
  });

  it('will not cancel a run whose money has already left', () => {
    // Unwinding a paid run is a refund, not a cancellation.
    expect(allowed('paid', 'cancel')).toBe(false);
    expect(allowed('cancelled', 'cancel')).toBe(false);
    expect(allowed('draft', 'cancel')).toBe(true);
    expect(allowed('approved', 'cancel')).toBe(true);
  });
});

describe('runTone', () => {
  it('marks a paid run as settled', () => {
    expect(runTone('paid')).toBe('positive');
  });

  it('marks an approved run as something still owed', () => {
    expect(runTone('approved')).toBe('caution');
  });
});

describe('totalCost', () => {
  it('is the gross plus the employer contribution', () => {
    // What an owner means by "what does my staff cost" — not the net, which
    // is only what lands in people's accounts.
    expect(totalCost(run())).toBe('17575.51');
  });

  it('is NULL when social insurance is unavailable', () => {
    // The figure is genuinely not known. Adding a zero would state a
    // regulatory liability of nil for the month.
    expect(
      totalCost(
        run({
          gosi_unavailable: true,
          employer_gosi: '0.00',
          gosi_blocked_reason: 'SA.GOSI.RATES has not been verified',
        }),
      ),
    ).toBeNull();
  });

  it('is null rather than NaN when a figure is not a number', () => {
    expect(totalCost(run({ employer_gosi: '' }))).toBeNull();
  });
});

describe('SLIP_LINES', () => {
  it('reads earnings, then deductions, then what is left', () => {
    const kinds = SLIP_LINES.map((l) => l.kind);
    expect(kinds[0]).toBe('earning');
    expect(SLIP_LINES[SLIP_LINES.length - 1]).toEqual({ key: 'net', kind: 'total' });
  });

  it('carries every deduction the entry has to account for', () => {
    // The four that make up `deductions`. The backend rule left absence and
    // other_deduction out of its posting and the month could not be approved;
    // leaving them off a payslip is the same omission in the other direction.
    const deductions = SLIP_LINES.filter((l) => l.kind === 'deduction').map(
      (l) => l.key,
    );
    expect(deductions).toEqual([
      'absence_deduction',
      'gosi_employee',
      'advance_recovery',
      'other_deduction',
    ]);
  });
});

describe('ratePercent', () => {
  it('reads a fraction as a percentage', () => {
    expect(ratePercent('0.02')).toBe('2%');
  });

  it('keeps a fractional percentage', () => {
    expect(ratePercent('0.025')).toBe('2.5%');
  });

  it('gives back what it was handed when that is not a number', () => {
    expect(ratePercent('')).toBe('');
  });
});
