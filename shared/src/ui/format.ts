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
export function money(amount: string | null | undefined, style: MoneyStyle = {}): string {
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
export function percent(value: string | null | undefined, signed = false): string {
  if (value === null || value === undefined || value === '') return '—';
  const n = value.trim();
  const positive = !n.startsWith('-');
  return `${signed && positive ? '+' : ''}${n}%`;
}

/** Which way a change should read. Used for an arrow and a colour together,
 *  never for colour alone. */
export function direction(value: string | null | undefined): 'up' | 'down' | 'flat' {
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
  const [, m = '', d = ''] = iso.split('-');
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  const month = months[Number(m) - 1];
  return month ? `${Number(d)} ${month}` : iso;
}

/** Payment methods, written the way a shop says them. */
export function tenderName(method: string): string {
  const named: Record<string, string> = {
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
  };
  return named[method] ?? method.replace(/_/g, ' ');
}
