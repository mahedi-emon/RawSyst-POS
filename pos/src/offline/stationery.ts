// The shop's own words, held on the terminal.
//
// The last of I2. The Back Office writes the header, the returns policy and the
// closing line (P35); the invoice screen renders them; and this is how they
// reach the document a customer actually walks out with — which is printed at
// the counter, often with no network, and cannot wait for a round trip.
//
// # A cache, and a fallback that is never wrong
//
// A till that has never been online prints on the RawSyst default. A till that
// has been online prints what the shop last said. Neither state is an error and
// neither blocks a sale: a receipt is not a legal document here — it says so on
// its own face — so stale wording is a cosmetic staleness, not an accounting
// one. That is the whole reason the terminal is allowed to hold this at all.
//
// # No logo
//
// `receipt.ts` is 42 columns of plain text, chosen so it prints on every
// counter printer rather than only the ones whose ESC/POS dialect we guessed
// right. Text cannot hold an image, so the logo is not fetched, not cached and
// not silently dropped somewhere the reader would have to go looking for it.

import type { Client } from '@rawsyst/shared/api/client';
import type { CachedStationery } from './sqlite';

/** What the till's own stationery route returns. */
interface StationeryPayload {
  base_currency?: string;
  store_name: string;
  vat_number: string;
  header_text: string;
  header_text_ar: string;
  footer_text: string;
  footer_text_ar: string;
  return_policy: string;
  return_policy_ar: string;
  show_tax_number: boolean;
}

/** Where the till reads and writes what it holds. */
export interface StationeryStore {
  read(): Promise<CachedStationery | null>;
  write(s: CachedStationery): Promise<void>;
}

/** The RawSyst fallback: what a receipt says before a client has said anything.
 *
 *  Deliberately not blank. A receipt with no seller on it is not a document,
 *  and the name is the one thing that must always be there. */
export const FALLBACK_STORE_NAME = 'RawSyst';

/** The closing line a receipt has always ended with. Kept as the default so a
 *  shop that writes nothing gets what they had before this existed. */
export const FALLBACK_CLOSING = 'Thank you';

export class Stationery {
  private held: CachedStationery | null = null;

  constructor(private readonly store: StationeryStore) {}

  /** Loads what the terminal is holding. Called once at startup, before any
   *  network is attempted, so the first receipt of the day prints correctly on
   *  a till that opened offline. */
  async load(): Promise<void> {
    this.held = await this.store.read();
  }

  /** What the till currently holds, or null if it has never fetched. */
  current(): CachedStationery | null {
    return this.held;
  }

  /**
   * Pulls the shop's stationery and stores it.
   *
   * A failure leaves what was already held. That is the important half: a till
   * that loses the network mid-shift keeps printing the shop's words rather
   * than reverting to the default, and a till whose company has never
   * configured anything keeps printing the default rather than nothing.
   */
  async refresh(client: Client): Promise<void> {
    const payload = await client.send<StationeryPayload>(
      'GET',
      '/api/v1/pos/stationery',
    );

    const next: CachedStationery = {
      storeName: payload.store_name || FALLBACK_STORE_NAME,
      vatNumber: payload.vat_number ?? '',
      baseCurrency: payload.base_currency ?? '',
      headerText: payload.header_text ?? '',
      headerTextAr: payload.header_text_ar ?? '',
      footerText: payload.footer_text ?? '',
      footerTextAr: payload.footer_text_ar ?? '',
      returnPolicy: payload.return_policy ?? '',
      returnPolicyAr: payload.return_policy_ar ?? '',
      showTaxNumber: payload.show_tax_number !== false,
      fetchedAt: new Date().toISOString(),
    };

    await this.store.write(next);
    this.held = next;
  }
}

/** The header block a receipt is printed with.
 *
 *  Mirrors what `buildReceipt` already takes, so the receipt builder did not
 *  have to learn about templates to be given one. */
export interface ReceiptStationery {
  storeName: string;
  vatNumber: string;
  /** The code the shop keeps its books in. Printed beside every amount, and
   *  shown beside every amount on the counter — a total is not a number, it is
   *  an amount of something, and this product sells into three currencies. */
  baseCurrency: string;
  addressLines: string[];
  returnPolicy: string;
  closing: string;
}

/**
 * Turns what the till holds into what the receipt prints.
 *
 * The fallback is applied here rather than in the renderer, so there is exactly
 * one place that decides what a receipt says when a shop has said nothing —
 * and so that place is a pure function a test can pin.
 *
 * Both languages print where both were written. A Saudi shop writes its policy
 * twice and a customer reads whichever they read; printing only one would make
 * the choice for them, and the receipt has the room.
 */
export function receiptStationery(
  held: CachedStationery | null,
): ReceiptStationery {
  if (!held) {
    return {
      storeName: FALLBACK_STORE_NAME,
      vatNumber: '',
      // Empty rather than a guess. A till that has never been online does not
      // know what country it is in, and printing the wrong code on a receipt
      // is worse than printing none.
      baseCurrency: '',
      addressLines: [],
      returnPolicy: '',
      closing: FALLBACK_CLOSING,
    };
  }

  return {
    storeName: held.storeName || FALLBACK_STORE_NAME,
    baseCurrency: held.baseCurrency ?? '',
    // The template decides whether it prints, not whether the shop happens to
    // have one: a business between registrations should not have its number
    // appear and disappear on its own.
    vatNumber: held.showTaxNumber ? held.vatNumber : '',
    addressLines: bothLanguages(held.headerText, held.headerTextAr),
    returnPolicy: joinLanguages(held.returnPolicy, held.returnPolicyAr),
    // A shop that wrote a closing line gets theirs; one that did not keeps the
    // line the receipt has always ended with.
    closing: firstWritten(held.footerText, held.footerTextAr) || FALLBACK_CLOSING,
  };
}

/** Header lines, each language on its own line, blanks dropped. */
function bothLanguages(en: string, ar: string): string[] {
  return [en, ar]
    .flatMap((block) => block.split('\n'))
    .map((line) => line.trim())
    .filter((line) => line !== '');
}

/** Two blocks as one, separated by a blank line when both were written. */
function joinLanguages(en: string, ar: string): string {
  return [en.trim(), ar.trim()].filter((b) => b !== '').join('\n\n');
}

function firstWritten(...blocks: string[]): string {
  for (const b of blocks) {
    if (b.trim() !== '') return b.trim();
  }
  return '';
}
