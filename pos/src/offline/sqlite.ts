// The durable side of the queue.
//
// SQLite in the app's own data directory, not localStorage or IndexedDB. A
// till's queue holds a day of takings and must survive a power cut mid-write;
// SQLite's write-ahead log is designed for exactly that, and the browser
// storage APIs make no such promise.
//
// Blueprint E1.3 puts the local ICV/PIH chain here too. That table is created
// now and left unused until signing is verified, because a chain the terminal
// cannot yet extend should still have somewhere to live rather than being
// bolted on later next to a queue that already works.

import Database from '@tauri-apps/plugin-sql';

import type {
  QueueCounts,
  QueueStore,
  QueuedSale,
  SettledState,
} from './queue';
import type {
  CachedVariant,
  CatalogueCursor,
  CatalogueStore,
} from './catalogue';

const SCHEMA = `
CREATE TABLE IF NOT EXISTS queued_sale (
  seq          INTEGER PRIMARY KEY,
  invoice_uuid TEXT NOT NULL UNIQUE,
  payload      TEXT NOT NULL,
  state        TEXT NOT NULL DEFAULT 'pending',
  error        TEXT,
  recorded_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS queued_sale_outstanding
  ON queued_sale (seq) WHERE state = 'pending';

-- The terminal's own chain, per E1.3. Unused until signing is verified; the
-- server allocates the ICV today. Created now so the chain has a home rather
-- than being retrofitted beside a working queue later.
CREATE TABLE IF NOT EXISTS local_chain (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  last_icv      INTEGER NOT NULL DEFAULT 0,
  last_hash     TEXT
);
INSERT OR IGNORE INTO local_chain (id, last_icv) VALUES (1, 0);

-- The catalogue this terminal can sell from with no network.
--
-- A cache, not a source of truth: the server reprices every line on replay, so
-- a stale row produces a receipt that needs correcting, never a wrong invoice.
-- No cost price and no margin — a Cashier is denied catalog.view_cost_price,
-- and a cache holding cost would put it on every till in the shop.
CREATE TABLE IF NOT EXISTS cached_variant (
  id            TEXT PRIMARY KEY,
  product_id    TEXT NOT NULL,
  sku           TEXT NOT NULL,
  barcode       TEXT NOT NULL DEFAULT '',
  name          TEXT NOT NULL DEFAULT '',
  name_ar       TEXT NOT NULL DEFAULT '',
  attributes    TEXT NOT NULL DEFAULT '{}',
  price         TEXT NOT NULL,
  tax_treatment TEXT NOT NULL DEFAULT 'standard',
  is_active     INTEGER NOT NULL DEFAULT 1,
  updated_at    TEXT NOT NULL
);

-- The scan path, and the only index that has to be fast: a cashier holds a
-- scanner over an item and expects the line to appear before they look up.
CREATE INDEX IF NOT EXISTS cached_variant_barcode
  ON cached_variant (barcode) WHERE barcode <> '';

CREATE INDEX IF NOT EXISTS cached_variant_sku ON cached_variant (sku);

-- Where the delta pull got to. One row, like local_chain.
CREATE TABLE IF NOT EXISTS catalogue_cursor (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  since    TEXT,
  since_id TEXT
);
INSERT OR IGNORE INTO catalogue_cursor (id) VALUES (1);
`;

interface Row {
  seq: number;
  invoice_uuid: string;
  payload: string;
  state: QueuedSale['state'];
  error: string | null;
  recorded_at: string;
}

/** Opens the terminal's local database, creating it on first run. */
export async function openLocalStore(): Promise<LocalStores> {
  const db = await Database.load('sqlite:rawsyst-pos.db');
  for (const statement of SCHEMA.split(';')) {
    const sql = statement.trim();
    if (sql) await db.execute(sql);
  }
  return {
    queue: new SqliteQueueStore(db),
    catalogue: new SqliteCatalogueStore(db),
  };
}

/** Everything the terminal keeps locally, opened together against one
 *  database file. Two connections to the same SQLite file would sooner or
 *  later meet each other's write lock. */
export interface LocalStores {
  queue: SqliteQueueStore;
  catalogue: SqliteCatalogueStore;
}

export class SqliteQueueStore implements QueueStore {
  constructor(private readonly db: Database) {}

  /**
   * The next sequence for this terminal.
   *
   * Derived from the highest ever used, INCLUDING settled and failed rows, so
   * a number is never reused. Reusing one would make two different sales
   * indistinguishable to the server's per-device ordering.
   */
  async nextSeq(): Promise<number> {
    const rows = await this.db.select<Array<{ next: number }>>(
      'SELECT COALESCE(MAX(seq), 0) + 1 AS next FROM queued_sale',
    );
    return rows[0]?.next ?? 1;
  }

  async put(entry: QueuedSale): Promise<void> {
    await this.db.execute(
      `INSERT INTO queued_sale (seq, invoice_uuid, payload, state, recorded_at)
       VALUES ($1, $2, $3, $4, $5)`,
      [
        entry.seq,
        entry.invoiceUuid,
        JSON.stringify(entry.payload),
        entry.state,
        entry.recordedAt,
      ],
    );
  }

  async outstanding(limit: number): Promise<QueuedSale[]> {
    const rows = await this.db.select<Row[]>(
      `SELECT seq, invoice_uuid, payload, state, error, recorded_at
       FROM queued_sale WHERE state = 'pending' ORDER BY seq LIMIT $1`,
      [limit],
    );
    return rows.map(toEntry);
  }

  async markSettled(seqs: number[], state: SettledState): Promise<void> {
    if (seqs.length === 0) return;
    // Named placeholders rather than interpolation, even for integers the code
    // produced itself. A query built by string concatenation is a habit that
    // eventually meets a value that came from outside.
    const holes = seqs.map((_, i) => `$${i + 2}`).join(',');
    await this.db.execute(
      `UPDATE queued_sale SET state = $1 WHERE seq IN (${holes})`,
      [state, ...seqs],
    );
  }

  async markFailed(seq: number, reason: string): Promise<void> {
    await this.db.execute(
      `UPDATE queued_sale SET state = 'failed', error = $2 WHERE seq = $1`,
      [seq, reason],
    );
  }

  async counts(): Promise<QueueCounts> {
    const rows = await this.db.select<Array<{ state: string; n: number }>>(
      `SELECT state, COUNT(*) AS n FROM queued_sale GROUP BY state`,
    );
    const by = Object.fromEntries(rows.map((r) => [r.state, r.n]));
    return { pending: by.pending ?? 0, failed: by.failed ?? 0 };
  }
}

function toEntry(row: Row): QueuedSale {
  return {
    seq: row.seq,
    invoiceUuid: row.invoice_uuid,
    payload: JSON.parse(row.payload),
    state: row.state,
    error: row.error ?? undefined,
    recordedAt: row.recorded_at,
  };
}

/** The cached catalogue, in SQLite.
 *
 * Reads here sit directly under a barcode scanner, so they are single indexed
 * lookups and nothing else. Writes arrive in bulk from the delta pull.
 */
export class SqliteCatalogueStore implements CatalogueStore {
  constructor(private readonly db: Database) {}

  /**
   * Writes a page of the delta.
   *
   * Upsert rather than insert: the same variant arrives again every time its
   * price changes, and a page re-downloaded after a crash must not fail on the
   * primary key. Row by row inside one call — the plugin exposes no batch, and
   * a page is five hundred rows on a machine doing nothing else.
   */
  async upsert(variants: CachedVariant[]): Promise<void> {
    for (const v of variants) {
      await this.db.execute(
        `INSERT INTO cached_variant
           (id, product_id, sku, barcode, name, name_ar, attributes,
            price, tax_treatment, is_active, updated_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
         ON CONFLICT(id) DO UPDATE SET
           product_id = excluded.product_id,
           sku = excluded.sku,
           barcode = excluded.barcode,
           name = excluded.name,
           name_ar = excluded.name_ar,
           attributes = excluded.attributes,
           price = excluded.price,
           tax_treatment = excluded.tax_treatment,
           is_active = excluded.is_active,
           updated_at = excluded.updated_at`,
        [
          v.id, v.productId, v.sku, v.barcode, v.name, v.nameAr,
          JSON.stringify(v.attributes ?? {}),
          v.price, v.taxTreatment, v.isActive ? 1 : 0, v.updatedAt,
        ],
      );
    }
  }

  /** Withdrawn variants are returned, not hidden.
   *
   * The counter needs to tell a cashier "that has been taken off sale", which
   * it cannot do if the lookup returns nothing — an inactive item and an
   * unknown barcode would produce the same message, and only one of them is
   * the cashier scanning the wrong thing.
   */
  async findByBarcode(barcode: string): Promise<CachedVariant | null> {
    if (!barcode) return null;
    const rows = await this.db.select<VariantRow[]>(
      `SELECT * FROM cached_variant WHERE barcode = $1 LIMIT 1`,
      [barcode],
    );
    const row = rows[0];
    return row ? toVariant(row) : null;
  }

  /** Typeahead over SKU and name, active items only — this one is a cashier
   *  choosing an item, not identifying one they are already holding. */
  async search(term: string, limit: number): Promise<CachedVariant[]> {
    const like = `%${term}%`;
    const rows = await this.db.select<VariantRow[]>(
      `SELECT * FROM cached_variant
       WHERE is_active = 1 AND (sku LIKE $1 OR name LIKE $1 OR name_ar LIKE $1)
       ORDER BY sku LIMIT $2`,
      [like, limit],
    );
    return rows.map(toVariant);
  }

  async cursor(): Promise<CatalogueCursor | null> {
    const rows = await this.db.select<Array<{ since: string | null; since_id: string | null }>>(
      `SELECT since, since_id FROM catalogue_cursor WHERE id = 1`,
    );
    const row = rows[0];
    if (!row?.since || !row.since_id) return null;
    return { since: row.since, sinceId: row.since_id };
  }

  async setCursor(cursor: CatalogueCursor): Promise<void> {
    await this.db.execute(
      `UPDATE catalogue_cursor SET since = $1, since_id = $2 WHERE id = 1`,
      [cursor.since, cursor.sinceId],
    );
  }

  async count(): Promise<number> {
    const rows = await this.db.select<Array<{ n: number }>>(
      `SELECT COUNT(*) AS n FROM cached_variant WHERE is_active = 1`,
    );
    return rows[0]?.n ?? 0;
  }
}

interface VariantRow {
  id: string;
  product_id: string;
  sku: string;
  barcode: string;
  name: string;
  name_ar: string;
  attributes: string;
  price: string;
  tax_treatment: string;
  is_active: number;
  updated_at: string;
}

function toVariant(row: VariantRow): CachedVariant {
  let attributes: Record<string, string> = {};
  try {
    const parsed: unknown = JSON.parse(row.attributes || '{}');
    if (parsed && typeof parsed === 'object') {
      attributes = parsed as Record<string, string>;
    }
  } catch {
    // A malformed attribute blob costs the variant its size and colour, not
    // its sellability.
  }
  return {
    id: row.id,
    productId: row.product_id,
    sku: row.sku,
    barcode: row.barcode,
    name: row.name,
    nameAr: row.name_ar,
    attributes,
    price: row.price,
    taxTreatment: row.tax_treatment,
    isActive: row.is_active === 1,
    updatedAt: row.updated_at,
  };
}
