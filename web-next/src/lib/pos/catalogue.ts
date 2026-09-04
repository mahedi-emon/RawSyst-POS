'use client';

// The till's own copy of the catalogue.
//
// # Why the till does not call the server per scan
//
// It did, at first, and live validation showed two things wrong with that.
//
// The small one is speed: a network round trip between the beep and the line
// appearing is the difference between a queue moving and a queue waiting, and
// it happens once per item rather than once per sale.
//
// The large one is correctness. `GET /catalog/scan` returns
// `{id, sku, attributes, price, is_active}` and **no tax treatment**, but
// `POST /pos/sales` refuses a line without one — so a till built on `scan`
// could ring items up all day and fail at the moment of payment. The endpoint
// that carries everything a sale needs is `GET /catalog/snapshot`: id, barcode,
// name, price, `tax_treatment`, and an `updated_at` cursor.
//
// The snapshot exists precisely for this. Its own comment in the Go source says
// so: without a local catalogue a terminal "can finish a sale it already has,
// but cannot look a barcode up, which means it cannot begin one — and a till
// that can only continue is not offline-capable in any way a shop cares about."
//
// # The cursor
//
// Paging is on `(since, since_id)`, not an offset, so the same route serves the
// first full download and every later delta. The till keeps the last pair it
// saw. Offsets would silently skip or repeat rows when the catalogue changes
// between pages, which on a till means a product that cannot be scanned.

import { api } from '../api/client';

/** One sellable line, as the snapshot describes it. */
export interface CatalogueItem {
  id: string;
  product_id: string;
  sku: string;
  barcode: string;
  name: string;
  /** A JSON string on the wire, not an object. Parsed once, on the way in. */
  attributes: string;
  price: string;
  /** Required on every sale line. This is the only place the till can get it. */
  tax_treatment: string;
  is_active: boolean;
  updated_at: string;
}

interface SnapshotPage {
  items: CatalogueItem[];
  next_since: string | null;
  next_since_id: string | null;
}

/** A variant the till can put on a line. */
export interface Sellable {
  variantId: string;
  sku: string;
  barcode: string;
  description: string;
  price: string;
  taxTreatment: string;
}

function describe(item: CatalogueItem): string {
  // The attributes are the variant's own words -- "Black · Large". The product
  // name alone is ambiguous the moment a shop sells two sizes of it.
  let attrs: Record<string, string> = {};
  try {
    attrs = JSON.parse(item.attributes || '{}') as Record<string, string>;
  } catch {
    // A malformed blob is not worth failing a sale over; the name still names
    // the thing.
  }
  const detail = Object.values(attrs).filter(Boolean).join(' · ');
  return detail ? `${item.name} · ${detail}` : item.name;
}

function toSellable(item: CatalogueItem): Sellable {
  return {
    variantId: item.id,
    sku: item.sku,
    barcode: item.barcode,
    description: describe(item),
    price: item.price,
    taxTreatment: item.tax_treatment,
  };
}

/**
 * An in-memory index of what this counter can sell.
 *
 * Held for the life of the till session. It is not persisted: a browser tab is
 * not the offline surface the desktop till is, and a stale catalogue that
 * survives a reload is worse than one that is fetched again in a second.
 */
export class Catalogue {
  private byBarcode = new Map<string, Sellable>();
  private bySku = new Map<string, Sellable>();
  private all: Sellable[] = [];

  get size(): number {
    return this.all.length;
  }

  /** Downloads the whole catalogue, following the cursor to the end. */
  async load(companyId: string, signal?: AbortSignal): Promise<void> {
    let since: string | null = null;
    let sinceId: string | null = null;

    // Bounded so a bug in the cursor cannot spin forever at a counter. 200 is
    // the API's page cap, so this covers 40,000 lines -- past that a shop needs
    // the desktop till, and saying so is better than hanging.
    for (let page = 0; page < 200; page += 1) {
      const res: SnapshotPage = await api.get<SnapshotPage>('/catalog/snapshot', {
        query: {
          company_id: companyId,
          limit: 200,
          since: since ?? undefined,
          since_id: sinceId ?? undefined,
        },
        signal,
      });

      for (const item of res.items) {
        if (!item.is_active) continue;
        const sellable = toSellable(item);
        this.all.push(sellable);
        if (item.barcode) this.byBarcode.set(item.barcode.trim(), sellable);
        if (item.sku) this.bySku.set(item.sku.trim().toUpperCase(), sellable);
      }

      if (res.items.length < 200 || !res.next_since_id) break;
      since = res.next_since;
      sinceId = res.next_since_id;
    }
  }

  /**
   * What was just scanned.
   *
   * Barcode first, because that is what a scanner sends. A code typed by hand
   * is often the SKU, so that is tried second rather than reported as unknown.
   */
  find(code: string): Sellable | null {
    const trimmed = code.trim();
    if (!trimmed) return null;
    return (
      this.byBarcode.get(trimmed) ??
      this.bySku.get(trimmed.toUpperCase()) ??
      null
    );
  }

  /**
   * Free-text search, for the counter with no barcode on the item.
   *
   * Substring, case-insensitive, capped. Not fuzzy: a cashier looking for
   * "abaya" wants the abayas, and a match that surprises them costs more time
   * than a miss.
   */
  search(term: string, limit = 20): Sellable[] {
    const q = term.trim().toLowerCase();
    if (q.length < 2) return [];
    const out: Sellable[] = [];
    for (const item of this.all) {
      if (
        item.description.toLowerCase().includes(q) ||
        item.sku.toLowerCase().includes(q)
      ) {
        out.push(item);
        if (out.length >= limit) break;
      }
    }
    return out;
  }
}
