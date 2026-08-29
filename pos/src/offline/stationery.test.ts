import { describe, expect, it, vi } from 'vitest';

import {
  FALLBACK_CLOSING,
  FALLBACK_STORE_NAME,
  Stationery,
  receiptStationery,
  type StationeryStore,
} from './stationery';
import type { CachedStationery } from './sqlite';
import { buildReceipt, renderReceipt, wrap } from '../pos/receipt';
import type { CartLine, CartTender, CartTotals } from '../pos/cart';

/** A terminal's local store, in memory. */
class MemoryStore implements StationeryStore {
  held: CachedStationery | null = null;
  writes = 0;

  async read(): Promise<CachedStationery | null> {
    return this.held;
  }

  async write(s: CachedStationery): Promise<void> {
    this.held = s;
    this.writes++;
  }
}

function payload(over: Record<string, unknown> = {}) {
  return {
    store_name: 'Olaya Trading',
    vat_number: '311111111111113',
    header_text: 'Olaya Branch, King Fahd Road',
    header_text_ar: 'فرع العليا',
    footer_text: 'See you again soon.',
    footer_text_ar: '',
    return_policy: 'Unworn items may be returned within 14 days with this receipt.',
    return_policy_ar: '',
    show_tax_number: true,
    ...over,
  };
}

/** A client that answers the stationery route, or refuses. */
function stubClient(answer: unknown) {
  return {
    send: vi.fn(async () => {
      if (answer instanceof Error) throw answer;
      return answer;
    }),
  } as never;
}

describe('the till holding the shop stationery', () => {
  it('starts with nothing and reads the RawSyst default', async () => {
    // A terminal that has never been online. Not an error: it prints on the
    // default, which is what it should do.
    const store = new MemoryStore();
    const s = new Stationery(store);
    await s.load();

    expect(s.current()).toBeNull();
    expect(receiptStationery(s.current())).toEqual({
      storeName: FALLBACK_STORE_NAME,
      vatNumber: '',
      // Empty rather than a guess: a till that has never been online does not
      // know which of the three currencies this shop keeps its books in, and
      // printing the wrong code is worse than printing none.
      baseCurrency: '',
      addressLines: [],
      returnPolicy: '',
      closing: FALLBACK_CLOSING,
    });
  });

  it('pulls the shop words and holds them', async () => {
    const store = new MemoryStore();
    const s = new Stationery(store);
    await s.refresh(stubClient(payload()));

    expect(store.writes).toBe(1);
    expect(s.current()?.storeName).toBe('Olaya Trading');
    expect(store.held?.fetchedAt).toBeTruthy();
  });

  it('survives a restart with no network', async () => {
    // The path that matters: pulled while online, read back after the terminal
    // was closed and reopened with the connection gone.
    const store = new MemoryStore();
    await new Stationery(store).refresh(stubClient(payload()));

    const afterRestart = new Stationery(store);
    await afterRestart.load();

    expect(receiptStationery(afterRestart.current()).storeName).toBe('Olaya Trading');
    expect(receiptStationery(afterRestart.current()).returnPolicy).toContain('14 days');
  });

  it('keeps what it holds when a refresh fails', async () => {
    // A till that loses the network mid-shift must keep printing the shop's
    // words rather than reverting to the default in front of a queue.
    const store = new MemoryStore();
    const s = new Stationery(store);
    await s.refresh(stubClient(payload()));

    await expect(
      s.refresh(stubClient(new Error('the till cannot reach the server'))),
    ).rejects.toThrow();

    expect(s.current()?.storeName).toBe('Olaya Trading');
    expect(store.writes).toBe(1);
  });

  it('never leaves a receipt with no seller on it', async () => {
    // A receipt with no name at the top is not a document.
    const s = new Stationery(new MemoryStore());
    await s.refresh(stubClient(payload({ store_name: '' })));
    expect(receiptStationery(s.current()).storeName).toBe(FALLBACK_STORE_NAME);
  });
});

describe('turning what is held into what is printed', () => {
  const held = (over: Partial<CachedStationery> = {}): CachedStationery => ({
    storeName: 'Olaya Trading',
    vatNumber: '311111111111113',
    baseCurrency: 'SAR',
    headerText: 'Olaya Branch',
    headerTextAr: 'فرع العليا',
    footerText: 'See you again soon.',
    footerTextAr: '',
    returnPolicy: 'Return within 14 days.',
    returnPolicyAr: '',
    showTaxNumber: true,
    fetchedAt: '2026-08-22T09:00:00Z',
    ...over,
  });

  it('prints both languages of the header, each on its own line', () => {
    // A Saudi shop writes twice and the customer reads whichever they read.
    expect(receiptStationery(held()).addressLines).toEqual([
      'Olaya Branch',
      'فرع العليا',
    ]);
  });

  it('drops a language the shop left empty', () => {
    expect(receiptStationery(held({ headerTextAr: '' })).addressLines).toEqual([
      'Olaya Branch',
    ]);
    expect(receiptStationery(held({ headerText: '', headerTextAr: '' })).addressLines)
      .toEqual([]);
  });

  it('honours the template rather than whether a number happens to exist', () => {
    // A business between registrations should not have its number appear and
    // disappear on its own.
    expect(receiptStationery(held({ showTaxNumber: false })).vatNumber).toBe('');
    expect(receiptStationery(held({ showTaxNumber: true })).vatNumber)
      .toBe('311111111111113');
  });

  it('keeps the line the receipt has always ended with when none was written', () => {
    const words = receiptStationery(held({ footerText: '', footerTextAr: '' }));
    expect(words.closing).toBe(FALLBACK_CLOSING);
  });

  it('joins a policy written in both languages', () => {
    const words = receiptStationery(
      held({ returnPolicy: 'Return within 14 days.', returnPolicyAr: 'الإرجاع خلال ١٤ يوما.' }),
    );
    expect(words.returnPolicy).toContain('14 days');
    expect(words.returnPolicy).toContain('١٤');
  });
});

describe('printing it on 42 columns', () => {
  const lines: CartLine[] = [
    {
      variantId: 'v1',
      description: 'Executive Abaya',
      qty: '1',
      unitPrice: '449.00',
      lineDiscount: '0',
      taxTreatment: 'standard',
    } as CartLine,
  ];
  const totals: CartTotals = {
    subtotalNet: '449.00',
    taxTotal: '67.35',
    totalInclusive: '516.35',
  } as CartTotals;
  const tenders: CartTender[] = [{ method: 'cash', amount: '516.35' } as CartTender];

  function print(header: ReturnType<typeof receiptStationery>) {
    return renderReceipt(
      buildReceipt({
        header,
        reference: 'ab12cd34',
        issuedAt: '2026-08-22T09:15:00Z',
        cashier: 'fatima',
        lines,
        totals,
        tenders,
        provisional: true,
      }),
    );
  }

  it('heads the receipt with the shop, not with RawSyst', () => {
    const out = print(
      receiptStationery({
        storeName: 'Olaya Trading',
        baseCurrency: 'SAR',
        vatNumber: '311111111111113',
        headerText: 'King Fahd Road',
        headerTextAr: '',
        footerText: 'See you soon.',
        footerTextAr: '',
        returnPolicy: 'Return within 14 days.',
        returnPolicyAr: '',
        showTaxNumber: true,
        fetchedAt: 'now',
      }),
    );

    expect(out).toContain('OLAYA TRADING');
    expect(out).toContain('King Fahd Road');
    expect(out).toContain('VAT 311111111111113');
    expect(out).toContain('Return within 14 days.');
    expect(out).toContain('See you soon.');
    // Every line still fits the roll. A receipt is plain text on 42 columns so
    // it prints on every counter printer, and that is not negotiable.
    for (const line of out.split('\n')) {
      expect(line.length).toBeLessThanOrEqual(42);
    }
  });

  it('still says what the document is', () => {
    // The honest statement is not a template block and a shop cannot write over
    // it. A receipt is not a tax invoice until the signing gate is resolved.
    const out = print(receiptStationery(null));
    expect(out).toContain('This is not a tax invoice.');
  });

  it('prints the RawSyst default on a till that has never been online', () => {
    const out = print(receiptStationery(null));
    expect(out).toContain('RAWSYST');
    expect(out).toContain('Thank you');
    // No VAT REGISTRATION line invented for a shop the till knows nothing
    // about. The totals block is labelled "VAT" too, so this looks for the
    // header's line — a centred "VAT <number>" and nothing else on it — rather
    // than for the word anywhere.
    expect(out).not.toMatch(/^\s*VAT \d+\s*$/m);
  });
});

describe('fitting a policy to the roll', () => {
  it('breaks a long sentence at word boundaries', () => {
    const out = wrap(
      'Unworn items may be returned within fourteen days provided the receipt is presented.',
      42,
    );
    for (const line of out) expect(line.length).toBeLessThanOrEqual(42);
    expect(out.join(' ')).toContain('fourteen days');
  });

  it('keeps line breaks a shop wrote on purpose', () => {
    // Three short rules on three lines means three lines.
    expect(wrap('No refunds on sale items.\nExchange within 7 days.', 42)).toEqual([
      'No refunds on sale items.',
      'Exchange within 7 days.',
    ]);
  });

  it('breaks a single word longer than the roll rather than letting it be cut', () => {
    // A URL, usually. Truncation loses the end of it silently.
    const out = wrap('https://example.test/returns-policy-for-this-shop-and-others', 42);
    for (const line of out) expect(line.length).toBeLessThanOrEqual(42);
    expect(out.join('')).toContain('returns-policy');
  });

  it('copes with nothing', () => {
    expect(wrap('', 42)).toEqual(['']);
  });
});
