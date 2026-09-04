// The cart.
//
// # Why the till does arithmetic at all
//
// The server computes the invoice and is the only authority on what it says.
// But a cashier cannot wait for a round trip to see a running total, and a
// customer standing at the counter is owed a figure before they are asked to
// pay. So the till computes the same sum locally, and the server recomputes it
// on submission -- two independent answers that must agree, which is a stronger
// check than either alone.
//
// # Decimal, never float
//
// Every amount here is a `Decimal`. `0.1 + 0.2` in a float64 is 0.30000000000000004,
// and a till that adds fifty lines that way disagrees with the invoice it hands
// over. Values arrive as strings from the API, stay strings in state, and are
// only widened into `Decimal` for the arithmetic itself.
//
// # The last part takes the remainder
//
// When an invoice discount is spread across lines, the last line takes whatever
// is left rather than being computed independently. Otherwise the parts do not
// sum back to the whole: 33.33 x 3 is 99.99 against 100.00 charged, and a
// hallala goes missing on every three-way split. The same rule already governs
// this calculation in the Go service; this is the client's copy of it, and it
// has to round the same way or the two answers will not match.

import { Decimal } from 'decimal.js';

export interface CartLine {
  /** Stable within the cart, so a re-scan of the same item finds its line. */
  variantId: string;
  sku: string;
  description: string;
  descriptionAr?: string;
  /** Decimal strings, exactly as the API sent and will receive them. */
  qty: string;
  unitPrice: string;
  lineDiscount: string;
  /** Set when the discount came from a campaign, so redemption is recorded. */
  promotionId?: string;
  /**
   * Required, not optional.
   *
   * `POST /pos/sales` refuses a line without one — "Choose a tax treatment for
   * this product." It is carried on every line from the moment it is rung up so
   * that a sale cannot be assembled that the server will reject at payment,
   * which is the worst possible moment to find out. The value comes from the
   * catalogue snapshot; `GET /catalog/scan` does not return it.
   */
  taxTreatment: string;
  /** What the till knew was on hand when the line was added. Display only. */
  onHand?: string;
}

export interface CartTotals {
  /** Sum of qty x unit price, before any discount. */
  gross: string;
  /** Sum of line discounts plus the invoice discount. */
  discount: string;
  /** What the customer pays, as the till believes it. */
  net: string;
  /** Units, not lines: two of one item is a quantity of two. */
  units: string;
  lineCount: number;
}

/** Rounds to the currency's places, half away from zero -- the shop's rule. */
function toMoney(value: Decimal, places: number): string {
  return value.toDecimalPlaces(places, Decimal.ROUND_HALF_UP).toFixed(places);
}

export function lineGross(line: CartLine): Decimal {
  return new Decimal(line.qty || '0').times(new Decimal(line.unitPrice || '0'));
}

export function lineNet(line: CartLine): Decimal {
  return lineGross(line).minus(new Decimal(line.lineDiscount || '0'));
}

/**
 * The running total.
 *
 * `invoiceDiscount` is the whole-sale discount a supervisor applies at the end;
 * it is not spread across the lines here, only subtracted, because the server
 * performs the allocation and the till has no reason to duplicate a result it
 * will be told.
 */
export function totalsFor(
  lines: readonly CartLine[],
  invoiceDiscount: string,
  places = 2,
): CartTotals {
  let gross = new Decimal(0);
  let lineDiscounts = new Decimal(0);
  let units = new Decimal(0);

  for (const line of lines) {
    gross = gross.plus(lineGross(line));
    lineDiscounts = lineDiscounts.plus(new Decimal(line.lineDiscount || '0'));
    units = units.plus(new Decimal(line.qty || '0'));
  }

  const invoice = new Decimal(invoiceDiscount || '0');
  const discount = lineDiscounts.plus(invoice);
  // Clamped at zero. A discount larger than the sale is a mistake somebody is
  // in the middle of typing, and showing a negative total mid-keystroke is
  // alarming in a way the eventual refusal is not.
  const net = Decimal.max(gross.minus(discount), new Decimal(0));

  return {
    gross: toMoney(gross, places),
    discount: toMoney(discount, places),
    net: toMoney(net, places),
    units: units.toDecimalPlaces(3).toString(),
    lineCount: lines.length,
  };
}

/** What has been tendered so far. */
export function tenderedTotal(
  tenders: readonly { amount: string }[],
  places = 2,
): string {
  let total = new Decimal(0);
  for (const t of tenders) total = total.plus(new Decimal(t.amount || '0'));
  return toMoney(total, places);
}

/**
 * What is still to pay, or what to hand back.
 *
 * Negative means change is due. Returned as a signed decimal string so the
 * screen decides how to say it -- "SAR 12.00 to pay" and "SAR 12.00 change" are
 * the same number and two very different sentences.
 */
export function balanceOf(
  net: string,
  tenders: readonly { amount: string }[],
  places = 2,
): string {
  return toMoney(
    new Decimal(net).minus(new Decimal(tenderedTotal(tenders, places))),
    places,
  );
}

/**
 * Adds a scanned item, merging into the line it is already on.
 *
 * Merging is what a cashier expects: scanning the same bottle three times is a
 * quantity of three, not three lines to read past. A line carrying a promotion
 * is NOT merged into, because the campaign was quoted for a specific quantity
 * and silently growing it would redeem something the server did not price.
 */
export function addScanned(
  lines: readonly CartLine[],
  scanned: Omit<CartLine, 'qty' | 'lineDiscount'> & { qty?: string },
): CartLine[] {
  const qty = scanned.qty ?? '1';
  const index = lines.findIndex(
    (l) => l.variantId === scanned.variantId && !l.promotionId,
  );

  if (index === -1) {
    return [...lines, { ...scanned, qty, lineDiscount: '0' }];
  }

  const next = [...lines];
  const existing = next[index];
  if (!existing) return next;
  next[index] = {
    ...existing,
    qty: new Decimal(existing.qty).plus(new Decimal(qty)).toString(),
  };
  return next;
}

export function setQty(
  lines: readonly CartLine[],
  variantId: string,
  qty: string,
): CartLine[] {
  return lines.map((l) => (l.variantId === variantId ? { ...l, qty } : l));
}

export function removeLine(
  lines: readonly CartLine[],
  variantId: string,
): CartLine[] {
  return lines.filter((l) => l.variantId !== variantId);
}

/** True when a quantity would take a line past what the shop holds. */
export function exceedsStock(line: CartLine): boolean {
  if (line.onHand === undefined) return false;
  return new Decimal(line.qty || '0').greaterThan(new Decimal(line.onHand));
}

/**
 * A sale identifier, minted on the till before any network call.
 *
 * This is what makes a retry after a lost response safe: the same sale carries
 * the same id, and the server recognises it rather than ringing it up twice.
 * It must be created when the cart starts, not when Pay is pressed, or a double
 * press produces two ids and two sales.
 */
export function newSaleId(): string {
  return crypto.randomUUID();
}
