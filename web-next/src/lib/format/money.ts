// Formatting money without ever making it a number.
//
// The API sends amounts as decimal STRINGS -- "1164.0000", not 1164.0 -- because
// JavaScript's `number` is a float64 and cannot hold every decimal exactly. That
// discipline is worth nothing if the display layer parses the string to group
// the thousands, so nothing here calls `parseFloat`, `Number()` or
// `Intl.NumberFormat` on the value itself.
//
// Instead the string is split at the decimal point and grouping is applied to
// the integer half by walking it. The fraction is carried through untouched, so
// a value the server sent to four places is shown at exactly the precision the
// caller asks for and rounded nowhere else.
//
// # No currency is assumed
//
// There is no default of SAR, and no symbol table that quietly turns an unknown
// code into a guess. The currency comes from the company (`base_currency` on
// `GET /companies`) and the market decides where the code sits and how the
// groups are separated. A shop in Dhaka, one in Riyadh and one in California
// are three different answers and the product knows which it is showing.

/** A market the product sells into. Drives every convention below. */
export type MarketCode = 'BD' | 'SA' | 'US' | 'INTL';

interface MarketConventions {
  /** Digits between separators, innermost group first. Bangladesh is 3 then 2. */
  grouping: readonly number[];
  groupSeparator: string;
  decimalSeparator: string;
  /** Where the currency code sits relative to the figure. */
  currencyPosition: 'before' | 'after';
  /** BCP 47 tag used for dates and for anything Intl genuinely should decide. */
  locale: string;
}

/**
 * Per-market conventions.
 *
 * Bangladesh groups in the lakh/crore pattern -- 12,34,567 and not 1,234,567 --
 * which is not a stylistic preference. An owner in Dhaka reads the second one
 * wrong, because the position of the commas is how the magnitude is read.
 */
const MARKETS: Record<MarketCode, MarketConventions> = {
  BD: {
    grouping: [3, 2],
    groupSeparator: ',',
    decimalSeparator: '.',
    currencyPosition: 'before',
    locale: 'en-BD',
  },
  SA: {
    grouping: [3],
    groupSeparator: ',',
    decimalSeparator: '.',
    currencyPosition: 'before',
    locale: 'en-SA',
  },
  US: {
    grouping: [3],
    groupSeparator: ',',
    decimalSeparator: '.',
    currencyPosition: 'before',
    locale: 'en-US',
  },
  INTL: {
    grouping: [3],
    groupSeparator: ',',
    decimalSeparator: '.',
    currencyPosition: 'before',
    locale: 'en',
  },
};

/** The market a country code belongs to. Anything else is International. */
export function marketOf(country: string | null | undefined): MarketCode {
  switch ((country ?? '').toUpperCase()) {
    case 'BD':
      return 'BD';
    case 'SA':
      return 'SA';
    case 'US':
      return 'US';
    default:
      return 'INTL';
  }
}

/**
 * How many places a currency is written to.
 *
 * Not a rounding rule -- the server has already decided the value. This only
 * says how much of the fraction to show, so 1164.0000 reads as 1,164.00 in a
 * two-place currency and 1,164 in a zero-place one.
 */
const CURRENCY_PLACES: Record<string, number> = {
  BHD: 3,
  IQD: 3,
  JOD: 3,
  KWD: 3,
  LYD: 3,
  OMR: 3,
  TND: 3,
  JPY: 0,
  KRW: 0,
  VND: 0,
  CLP: 0,
  ISK: 0,
};

export function placesFor(currency: string): number {
  return CURRENCY_PLACES[currency.toUpperCase()] ?? 2;
}

/** Splits a decimal string into sign, integer digits and fraction digits. */
function split(value: string): { negative: boolean; whole: string; fraction: string } {
  let s = value.trim();
  let negative = false;
  if (s.startsWith('-')) {
    negative = true;
    s = s.slice(1);
  } else if (s.startsWith('+')) {
    s = s.slice(1);
  }
  const dot = s.indexOf('.');
  if (dot === -1) return { negative, whole: s || '0', fraction: '' };
  return {
    negative,
    whole: s.slice(0, dot) || '0',
    fraction: s.slice(dot + 1),
  };
}

/**
 * Applies a grouping pattern to a string of digits, right to left.
 *
 * Walking the string rather than using Intl keeps the value exact: an amount
 * with more significant digits than a float64 can hold comes out the same on
 * the other side.
 */
function group(digits: string, pattern: readonly number[], separator: string): string {
  if (digits.length <= (pattern[0] ?? 3)) return digits;

  const parts: string[] = [];
  let rest = digits;
  let i = 0;
  while (rest.length > 0) {
    // The last size in the pattern repeats for everything above it, which is
    // what makes 12,34,56,789 come out right in Bangladesh.
    const size = pattern[Math.min(i, pattern.length - 1)] ?? 3;
    if (rest.length <= size) {
      parts.unshift(rest);
      break;
    }
    parts.unshift(rest.slice(rest.length - size));
    rest = rest.slice(0, rest.length - size);
    i += 1;
  }
  return parts.join(separator);
}

/** Pads or truncates a fraction to exactly `places` digits, without rounding. */
function fractionTo(fraction: string, places: number): string {
  if (places === 0) return '';
  if (fraction.length >= places) return fraction.slice(0, places);
  return fraction.padEnd(places, '0');
}

export interface MoneyFormat {
  currency: string;
  market?: MarketCode;
  /** Overrides the currency's own precision. Used by unit prices. */
  places?: number;
  /** Drops the currency code. For a column already headed with it. */
  bare?: boolean;
}

/**
 * A money value, ready to render.
 *
 * Returns the pieces as well as the joined string, because a table cell often
 * wants the code set quieter than the figure and cannot do that with one blob
 * of text.
 */
export function formatMoneyParts(
  value: string | null | undefined,
  opts: MoneyFormat,
): { figure: string; currency: string; text: string } {
  const market = MARKETS[opts.market ?? 'INTL'];
  const places = opts.places ?? placesFor(opts.currency);

  if (value === null || value === undefined || value === '') {
    return { figure: '—', currency: '', text: '—' };
  }

  const { negative, whole, fraction } = split(value);
  const grouped = group(whole, market.grouping, market.groupSeparator);
  const frac = fractionTo(fraction, places);

  let figure = grouped + (frac ? market.decimalSeparator + frac : '');
  // A leading minus, not brackets. Brackets are an accounting convention that
  // a shopkeeper reading a refund line does not necessarily share, and the
  // minus is unambiguous in every market this product sells into.
  if (negative) figure = `-${figure}`;

  const currency = opts.bare ? '' : opts.currency.toUpperCase();
  const text = !currency
    ? figure
    : market.currencyPosition === 'before'
      ? `${currency} ${figure}`
      : `${figure} ${currency}`;

  return { figure, currency, text };
}

/** The joined string, for the common case. */
export function formatMoney(
  value: string | null | undefined,
  opts: MoneyFormat,
): string {
  return formatMoneyParts(value, opts).text;
}

/**
 * A quantity: grouped, but with trailing zeros trimmed.
 *
 * Stock is held to several decimal places because a recipe needs them, but a
 * shelf count of "12.000" reads as a measurement rather than a count, so the
 * zeros come off unless they carry information.
 */
export function formatQuantity(
  value: string | null | undefined,
  market: MarketCode = 'INTL',
): string {
  if (value === null || value === undefined || value === '') return '—';
  const conventions = MARKETS[market];
  const { negative, whole, fraction } = split(value);
  const trimmed = fraction.replace(/0+$/, '');
  const grouped = group(whole, conventions.grouping, conventions.groupSeparator);
  const text = grouped + (trimmed ? conventions.decimalSeparator + trimmed : '');
  return negative ? `-${text}` : text;
}

/** True when a decimal string is negative, without parsing it. */
export function isNegative(value: string | null | undefined): boolean {
  return typeof value === 'string' && value.trim().startsWith('-');
}

/** True when a decimal string is zero, without parsing it. */
export function isZero(value: string | null | undefined): boolean {
  if (!value) return true;
  return /^[+-]?0*(\.0*)?$/.test(value.trim());
}

export function localeTagFor(market: MarketCode): string {
  return MARKETS[market].locale;
}
