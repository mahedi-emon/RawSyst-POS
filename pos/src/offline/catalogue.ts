// The catalogue, held on the terminal.
//
// This is what makes the till genuinely offline-capable rather than merely
// offline-tolerant. Without it a cashier can finish a sale already in the cart
// but cannot look a barcode up, so cannot begin one — and a till that can only
// continue is not something a shop would call working.
//
// # It is a cache, not a source of truth
//
// Prices here are what the server last said. The server reprices every line on
// replay: the payload the queue sends carries the unit price the cashier saw,
// and the sale service is free to disagree with it. A stale cache therefore
// produces a receipt that needs correcting, never a wrong invoice or a wrong
// journal. That is the whole reason the terminal is allowed to hold prices at
// all.
//
// # Pulled as a delta
//
// GET /api/v1/catalog/snapshot is cursored on (updated_at, id). The terminal
// stores the last pair it saw, so the first sync downloads everything and
// every later one downloads only what changed. A till that has been off for a
// week pulls the difference rather than the catalogue.
//
// Withdrawn variants arrive in that delta rather than being filtered out
// server-side. A row silently omitted would stay in the local cache forever
// and the cashier would keep selling something taken off sale, so the server
// sends `is_active: false` and this code stores it — the absence of news is
// not the same as news of an absence.

import type { Client } from '@rawsyst/shared/api/client';

/** One sellable line of the cached catalogue. Money stays a string. */
export interface CachedVariant {
  id: string;
  productId: string;
  sku: string;
  barcode: string;
  name: string;
  nameAr: string;
  attributes: Record<string, string>;
  price: string;
  taxTreatment: string;
  isActive: boolean;
  updatedAt: string;
}

/** The wire shape of GET /api/v1/catalog/snapshot. */
interface SnapshotResponse {
  items: Array<{
    id: string;
    product_id: string;
    sku: string;
    barcode?: string;
    name: string;
    name_ar?: string;
    attributes: string;
    price: string;
    tax_treatment: string;
    is_active: boolean;
    updated_at: string;
  }>;
  next_since?: string;
  next_since_id?: string;
}

/** Where the cursor was left. Absent on a terminal that has never synced. */
export interface CatalogueCursor {
  since: string;
  sinceId: string;
}

/** The storage this needs. Kept an interface so the tests can run without
 *  Tauri, exactly as the queue's store does. */
export interface CatalogueStore {
  upsert(variants: CachedVariant[]): Promise<void>;
  findByBarcode(barcode: string): Promise<CachedVariant | null>;
  search(term: string, limit: number): Promise<CachedVariant[]>;
  cursor(): Promise<CatalogueCursor | null>;
  setCursor(cursor: CatalogueCursor): Promise<void>;
  count(): Promise<number>;
}

/** How many variants to pull per request. Large enough that a small shop
 *  syncs in one call, small enough not to hold a slow connection open. */
const PAGE = 500;

/** A stop, so a runaway cursor cannot loop forever against a server that
 *  keeps returning the same page. */
const MAX_PAGES = 200;

export class Catalogue {
  constructor(
    private readonly store: CatalogueStore,
    private readonly client: Pick<Client, 'send'>,
  ) {}

  /**
   * Looks a barcode up locally.
   *
   * Local FIRST, always — not "local if offline". Checking the network first
   * would make every scan wait out a timeout whenever the connection is merely
   * slow, which is the common case in a shop with poor signal and much worse
   * for a queue of customers than a price that is a day old.
   *
   * Returns null when the barcode is not cached, which the caller may then try
   * against the server. That fallback is what covers a product added since the
   * last sync.
   */
  lookup(barcode: string): Promise<CachedVariant | null> {
    return this.store.findByBarcode(barcode.trim());
  }

  /** Typeahead for an item with no barcode, or an unreadable one. */
  search(term: string, limit = 20): Promise<CachedVariant[]> {
    const t = term.trim();
    return t ? this.store.search(t, limit) : Promise.resolve([]);
  }

  /** How many variants this terminal can sell without a network. */
  size(): Promise<number> {
    return this.store.count();
  }

  /**
   * Pulls everything changed since the last pull.
   *
   * Returns the number of variants written. Throws on a transport failure so
   * the caller can distinguish "no network" from "nothing changed" — the two
   * look identical from the count alone, and a till that reported "catalogue
   * up to date" while unreachable would be lying to the cashier.
   *
   * The cursor advances only after the page is stored. A crash between the two
   * re-downloads that page next time, which is harmless because the write is
   * an upsert.
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
        `/api/v1/catalog/snapshot?${query.toString()}`,
      );

      const items = res.items ?? [];
      if (items.length === 0) break;

      await this.store.upsert(items.map(fromWire));
      written += items.length;

      if (!res.next_since || !res.next_since_id) break;
      cursor = { since: res.next_since, sinceId: res.next_since_id };
      await this.store.setCursor(cursor);

      // A short page means the server had nothing more to give.
      if (items.length < PAGE) break;
    }

    return written;
  }
}

function fromWire(row: SnapshotResponse['items'][number]): CachedVariant {
  return {
    id: row.id,
    productId: row.product_id,
    sku: row.sku,
    barcode: row.barcode ?? '',
    name: row.name,
    nameAr: row.name_ar ?? '',
    attributes: parseAttributes(row.attributes),
    price: row.price,
    taxTreatment: row.tax_treatment,
    isActive: row.is_active,
    updatedAt: row.updated_at,
  };
}

/** Attributes arrive as a JSON string from a jsonb column. A malformed one
 *  costs the variant its size and colour, not its sellability. */
function parseAttributes(raw: string): Record<string, string> {
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    return parsed && typeof parsed === 'object'
      ? (parsed as Record<string, string>)
      : {};
  } catch {
    return {};
  }
}

/** A readable name for the cart line. */
export function describeVariant(v: CachedVariant): string {
  const parts = Object.values(v.attributes ?? {});
  if (v.name && parts.length > 0) return `${v.name} · ${parts.join(' · ')}`;
  return v.name || parts.join(' · ') || v.sku;
}
