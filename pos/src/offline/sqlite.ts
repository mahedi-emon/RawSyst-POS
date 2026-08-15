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
export async function openLocalStore(): Promise<SqliteQueueStore> {
  const db = await Database.load('sqlite:rawsyst-pos.db');
  for (const statement of SCHEMA.split(';')) {
    const sql = statement.trim();
    if (sql) await db.execute(sql);
  }
  return new SqliteQueueStore(db);
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
