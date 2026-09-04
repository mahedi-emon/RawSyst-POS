import { describe, expect, it } from 'vitest';

import { lineTotals, orderTotals, type Draft } from './draft-order';

function line(over: Partial<Draft> = {}): Draft {
  return {
    variant_id: 'v1',
    description: 'Abaya, black, medium',
    qty: '24',
    unit_cost: '18.50',
    tax_percent: '15',
    ...over,
  };
}

describe('a draft purchase order line', () => {
  it('agrees with what the server computed for the same order', () => {
    // The live order: 24 at 18.50 with a fifteen per cent rate came back as
    // net 444.0000, tax 66.6000, total 510.6000. If this screen disagreed,
    // a buyer would approve one number and commit to another.
    const sums = lineTotals(line());
    expect(sums.net).toBe('444.0000');
    expect(sums.tax).toBe('66.6000');
    expect(sums.gross).toBe('510.6000');
  });

  it('reads the rate as a percentage, not as a multiplier', () => {
    // 15 meant 1500% on the wire until the boundary started refusing it, and
    // the whole reason this field is a percentage is that a buyer types 15.
    const sums = lineTotals(line({ tax_percent: '15' }));
    expect(sums.tax).toBe('66.6000');
    expect(sums.tax).not.toBe('6660.0000');
  });

  it('carries no tax at all when the rate is zero', () => {
    const sums = lineTotals(line({ tax_percent: '0' }));
    expect(sums.tax).toBe('0.0000');
    expect(sums.gross).toBe('444.0000');
  });

  it('treats an empty box as nothing said yet, not as a broken number', () => {
    // A line that has just been added has no quantity, and the total under it
    // should read zero rather than NaN.
    const sums = lineTotals(line({ qty: '', unit_cost: '', tax_percent: '' }));
    expect(sums.net).toBe('0.0000');
    expect(sums.gross).toBe('0.0000');
  });

  it('survives a number somebody is part-way through typing', () => {
    for (const partial of ['1.', '-', '.', '']) {
      expect(() => lineTotals(line({ unit_cost: partial }))).not.toThrow();
    }
  });

  it('keeps four decimals, because that is what the order stores', () => {
    // 3.333 per metre is valid input; two decimals here would show a buyer a
    // total the order does not have.
    const sums = lineTotals(line({ qty: '3', unit_cost: '3.333', tax_percent: '0' }));
    expect(sums.net).toBe('9.9990');
  });

  it('does not go through a float', () => {
    // 0.1 + 0.2 is 0.30000000000000004 in binary floating point. Ten lines of
    // 0.10 must come to exactly 1.00.
    const tenth = line({ qty: '1', unit_cost: '0.10', tax_percent: '0' });
    const totals = orderTotals(Array.from({ length: 10 }, () => tenth));
    expect(totals.net).toBe('1.0000');
  });
});

describe('a whole draft order', () => {
  it('sums the rounded lines rather than rounding the sum', () => {
    // The server rounds and stores each line, then the header is the sum of
    // what was stored. Rounding once at the end gives a total that disagrees
    // with the lines printed under it, which is the complaint a customer is
    // always right to make.
    const odd = line({ qty: '1', unit_cost: '0.33335', tax_percent: '0' });
    const totals = orderTotals([odd, odd]);
    // 0.3334 stored twice, not 0.6667 computed once.
    expect(totals.net).toBe('0.6668');
  });

  it('is zero for an order with nothing on it', () => {
    const totals = orderTotals([]);
    expect(totals.net).toBe('0.0000');
    expect(totals.tax).toBe('0.0000');
    expect(totals.gross).toBe('0.0000');
  });

  it('adds lines taxed at different rates', () => {
    const totals = orderTotals([
      line({ qty: '10', unit_cost: '10.00', tax_percent: '15' }),
      line({ variant_id: 'v2', qty: '10', unit_cost: '10.00', tax_percent: '0' }),
    ]);
    expect(totals.net).toBe('200.0000');
    expect(totals.tax).toBe('15.0000');
    expect(totals.gross).toBe('215.0000');
  });
});
