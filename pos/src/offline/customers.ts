// The customer book, held on the terminal.
//
// The mirror of catalogue.ts, and for the same reason: without it a cashier
// with the network down can finish a sale but cannot attach it to the regular
// standing at the counter — and a shop that extends credit needs to be able to
// say who owes it, especially when the connection is the thing that failed.
//
// # It is a cache, and the balance in it is stale by design
//
// A customer's balance moves with every sale and every receipt, not with their
// `updated_at`, so a delta pull cannot keep it current. That is accepted rather
// than engineered around. The till uses the cached figure to decide what to
// OFFER; the server re-reads the real balance under a row lock and refuses a
// breach on replay. Same bargain the catalogue already makes with prices: a
// stale row costs a corrected answer at the counter, never a wrong receivable.
//
// # Online, the server is asked instead
//
// Unlike a barcode, choosing a customer happens once per sale while somebody is
// typing a name, so a round trip is affordable — and the answer is a credit
// decision rather than a price, where being current is worth more than being
// instant. So the picker asks the server when it can and falls back to this
// when it cannot, which is the opposite of the scan path and deliberately so.

import type { Client } from '@rawsyst/shared/api/client';

/** One customer a till can sell to. Money stays a string. */
export interface CachedCustomer {
  id: string;
  code: string;
  name: string;
  nameAr: string;
  customerType: string;
  phone: string;
  paymentTermsDays: number;
  /** Empty means no credit account, which means no sale on account at all. */
  creditLimit: string;
  balance: string;
  available: string;
  isActive: boolean;
  updatedAt: string;
}

/** The wire shape of GET /api/v1/customers/snapshot. */
interface SnapshotResponse {
  items: Array<{
    id: string;
    code: string;
    name: string;
    name_ar?: string;
    customer_type: string;
    phone?: string;
    payment_terms_days: number;
    credit_limit?: string;
    balance: string;
    available?: string;
    is_active: boolean;
    updated_at: string;
  }>;
  next_since?: string;
  next_since_id?: string;
}

export interface CustomerCursor {
  since: string;
  sinceId: string;
}

/** The storage this needs, kept an interface so the tests run without Tauri. */
export interface CustomerStore {
  upsert(customers: CachedCustomer[]): Promise<void>;
  search(term: string, limit: number): Promise<CachedCustomer[]>;
  find(id: string): Promise<CachedCustomer | null>;
  cursor(): Promise<CustomerCursor | null>;
  setCursor(cursor: CustomerCursor): Promise<void>;
  count(): Promise<number>;
}

const PAGE = 500;
const MAX_PAGES = 200;

export class Customers {
  constructor(
    private readonly store: CustomerStore,
    private readonly client: Pick<Client, 'send'>,
  ) {}

  /** Typeahead over name, code and phone. */
  search(term: string, limit = 12): Promise<CachedCustomer[]> {
    const t = term.trim();
    return t ? this.store.search(t, limit) : Promise.resolve([]);
  }

  find(id: string): Promise<CachedCustomer | null> {
    return this.store.find(id);
  }

  /** How many customers this terminal can find without a network. */
  size(): Promise<number> {
    return this.store.count();
  }

  /**
   * Pulls everything changed since the last pull.
   *
   * Throws on a transport failure so the caller can tell "no network" from
   * "nothing changed" — a till reporting "customers up to date" while
   * unreachable would be lying to the cashier.
   *
   * The cursor advances only after the page is stored. A crash between the two
   * re-downloads that page, which is harmless because the write is an upsert.
   */
  async sync(): Promise<number> {
    let cursor = await this.store.cursor();
    let written = 0;

    for (let page = 0; page < MAX_PAGES; page++) {
      const query = new URLSearchParams({ limit: String(PAGE) });
      if (cursor) {
        query.set('since', cursor.since);
        query.set('since_id', cursor.sinceId);
      }

      const res = await this.client.send<SnapshotResponse>(
        'GET',
        `/api/v1/customers/snapshot?${query.toString()}`,
      );

      const items = res.items ?? [];
      if (items.length === 0) break;

      await this.store.upsert(items.map(fromWire));
      written += items.length;

      if (!res.next_since || !res.next_since_id) break;
      cursor = { since: res.next_since, sinceId: res.next_since_id };
      await this.store.setCursor(cursor);

      if (items.length < PAGE) break;
    }

    return written;
  }
}

function fromWire(row: SnapshotResponse['items'][number]): CachedCustomer {
  return {
    id: row.id,
    code: row.code,
    name: row.name,
    nameAr: row.name_ar ?? '',
    customerType: row.customer_type,
    phone: row.phone ?? '',
    paymentTermsDays: row.payment_terms_days,
    creditLimit: row.credit_limit ?? '',
    balance: row.balance,
    available: row.available ?? '',
    isActive: row.is_active,
    updatedAt: row.updated_at,
  };
}
