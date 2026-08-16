// What an order-in-progress comes to, for the buyer's eyes only.
//
// The server recomputes every figure from the lines when the order is saved and
// is the authority on all of them — a client that could state its own total
// could authorise an amount different from what its lines add up to, and the
// purchase order is what a supplier holds the shop to.
//
// This exists so a buyer knows roughly what they are committing to before they
// press Save. It is nonetheless done in MINOR UNITS rather than through a
// float, because a preview that disagreed with the saved order by a hallala
// would be worse than showing no preview at all: it would teach the buyer that
// the numbers on this screen are approximate, and they are not supposed to be.

/** A line being typed. Every value is a string, exactly as it came off the
 *  keyboard and exactly as it will be sent. */
export interface DraftLine {
  variantId: string;
  description: string;
  qty: string;
  unitCost: string;
  taxRate: string;
}

export interface Totals {
  net: string;
  tax: string;
  gross: string;
}

/** One line's net, tax and gross. */
export function lineTotals(line: DraftLine): Totals {
  const qty = line.qty.trim();
  const rate = line.taxRate.trim();

  // A half-typed line is worth nothing rather than NaN. Somebody midway
  // through entering "1." must not see the running total blank out.
  if (!isNumeric(qty) || !isNumeric(rate)) return zero();

  const cost = toMinor(line.unitCost);
  // Quantity and rate are multiplied as scaled integers too, so a fractional
  // quantity and a percentage rate never reach a float either.
  const net = scaledMul(cost, qty);
  if (net === null) return zero();
  const tax = scaledMul(net, rate);
  if (tax === null) return zero();
  if (net <= 0n && tax <= 0n && cost === 0n) {
    return { net: '0.00', tax: '0.00', gross: '0.00' };
  }

  return { net: toMajor(net), tax: toMajor(tax), gross: toMajor(net + tax) };
}

/**
 * The order's totals.
 *
 * Summed from each line's ROUNDED figures rather than by rounding a sum. That
 * is the order the server computes them in, and doing it the other way here
 * would produce a preview that differs from the saved order by a unit on some
 * combinations — the classic rounding disagreement, arrived at by being
 * slightly more accurate than the thing you have to agree with.
 */
export function orderTotals(lines: DraftLine[]): Totals {
  let net = 0n;
  let tax = 0n;
  for (const line of lines) {
    const t = lineTotals(line);
    net += toMinor(t.net);
    tax += toMinor(t.tax);
  }
  return { net: toMajor(net), tax: toMajor(tax), gross: toMajor(net + tax) };
}

function zero(): Totals {
  return { net: '0.00', tax: '0.00', gross: '0.00' };
}

function isNumeric(s: string): boolean {
  return s !== '' && /^-?\d*\.?\d*$/.test(s) && /\d/.test(s);
}

/**
 * A decimal string to hundredths, as a BigInt.
 *
 * BigInt rather than number, and that is not defensive dressing: minor units
 * of a line worth more than about ninety trillion exceed
 * Number.MAX_SAFE_INTEGER, and the arithmetic silently loses the last hallala.
 * A test caught it. Nobody raises a purchase order that large, but "nobody
 * does that" is how the bug survives to the one customer who does.
 */
function toMinor(amount: string): bigint {
  const trimmed = amount.trim();
  if (!isNumeric(trimmed)) return 0n;

  const negative = trimmed.startsWith('-');
  const [whole = '0', frac = ''] = trimmed.replace('-', '').split('.');
  const cents =
    BigInt(whole || '0') * 100n + BigInt((frac + '00').slice(0, 2) || '0');
  return negative ? -cents : cents;
}

/**
 * Multiplies a minor-unit amount by a decimal multiplier, rounding half up.
 *
 * The multiplier is scaled to an integer first, so "2.5" and "0.15" are exact
 * rather than the nearest double to them. Returns null when the multiplier is
 * not a number, so a caller can decide what a half-typed field means.
 */
function scaledMul(amount: bigint, multiplier: string): bigint | null {
  const m = multiplier.trim();
  if (!isNumeric(m)) return null;

  const negative = m.startsWith('-');
  const [whole = '0', frac = ''] = m.replace('-', '').split('.');
  const scale = 10n ** BigInt(frac.length);
  const scaled = BigInt(whole || '0') * scale + BigInt(frac || '0');

  const product = amount * scaled;
  // Half up, matching the server's decimal rounding rather than JavaScript's
  // banker-ish Math.round on negatives.
  const rounded = (product + (product < 0n ? -scale / 2n : scale / 2n)) / scale;
  return negative ? -rounded : rounded;
}

function toMajor(cents: bigint): string {
  const negative = cents < 0n;
  const abs = negative ? -cents : cents;
  const whole = abs / 100n;
  const frac = abs % 100n;
  return `${negative ? '-' : ''}${whole}.${String(frac).padStart(2, '0')}`;
}

/** Whether the order is worth sending to the server. */
export function readyToSave(
  supplierId: string,
  warehouseId: string,
  lines: DraftLine[],
): boolean {
  if (!supplierId || !warehouseId) return false;
  return lines.some((l) => l.variantId !== '' && Number(l.qty) > 0);
}