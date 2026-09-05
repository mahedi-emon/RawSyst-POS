import { describe, expect, it } from 'vitest';

import {
  anythingLeft,
  claimFor,
  claimLines,
  overReturnable,
  readiness,
  type Returnable,
} from './returns';

function line(over: Partial<Returnable> = {}): Returnable {
  return {
    bill_line_id: 'b1',
    line_no: 1,
    description: 'Abaya, Black',
    qty_billed: '10',
    qty_returned: '0',
    qty_returnable: '10',
    unit_cost: '100.00',
    tax_treatment: 'standard',
    tax_rate: '0.150000',
    ...over,
  };
}

describe('what the supplier will be claimed', () => {
  it('uses the bill price and the bill rate', () => {
    // Not a rate the screen decides. The supplier owes back what they charged,
    // at the rate that was on their invoice.
    expect(claimFor([line()], { b1: '3' })).toEqual({
      net: '300.00',
      tax: '45.00',
      total: '345.00',
      lines: 1,
    });
  });

  it('adds several lines', () => {
    const lines = [
      line(),
      line({ bill_line_id: 'b2', unit_cost: '40.00', tax_rate: '0.150000' }),
    ];
    expect(claimFor(lines, { b1: '1', b2: '2' })).toMatchObject({
      net: '180.00',
      tax: '27.00',
      total: '207.00',
      lines: 2,
    });
  });

  it('claims no tax on a line that carried none', () => {
    // Zero-rated and exempt purchases exist, and the rate on the bill line is
    // the only thing that says so.
    expect(claimFor([line({ tax_rate: '0', tax_treatment: 'exempt' })], { b1: '2' }))
      .toMatchObject({ net: '200.00', tax: '0.00', total: '200.00' });
  });

  it('ignores a line nobody chose', () => {
    expect(claimFor([line()], {})).toMatchObject({ total: '0.00', lines: 0 });
    expect(claimFor([line()], { b1: '0' })).toMatchObject({ lines: 0 });
  });

  it('ignores a quantity somebody is half-way through typing', () => {
    // decimal.js parses "1." as 1, so the claim would flicker while somebody
    // typed "1.5" -- and this figure goes on a document.
    expect(claimFor([line()], { b1: '1.' })).toMatchObject({ lines: 0 });
    expect(claimFor([line()], { b1: '-' })).toMatchObject({ lines: 0 });
  });

  it('rounds per line, as the server does', () => {
    // 0.335 at 15% is 0.05025 a line. Rounded per line and summed it is 0.10;
    // summed and then rounded it is 0.10 as well here, but the discipline is
    // what keeps the screen and the debit note the same on a long claim.
    const odd = line({ unit_cost: '0.335', tax_rate: '0.150000' });
    expect(claimFor([odd], { b1: '2' }).net).toBe('0.67');
  });

  it('keeps money as strings and never as numbers', () => {
    const penny = line({ unit_cost: '0.10', tax_rate: '0' });
    const other = line({ bill_line_id: 'b2', unit_cost: '0.20', tax_rate: '0' });
    expect(claimFor([penny, other], { b1: '1', b2: '1' }).net).toBe('0.30');
  });
});

describe('what may still go back', () => {
  it('is the server’s answer, cumulative across earlier returns', () => {
    const partly = line({ qty_returned: '6', qty_returnable: '4' });
    expect(overReturnable(partly, '4')).toBe(false);
    expect(overReturnable(partly, '5')).toBe(true);
  });

  it('says when a line is finished', () => {
    expect(anythingLeft(line())).toBe(true);
    expect(anythingLeft(line({ qty_returnable: '0' }))).toBe(false);
  });

  it('sends only the lines with a quantity on them', () => {
    expect(claimLines({ b1: '2', b2: '0', b3: '' })).toEqual([
      { bill_line_id: 'b1', qty: '2' },
    ]);
  });
});

describe('whether the claim can be sent', () => {
  it('is ready with a quantity and a reason', () => {
    expect(readiness([line()], { b1: '2' }, 'Stitching split')).toEqual({ ok: true });
  });

  it('refuses an empty claim', () => {
    expect(readiness([line()], {}, 'Stitching split')).toEqual({
      ok: false,
      reason: 'nothing_chosen',
    });
  });

  it('refuses more than may go back', () => {
    expect(readiness([line()], { b1: '11' }, 'Stitching split')).toEqual({
      ok: false,
      reason: 'too_many',
    });
  });

  it('insists on a reason, because an unexplained return hides a loss', () => {
    expect(readiness([line()], { b1: '2' }, '')).toEqual({
      ok: false,
      reason: 'no_reason',
    });
    expect(readiness([line()], { b1: '2' }, 'ok')).toEqual({
      ok: false,
      reason: 'no_reason',
    });
  });
});
