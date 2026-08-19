// The customer cache: can a cashier attach a sale to a regular with the network
// down, and know what that regular is allowed to owe?
//
// That is the question this exists to answer, so the tests are written as it.

import { describe, expect, it, vi } from 'vitest';

import {
  Customers,
  type CachedCustomer,
  type CustomerCursor,
  type CustomerStore,
} from './customers';

/** An in-memory stand-in for SQLite, exactly as the catalogue tests do it. */
class MemoryStore implements CustomerStore {
  rows = new Map<string, CachedCustomer>();
  saved: CustomerCursor | null = null;

  upsert(customers: CachedCustomer[]): Promise<void> {
    for (const c of customers) this.rows.set(c.id, c);
    return Promise.resolve();
  }

  search(term: string, limit: number): Promise<CachedCustomer[]> {
    const t = term.toLowerCase();
    const hits = [...this.rows.values()].filter(
      (c) =>
        c.isActive &&
        (c.name.toLowerCase().includes(t) ||
          c.code.toLowerCase().includes(t) ||
          c.phone.includes(t)),
    );
    return Promise.resolve(hits.slice(0, limit));
  }

  find(id: string): Promise<CachedCustomer | null> {
    return Promise.resolve(this.rows.get(id) ?? null);
  }

  cursor(): Promise<CustomerCursor | null> {
    return Promise.resolve(this.saved);
  }

  setCursor(cursor: CustomerCursor): Promise<void> {
    this.saved = cursor;
    return Promise.resolve();
  }

  count(): Promise<number> {
    return Promise.resolve(this.rows.size);
  }
}

function wireRow(n: number, over: Record<string, unknown> = {}) {
  return {
    id: `c${n}`,
    code: `CUST${n}`,
    name: `Customer ${n}`,
    customer_type: 'retail',
    phone: `05550001${n}`,
    payment_terms_days: 30,
    credit_limit: '500.00',
    balance: '0.00',
    available: '500.00',
    is_active: true,
    updated_at: `2026-08-18T09:0${n}:00Z`,
    ...over,
  };
}

const clientReturning = (...pages: unknown[]) => {
  const send = vi.fn();
  for (const page of pages) send.mockResolvedValueOnce(page);
  send.mockResolvedValue({ items: [] });
  return { send: send as never, calls: send };
};

describe('pulling the customer book', () => {
  it('downloads everything on a terminal that has never synced', async () => {
    const store = new MemoryStore();
    const client = clientReturning({
      items: [wireRow(1), wireRow(2)],
      next_since: '2026-08-18T09:02:00Z',
      next_since_id: 'c2',
    });

    const written = await new Customers(store, client).sync();

    expect(written).toBe(2);
    expect(await store.count()).toBe(2);
    // No `since` on the first call: the whole book, not a delta from nowhere.
    expect(String(client.calls.mock.calls[0]?.[1])).not.toContain('since=');
  });

  it('asks only for what changed on a later pull', async () => {
    const store = new MemoryStore();
    store.saved = { since: '2026-08-17T00:00:00Z', sinceId: 'c9' };
    const client = clientReturning({ items: [wireRow(3)] });

    await new Customers(store, client).sync();

    const url = String(client.calls.mock.calls[0]?.[1]);
    expect(url).toContain('since=2026-08-17');
    expect(url).toContain('since_id=c9');
  });

  it('keeps the cursor it had when nothing changed', async () => {
    const store = new MemoryStore();
    store.saved = { since: '2026-08-17T00:00:00Z', sinceId: 'c9' };
    const client = clientReturning({ items: [] });

    expect(await new Customers(store, client).sync()).toBe(0);
    expect(store.saved).toEqual({ since: '2026-08-17T00:00:00Z', sinceId: 'c9' });
  });

  it('overwrites a customer whose terms or limit changed', async () => {
    const store = new MemoryStore();
    const customers = new Customers(
      store,
      clientReturning({ items: [wireRow(1, { credit_limit: '500.00' })] }),
    );
    await customers.sync();

    const raised = new Customers(
      store,
      clientReturning({ items: [wireRow(1, { credit_limit: '2000.00' })] }),
    );
    await raised.sync();

    expect(await store.count()).toBe(1);
    expect((await store.find('c1'))?.creditLimit).toBe('2000.00');
  });

  it('stores a retired customer rather than dropping them', async () => {
    // A row silently omitted stays in the cache forever, and the cashier keeps
    // selling to somebody the shop has stopped dealing with. Same reasoning as
    // the catalogue's withdrawn variants.
    const store = new MemoryStore();
    await new Customers(
      store,
      clientReturning({ items: [wireRow(1, { is_active: false })] }),
    ).sync();

    expect((await store.find('c1'))?.isActive).toBe(false);
  });

  it('lets a transport failure through rather than reporting success', async () => {
    // A till saying "customers up to date" while unreachable would be lying.
    const send = vi.fn().mockRejectedValue(new Error('offline'));
    await expect(
      new Customers(new MemoryStore(), { send: send as never }).sync(),
    ).rejects.toThrow('offline');
  });

  it('reads a customer with no credit account as having none', async () => {
    // Absent, not zero: the two mean different things and only one of them can
    // be raised later without a decision.
    const store = new MemoryStore();
    await new Customers(
      store,
      clientReturning({
        items: [wireRow(1, { credit_limit: undefined, available: undefined })],
      }),
    ).sync();

    const c = await store.find('c1');
    expect(c?.creditLimit).toBe('');
    expect(c?.available).toBe('');
  });
});

describe('finding somebody at the counter', () => {
  const seeded = async () => {
    const store = new MemoryStore();
    await new Customers(
      store,
      clientReturning({
        items: [
          wireRow(1, { name: 'Al Noor Trading', phone: '0555000111' }),
          wireRow(2, { name: 'Bright Star Co', phone: '0555000222' }),
          wireRow(3, { name: 'Retired Shop', is_active: false }),
        ],
      }),
    ).sync();
    return store;
  };

  it('finds by part of a name', async () => {
    const customers = new Customers(await seeded(), clientReturning());
    const hits = await customers.search('noor');
    expect(hits.map((c) => c.name)).toEqual(['Al Noor Trading']);
  });

  it('finds by the digits a customer reads out', async () => {
    const customers = new Customers(await seeded(), clientReturning());
    const hits = await customers.search('000222');
    expect(hits.map((c) => c.name)).toEqual(['Bright Star Co']);
  });

  it('does not offer a retired customer to sell to', async () => {
    const customers = new Customers(await seeded(), clientReturning());
    expect(await customers.search('Retired')).toEqual([]);
  });

  it('searches for nothing on an empty box rather than listing everybody', async () => {
    const customers = new Customers(await seeded(), clientReturning());
    expect(await customers.search('   ')).toEqual([]);
  });
});
