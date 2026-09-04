import { describe, expect, it } from 'vitest';

import {
  formatMoney,
  formatMoneyParts,
  formatQuantity,
  isNegative,
  isZero,
  marketOf,
  placesFor,
} from './money';

describe('money is formatted without becoming a number', () => {
  it('keeps every digit of a value a float64 could not hold', () => {
    // 9007199254740993 is 2^53 + 1. Parsing this to a number and back loses the
    // last digit, which is the entire reason this module walks the string.
    expect(
      formatMoney('9007199254740993.15', { currency: 'USD', market: 'US' }),
    ).toBe('USD 9,007,199,254,740,993.15');
  });

  it('does not round the fraction it is given', () => {
    // The server has already decided the value. Showing two places of a
    // four-place amount truncates the display; it must never round it up and
    // disagree with the invoice.
    expect(formatMoney('1164.9999', { currency: 'SAR', market: 'SA' })).toBe(
      'SAR 1,164.99',
    );
  });

  it('pads a short fraction rather than showing a ragged column', () => {
    expect(formatMoney('1164', { currency: 'SAR', market: 'SA' })).toBe(
      'SAR 1,164.00',
    );
    expect(formatMoney('1164.5', { currency: 'SAR', market: 'SA' })).toBe(
      'SAR 1,164.50',
    );
  });
});

describe('grouping follows the market, not the browser', () => {
  it('groups in lakh and crore for Bangladesh', () => {
    // 12,34,567 and not 1,234,567. The position of the commas is how the
    // magnitude is read; the western pattern is read wrong in Dhaka.
    expect(formatMoney('1234567.89', { currency: 'BDT', market: 'BD' })).toBe(
      'BDT 12,34,567.89',
    );
    expect(formatMoney('123456789', { currency: 'BDT', market: 'BD' })).toBe(
      'BDT 12,34,56,789.00',
    );
  });

  it('groups in thousands everywhere else', () => {
    expect(formatMoney('1234567.89', { currency: 'USD', market: 'US' })).toBe(
      'USD 1,234,567.89',
    );
    expect(formatMoney('1234567.89', { currency: 'SAR', market: 'SA' })).toBe(
      'SAR 1,234,567.89',
    );
  });

  it('leaves a short value ungrouped', () => {
    expect(formatMoney('99.5', { currency: 'USD', market: 'US' })).toBe('USD 99.50');
  });
});

describe('currency precision comes from the currency', () => {
  it('gives three places to a currency that has them', () => {
    expect(placesFor('KWD')).toBe(3);
    expect(formatMoney('12.3456', { currency: 'KWD' })).toBe('KWD 12.345');
  });

  it('gives none to a currency that has none', () => {
    expect(placesFor('JPY')).toBe(0);
    expect(formatMoney('1200.00', { currency: 'JPY' })).toBe('JPY 1,200');
  });

  it('assumes two for a currency it does not know, and does not guess a symbol', () => {
    expect(formatMoney('50', { currency: 'XAF' })).toBe('XAF 50.00');
  });
});

describe('negatives and empties', () => {
  it('writes a refund with a minus, not brackets', () => {
    expect(formatMoney('-449.00', { currency: 'SAR', market: 'SA' })).toBe(
      'SAR -449.00',
    );
  });

  it('shows an em dash for a value that is absent, not a zero', () => {
    // A missing figure and a figure of zero mean different things on a
    // statement, and a zero shown for an absent value is a wrong answer.
    expect(formatMoney(null, { currency: 'SAR' })).toBe('—');
    expect(formatMoney(undefined, { currency: 'SAR' })).toBe('—');
    expect(formatMoney('', { currency: 'SAR' })).toBe('—');
    expect(formatMoney('0', { currency: 'SAR' })).toBe('SAR 0.00');
  });

  it('detects sign and zero without parsing', () => {
    expect(isNegative('-0.0001')).toBe(true);
    expect(isNegative('0.0001')).toBe(false);
    expect(isZero('0.0000')).toBe(true);
    expect(isZero('-0.00')).toBe(true);
    expect(isZero('0.0001')).toBe(false);
  });
});

describe('parts are returned separately', () => {
  it('lets a table set the code quieter than the figure', () => {
    const parts = formatMoneyParts('1234.5', { currency: 'usd', market: 'US' });
    expect(parts.figure).toBe('1,234.50');
    expect(parts.currency).toBe('USD');
    expect(parts.text).toBe('USD 1,234.50');
  });

  it('drops the code for a column already headed with it', () => {
    expect(formatMoney('1234.5', { currency: 'USD', market: 'US', bare: true })).toBe(
      '1,234.50',
    );
  });
});

describe('quantities', () => {
  it('trims trailing zeros so a shelf count reads as a count', () => {
    expect(formatQuantity('12.000')).toBe('12');
    expect(formatQuantity('12.500')).toBe('12.5');
    expect(formatQuantity('0.125')).toBe('0.125');
  });

  it('groups by market like money does', () => {
    expect(formatQuantity('1234567', 'BD')).toBe('12,34,567');
    expect(formatQuantity('1234567', 'US')).toBe('1,234,567');
  });
});

describe('market resolution', () => {
  it('maps the three named markets and treats the rest as international', () => {
    expect(marketOf('BD')).toBe('BD');
    expect(marketOf('sa')).toBe('SA');
    expect(marketOf('US')).toBe('US');
    expect(marketOf('GB')).toBe('INTL');
    expect(marketOf(null)).toBe('INTL');
    expect(marketOf(undefined)).toBe('INTL');
  });
});
