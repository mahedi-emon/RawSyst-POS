// The catalogue cache: can a cashier start a sale with the network down?
//
// That is the whole question this milestone exists to answer, so the tests are
// written as it — not as "does the store round-trip a row".

import { describe, expect, it, vi } from 'vitest';

import {
  Catalogue,
  describeVariant,
  type CachedVariant,
  type CatalogueCursor,
  type CatalogueStore,
} from './catalogue';

/** An in-memory stand-in for SQLite, exactly as the queue tests do it. */
class MemoryStore implements CatalogueStore {
  rows = new Map<string, CachedVariant>();
  saved: CatalogueCursor | null = null;

  upsert(variants: CachedVariant[]): Promise<void> {
    for (const v of variants) this.rows.set(v.id, v);
    return Promise.resolve();
  }

  findByBarcode(barcode: string): Promise<CachedVariant | null> {
    for (const v of this.rows.values()) {
      if (v.barcode && v.barcode === barcode) return Promise.resolve(v);
    }
    return Promise.resolve(null);
  }

  search(term: string, limit: number): Promise<CachedVariant[]> {
    const hits = [...this.rows.values()].filter(
      (v) => v.isActive && (v.sku.includes(term) || v.name.includes(term)),
    );
    return Promise.resolve(hits.slice(0, limit));
  }

  cursor(): Promise<CatalogueCursor | null> {
    return Promise.resolve(this.saved);
  }

  setCursor(cursor: CatalogueCursor): Promise<void> {
    this.saved = cursor;
    return Promise.resolve();
  }

  count(): Promise<number> {
    return Promise.resolve(
      [...this.rows.values()].filter((v) => v.isActive).length,
    );
  }
}

function wireRow(n: number, over: Record<string, unknown> = {}) {
  return {
    id: `v${n}`,
    product_id: `p${n}`,
    sku: `SKU-${n}`,
    barcode: `600000000${n}`,
    name: `Item ${n}`,
    name_ar: '',
    attributes: '{"size":"M"}',
    price: '25.00',
    tax_treatment: 'standard',
    is_active: true,
    updated_at: `2026-08-15T10:0${n}:00+03:00`,
    ...over,
  };
}

/** A client that serves canned snapshot pages. */
function server(pages: Array<{ items: unknown[]; next?: [string, string] }>) {
  let call = 0;
  return {
    urls: [] as string[],
    calls: () => call,
    send<T>(_method: string, path: string): Promise<T> {
      const page = pages[call++];
      if (!page) throw new Error('the terminal asked for more pages than exist');
      this.urls.push(path);
      const body: Record<string, unknown> = { items: page.items };
      if (page.next) {
        body.next_since = page.next[0];
        body.next_since_id = page.next[1];
      }
      return Promise.resolve(body as T);
    },
  };
}

describe('scanning offline', () => {
  it('finds a cached barcode with no network at all', async () => {
    const store = new MemoryStore();
    // The client throws on any use — proving the lookup never reaches for it.
    const dead = {
      send: () => {
        throw new Error('the network was touched during a scan');
      },
    };
    const cat = new Catalogue(store, dead);

    await store.upsert([toCached(wireRow(1))]);
    const hit = await cat.lookup('6000000001');

    expect(hit?.sku).toBe('SKU-1');
    expect(hit?.price).toBe('25.00');
  });

  it('returns null for a barcode it has never been told about', async () => {
    const cat = new Catalogue(new MemoryStore(), server([]));
    expect(await cat.lookup('9999999999')).toBeNull();
  });

  it('tolerates the scanner sending trailing whitespace', async () => {
    const store = new MemoryStore();
    await store.upsert([toCached(wireRow(1))]);
    const cat = new Catalogue(store, server([]));

    // Some scanners append a carriage return with the Enter keystroke.
    expect(await cat.lookup('  6000000001\n')).not.toBeNull();
  });

  it('reports how many items can be sold without a network', async () => {
    const store = new MemoryStore();
    const cat = new Catalogue(store, server([]));

    await store.upsert([
      toCached(wireRow(1)),
      toCached(wireRow(2)),
      toCached(wireRow(3, { is_active: false })),
    ]);

    // The withdrawn one is cached but not sellable, so it is not counted.
    expect(await cat.size()).toBe(2);
  });
});

describe('pulling the delta', () => {
  it('downloads everything on a terminal that has never synced', async () => {
    const store = new MemoryStore();
    const api = server([{ items: [wireRow(1), wireRow(2)] }]);
    const cat = new Catalogue(store, api);

    expect(await cat.sync()).toBe(2);
    expect(store.rows.size).toBe(2);
    // No cursor sent on the first call: there was nothing to send.
    expect(api.urls[0]).not.toContain('since=');
  });

  it('sends the stored cursor so a later pull is only what changed', async () => {
    const store = new MemoryStore();
    store.saved = { since: '2026-08-15T10:01:00+03:00', sinceId: 'v1' };
    const api = server([{ items: [wireRow(2)] }]);

    await new Catalogue(store, api).sync();

    expect(api.urls[0]).toContain('since=2026-08-15T10%3A01%3A00%2B03%3A00');
    expect(api.urls[0]).toContain('since_id=v1');
  });

  it('follows the cursor across pages', async () => {
    const store = new MemoryStore();
    const full = Array.from({ length: 500 }, (_, i) => wireRow(i));
    const api = server([
      { items: full, next: ['2026-08-15T11:00:00+03:00', 'v499'] },
      { items: [wireRow(900)] },
    ]);

    const written = await new Catalogue(store, api).sync();

    expect(written).toBe(501);
    expect(api.calls()).toBe(2);
  });

  it('stops when the server has nothing new', async () => {
    const store = new MemoryStore();
    store.saved = { since: '2026-08-15T10:02:00+03:00', sinceId: 'v2' };
    const api = server([{ items: [] }]);

    expect(await new Catalogue(store, api).sync()).toBe(0);
    // The cursor is untouched — a caught-up till does not reset itself.
    expect(store.saved?.sinceId).toBe('v2');
  });

  it('advances the cursor only after the page is stored', async () => {
    const store = new MemoryStore();
    const order: string[] = [];
    vi.spyOn(store, 'upsert').mockImplementation(async () => {
      order.push('write');
    });
    vi.spyOn(store, 'setCursor').mockImplementation(async () => {
      order.push('cursor');
    });

    const api = server([
      { items: [wireRow(1)], next: ['2026-08-15T10:01:00+03:00', 'v1'] },
      { items: [] },
    ]);
    await new Catalogue(store, api).sync();

    // A crash between the two must re-download that page, never skip it. The
    // write is an upsert, so re-downloading costs nothing.
    expect(order).toEqual(['write', 'cursor']);
  });

  it('throws on a transport failure rather than reporting "up to date"', async () => {
    const dead = {
      send: () => Promise.reject(new Error('offline')),
    };
    // Zero written and no network are indistinguishable from the count alone,
    // and a till claiming its catalogue was current while unreachable would be
    // lying to the cashier.
    await expect(new Catalogue(new MemoryStore(), dead).sync()).rejects.toThrow();
  });
});

describe('withdrawn items', () => {
  it('caches an inactive variant instead of dropping it', async () => {
    const store = new MemoryStore();
    const api = server([{ items: [wireRow(1, { is_active: false })] }]);

    await new Catalogue(store, api).sync();

    const found = await store.findByBarcode('6000000001');
    // Kept, so the counter can say "withdrawn from sale". Omitting it would
    // leave whatever the till already held in place forever, and the cashier
    // would keep selling it.
    expect(found).not.toBeNull();
    expect(found?.isActive).toBe(false);
  });

  it('overwrites a previously active row when it is withdrawn', async () => {
    const store = new MemoryStore();
    await new Catalogue(store, server([{ items: [wireRow(1)] }])).sync();
    expect((await store.findByBarcode('6000000001'))?.isActive).toBe(true);

    await new Catalogue(
      store,
      server([{ items: [wireRow(1, { is_active: false })] }]),
    ).sync();

    expect((await store.findByBarcode('6000000001'))?.isActive).toBe(false);
    expect(store.rows.size).toBe(1);
  });
});

describe('the shape of what is cached', () => {
  it('never carries a cost price', async () => {
    const store = new MemoryStore();
    // A Cashier is denied catalog.view_cost_price, so a cache holding cost
    // would defeat the masking the permission exists to provide.
    await new Catalogue(store, server([{ items: [wireRow(1)] }])).sync();

    const cached = store.rows.get('v1');
    expect(Object.keys(cached ?? {})).not.toContain('cost');
    expect(JSON.stringify(cached)).not.toMatch(/cost/i);
  });

  it('keeps money as a string', async () => {
    const store = new MemoryStore();
    await new Catalogue(
      store,
      server([{ items: [wireRow(1, { price: '0.15' })] }]),
    ).sync();

    const price = store.rows.get('v1')?.price;
    expect(price).toBe('0.15');
    expect(typeof price).toBe('string');
  });

  it('survives a malformed attributes blob', async () => {
    const store = new MemoryStore();
    await new Catalogue(
      store,
      server([{ items: [wireRow(1, { attributes: 'not json' })] }]),
    ).sync();

    // The variant loses its size and colour, not its sellability.
    const cached = store.rows.get('v1');
    expect(cached?.attributes).toEqual({});
    expect(cached?.price).toBe('25.00');
  });

  it('builds a cart description a cashier can read', () => {
    const v = toCached(wireRow(1));
    expect(describeVariant(v)).toBe('Item 1 · M');

    expect(describeVariant({ ...v, name: '' })).toBe('M');
    expect(describeVariant({ ...v, name: '', attributes: {} })).toBe('SKU-1');
  });
});

/** Puts a wire row through the same conversion sync uses. */
function toCached(row: ReturnType<typeof wireRow>): CachedVariant {
  return {
    id: row.id,
    productId: row.product_id,
    sku: row.sku,
    barcode: row.barcode,
    name: row.name,
    nameAr: row.name_ar,
    attributes: JSON.parse(row.attributes) as Record<string, string>,
    price: row.price,
    taxTreatment: row.tax_treatment,
    isActive: row.is_active,
    updatedAt: row.updated_at,
  };
}
