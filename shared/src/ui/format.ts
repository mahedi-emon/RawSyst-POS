// How money and figures are written, product-wide.
//
// One place, because inconsistent number formatting is the fastest way to make
// a financial product feel untrustworthy. An owner who sees "1,234.5" in one
// tile and "1234.50" in the next stops believing both.
//
// # Strings in, strings out
//
// Amounts arrive from the server as decimal strings and are formatted without
// ever becoming a float. `Number('0.1') + Number('0.2')` is 0.30000000000000004,
// and a dashboard is exactly where that surfaces — as a total that is out by a
// hallala against a report the same server produced.
//
// Grouping is applied to the integer part by string manipulation for the same
// reason. Intl.NumberFormat would do it beautifully and would require a float
// to do it to.

import type { Key, Locale, Translate } from '../i18n/strings';

/** Where the currency code sits, and how the number is grouped. */
export interface MoneyStyle {
  /** ISO code — SAR, BDT, USD. Shown as a code, never a symbol.
   *
   * Deliberate: ﷼, ৳ and $ are ambiguous across the markets this product
   * serves, and $ in particular is claimed by a dozen currencies. A code is
   * unglamorous and unmistakable, which is the right trade for a ledger. */
  currency?: string;
  /** Show a + on positive figures. Off by default — a sales total does not
   *  need a sign, but a change against yesterday does. */
  signed?: boolean;
}

/**
 * Formats a decimal string as money.
 *
 * Always two decimal places. A column where some cells show one and others two
 * cannot be scanned, and "12.5" beside "12.50" invites the reader to wonder
 * which is rounded.
 */
export function money(
  amount: string | null | undefined,
  style: MoneyStyle = {},
): string {
  if (amount === null || amount === undefined || amount === '') return '—';

  const trimmed = amount.trim();
  const negative = trimmed.startsWith('-');
  const digits = negative ? trimmed.slice(1) : trimmed;

  const [wholeRaw = '0', fracRaw = ''] = digits.split('.');
  const whole = wholeRaw.replace(/\D/g, '') || '0';
  const frac = (fracRaw.replace(/\D/g, '') + '00').slice(0, 2);

  let out = group(whole) + '.' + frac;

  if (negative) {
    // Parentheses, not a hyphen. A minus sign is easy to miss at the start of
    // a right-aligned column and is sometimes lost entirely in print;
    // accounting convention has used brackets for a century for that reason.
    out = `(${out})`;
  } else if (style.signed) {
    out = '+' + out;
  }

  return style.currency ? `${style.currency} ${out}` : out;
}

/** Thousands separators, applied to the integer part only. */
function group(whole: string): string {
  let out = '';
  for (let i = 0; i < whole.length; i++) {
    if (i > 0 && (whole.length - i) % 3 === 0) out += ',';
    out += whole[i];
  }
  return out;
}

/** A percentage, as the server stated it. Null renders as an em dash rather
 *  than as 0%, because "we did not compute this" and "no change" are different
 *  facts and only one of them is reassuring. */
export function percent(
  value: string | null | undefined,
  signed = false,
): string {
  if (value === null || value === undefined || value === '') return '—';
  const n = value.trim();
  const positive = !n.startsWith('-');
  return `${signed && positive ? '+' : ''}${n}%`;
}

/** Which way a change should read. Used for an arrow and a colour together,
 *  never for colour alone. */
export function direction(
  value: string | null | undefined,
): 'up' | 'down' | 'flat' {
  if (value === null || value === undefined || value === '') return 'flat';
  const n = value.trim();
  if (n.startsWith('-')) return 'down';
  if (/^[0.]+$/.test(n)) return 'flat';
  return 'up';
}

/** True when a decimal string is zero, whatever its notation.
 *
 * "0", "0.00" and "0.0000" all arrive from Postgres depending on the column,
 * and a screen that tested `=== '0.00'` would show a zero row as non-zero
 * half the time. */
export function isZero(amount: string | null | undefined): boolean {
  if (!amount) return true;
  return /^-?0*\.?0*$/.test(amount.trim());
}

/** A short, unambiguous date for a chart axis or a row.
 *
 * Deliberately not locale-formatted: the axis has room for five characters and
 * "16 Aug" is readable to every market this product serves, where "8/16" and
 * "16/8" mean different things in different ones. */
export function shortDate(iso: string): string {
  // The DATE part, before anything else is looked at.
  //
  // This was written for `YYYY-MM-DD` and splitting on the hyphen worked
  // perfectly for one. Half its callers hand it a timestamp instead — the API
  // sends RFC3339 for anything with a time on it — and then the third piece is
  // "28T22:45:00+06:00", whose Number() is NaN. Card settlement listed eleven
  // payments as taken on "NaN Aug", and the Terminals list said the same about
  // when each till was last seen.
  //
  // Trimmed here rather than at each call site: a formatter that is wrong for
  // half the strings the product actually holds is the formatter's problem,
  // and the next screen to pass a timestamp should not have to know.
  const [date = ''] = iso.split('T');
  const [, m = '', d = ''] = date.split('-');
  const months = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ];
  const month = months[Number(m) - 1];
  const day = Number(d);
  if (!month || !Number.isFinite(day) || day < 1 || day > 31) return iso;
  return `${day} ${month}`;
}

/** A date on a document, with its year.
 *
 * `shortDate` drops the year because a chart axis has room for five characters
 * and every point on it is inside one window. A purchase order, a bill and an
 * invoice are not: a list of them spans years, and "28 Aug" on a document whose
 * payment terms have run out for two years is a genuinely misleading thing to
 * print.
 *
 * Written out rather than passed to `toLocaleDateString`, for the same reason
 * shortDate is: "08/28/2026" and "28/08/2026" are both plausible readings of
 * the same nine characters, and this product is sold into markets that read
 * them differently. A named month cannot be misread.
 *
 * The ISO string is what the API sends and what the eight screens using this
 * were printing RAW — `2026-08-28`, set in a monospace face, which reads as a
 * database column rather than as the date somebody placed an order. */
export function longDate(iso: string | null | undefined): string {
  if (!iso) return '—';
  const [y = '', m = '', rest = ''] = iso.split('-');
  const d = rest.slice(0, 2);
  const months = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ];
  const month = months[Number(m) - 1];
  if (!month || !y || !d) return iso;
  return `${Number(d)} ${month} ${y}`;
}

/**
 * A record's own name, in the language of whoever is reading it.
 *
 * Some names are not in the catalogue and never can be: an account, a product,
 * an expense head is a shop's own word for its own thing. Those carry their
 * translation as data, and the server sends both names so the screen can pick
 * — which is why this takes two strings rather than a key.
 *
 * English is the fallback in both directions. A company that has not written
 * Arabic for an account it created reads the name it did write, which is a
 * name; the alternative is a blank cell where the account should be.
 */
export function localName(
  locale: Locale,
  name: string,
  nameAr?: string | null,
): string {
  if (locale === 'ar' && nameAr && nameAr.trim() !== '') return nameAr;
  return name;
}

/**
 * Payment methods, written the way a shop says them.
 *
 * `translate` is the catalogue's t(). Passing it rather than reaching for a
 * hook keeps this a pure function that the tests can call and that a receipt
 * printer can use, and every caller already has one.
 *
 * Called without it, this answers in English — which is what the till's
 * receipt builder and any non-React caller want, and is also the honest
 * fallback for a method nobody has taught it.
 *
 * Brands keep their own spelling in both languages. Mada is مدى in Arabic
 * because that is its own name, printed on the cards; Mastercard has no Arabic
 * name, and inventing one would put a word on a receipt that no cardholder
 * recognises.
 */
export function tenderName(method: string, translate?: Translate): string {
  const english: Record<string, string> = {
    cash: 'Cash',
    mada: 'Mada',
    visa: 'Visa',
    mastercard: 'Mastercard',
    amex: 'Amex',
    apple_pay: 'Apple Pay',
    stc_pay: 'STC Pay',
    samsung_pay: 'Samsung Pay',
    bank_transfer: 'Bank transfer',
    cheque: 'Cheque',
    store_credit: 'Store credit',
    customer_due: 'On account',
    loyalty_points: 'Points',
    tabby: 'Tabby',
    tamara: 'Tamara',
    bkash: 'bKash',
    nagad: 'Nagad',
    sadad: 'SADAD',
    exchange_clearing: 'Exchange clearing',
  };

  const known = method in english;
  if (translate && known) {
    // The cast is the seam between an open string from the API and the closed
    // union of catalogue keys. `known` is what makes it safe: only a method
    // this function already names reaches here, every one of those has a
    // `tender.` key, and the two lists are kept in step by a test.
    return translate(`tender.${method}` as Key);
  }
  return english[method] ?? method.replace(/_/g, ' ');
}
