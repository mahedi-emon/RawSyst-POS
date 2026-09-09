import { describe, expect, it } from 'vitest';

import {
  departmentsAddUp,
  expiryPressure,
  otherThanSaudi,
  rankedDepartments,
  saudiFraction,
  type Workforce,
} from './workforce';

const report = (over: Partial<Workforce> = {}): Workforce => ({
  total: 10,
  saudi: 4,
  non_saudi: 6,
  saudi_share: '40.00',
  expiring_soon: 0,
  expired: 0,
  by_department: [
    { department: 'Shop floor', total: 6, saudi: 2 },
    { department: 'Office', total: 4, saudi: 2 },
  ],
  ...over,
});

describe('what the document counts mean this morning', () => {
  it('puts a lapsed permit above one lapsing next month', () => {
    // Someone whose permit has expired cannot legally be on shift today.
    // Someone whose permit lapses in six weeks is a diary entry. Reporting
    // the larger of the two numbers, or their sum, loses that distinction.
    expect(expiryPressure(report({ expired: 1, expiring_soon: 9 }))).toBe('expired');
    expect(expiryPressure(report({ expired: 0, expiring_soon: 9 }))).toBe('due_soon');
    expect(expiryPressure(report())).toBe('clear');
  });
});

describe('the department breakdown', () => {
  it('derives the non-national count rather than expecting one', () => {
    expect(otherThanSaudi({ department: 'Shop floor', total: 6, saudi: 2 })).toBe(4);
  });

  it('never reports a negative count if the two figures disagree', () => {
    // Not reachable through the route as written, and a negative head count
    // printed on a compliance screen is the kind of number somebody
    // photographs and sends to a lawyer.
    expect(otherThanSaudi({ department: 'Odd', total: 1, saudi: 3 })).toBe(0);
  });

  it('notices when the departments do not account for everybody', () => {
    expect(departmentsAddUp(report())).toBe(true);
    // Six people missing from the breakdown. The screen has to say so rather
    // than print two totals and leave the reader to spot it.
    expect(
      departmentsAddUp(
        report({ by_department: [{ department: 'Shop floor', total: 4, saudi: 2 }] }),
      ),
    ).toBe(false);
  });

  it('ranks by size, and settles a tie by name', () => {
    const ranked = rankedDepartments(
      report({
        by_department: [
          { department: 'Office', total: 4, saudi: 2 },
          { department: 'Warehouse', total: 4, saudi: 1 },
          { department: 'Shop floor', total: 9, saudi: 3 },
        ],
      }),
    );
    expect(ranked.map((l) => l.department)).toEqual([
      'Shop floor',
      'Office',
      'Warehouse',
    ]);
  });

  it('leaves the report own order alone', () => {
    const original = report();
    rankedDepartments(original);
    expect(original.by_department[0]?.department).toBe('Shop floor');
  });
});

describe('the bar beside the percentage', () => {
  it('comes from the counts, not from parsing the percentage string', () => {
    // The printed figure and the drawn bar have to agree, and they only do if
    // one of them is not a second reading of the other.
    expect(saudiFraction(report())).toBeCloseTo(0.4);
  });

  it('draws nothing for a business with nobody in it', () => {
    expect(
      saudiFraction(report({ total: 0, saudi: 0, non_saudi: 0, by_department: [] })),
    ).toBe(0);
  });

  it('never draws past the end of the track', () => {
    expect(saudiFraction(report({ total: 2, saudi: 5 }))).toBe(1);
  });
});
