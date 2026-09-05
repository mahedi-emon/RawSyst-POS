import { describe, expect, it } from 'vitest';

import {
  isPeriodic,
  readyToFile,
  yearToDate,
  type VatReturn,
} from './statements';

function vat(over: Partial<VatReturn> = {}): VatReturn {
  return {
    country: 'sa',
    from: '2026-01-01',
    to: '2026-03-31',
    base_currency: 'SAR',
    model: 'vat',
    supplies: [],
    total_net: '0.00',
    output_tax_total: '0.00',
    input_tax_total: '0.00',
    billed_input_tax: '0.00',
    input_difference: '0.00',
    net_payable: '0.00',
    ledger_output_tax: '0.00',
    difference: '0.00',
    reconciled: true,
    outstanding: [],
    filed: false,
    ...over,
  };
}

describe('whether a return may be filed', () => {
  it('is ready when it reconciles and nothing is outstanding', () => {
    expect(readyToFile(vat())).toBe(true);
  });

  it('is not ready while anything is outstanding', () => {
    // The live return carries three of these, including "the official return
    // form layout has not been verified against the tax authority". A screen
    // that showed the totals without them would present an unfiled draft as a
    // filing.
    expect(
      readyToFile(
        vat({
          outstanding: [
            'the official return form layout has not been verified against the tax authority',
          ],
        }),
      ),
    ).toBe(false);
  });

  it('is not ready while it disagrees with the ledger', () => {
    expect(readyToFile(vat({ reconciled: false }))).toBe(false);
  });

  it('is not ready once it has been filed', () => {
    expect(readyToFile(vat({ filed: true }))).toBe(false);
  });

  it('treats a missing outstanding list as nothing outstanding', () => {
    // `omitempty` on the wire: an empty list is absent rather than `[]`, and
    // reading that as "unknown" would block every clean return.
    expect(readyToFile(vat({ outstanding: undefined }))).toBe(true);
  });
});

describe('the period a financial screen opens on', () => {
  it('is this year to today', () => {
    // Year to date, because "how are we doing" is the question, and a month on
    // its own answers it only in December.
    expect(yearToDate(new Date(2026, 8, 5))).toEqual({
      from: '2026-01-01',
      to: '2026-09-05',
    });
  });

  it('pads a single-digit month and day', () => {
    expect(yearToDate(new Date(2026, 0, 3))).toEqual({
      from: '2026-01-01',
      to: '2026-01-03',
    });
  });

  it('reads the local calendar date, not a UTC timestamp', () => {
    // A shop in Dhaka opening this at nine in the morning is in a different
    // UTC day, and a report that quietly started yesterday would be wrong in a
    // way nobody would notice.
    const localMidnight = new Date(2026, 11, 31, 0, 30);
    expect(yearToDate(localMidnight).to).toBe('2026-12-31');
  });
});

describe('which statements cover a period', () => {
  it('knows a profit and loss and a cash flow do', () => {
    expect(isPeriodic('profit')).toBe(true);
    expect(isPeriodic('cash')).toBe(true);
  });

  it('knows a balance sheet and a trial balance stand at a date', () => {
    // "What we own on the 31st" is not a range, and offering a from-date for
    // one would be offering a control that changes nothing.
    expect(isPeriodic('balance')).toBe(false);
    expect(isPeriodic('trial')).toBe(false);
  });
});
