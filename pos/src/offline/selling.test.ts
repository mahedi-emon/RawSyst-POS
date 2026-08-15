// The guarantee that outranks everything else here: a cashier can always
// finish a sale.
//
// The connectivity monitor, the catalogue pull and the flush all run on the
// same terminal as the selling, and every one of them makes network calls that
// can hang for their full timeout. None of them may sit between a cashier
// pressing Finish and the sale being durable. The individual unit tests check
// each piece; this file composes them and puts a sale through while they are
// all stuck, because that is the arrangement that actually ships.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ConnectivityMonitor, type Prober } from './connectivity';
import { Catalogue, type CachedVariant, type CatalogueCursor, type CatalogueStore } from './catalogue';
import { SaleQueue, type OfflineSalePayload, type QueueCounts, type QueuedSale, type QueueStore, type SettledState } from './queue';

class MemoryQueueStore implements QueueStore {
  rows: QueuedSale[] = [];

  nextSeq() {
    return Promise.resolve(
      this.rows.reduce((m, r) => Math.max(m, r.seq), 0) + 1,
    );
  }
  put(entry: QueuedSale) {
    this.rows.push(entry);
    return Promise.resolve();
  }
  outstanding(limit: number) {
    return Promise.resolve(
      this.rows.filter((r) => r.state === 'pending').slice(0, limit),
    );
  }
  markSettled(seqs: number[], state: SettledState) {
    for (const r of this.rows) if (seqs.includes(r.seq)) r.state = state;
    return Promise.resolve();
  }
  markFailed(seq: number, reason: string) {
    for (const r of this.rows) {
      if (r.seq === seq) {
        r.state = 'failed';
        r.error = reason;
      }
    }
    return Promise.resolve();
  }
  counts(): Promise<QueueCounts> {
    return Promise.resolve({
      pending: this.rows.filter((r) => r.state === 'pending').length,
      failed: this.rows.filter((r) => r.state === 'failed').length,
    });
  }
}

class MemoryCatalogueStore implements CatalogueStore {
  rows = new Map<string, CachedVariant>();
  saved: CatalogueCursor | null = null;

  upsert(vs: CachedVariant[]) {
    for (const v of vs) this.rows.set(v.id, v);
    return Promise.resolve();
  }
  findByBarcode(barcode: string) {
    return Promise.resolve(
      [...this.rows.values()].find((v) => v.barcode === barcode) ?? null,
    );
  }
  search() {
    return Promise.resolve([]);
  }
  cursor() {
    return Promise.resolve(this.saved);
  }
  setCursor(c: CatalogueCursor) {
    this.saved = c;
    return Promise.resolve();
  }
  count() {
    return Promise.resolve(this.rows.size);
  }
}

function sale(uuid: string): OfflineSalePayload {
  return {
    invoice_uuid: uuid,
    doc_type: 'simplified',
    issued_at: '2026-08-16T11:00:00+03:00',
    cashier_id: 'c1',
    prices_include_tax: true,
    lines: [
      {
        variant_id: 'v1',
        description: 'Item 1 · M',
        qty: '1',
        unit_price: '25.00',
        line_discount: '0',
        tax_treatment: 'standard',
      },
    ],
    tenders: [{ method: 'cash', amount: '25.00' }],
  };
}

/** A network where nothing ever answers. */
function hangingNetwork() {
  let stuck = 0;
  return {
    stuck: () => stuck,
    hang<T>(): Promise<T> {
      stuck++;
      return new Promise<T>(() => {});
    },
  };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('a sale completes whatever the network is doing', () => {
  it('records while a connectivity probe is hung', async () => {
    const net = hangingNetwork();
    const probe: Prober = { probe: () => net.hang() };
    const monitor = new ConnectivityMonitor(probe, {
      onlineIntervalMs: 1000,
      offlineBaseMs: 100,
      offlineMaxMs: 800,
      // Longer than this test runs, so the probe is genuinely still in flight
      // when the sale is recorded rather than having quietly timed out.
      timeoutMs: 60_000,
    });

    monitor.start();
    expect(net.stuck()).toBe(1);

    const store = new MemoryQueueStore();
    const queue = new SaleQueue(store, { push: () => net.hang() });

    // The probe is stuck and will not answer. The sale must not care.
    await queue.record(sale('11111111-1111-4111-8111-111111111111'));

    expect(store.rows).toHaveLength(1);
    expect(store.rows[0]?.state).toBe('pending');
    expect(monitor.current().checked).toBe(false); // still waiting, as intended
    monitor.stop();
  });

  it('records while the catalogue pull is hung', async () => {
    const net = hangingNetwork();
    const catalogue = new Catalogue(new MemoryCatalogueStore(), {
      send: () => net.hang(),
    });

    // Deliberately not awaited — this is how the hook starts it. One tick to
    // get past the cursor read, then the request is out and never returns.
    void catalogue.sync();
    await vi.advanceTimersByTimeAsync(0);
    expect(net.stuck()).toBe(1);

    const store = new MemoryQueueStore();
    const queue = new SaleQueue(store, { push: () => net.hang() });

    await queue.record(sale('22222222-2222-4222-8222-222222222222'));
    expect(store.rows).toHaveLength(1);
  });

  it('records repeatedly with every network call stuck', async () => {
    const net = hangingNetwork();
    const store = new MemoryQueueStore();
    const queue = new SaleQueue(store, { push: () => net.hang() });

    const monitor = new ConnectivityMonitor(
      { probe: () => net.hang() },
      {
        onlineIntervalMs: 1000,
        offlineBaseMs: 100,
        offlineMaxMs: 800,
        timeoutMs: 60_000,
      },
    );
    monitor.start();

    // A busy hour with no network. Every sale lands, in order, each with its
    // own sequence — the ordering the server's per-device ICV chain needs.
    for (let i = 0; i < 20; i++) {
      await queue.record(sale(`3333333${i % 10}-3333-4333-8333-33333333333${i % 10}`));
    }

    expect(store.rows).toHaveLength(20);
    expect(store.rows.map((r) => r.seq)).toEqual(
      Array.from({ length: 20 }, (_, i) => i + 1),
    );
    expect((await store.counts()).pending).toBe(20);
    monitor.stop();
  });

  it('scans from the cache while the connectivity probe is hung', async () => {
    const net = hangingNetwork();
    const store = new MemoryCatalogueStore();
    const catalogue = new Catalogue(store, { send: () => net.hang() });

    await store.upsert([
      {
        id: 'v1',
        productId: 'p1',
        sku: 'SKU-1',
        barcode: '6000000001',
        name: 'Item 1',
        nameAr: '',
        attributes: { size: 'M' },
        price: '25.00',
        taxTreatment: 'standard',
        isActive: true,
        updatedAt: '2026-08-15T10:00:00+03:00',
      },
    ]);

    const monitor = new ConnectivityMonitor(
      { probe: () => net.hang() },
      {
        onlineIntervalMs: 1000,
        offlineBaseMs: 100,
        offlineMaxMs: 800,
        timeoutMs: 60_000,
      },
    );
    monitor.start();

    // The whole milestone in one assertion: a brand new sale can be STARTED
    // with nothing reachable.
    const hit = await catalogue.lookup('6000000001');
    expect(hit?.price).toBe('25.00');
    monitor.stop();
  });
});

describe('the monitor and the queue stay out of each other', () => {
  it('a reconnect drains the queue exactly once, not once per probe', async () => {
    const store = new MemoryQueueStore();
    let pushes = 0;
    const queue = new SaleQueue(store, {
      push: () => {
        pushes++;
        return Promise.resolve({
          applied: 1,
          duplicates: 0,
          failed: 0,
          blocked: 0,
          cursor: 1,
          items: [{ seq: 1, state: 'applied' as const }],
        });
      },
    });

    await queue.record(sale('44444444-4444-4444-8444-444444444444'));
    const afterRecord = pushes;

    let down = true;
    const monitor = new ConnectivityMonitor(
      {
        probe: () =>
          down
            ? Promise.reject(new Error('offline'))
            : Promise.resolve({ ok: true, authenticated: true }),
      },
      {
        onlineIntervalMs: 1000,
        offlineBaseMs: 100,
        offlineMaxMs: 800,
        timeoutMs: 50,
      },
      () => void queue.flush(),
    );

    monitor.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(pushes).toBe(afterRecord);

    down = false;
    await vi.advanceTimersByTimeAsync(100);
    expect(monitor.current().reachable).toBe(true);
    expect(pushes).toBe(afterRecord + 1);

    // Three more successful probes. The queue is empty and the transition has
    // already fired, so nothing further is sent.
    await vi.advanceTimersByTimeAsync(3000);
    expect(pushes).toBe(afterRecord + 1);
    monitor.stop();
  });
});
