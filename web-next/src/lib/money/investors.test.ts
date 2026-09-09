import { describe, expect, it } from 'vitest';

import {
  closesWhereItSays,
  movedInPeriod,
  periodProblem,
  periodQuery,
  withRunningBalance,
  type InvestorStatement,
  type Movement,
} from './investors';

const movement = (over: Partial<Movement> = {}): Movement => ({
  id: 'm1',
  direction: 'contribution',
  amount: '100.00',
  moved_on: '2026-03-01',
  currency: 'SAR',
  ...over,
});

/** The route answers newest first, so the fixture is written that way too. */
const statement = (over: Partial<InvestorStatement> = {}): InvestorStatement => ({
  investor_id: 'i1',
  investor: 'A partner',
  currency: 'SAR',
  opening: '80000.00',
  contributed: '5000.00',
  withdrawn: '2000.00',
  closing: '83000.00',
  movements: [
    movement({ id: 'm3', amount: '2000.00', direction: 'withdrawal', moved_on: '2026-03-20' }),
    movement({ id: 'm2', amount: '3000.00', moved_on: '2026-03-10' }),
    movement({ id: 'm1', amount: '2000.00', moved_on: '2026-03-05' }),
  ],
  ...over,
});

describe('the running balance', () => {
  it('reads downwards from the opening, not from the newest row', () => {
    // The route orders newest first because that is what a register wants. A
    // statement is read in the other direction, and a running balance means
    // nothing in the route's order.
    const rows = withRunningBalance(statement());
    expect(rows.map((r) => r.id)).toEqual(['m1', 'm2', 'm3']);
    expect(rows.map((r) => r.balance)).toEqual([
      '82000.00', // 80,000 + 2,000
      '85000.00', // + 3,000
      '83000.00', // - 2,000
    ]);
  });

  it('subtracts a withdrawal and marks it as one', () => {
    const rows = withRunningBalance(statement());
    expect(rows[2]?.outward).toBe(true);
    expect(rows[0]?.outward).toBe(false);
  });

  it('does the arithmetic in decimal, not through a float', () => {
    // 0.1 + 0.2 through Number is 0.30000000000000004, and a statement is
    // exactly where somebody would notice.
    const rows = withRunningBalance(
      statement({
        opening: '0.10',
        contributed: '0.20',
        withdrawn: '0.00',
        closing: '0.30',
        movements: [movement({ amount: '0.20' })],
      }),
    );
    expect(rows[0]?.balance).toBe('0.30');
  });

  it('leaves the statement own array alone', () => {
    const original = statement();
    withRunningBalance(original);
    expect(original.movements[0]?.id).toBe('m3');
  });
});

describe('whether the statement agrees with itself', () => {
  it('accepts a statement whose rows reach the stated closing balance', () => {
    expect(closesWhereItSays(statement())).toBe(true);
  });

  it('reports a closing balance the rows do not reach', () => {
    // Two figures on one document that disagree is the thing somebody signs
    // and then has to explain. Better to say so on the screen.
    expect(closesWhereItSays(statement({ closing: '99999.00' }))).toBe(false);
  });

  it('accepts a period with no movements in it at all', () => {
    expect(
      closesWhereItSays(
        statement({
          movements: [],
          contributed: '0.00',
          withdrawn: '0.00',
          closing: '80000.00',
        }),
      ),
    ).toBe(true);
  });
});

describe('the net movement over the window', () => {
  it('is what went in less what came out', () => {
    expect(movedInPeriod(statement())).toBe('3000.00');
  });

  it('is negative when the investor drew more than they put in', () => {
    expect(movedInPeriod(statement({ contributed: '1000.00', withdrawn: '4000.00' }))).toBe(
      '-3000.00',
    );
  });
});

describe('the period boxes', () => {
  it('accepts either end on its own, and neither', () => {
    expect(periodProblem({ from: '', to: '' })).toBeNull();
    expect(periodProblem({ from: '2026-03-01', to: '' })).toBeNull();
    expect(periodProblem({ from: '', to: '2026-03-31' })).toBeNull();
  });

  it('refuses a period that ends before it starts, before the server has to', () => {
    expect(periodProblem({ from: '2026-03-31', to: '2026-03-01' })).toBe('backwards');
  });

  it('refuses a date written any other way', () => {
    // 03/04/2026 is the third of April in Dhaka and the fourth of March in
    // California, and nothing in the string says which.
    expect(periodProblem({ from: '03/04/2026', to: '' })).toBe('unreadable');
  });

  it('leaves an empty end off the query rather than sending it blank', () => {
    expect(periodQuery({ from: '2026-03-01', to: '' })).toEqual({ from: '2026-03-01' });
    expect(periodQuery({ from: '', to: '' })).toEqual({});
  });
});
