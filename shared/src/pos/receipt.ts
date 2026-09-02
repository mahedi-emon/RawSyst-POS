// The piece of paper the customer takes away.
//
// # What it may and may not claim
//
// A simplified tax invoice under ZATCA Phase 2 carries a QR code containing a
// TLV payload derived from the signed document. This terminal cannot sign yet
// — the P1 verification gate is open, and the canonicalisation and TLV field
// encoding are unverified — so there is no QR to print and none is invented.
//
// The receipt therefore prints as a SALES RECEIPT and says, in words, that the
// tax invoice follows. That is the honest description of what it is: the sale
// is real, the money is real, the stock and the journal are real, and the only
// outstanding part is the regulatory document. Printing a box labelled "ZATCA
// QR" with a placeholder inside would be worse than printing nothing, because
// a customer and an inspector would both read it as a compliant invoice.
//
// Nothing printed here claims certification or guaranteed compliance in any
// form. The software SUPPORTS the requirements; it never warrants them, and
// `make lint-wording` enforces that across the repository — including on this
// file, which is why the banned phrasings are described rather than quoted.
//
// # It is rendered from what the terminal recorded
//
// Not from a server response, because there may not have been one — a sale
// finished offline must still produce a receipt, immediately, at the counter.
// The figures shown are the terminal's own, computed by `totalCart`, which is
// why they are marked as provisional where the server may yet disagree.

import type { Key, Translate } from '@rawsyst/shared/i18n/strings';

import type { CartLine, CartTender } from './cart';
import type { CartTotals } from './cart';

export interface ReceiptHeader {
  /** The code the shop keeps its books in — SAR, BDT, USD. Printed beside
   *  every amount. Empty on a till that has never reached the server, which
   *  prints the figure alone rather than a currency it is guessing at.
   *
   *  Named as the stationery names it, because the stationery is handed to
   *  this whole as the header and a second name for one fact is a mapping
   *  somebody has to remember. */
  baseCurrency?: string;
  /** The trading name as the customer knows it. */
  storeName: string;
  /** Printed because a simplified tax invoice must carry the seller's VAT
   *  registration number. Supplied by the server at device registration; the
   *  till does not compose or validate it. */
  vatNumber: string;
  addressLines: string[];

  /** What the shop wrote for its returns policy (I2). Blank when they have
   *  written none, which is the ordinary state and prints nothing. */
  returnPolicy?: string;
  /** The last line on the page. Defaults to the line the receipt has always
   *  ended with, so a shop that writes nothing gets what it had before. */
  closing?: string;
}

export interface Receipt {
  header: ReceiptHeader;
  /** The terminal's own id for the sale, printed so a customer returning goods
   *  can be matched to it before the invoice number exists. */
  reference: string;
  issuedAt: string;
  cashier: string;
  lines: ReceiptLine[];
  totals: CartTotals;
  tenders: CartTender[];
  change: string;
  /** True when the sale has not yet been acknowledged by the server. */
  provisional: boolean;
}

export interface ReceiptLine {
  description: string;
  qty: string;
  unitPrice: string;
  lineTotal: string;
}

/** Builds the receipt from what the terminal recorded. */
export function buildReceipt(input: {
  header: ReceiptHeader;
  reference: string;
  issuedAt: string;
  cashier: string;
  lines: CartLine[];
  totals: CartTotals;
  tenders: CartTender[];
  provisional: boolean;
}): Receipt {
  return {
    header: input.header,
    reference: input.reference,
    issuedAt: input.issuedAt,
    cashier: input.cashier,
    lines: input.lines.map((l) => ({
      description: l.description,
      qty: l.qty,
      unitPrice: l.unitPrice,
      lineTotal: lineTotal(l),
    })),
    totals: input.totals,
    tenders: input.tenders,
    change: changeDue(input.totals.totalInclusive, input.tenders),
    provisional: input.provisional,
  };
}

/**
 * What one line comes to, VAT included.
 *
 * The receipt's own arithmetic, deliberately kept simple: quantity times price
 * less the line discount. It is NOT the figure the invoice will carry — the
 * server recomputes every line, allocates the invoice-level discount with the
 * rounding remainder, and may resolve a different VAT rate from the registry.
 * A receipt printed at the counter shows the customer what they were charged;
 * the tax invoice that follows is the authority.
 */
function lineTotal(line: CartLine): string {
  const qty = Number(line.qty);
  if (!Number.isFinite(qty)) return '0.00';
  const gross = Math.round(minor(line.unitPrice) * qty);
  return major(Math.max(0, gross - minor(line.lineDiscount || '0')));
}

/**
 * Change owed to the customer.
 *
 * String arithmetic on the minor unit, not floats: a receipt that says 0.1 +
 * 0.2 is 0.30000000000000004 has failed at the one job it has. Two decimal
 * places throughout, which holds for SAR, BDT and USD alike; a currency with
 * a different minor unit would need this revisited, and none of the three
 * target markets has one.
 */
export function changeDue(total: string, tenders: CartTender[]): string {
  const paid = tenders.reduce((sum, t) => sum + minor(t.amount), 0);
  const owed = paid - minor(total);
  return owed > 0 ? major(owed) : '0.00';
}

function minor(amount: string): number {
  const negative = amount.trimStart().startsWith('-');
  const [whole = '0', frac = ''] = amount.replace('-', '').split('.');
  const cents = Number(whole) * 100 + Number((frac + '00').slice(0, 2));
  return negative ? -cents : cents;
}

function major(cents: number): string {
  const sign = cents < 0 ? '-' : '';
  const abs = Math.abs(cents);
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`;
}

/**
 * The receipt as plain text, for a thermal printer.
 *
 * 42 columns, the width of the 80mm roll almost every counter printer in these
 * markets uses. Plain text rather than ESC/POS: the escape sequences differ by
 * manufacturer, and a text receipt prints correctly on all of them and can be
 * read on screen when no printer is attached.
 */
export function renderReceipt(
  receipt: Receipt,
  width = 42,
  // The language the customer reads. A parameter rather than a hook: this
  // builds a string for a thermal printer and is called from a test, from the
  // counter, and from a reprint. English when it is absent, which is what a
  // caller with no locale should get rather than a blank line on the paper.
  translate?: Translate,
): string {
  const say = (key: Key, fallback: string) =>
    translate ? translate(key) : fallback;

  const out: string[] = [];
  const rule = '-'.repeat(width);

  out.push(centre(receipt.header.storeName.toUpperCase(), width));
  for (const line of receipt.header.addressLines) out.push(centre(line, width));
  if (receipt.header.vatNumber) {
    out.push(centre(`VAT ${receipt.header.vatNumber}`, width));
  }
  out.push('');
  out.push(centre(say('receipt.salesReceipt', 'SALES RECEIPT'), width));
  out.push('');
  out.push(`Ref    ${receipt.reference}`);
  out.push(`Date   ${receipt.issuedAt}`);
  out.push(`Served ${receipt.cashier}`);
  out.push(rule);

  for (const line of receipt.lines) {
    out.push(line.description.slice(0, width));
    out.push(columns(`  ${line.qty} x ${line.unitPrice}`, line.lineTotal, width));
  }

  out.push(rule);

  // Every amount on the paper carries the code the books are kept in.
  //
  // It carried none: a receipt read "TOTAL 172.50", which is legible only to
  // somebody who already knows which country the till is in. This product is
  // sold into three currencies, and a Bangladeshi shop's receipt saying the
  // same thing as a Saudi one is a document that cannot be filed.
  //
  // Only the code is added — the figure is the string the server computed and
  // is not re-formatted here. Empty on a till that has never been online,
  // which prints the bare amount rather than a currency it is guessing at.
  const ccy = (amount: string) =>
    receipt.header.baseCurrency
      ? `${receipt.header.baseCurrency} ${amount}`
      : amount;

  out.push(columns(say('receipt.subtotal', 'Subtotal'), ccy(receipt.totals.subtotalNet), width));
  out.push(columns(say('receipt.vat', 'VAT'), ccy(receipt.totals.taxTotal), width));
  out.push(
    columns(say('receipt.total', 'TOTAL'), ccy(receipt.totals.totalInclusive), width),
  );
  out.push('');

  for (const tender of receipt.tenders) {
    out.push(columns(label(tender.method), ccy(tender.amount), width));
  }
  if (receipt.change !== '0.00') {
    out.push(columns(say('receipt.change', 'Change'), ccy(receipt.change), width));
  }

  out.push(rule);

  // The honest statement, always. Not a placeholder QR, not a claim of
  // compliance — what this document is, and what is still coming.
  out.push(centre(say('receipt.notATaxInvoice', 'This is not a tax invoice.'), width));
  out.push(
    centre(say('receipt.willBeIssued', 'Your tax invoice will be issued'), width),
  );
  out.push(centre(say('receipt.andSentSeparately', 'and sent separately.'), width));

  if (receipt.provisional) {
    out.push('');
    out.push(
      centre(say('receipt.recordedHere', 'Recorded on this terminal.'), width),
    );
  }

  // What the shop wrote, if anything (I2). After the honest statement above
  // rather than before it: that statement is about what this document IS, and
  // burying it under a returns policy would be the wrong order.
  if (receipt.header.returnPolicy) {
    out.push('');
    for (const line of wrap(receipt.header.returnPolicy, width)) {
      out.push(centre(line, width));
    }
  }

  out.push('');
  out.push(
    centre(
      receipt.header.closing || say('receipt.thankYou', 'Thank you'),
      width,
    ),
  );
  return out.join('\n');
}

/**
 * Breaks a block onto lines that fit the roll.
 *
 * A returns policy is written in a box on a wide screen and printed on 42
 * columns of paper. Without this the last two thirds of every sentence would be
 * cut off, which is worse than not printing it at all — a policy a customer
 * cannot read is one they will argue was never given.
 *
 * Line breaks a shop wrote are kept: three short rules on three lines means
 * three lines.
 */
export function wrap(text: string, width: number): string[] {
  const out: string[] = [];

  for (const paragraph of text.split('\n')) {
    const words = paragraph.trim().split(/\s+/).filter((w) => w !== '');
    if (words.length === 0) {
      out.push('');
      continue;
    }

    let line = '';
    for (const word of words) {
      if (line === '') {
        line = word;
      } else if (line.length + 1 + word.length <= width) {
        line += ' ' + word;
      } else {
        out.push(line);
        line = word;
      }
    }
    if (line !== '') out.push(line);
  }

  // A single word longer than the roll — a URL, usually — is broken rather
  // than left for the printer to truncate, which loses its end silently.
  return out.flatMap((line) =>
    line.length <= width
      ? [line]
      : (line.match(new RegExp(`.{1,${width}}`, 'g')) ?? [line]),
  );
}

function label(method: string): string {
  return method === 'mada' ? 'Mada' : method.charAt(0).toUpperCase() + method.slice(1);
}

function centre(text: string, width: number): string {
  const t = text.slice(0, width);
  const pad = Math.max(0, Math.floor((width - t.length) / 2));
  return ' '.repeat(pad) + t;
}

/** Left label, right-aligned amount, the layout every receipt uses. */
function columns(left: string, right: string, width: number): string {
  const l = left.slice(0, width - right.length - 1);
  return l + ' '.repeat(Math.max(1, width - l.length - right.length)) + right;
}
