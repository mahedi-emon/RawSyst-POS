// Number formatting, which is where a financial product earns or loses trust.
//
// The cases that matter are the ones a float would get wrong and the ones a
// careless implementation renders inconsistently across a column.

import { describe, expect, it } from 'vitest';

import {
  direction,
  isZero,
  localName,
  money,
  percent,
  shortDate,
  tenderName,
} from './format';
import { ar, type Key } from '../i18n/strings';

/** The Arabic catalogue as a translate function, which is what tenderName
 *  takes. No provider and no React: the point of the parameter is that a
 *  formatter stays a pure function. */
const translate = (key: Key) => ar[key];

describe('money', () => {
  it('always shows two decimal places', () => {
    // A column mixing "12.5" and "12.50" cannot be scanned, and invites the
    // reader to wonder which one is rounded.
    expect(money('12.5')).toBe('12.50');
    expect(money('12')).toBe('12.00');
    expect(money('12.500')).toBe('12.50');
  });

  it('groups thousands', () => {
    expect(money('1234.56')).toBe('1,234.56');
    expect(money('1234567.89')).toBe('1,234,567.89');
    expect(money('999.99')).toBe('999.99');
  });

  it('never goes through a float', () => {
    // The canonical case. Number('0.1') + Number('0.2') is
    // 0.30000000000000004, and a dashboard is exactly where that surfaces.
    expect(money('0.1')).toBe('0.10');
    expect(money('0.15')).toBe('0.15');
    // Beyond what a float can represent exactly, and beyond Number.MAX_SAFE.
    expect(money('9007199254740993.15')).toBe('9,007,199,254,740,993.15');
  });

  it('brackets negatives rather than using a hyphen', () => {
    // A minus sign is easy to miss at the start of a right-aligned column and
    // is sometimes lost in print. Accounting has used brackets for a century.
    expect(money('-450.00')).toBe('(450.00)');
    expect(money('-1234.5')).toBe('(1,234.50)');
  });

  it('shows the currency as a code, never a symbol', () => {
    // $ is claimed by a dozen currencies and this product serves several
    // markets. A code is unglamorous and unmistakable.
    expect(money('1234.56', { currency: 'SAR' })).toBe('SAR 1,234.56');
    expect(money('1234.56', { currency: 'BDT' })).toBe('BDT 1,234.56');
    expect(money('1234.56', { currency: 'USD' })).toBe('USD 1,234.56');
  });

  it('signs a figure only when asked', () => {
    expect(money('50.00')).toBe('50.00');
    expect(money('50.00', { signed: true })).toBe('+50.00');
  });

  it('renders absent as an em dash, not as zero', () => {
    // "We have no figure" and "the figure is zero" are different facts, and
    // only one of them should reassure an owner.
    expect(money(null)).toBe('—');
    expect(money(undefined)).toBe('—');
    expect(money('')).toBe('—');
    expect(money('0')).toBe('0.00');
  });

  it('survives the notations Postgres actually returns', () => {
    expect(money('0.0000')).toBe('0.00');
    expect(money('345.0000')).toBe('345.00');
    expect(money('-600.0000')).toBe('(600.00)');
  });
});

describe('percent', () => {
  it('renders what the server said', () => {
    expect(percent('12.4')).toBe('12.4%');
    expect(percent('-3.1')).toBe('-3.1%');
  });

  it('signs positives when asked, and never signs a negative twice', () => {
    expect(percent('12.4', true)).toBe('+12.4%');
    expect(percent('-3.1', true)).toBe('-3.1%');
  });

  it('renders an absent change as an em dash', () => {
    // The server omits the change when yesterday was zero, because "up 100%"
    // from nothing is a division artefact. Showing 0% would be a claim.
    expect(percent(null)).toBe('—');
    expect(percent(undefined)).toBe('—');
  });
});

describe('direction', () => {
  it('reads the sign, so an arrow and a colour agree', () => {
    expect(direction('12.4')).toBe('up');
    expect(direction('-3.1')).toBe('down');
    expect(direction('0')).toBe('flat');
    expect(direction('0.0')).toBe('flat');
    expect(direction(null)).toBe('flat');
  });
});

describe('isZero', () => {
  it('recognises every notation the database returns', () => {
    // A screen testing `=== '0.00'` would show a zero row as non-zero half the
    // time, because the notation depends on the column's scale.
    for (const zero of ['0', '0.0', '0.00', '0.0000', '-0.00', '', null, undefined]) {
      expect(isZero(zero)).toBe(true);
    }
    for (const notZero of ['0.01', '-0.01', '100', '0.0001']) {
      expect(isZero(notZero)).toBe(false);
    }
  });
});

describe('shortDate', () => {
  it('is unambiguous across markets', () => {
    // "8/16" and "16/8" mean different things in different countries; "16 Aug"
    // means one thing everywhere.
    expect(shortDate('2026-08-16')).toBe('16 Aug');
    expect(shortDate('2026-01-02')).toBe('2 Jan');
    expect(shortDate('2026-12-31')).toBe('31 Dec');
  });

  it('falls back to the raw value rather than showing NaN', () => {
    expect(shortDate('not-a-date')).toBe('not-a-date');
  });

  // Half the product's dates carry a time on them, because the API sends
  // RFC3339 for anything that has one. Splitting the whole string on the hyphen
  // made the day "28T22:45:00+06:00", and card settlement listed eleven
  // payments as taken on "NaN Aug" while the Terminals list said the same about
  // when each till was last seen. Found by looking at the screens.
  it('reads a timestamp as the day it falls on', () => {
    expect(shortDate('2026-08-28T22:45:00+06:00')).toBe('28 Aug');
    expect(shortDate('2026-08-28T22:45:00Z')).toBe('28 Aug');
    expect(shortDate('2026-01-02T00:00:00-05:00')).toBe('2 Jan');
  });

  it('never renders NaN, whatever it is given', () => {
    for (const odd of ['', 'today', '2026', '2026-13-40', '2026-08-']) {
      expect(shortDate(odd)).not.toContain('NaN');
    }
  });
});

describe('tenderName', () => {
  it('writes methods the way a shop says them', () => {
    expect(tenderName('mada')).toBe('Mada');
    expect(tenderName('stc_pay')).toBe('STC Pay');
    expect(tenderName('bkash')).toBe('bKash');
    expect(tenderName('customer_due')).toBe('On account');
  });

  it('degrades readably for a method it has not been taught', () => {
    // A new tender method must never render as a raw enum on an owner's
    // screen.
    expect(tenderName('some_new_wallet')).toBe('some new wallet');
  });

  // The Arabic half.
  //
  // A browser audit found "Mada" and "Cash" sitting in Arabic tables on the
  // settlement screen and in the payment mix, because this function held one
  // English table and had no way to be told otherwise.
  it('answers in the language of whoever is reading it', () => {
    expect(tenderName('cash', translate)).toBe(ar['tender.cash']);
    expect(tenderName('mada', translate)).toBe(ar['tender.mada']);
    expect(tenderName('customer_due', translate)).toBe(ar['tender.customer_due']);
  });

  it('leaves a brand alone in both languages', () => {
    // Mastercard has no Arabic name. Inventing one would print a word on a
    // receipt that no cardholder recognises.
    expect(tenderName('mastercard', translate)).toBe('Mastercard');
    expect(tenderName('visa', translate)).toBe('Visa');
  });

  it('still degrades readably when it is given a translator', () => {
    // The cast inside tenderName is safe only because an unknown method never
    // reaches it. If that ever stops being true, this fails with a raw key.
    expect(tenderName('some_new_wallet', translate)).toBe('some new wallet');
  });

  // Every method the function names must have a catalogue key, or the cast
  // inside it would hand a missing key to the translator and render
  // "undefined" on a shop's screen.
  it('has an Arabic string for every method it names', () => {
    const missing = METHODS.filter((m) => !(`tender.${m}` in ar));
    expect(missing.join(', ')).toBe('');
  });
});

/** Every method tenderName writes an English name for. */
const METHODS = [
  'cash', 'mada', 'visa', 'mastercard', 'amex', 'apple_pay', 'stc_pay',
  'samsung_pay', 'bank_transfer', 'cheque', 'store_credit', 'customer_due',
  'loyalty_points', 'tabby', 'tamara', 'bkash', 'nagad', 'sadad',
  'exchange_clearing',
];

// An account, a product and an expense head are a shop's own words for its own
// things, so they cannot live in the catalogue. They carry a translation as
// DATA, the server sends both, and this picks.
//
// The chart of accounts had none at all until a look at the Arabic dashboard
// found "Stock Write-off", "Cash Over/Short" and "Bank & Card Charges" sitting
// in English on the panel that says where today's money went.
describe('localName', () => {
  it('gives an Arabic reader the Arabic name', () => {
    expect(localName('ar', 'Stock Write-off', 'إعدام المخزون')).toBe(
      'إعدام المخزون',
    );
  });

  it('gives an English reader the English name, whatever else is there', () => {
    expect(localName('en', 'Stock Write-off', 'إعدام المخزون')).toBe(
      'Stock Write-off',
    );
  });

  it('falls back to the name that exists rather than to nothing', () => {
    // A company that has written no Arabic for an account it created itself
    // must still see the account. A blank cell where a name belongs is worse
    // than a name in the wrong language.
    expect(localName('ar', 'Van hire')).toBe('Van hire');
    expect(localName('ar', 'Van hire', '')).toBe('Van hire');
    expect(localName('ar', 'Van hire', '   ')).toBe('Van hire');
    expect(localName('ar', 'Van hire', null)).toBe('Van hire');
  });
});
