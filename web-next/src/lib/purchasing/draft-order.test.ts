import { describe, expect, it } from 'vitest';

import { lineNet, orderNet, type Draft } from './draft-order';

function line(over: Partial<Draft> = {}): Draft {
  return {
    variant_id: 'v1',
    description: 'Abaya, black, medium',
    qty: '24',
    unit_cost: '18.50',
    tax_treatment: 'standard',
    ...over,
  };
}

describe('a draft purchase order line', () => {
  it('agrees with the net the server computed for the same order', () => {
    // The live order: 24 at 18.50 came back as net 444.0000. If this screen
    // disagreed, a buyer would approve one number and commit to another.
    expect(lineNet(line())).toBe('444.0000');
  });

  it('does not compute tax, because it does not know the rate', () => {
    // Deliberate. CreateOrder resolves the rate from the regulatory register
    // at the order date, and a guess here would be a number the buyer reads,
    // remembers, and then finds different on the order they raised.
    const net = lineNet(line({ tax_treatment: 'zero_rated' }));
    expect(net).toBe(lineNet(line({ tax_treatment: 'standard' })));
  });

  it('treats an empty box as nothing said yet, not as a broken number', () => {
    expect(lineNet(line({ qty: '', unit_cost: '' }))).toBe('0.0000');
  });

  it('survives a number somebody is part-way through typing', () => {
    for (const partial of ['1.', '-', '.', '']) {
      expect(() => lineNet(line({ unit_cost: partial }))).not.toThrow();
    }
  });

  it('keeps four decimals, because that is what the order stores', () => {
    // 3.333 per metre is valid input; two decimals here would show a buyer a
    // total the order does not have.
    expect(lineNet(line({ qty: '3', unit_cost: '3.333' }))).toBe('9.9990');
  });

  it('does not go through a float', () => {
    // 0.1 + 0.2 is 0.30000000000000004 in binary floating point. Ten lines of
    // 0.10 must come to exactly 1.00.
    const tenth = line({ qty: '1', unit_cost: '0.10' });
    expect(orderNet(Array.from({ length: 10 }, () => tenth))).toBe('1.0000');
  });
});

describe('a whole draft order', () => {
  it('sums the rounded lines rather than rounding the sum', () => {
    // The server rounds and stores each line, then the header is the sum of
    // what was stored. Rounding once at the end gives a total that disagrees
    // with the lines printed under it, which is the complaint a customer is
    // always right to make.
    const odd = line({ qty: '1', unit_cost: '0.33335' });
    // 0.3334 stored twice, not 0.6667 computed once.
    expect(orderNet([odd, odd])).toBe('0.6668');
  });

  it('is zero for an order with nothing on it', () => {
    expect(orderNet([])).toBe('0.0000');
  });

  it('adds lines whatever they are taxed as', () => {
    expect(
      orderNet([
        line({ qty: '10', unit_cost: '10.00', tax_treatment: 'standard' }),
        line({ variant_id: 'v2', qty: '10', unit_cost: '10.00', tax_treatment: 'exempt' }),
      ]),
    ).toBe('200.0000');
  });
});
