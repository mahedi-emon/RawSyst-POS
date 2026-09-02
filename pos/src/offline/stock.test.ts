import { describe, expect, it } from 'vitest';

import { addDecimals, type CachedStock } from './sqlite';
import { StockCache } from './stock';

// The till's stock cache, and the three things it must never do.
//
//   - It must never drift from the ledger it is caching, which is what the
//     decimal arithmetic is for.
//   - It must never invent a figure it was not told.
//   - It must never refuse a sale.

// A stand-in for the SQLite store. The real one is exercised by the till; what
// is worth testing here is the CACHE's rules, and a fake keeps them visible.
class FakeStore {
  rows = new Map<string, CachedStock>();

  private key(variantId: string, warehouseId: string) {
    return `${variantId}@${warehouseId}`;
  }

  async everything(): Promise<CachedStock[]> {
    return [...this.rows.values()];
  }

  async all(warehouseId: string): Promise<CachedStock[]> {
    return [...this.rows.values()].filter((r) => r.warehouseId === warehouseId);
  }

  async get(variantId: string, warehouseId: string) {
    return this.rows.get(this.key(variantId, warehouseId)) ?? null;
  }

  async put(lines: CachedStock[]) {
    for (const l of lines)
      this.rows.set(this.key(l.variantId, l.warehouseId), l);
  }

  async applyDelta(
    variantId: string,
    warehouseId: string,
    delta: string,
    at: string,
  ) {
    const held = this.rows.get(this.key(variantId, warehouseId));
    if (!held) return false;
    this.rows.set(this.key(variantId, warehouseId), {
      ...held,
      onHand: addDecimals(held.onHand, delta),
      asOf: at,
    });
    return true;
  }

  async clear() {
    this.rows.clear();
  }
}

function cacheWith(rows: CachedStock[]) {
  const store = new FakeStore();
  for (const r of rows) store.rows.set(`${r.variantId}@${r.warehouseId}`, r);
  // The fake satisfies the shape the cache actually calls.
  const cache = new StockCache(
    store as unknown as ConstructorParameters<typeof StockCache>[0],
  );
  return { cache, store };
}

const line = (over: Partial<CachedStock> = {}): CachedStock => ({
  variantId: 'v1',
  warehouseId: 'w1',
  onHand: '10',
  reorderLevel: '',
  asOf: '2026-09-02T09:00:00Z',
  ...over,
});

describe('adding decimal quantities', () => {
  // The reason this exists rather than `a + b`: a till accumulating deltas in
  // a float64 drifts away from the ledger it is caching, slowly and invisibly.
  it('does not drift the way a float would', () => {
    expect(addDecimals('0.1', '0.2')).toBe('0.3');
    expect(0.1 + 0.2).not.toBe(0.3);
  });

  it('handles the signs a sale and a return produce', () => {
    expect(addDecimals('10', '-1')).toBe('9');
    expect(addDecimals('0', '-1')).toBe('-1');
    expect(addDecimals('-1', '1')).toBe('0');
    expect(addDecimals('9.5', '0.5')).toBe('10');
  });

  it('keeps differing decimal places exact', () => {
    expect(addDecimals('1.005', '2.1')).toBe('3.105');
    expect(addDecimals('100', '0.001')).toBe('100.001');
  });

  it('treats nonsense as nothing rather than NaN', () => {
    // A malformed delta must not turn a real quantity into "NaN" on a screen.
    expect(addDecimals('10', 'oops')).toBe('10');
    expect(addDecimals('', '5')).toBe('5');
  });
});

describe('applying what the server announced', () => {
  it('moves the quantity it holds', async () => {
    const { cache, store } = cacheWith([line({ onHand: '10' })]);
    await cache.hydrate();

    await cache.apply('v1', 'w1', '-3');

    expect(cache.onHand('v1')?.onHand).toBe('7');
    expect(store.rows.get('v1@w1')?.onHand).toBe('7');
  });

  it('puts stock back when a return announces a positive delta', async () => {
    const { cache } = cacheWith([line({ onHand: '7' })]);
    await cache.hydrate();

    await cache.apply('v1', 'w1', '1');

    expect(cache.onHand('v1')?.onHand).toBe('8');
  });

  // A delta says how much a quantity MOVED, not what it became. Applying one to
  // a variant this till never pulled would invent a figure from nothing, and an
  // invented quantity is worse than an absent one because a screen shows it
  // just as confidently.
  it('refuses to invent a row it was never given', async () => {
    const { cache, store } = cacheWith([]);
    await cache.hydrate();

    await cache.apply('never-pulled', 'w1', '-3');

    expect(cache.onHand('never-pulled')).toBeNull();
    expect(store.rows.size).toBe(0);
  });

  it('ignores another warehouse', async () => {
    const { cache } = cacheWith([line({ onHand: '10' })]);
    await cache.hydrate();

    await cache.apply('v1', 'somewhere-else', '-3');

    expect(cache.onHand('v1')?.onHand).toBe('10');
  });
});

describe('what to tell the cashier', () => {
  it('says nothing about a variant it has no opinion on', async () => {
    const { cache } = cacheWith([]);
    await cache.hydrate();
    expect(cache.shortfall('v1', '1')).toBeNull();
  });

  it('says nothing when there is plenty', async () => {
    const { cache } = cacheWith([line({ onHand: '10' })]);
    await cache.hydrate();
    expect(cache.shortfall('v1', '1')).toBeNull();
  });

  it('names the case where the shelf is empty', async () => {
    const { cache } = cacheWith([line({ onHand: '0' })]);
    await cache.hydrate();

    const warning = cache.shortfall('v1', '1');
    expect(warning?.kind).toBe('none-left');
    // Dated, so the screen can say how old the figure is rather than present a
    // stale guess as a fact.
    expect(warning?.asOf).toBe('2026-09-02T09:00:00Z');
  });

  it('names the case where there is some but not enough', async () => {
    const { cache } = cacheWith([line({ onHand: '2' })]);
    await cache.hydrate();
    expect(cache.shortfall('v1', '5')?.kind).toBe('not-enough');
  });

  it('names a sale that would take it below the reorder point', async () => {
    const { cache } = cacheWith([line({ onHand: '10', reorderLevel: '8' })]);
    await cache.hydrate();
    expect(cache.shortfall('v1', '3')?.kind).toBe('below-reorder');
  });

  it('does not mention a reorder point the shop has not set', async () => {
    const { cache } = cacheWith([line({ onHand: '10', reorderLevel: '' })]);
    await cache.hydrate();
    expect(cache.shortfall('v1', '3')).toBeNull();
  });
});

// The property the whole design rests on. Design 03 chooses accurate detection
// over false confidence: an offline till CANNOT prevent overselling, and a
// cached figure that refuses a real customer at the counter is exactly the
// false confidence that rules out.
//
// So this checks the shape of the API rather than a behaviour: there is nothing
// on the cache that returns a verdict. `shortfall` returns something to SAY.
describe('the cache never refuses a sale', () => {
  it('offers no method that could block one', async () => {
    const { cache } = cacheWith([line({ onHand: '0' })]);
    await cache.hydrate();

    const surface = Object.getOwnPropertyNames(
      Object.getPrototypeOf(cache),
    ).filter((n) => n !== 'constructor');

    for (const forbidden of ['canSell', 'allow', 'blocked', 'available']) {
      expect(surface).not.toContain(forbidden);
    }

    // And the one thing it does say about an empty shelf is advisory: a
    // description, with no verdict attached.
    const warning = cache.shortfall('v1', '1');
    expect(warning).not.toBeNull();
    expect(Object.keys(warning ?? {}).sort()).toEqual([
      'asOf',
      'kind',
      'onHand',
    ]);
  });
});

describe('how the cache stands', () => {
  it('reports itself unloaded before anything was pulled', async () => {
    const { cache } = cacheWith([]);
    await cache.hydrate();

    const state = cache.state();
    expect(state.loaded).toBe(false);
    expect(state.count).toBe(0);
    expect(state.asOf).toBeNull();
  });

  it('reports the newest agreement it holds', async () => {
    const { cache } = cacheWith([
      line({ variantId: 'v1', asOf: '2026-09-01T09:00:00Z' }),
      line({ variantId: 'v2', asOf: '2026-09-02T14:30:00Z' }),
    ]);
    await cache.hydrate();

    const state = cache.state();
    expect(state.loaded).toBe(true);
    expect(state.count).toBe(2);
    expect(state.asOf).toBe('2026-09-02T14:30:00Z');
  });
});
