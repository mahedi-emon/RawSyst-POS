import { describe, expect, it } from 'vitest';

import {
  daysBetween,
  documentState,
  lastFullMonth,
  leaveProblem,
  maySeePay,
  monthLabel,
  monthlyPay,
  type Employee,
} from './staff';

function employee(over: Partial<Employee> = {}): Employee {
  return {
    id: 'e1',
    employee_no: 'EMP-00001',
    full_name: 'Faisal Al-Otaibi',
    id_expiring_soon: false,
    id_expired: false,
    is_saudi: true,
    joined_on: '2023-03-01',
    status: 'active',
    commission_eligible: false,
    ...over,
  };
}

describe('maySeePay', () => {
  it('is false when the server omitted the pay fields', () => {
    expect(maySeePay(employee())).toBe(false);
  });

  it('is TRUE for somebody genuinely paid nothing but commission', () => {
    // The case that makes presence, not value, the right test. A
    // commission-only salesperson has a basic of zero and a reader entitled
    // to see it must see the zero, not a blank.
    expect(maySeePay(employee({ basic_salary: '0.00' }))).toBe(true);
  });
});

describe('monthlyPay', () => {
  it('adds the four components', () => {
    expect(
      monthlyPay(
        employee({
          basic_salary: '6000.00',
          housing_allowance: '1500.00',
          transport_allowance: '500.00',
          other_allowance: '0.00',
        }),
      ),
    ).toBe('8000.00');
  });

  it('is null rather than zero when pay is not visible', () => {
    // The distinction the whole file exists for: a Store Manager without
    // hr.view_pay must not be shown a salary of nothing.
    expect(monthlyPay(employee())).toBeNull();
  });

  it('handles a record with only a basic', () => {
    expect(monthlyPay(employee({ basic_salary: '2500.00' }))).toBe('2500.00');
  });

  it('is null when a figure is not a number', () => {
    expect(monthlyPay(employee({ basic_salary: 'oops' }))).toBeNull();
  });
});

describe('documentState', () => {
  it('says nothing at all when no document is on file', () => {
    expect(documentState(employee())).toBe('none');
  });

  it('reads the expiry flags off the server', () => {
    expect(
      documentState(employee({ id_expires_on: '2026-10-06', id_expiring_soon: true })),
    ).toBe('expiring');
  });

  it('lets expired win over expiring', () => {
    // Both flags can be on at once. An expired permit is not a warning about
    // next month, it is somebody who cannot work today.
    expect(
      documentState(
        employee({
          id_expires_on: '2026-01-01',
          id_expiring_soon: true,
          id_expired: true,
        }),
      ),
    ).toBe('expired');
  });

  it('is fine for a document with years left', () => {
    expect(documentState(employee({ id_expires_on: '2030-01-01' }))).toBe('fine');
  });
});

describe('daysBetween', () => {
  it('counts a single day as one', () => {
    expect(daysBetween('2026-09-15', '2026-09-15')).toBe('1');
  });

  it('counts an inclusive span', () => {
    expect(daysBetween('2026-09-15', '2026-09-19')).toBe('5');
  });

  it('crosses a month end', () => {
    expect(daysBetween('2026-09-29', '2026-10-02')).toBe('4');
  });

  it('is empty when the dates run backwards', () => {
    expect(daysBetween('2026-09-19', '2026-09-15')).toBe('');
  });

  it('is empty when a date is missing', () => {
    expect(daysBetween('', '2026-09-15')).toBe('');
  });
});

describe('leaveProblem', () => {
  const good = {
    employeeID: 'e1',
    kind: 'annual',
    from: '2026-09-15',
    to: '2026-09-19',
  };

  it('passes a complete request', () => {
    expect(leaveProblem(good)).toBe('none');
  });

  it('needs somebody to be asking', () => {
    expect(leaveProblem({ ...good, employeeID: '' })).toBe('no_employee');
  });

  it('needs a kind', () => {
    expect(leaveProblem({ ...good, kind: '   ' })).toBe('no_kind');
  });

  it('needs both dates', () => {
    expect(leaveProblem({ ...good, to: '' })).toBe('no_dates');
  });

  it('refuses a span that ends before it starts', () => {
    expect(leaveProblem({ ...good, from: '2026-09-19', to: '2026-09-15' })).toBe(
      'backwards',
    );
  });
});

describe('lastFullMonth', () => {
  it('is the month before the one being stood in', () => {
    expect(lastFullMonth(new Date(2026, 8, 5))).toBe('2026-08');
  });

  it('steps back over a year end', () => {
    expect(lastFullMonth(new Date(2026, 0, 3))).toBe('2025-12');
  });

  it('is built from the local date, not a UTC timestamp', () => {
    // Nine in the morning in Dhaka on the first is still the last day of the
    // previous month in UTC. Reading the timestamp would offer the month
    // before the one this is meant to offer.
    expect(lastFullMonth(new Date(2026, 8, 1, 9, 0, 0))).toBe('2026-08');
  });
});

describe('monthLabel', () => {
  it('names the month', () => {
    expect(monthLabel('2026-08', 'en-GB')).toBe('August 2026');
  });

  it('gives back what it was handed when that is not a month', () => {
    expect(monthLabel('rubbish', 'en-GB')).toBe('rubbish');
  });
});
