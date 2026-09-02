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
import type { HeldCart, HeldCartStore } from '../pos/held';
import type {
  CachedVariant,
  CatalogueCursor,
  CatalogueStore,
} from './catalogue';
import type {
  CachedCustomer,
  CustomerCursor,
  CustomerStore,
} from './customers';

// The terminal's schema, as a LIST of statements rather than one script.
//
// It used to be a single template string that openLocalStore split on `;`, and
// that is not a thing a semicolon can be trusted to do. The comment above
// local_chain reads "Unused until signing is verified; the server allocates the
// ICV today" — an ordinary semicolon in an ordinary English sentence — so the
// split cut the script in the middle of a comment and handed SQLite a fragment
// beginning with the word "the".
//
//   error returned from database: (code: 1) near "the": syntax error
//
// openLocalStore caught it, useTerminal caught that, and the till reported "no
// local storage" and refused to sell. Every installed terminal, on every start,
// from the moment that comment was written.
//
// A list cannot be mis-split. The prose stays where it belongs — beside the
// table it explains — and a future comment can contain whatever punctuation the
// sentence needs.
export const SCHEMA: readonly string[] = [
  `CREATE TABLE IF NOT EXISTS queued_sale (
  seq          INTEGER PRIMARY KEY,
  invoice_uuid TEXT NOT NULL UNIQUE,
  payload      TEXT NOT NULL,
  state        TEXT NOT NULL DEFAULT 'pending',
  error        TEXT,
  recorded_at  TEXT NOT NULL
);`,

  `
CREATE INDEX IF NOT EXISTS queued_sale_outstanding
  ON queued_sale (seq) WHERE state = 'pending';`,

  `
-- The terminal's own chain, per E1.3. Unused until signing is verified; the
-- server allocates the ICV today. Created now so the chain has a home rather
-- than being retrofitted beside a working queue later.
CREATE TABLE IF NOT EXISTS local_chain (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  last_icv      INTEGER NOT NULL DEFAULT 0,
  last_hash     TEXT
);`,

  `INSERT OR IGNORE INTO local_chain (id, last_icv) VALUES (1, 0);`,

  `
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
);`,

  `
-- The scan path, and the only index that has to be fast: a cashier holds a
-- scanner over an item and expects the line to appear before they look up.
CREATE INDEX IF NOT EXISTS cached_variant_barcode
  ON cached_variant (barcode) WHERE barcode <> '';`,

  `
CREATE INDEX IF NOT EXISTS cached_variant_sku ON cached_variant (sku);`,

  `
-- Where the delta pull got to. One row, like local_chain.
CREATE TABLE IF NOT EXISTS catalogue_cursor (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  since    TEXT,
  since_id TEXT
);`,

  `INSERT OR IGNORE INTO catalogue_cursor (id) VALUES (1);`,

  `
-- The customers this terminal can attach a sale to with no network.
--
-- A cache, like the catalogue, and the balance in it is stale by design: it
-- moves with every sale and receipt rather than with the customer's own
-- updated_at, so a delta cannot keep it current. The till uses it to decide
-- what to OFFER; the server re-reads the real figure under a row lock and is
-- the authority on whether a sale may go on account.
--
-- No address, no email, no notes. A cache on every till in the shop holds what
-- selling needs and nothing more, for the same reason this one holds no cost
-- price.
CREATE TABLE IF NOT EXISTS cached_customer (
  id                 TEXT PRIMARY KEY,
  code               TEXT NOT NULL,
  name               TEXT NOT NULL,
  name_ar            TEXT NOT NULL DEFAULT '',
  customer_type      TEXT NOT NULL DEFAULT 'retail',
  phone              TEXT NOT NULL DEFAULT '',
  payment_terms_days INTEGER NOT NULL DEFAULT 0,
  credit_limit       TEXT NOT NULL DEFAULT '',
  balance            TEXT NOT NULL DEFAULT '0.00',
  available          TEXT NOT NULL DEFAULT '',
  is_active          INTEGER NOT NULL DEFAULT 1,
  updated_at         TEXT NOT NULL
);`,

  `
-- A counter looks a customer up by phone more than by anything else.
CREATE INDEX IF NOT EXISTS cached_customer_phone
  ON cached_customer (phone) WHERE phone <> '';`,

  `CREATE INDEX IF NOT EXISTS cached_customer_name ON cached_customer (name);`,

  `
CREATE TABLE IF NOT EXISTS customer_cursor (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  since    TEXT,
  since_id TEXT
);`,

  `INSERT OR IGNORE INTO customer_cursor (id) VALUES (1);`,

  `
-- Carts parked mid-sale. NOT sales: no invoice UUID, no ICV, no stock, no
-- journal entry. A held cart is a note about what somebody was buying, which
-- is why it never leaves the terminal.
-- The shop's own stationery, so the till can print its receipt with no network.
--
-- The last of I2. The Back Office writes the words and the terminal has to be
-- holding them before the connection goes, because the receipt a customer walks
-- out with is printed at the counter and cannot wait for a round trip.
--
-- One row, replaced wholesale on every refresh. There is no delta to take: it
-- is a handful of short strings, and a cursor over four fields would be more
-- machinery than the thing it manages.
--
-- No logo. The receipt is 42 columns of plain text so it prints on every
-- counter printer, and text cannot hold an image.
CREATE TABLE IF NOT EXISTS cached_stationery (
  id              INTEGER PRIMARY KEY CHECK (id = 1),
  store_name      TEXT NOT NULL DEFAULT '',
  vat_number      TEXT NOT NULL DEFAULT '',
  -- SAR, BDT, USD. Empty on a till that has never been online, which prints
  -- the amount without a code rather than inventing one.
  base_currency   TEXT NOT NULL DEFAULT '',
  header_text     TEXT NOT NULL DEFAULT '',
  header_text_ar  TEXT NOT NULL DEFAULT '',
  footer_text     TEXT NOT NULL DEFAULT '',
  footer_text_ar  TEXT NOT NULL DEFAULT '',
  return_policy   TEXT NOT NULL DEFAULT '',
  return_policy_ar TEXT NOT NULL DEFAULT '',
  show_tax_number INTEGER NOT NULL DEFAULT 1,
  -- When it was last pulled. A till printing a month-old policy is doing the
  -- right thing; one that never pulled at all is a different situation, and
  -- the difference is worth being able to see.
  fetched_at      TEXT
);`,

  `INSERT OR IGNORE INTO cached_stationery (id) VALUES (1);`,



  `
CREATE TABLE IF NOT EXISTS held_cart (
  id          TEXT PRIMARY KEY,
  label       TEXT NOT NULL DEFAULT '',
  payload     TEXT NOT NULL,
  total       TEXT NOT NULL,
  item_count  INTEGER NOT NULL,
  held_at     TEXT NOT NULL
);`,

  `
CREATE INDEX IF NOT EXISTS held_cart_recent ON held_cart (held_at DESC);`,

  `
-- What the MAIN stock ledger last told this till.
--
-- A cache, and nothing more. The server-side ledger is the single source of
-- truth; this is a copy of part of it that goes stale the moment another till
-- sells something, and it is brought back into line two ways: a full pull when
-- the terminal comes online, and a stock.moved delta while it stays online.
--
-- It is never consulted to decide whether a sale may happen. Design 03 is
-- explicit that an offline till cannot prevent overselling and that the product
-- chooses accurate detection over false confidence — a stale figure refusing a
-- real customer is exactly the false confidence that rules out.
CREATE TABLE IF NOT EXISTS cached_stock (
  variant_id    TEXT NOT NULL,
  warehouse_id  TEXT NOT NULL,
  -- A decimal STRING, like every other quantity that crosses this boundary.
  -- Through a JavaScript number it would be a float64, and a till that
  -- accumulated deltas in one would drift away from the ledger it is caching.
  on_hand       TEXT NOT NULL,
  -- B4's minimum-stock level, so a warning can say "below the reorder point"
  -- rather than only "none left".
  reorder_level TEXT NOT NULL DEFAULT '',
  -- When this row was last agreed with the server, so a screen can say how old
  -- the figure is instead of presenting a guess as a fact.
  as_of         TEXT NOT NULL,
  PRIMARY KEY (variant_id, warehouse_id)
);`
];

interface Row {
  seq: number;
  invoice_uuid: string;
  payload: string;
  state: QueuedSale['state'];
  error: string | null;
  recorded_at: string;
}

/** Opens the terminal's local database, creating it on first run. */
/**
 * Statements that are EXPECTED to fail on a till that has already run them.
 *
 * `CREATE TABLE IF NOT EXISTS` does nothing to a table that exists, so a column
 * added to a table an installed till already has needs an ALTER — and SQLite
 * has no `IF NOT EXISTS` for one. Running it inside SCHEMA would abort the
 * whole schema on the second launch and leave the till with no local store at
 * all, which is the failure that once reported itself as "this terminal has no
 * local storage".
 *
 * So they are separate and their failures are swallowed. A statement here must
 * be safe to run twice and safe to fail: adding a column with a default is
 * both.
 */
export const MIGRATIONS: readonly string[] = [
  `ALTER TABLE cached_stationery ADD COLUMN base_currency TEXT NOT NULL DEFAULT '';`,
];

export async function openLocalStore(): Promise<LocalStores> {
  const db = await Database.load('sqlite:rawsyst-pos.db');
  for (const statement of SCHEMA) {
    const sql = statement.trim();
    if (sql) await db.execute(sql);
  }
  for (const statement of MIGRATIONS) {
    const sql = statement.trim();
    if (!sql) continue;
    try {
      await db.execute(sql);
    } catch {
      // Already applied. See MIGRATIONS for why this is the expected path on
      // every launch after the first.
    }
  }
  return {
    queue: new SqliteQueueStore(db),
    catalogue: new SqliteCatalogueStore(db),
    customers: new SqliteCustomerStore(db),
    held: new SqliteHeldCartStore(db),
    stationery: new SqliteStationeryStore(db),
    stock: new SqliteStockStore(db),
  };
}

/** Everything the terminal keeps locally, opened together against one
 *  database file. Two connections to the same SQLite file would sooner or
 *  later meet each other's write lock. */
export interface LocalStores {
  queue: SqliteQueueStore;
  catalogue: SqliteCatalogueStore;
  customers: SqliteCustomerStore;
  held: SqliteHeldCartStore;
  stationery: SqliteStationeryStore;
  /** The till's cached copy of part of the main stock ledger. */
  stock: SqliteStockStore;
}

/** The shop's own words, held on the terminal so a receipt still prints with
 *  no network. */
export class SqliteStationeryStore {
  constructor(private readonly db: Database) {}

  async read(): Promise<CachedStationery | null> {
    const rows = await this.db.select<Array<Record<string, unknown>>>(
      `SELECT store_name, vat_number, base_currency, header_text,
              header_text_ar, footer_text, footer_text_ar, return_policy,
              return_policy_ar, show_tax_number, fetched_at
       FROM cached_stationery WHERE id = 1`,
    );
    const row = rows[0];
    // Never fetched. Not an error: a till that has never been online prints on
    // the RawSyst default, which is what it should do.
    if (!row || !row.fetched_at) return null;

    return {
      storeName: String(row.store_name ?? ''),
      vatNumber: String(row.vat_number ?? ''),
      baseCurrency: String(row.base_currency ?? ''),
      headerText: String(row.header_text ?? ''),
      headerTextAr: String(row.header_text_ar ?? ''),
      footerText: String(row.footer_text ?? ''),
      footerTextAr: String(row.footer_text_ar ?? ''),
      returnPolicy: String(row.return_policy ?? ''),
      returnPolicyAr: String(row.return_policy_ar ?? ''),
      showTaxNumber: Number(row.show_tax_number ?? 1) !== 0,
      fetchedAt: String(row.fetched_at),
    };
  }

  async write(s: CachedStationery): Promise<void> {
    await this.db.execute(
      `UPDATE cached_stationery SET
         store_name = $1, vat_number = $2, base_currency = $3, header_text = $4,
         header_text_ar = $5, footer_text = $6, footer_text_ar = $7,
         return_policy = $8, return_policy_ar = $9, show_tax_number = $10,
         fetched_at = $11
       WHERE id = 1`,
      [
        s.storeName, s.vatNumber, s.baseCurrency, s.headerText, s.headerTextAr,
        s.footerText, s.footerTextAr, s.returnPolicy, s.returnPolicyAr,
        s.showTaxNumber ? 1 : 0, s.fetchedAt,
      ],
    );
  }
}

/** One variant's cached quantity in one warehouse. */
export interface CachedStock {
  variantId: string;
  warehouseId: string;
  /** A decimal string. See the note in the schema on why not a number. */
  onHand: string;
  reorderLevel: string;
  /** When the server last agreed this figure, ISO 8601. */
  asOf: string;
}

/**
 * The till's copy of part of the main stock ledger.
 *
 * A CACHE. The ledger on the server is the single source of truth and this is a
 * copy of it that is stale the moment another till sells something. It is
 * brought back into line by a full pull when the terminal comes online and by
 * `stock.moved` deltas while it stays online.
 *
 * Nothing here decides whether a sale may happen — see the schema note.
 */
export class SqliteStockStore {
  constructor(private readonly db: Database) {}

  /**
   * Everything held, whichever warehouse it is for.
   *
   * A till sells out of exactly one location, so this is normally one
   * warehouse's rows — but it is read WITHOUT naming one, because on start-up
   * the terminal does not yet know which it sells from. It learns that from
   * the server on its first pull.
   */
  async everything(): Promise<CachedStock[]> {
    const rows = await this.db.select<Array<Record<string, unknown>>>(
      `SELECT variant_id, warehouse_id, on_hand, reorder_level, as_of
       FROM cached_stock`,
    );
    return rows.map(toStock);
  }

  async all(warehouseId: string): Promise<CachedStock[]> {
    const rows = await this.db.select<Array<Record<string, unknown>>>(
      `SELECT variant_id, warehouse_id, on_hand, reorder_level, as_of
       FROM cached_stock WHERE warehouse_id = $1`,
      [warehouseId],
    );
    return rows.map(toStock);
  }

  async get(variantId: string, warehouseId: string): Promise<CachedStock | null> {
    const rows = await this.db.select<Array<Record<string, unknown>>>(
      `SELECT variant_id, warehouse_id, on_hand, reorder_level, as_of
       FROM cached_stock WHERE variant_id = $1 AND warehouse_id = $2`,
      [variantId, warehouseId],
    );
    const row = rows[0];
    return row ? toStock(row) : null;
  }

  /** Replaces what is held for these variants, from an authoritative read. */
  async put(lines: CachedStock[]): Promise<void> {
    for (const line of lines) {
      await this.db.execute(
        `INSERT INTO cached_stock
           (variant_id, warehouse_id, on_hand, reorder_level, as_of)
         VALUES ($1, $2, $3, $4, $5)
         ON CONFLICT (variant_id, warehouse_id) DO UPDATE SET
           on_hand = excluded.on_hand,
           reorder_level = excluded.reorder_level,
           as_of = excluded.as_of`,
        [
          line.variantId,
          line.warehouseId,
          line.onHand,
          line.reorderLevel,
          line.asOf,
        ],
      );
    }
  }

  /**
   * Applies a delta the server announced.
   *
   * A row that is not held is NOT created. A delta says how much a quantity
   * moved, not what it became, so applying one to a variant this till has never
   * pulled would invent a figure from nothing — and a made-up quantity is worse
   * than an absent one, because a screen will show it.
   *
   * Returns whether anything was updated, so a caller can decide to re-pull.
   */
  async applyDelta(
    variantId: string,
    warehouseId: string,
    delta: string,
    at: string,
  ): Promise<boolean> {
    const held = await this.get(variantId, warehouseId);
    if (!held) return false;

    const next = addDecimals(held.onHand, delta);
    await this.db.execute(
      `UPDATE cached_stock SET on_hand = $3, as_of = $4
       WHERE variant_id = $1 AND warehouse_id = $2`,
      [variantId, warehouseId, next, at],
    );
    return true;
  }

  /** Empties the cache, for a till switching warehouse or signing out. */
  async clear(): Promise<void> {
    await this.db.execute(`DELETE FROM cached_stock`);
  }
}

function toStock(row: Record<string, unknown>): CachedStock {
  return {
    variantId: String(row.variant_id ?? ''),
    warehouseId: String(row.warehouse_id ?? ''),
    onHand: String(row.on_hand ?? '0'),
    reorderLevel: String(row.reorder_level ?? ''),
    asOf: String(row.as_of ?? ''),
  };
}

/**
 * Adds two decimal strings without going through a float.
 *
 * `0.1 + 0.2` is not `0.3` in a float64, and a till that accumulated deltas
 * that way would drift away from the ledger it is caching — slowly, invisibly,
 * and in a direction nobody could reconstruct. Quantities here are small and
 * have at most a few decimal places, so integer arithmetic on the scaled values
 * is exact and short.
 */
export function addDecimals(a: string, b: string): string {
  const left = parseDecimal(a);
  const right = parseDecimal(b);
  const scale = Math.max(left.scale, right.scale);

  const total =
    left.units * BigInt(10 ** (scale - left.scale)) +
    right.units * BigInt(10 ** (scale - right.scale));

  if (scale === 0) return total.toString();

  const negative = total < 0n;
  const digits = (negative ? -total : total).toString().padStart(scale + 1, '0');
  const whole = digits.slice(0, digits.length - scale);
  const fraction = digits.slice(digits.length - scale).replace(/0+$/, '');

  const body = fraction ? `${whole}.${fraction}` : whole;
  return negative ? `-${body}` : body;
}

function parseDecimal(value: string): { units: bigint; scale: number } {
  const trimmed = (value ?? '').trim();
  if (!/^-?\d+(\.\d+)?$/.test(trimmed)) return { units: 0n, scale: 0 };

  const [whole = '0', fraction = ''] = trimmed.split('.');
  const negative = whole.startsWith('-');
  const digits = (negative ? whole.slice(1) : whole) + fraction;
  const units = BigInt(digits || '0');
  return { units: negative ? -units : units, scale: fraction.length };
}

/** What the till holds to print a receipt with. */
export interface CachedStationery {
  storeName: string;
  vatNumber: string;
  /** The code the shop keeps its books in — SAR, BDT, USD.
   *
   *  Cached with the rest of the stationery because it is the same kind of
   *  fact for the same reason: the receipt printed at the counter has to carry
   *  it, and cannot wait for a round trip to find out what it is. */
  baseCurrency: string;
  headerText: string;
  headerTextAr: string;
  footerText: string;
  footerTextAr: string;
  returnPolicy: string;
  returnPolicyAr: string;
  showTaxNumber: boolean;
  fetchedAt: string;
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

/** Held carts, in SQLite.
 *
 * The cart itself is stored as JSON rather than normalised into lines: it is
 * an opaque blob to everything except the counter that wrote it, has no
 * queries run against its contents, and normalising it would create a second
 * schema for a cart that must then be kept in step with the first.
 */
export class SqliteHeldCartStore implements HeldCartStore {
  constructor(private readonly db: Database) {}

  async put(cart: HeldCart): Promise<void> {
    await this.db.execute(
      `INSERT INTO held_cart (id, label, payload, total, item_count, held_at)
       VALUES ($1, $2, $3, $4, $5, $6)`,
      [
        cart.id,
        cart.label,
        JSON.stringify({ lines: cart.lines, tenders: cart.tenders }),
        cart.total,
        cart.itemCount,
        cart.heldAt,
      ],
    );
  }

  async list(): Promise<HeldCart[]> {
    const rows = await this.db.select<HeldRow[]>(
      `SELECT * FROM held_cart ORDER BY held_at DESC`,
    );
    return rows.map(toHeld);
  }

  /** Reads then deletes. A cart handed to a cashier must leave the list in the
   *  same breath, or a second till can resume it too. */
  async take(id: string): Promise<HeldCart | null> {
    const rows = await this.db.select<HeldRow[]>(
      `SELECT * FROM held_cart WHERE id = $1`,
      [id],
    );
    const row = rows[0];
    if (!row) return null;
    await this.db.execute(`DELETE FROM held_cart WHERE id = $1`, [id]);
    return toHeld(row);
  }

  async purgeBefore(cutoff: string): Promise<number> {
    const before = await this.count();
    await this.db.execute(`DELETE FROM held_cart WHERE held_at < $1`, [cutoff]);
    return before - (await this.count());
  }

  async count(): Promise<number> {
    const rows = await this.db.select<Array<{ n: number }>>(
      `SELECT COUNT(*) AS n FROM held_cart`,
    );
    return rows[0]?.n ?? 0;
  }
}

interface HeldRow {
  id: string;
  label: string;
  payload: string;
  total: string;
  item_count: number;
  held_at: string;
}

function toHeld(row: HeldRow): HeldCart {
  const body = JSON.parse(row.payload) as Pick<HeldCart, 'lines' | 'tenders'>;
  return {
    id: row.id,
    label: row.label,
    lines: body.lines ?? [],
    tenders: body.tenders ?? [],
    total: row.total,
    itemCount: row.item_count,
    heldAt: row.held_at,
  };
}

/** The cached customer book, in SQLite.
 *
 * Small next to the catalogue — a shop sells to everybody but extends credit to
 * a few — so the search is a plain LIKE over the three things a cashier types:
 * a name, a code, or the phone number the customer reads out.
 */
export class SqliteCustomerStore implements CustomerStore {
  constructor(private readonly db: Database) {}

  /** Upsert, for the same reason the catalogue's is: a customer arrives again
   *  every time their details or their terms change, and a page re-downloaded
   *  after a crash must not fail on the primary key. */
  async upsert(customers: CachedCustomer[]): Promise<void> {
    for (const c of customers) {
      await this.db.execute(
        `INSERT INTO cached_customer
           (id, code, name, name_ar, customer_type, phone, payment_terms_days,
            credit_limit, balance, available, is_active, updated_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
         ON CONFLICT(id) DO UPDATE SET
           code = excluded.code,
           name = excluded.name,
           name_ar = excluded.name_ar,
           customer_type = excluded.customer_type,
           phone = excluded.phone,
           payment_terms_days = excluded.payment_terms_days,
           credit_limit = excluded.credit_limit,
           balance = excluded.balance,
           available = excluded.available,
           is_active = excluded.is_active,
           updated_at = excluded.updated_at`,
        [
          c.id, c.code, c.name, c.nameAr, c.customerType, c.phone,
          c.paymentTermsDays, c.creditLimit, c.balance, c.available,
          c.isActive ? 1 : 0, c.updatedAt,
        ],
      );
    }
  }

  /** Active customers only: this is a cashier choosing who to sell to, not
   *  identifying somebody already in front of them. A retired customer must
   *  not appear in a picker — the server would refuse the sale anyway. */
  async search(term: string, limit: number): Promise<CachedCustomer[]> {
    const like = `%${term}%`;
    const rows = await this.db.select<CustomerRow[]>(
      `SELECT * FROM cached_customer
       WHERE is_active = 1
         AND (name LIKE $1 OR name_ar LIKE $1 OR code LIKE $1 OR phone LIKE $1)
       ORDER BY name LIMIT $2`,
      [like, limit],
    );
    return rows.map(toCustomer);
  }

  /** By id, INCLUDING retired — a sale in progress may already name somebody
   *  who was retired since, and the counter has to be able to say so. */
  async find(id: string): Promise<CachedCustomer | null> {
    const rows = await this.db.select<CustomerRow[]>(
      `SELECT * FROM cached_customer WHERE id = $1 LIMIT 1`,
      [id],
    );
    const row = rows[0];
    return row ? toCustomer(row) : null;
  }

  async cursor(): Promise<CustomerCursor | null> {
    const rows = await this.db.select<Array<{ since: string | null; since_id: string | null }>>(
      `SELECT since, since_id FROM customer_cursor WHERE id = 1`,
    );
    const row = rows[0];
    if (!row?.since || !row.since_id) return null;
    return { since: row.since, sinceId: row.since_id };
  }

  async setCursor(cursor: CustomerCursor): Promise<void> {
    await this.db.execute(
      `UPDATE customer_cursor SET since = $1, since_id = $2 WHERE id = 1`,
      [cursor.since, cursor.sinceId],
    );
  }

  async count(): Promise<number> {
    const rows = await this.db.select<Array<{ n: number }>>(
      `SELECT COUNT(*) AS n FROM cached_customer`,
    );
    return rows[0]?.n ?? 0;
  }
}

interface CustomerRow {
  id: string;
  code: string;
  name: string;
  name_ar: string;
  customer_type: string;
  phone: string;
  payment_terms_days: number;
  credit_limit: string;
  balance: string;
  available: string;
  is_active: number;
  updated_at: string;
}

function toCustomer(row: CustomerRow): CachedCustomer {
  return {
    id: row.id,
    code: row.code,
    name: row.name,
    nameAr: row.name_ar,
    customerType: row.customer_type,
    phone: row.phone,
    paymentTermsDays: row.payment_terms_days,
    creditLimit: row.credit_limit,
    balance: row.balance,
    available: row.available,
    isActive: row.is_active === 1,
    updatedAt: row.updated_at,
  };
}
