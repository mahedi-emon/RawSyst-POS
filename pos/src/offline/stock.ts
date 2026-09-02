// The till's cached view of the main stock ledger.
//
// # What is true, and what this is
//
// The stock ledger on the server is the single source of truth. Every sale —
// rung up online, or queued offline and replayed on reconnect — eventually
// lands in that one ledger, and it is the ledger that says what a shop owns.
//
// This is a CACHE of part of it: the quantities for one warehouse, as the
// server last agreed them. It is stale the moment another till sells
// something, and it is brought back into line two ways:
//
//   - a full pull when the terminal comes online, which replaces what is held;
//   - a `stock.moved` delta while it stays online, which nudges one row.
//
// # It never decides whether a sale may happen
//
// Design 03 is explicit: an offline till cannot be prevented from overselling,
// and the product chooses accurate detection over false confidence. A cached
// figure refusing a real customer at the counter is exactly the false
// confidence that rules out — the cache can be an hour old, or belong to the
// wrong warehouse, or have missed a delta while the socket reconnected.
//
// So `shortfall` below returns something to SAY, never something to enforce.
// The server is the only thing that decides whether the stock was really
// there, and it records a shortfall on the sale when it was not.
//
// # A delta is not a value
//
// `applyDelta` refuses to create a row it does not already hold. A delta says
// how much a quantity moved, not what it became, so applying one to a variant
// this till never pulled would invent a figure out of nothing — and an
// invented quantity is worse than an absent one, because a screen will show it
// as confidently as a real one.

import type { Client } from '@rawsyst/shared/api/client';

import type { CachedStock, SqliteStockStore } from './sqlite';

/** One line of the authoritative on-hand read. */
interface OnHandWire {
  variant_id?: string;
  on_hand?: string;
  reorder_level?: string;
}

/**
 * What GET /api/v1/pos/stock answers.
 *
 * The warehouse comes BACK rather than going out: a till does not know which
 * one it sells from, and every POS route resolves that from the device. See
 * handleTerminalStock.
 */
interface TerminalStockWire {
  warehouse_id?: string;
  lines?: OnHandWire[];
}

/** How the cache stands, for a screen that wants to say so. */
export interface StockCacheState {
  /** False when this till has never managed a pull — no figures at all. */
  loaded: boolean;
  /** When the last full pull agreed with the server, ISO 8601. */
  asOf: string | null;
  /** How many variants are held. */
  count: number;
}

/**
 * What to tell a cashier about an item they are adding.
 *
 * `null` means say nothing: either the cache holds no opinion, or the opinion
 * is unremarkable.
 */
export interface StockWarning {
  kind: 'none-left' | 'not-enough' | 'below-reorder';
  /** What the cache believes is on the shelf. */
  onHand: string;
  /** When it last agreed with the server, so the warning can date itself. */
  asOf: string;
}

export class StockCache {
  private loaded = false;
  private lastPull: string | null = null;
  private held = new Map<string, CachedStock>();

  /**
   * Which warehouse these figures are for, once the server has said.
   *
   * Learned rather than configured: a till does not know which location it
   * sells from and is not told, because a terminal that could name its own
   * would be a terminal that could read another branch's figures. The server
   * resolves it from the device and returns it, exactly as it resolves the
   * company for a sale.
   */
  private warehouseId: string | null = null;

  constructor(private readonly store: SqliteStockStore) {}

  /** Reads what is already on disk, so a till works before its first pull. */
  async hydrate(): Promise<void> {
    const rows = await this.store.everything();
    this.held = new Map(rows.map((r) => [r.variantId, r]));
    this.warehouseId = rows[0]?.warehouseId ?? null;
    this.loaded = rows.length > 0;
    this.lastPull =
      rows.reduce<string | null>(
        (latest, r) => (!latest || r.asOf > latest ? r.asOf : latest),
        null,
      ) ?? null;
  }

  /**
   * Pulls the authoritative figures for this terminal's own warehouse.
   *
   * Refused by the server for a cashier without `inventory.view`, and that is
   * a legitimate answer rather than a fault: the till simply holds no cached
   * quantity and says nothing about stock, exactly as it did before this
   * existed. So a refusal leaves what is held alone and reports false.
   */
  async pull(client: Client): Promise<boolean> {
    let body: TerminalStockWire;
    try {
      body = await client.send<TerminalStockWire>('GET', '/api/v1/pos/stock');
    } catch {
      // Offline, or not allowed. Neither is worth surfacing: what the till
      // already holds is still the best it has, and every row carries the date
      // it was true.
      return false;
    }

    const warehouseId = body.warehouse_id;
    if (!warehouseId) return false;

    // A till moved between branches sells out of a different location, and the
    // quantities it was holding describe somewhere else. Cleared rather than
    // merged: two warehouses' figures under one variant would be a number that
    // is true of nowhere.
    if (this.warehouseId && this.warehouseId !== warehouseId) {
      await this.store.clear();
      this.held.clear();
    }
    this.warehouseId = warehouseId;

    const at = new Date().toISOString();
    const lines: CachedStock[] = [];
    for (const row of body.lines ?? []) {
      if (!row.variant_id) continue;
      lines.push({
        variantId: row.variant_id,
        warehouseId,
        onHand: row.on_hand ?? '0',
        reorderLevel: row.reorder_level ?? '',
        asOf: at,
      });
    }

    await this.store.put(lines);
    this.held = new Map(lines.map((l) => [l.variantId, l]));
    this.loaded = true;
    this.lastPull = at;
    return true;
  }

  /**
   * Applies a `stock.moved` announcement.
   *
   * Ignored for another warehouse and for a variant this till does not hold —
   * see the note at the top on why a delta cannot create a row.
   */
  async apply(
    variantId: string,
    warehouseId: string,
    delta: string,
  ): Promise<void> {
    if (!this.warehouseId || warehouseId !== this.warehouseId) return;
    if (!this.held.has(variantId)) return;

    const at = new Date().toISOString();
    if (!(await this.store.applyDelta(variantId, warehouseId, delta, at))) {
      return;
    }
    const fresh = await this.store.get(variantId, warehouseId);
    if (fresh) this.held.set(variantId, fresh);
  }

  /** What the cache believes, or null when it holds no opinion. */
  onHand(variantId: string): CachedStock | null {
    return this.held.get(variantId) ?? null;
  }

  state(): StockCacheState {
    return { loaded: this.loaded, asOf: this.lastPull, count: this.held.size };
  }

  /**
   * What to warn about when `wanted` of a variant is going into the cart.
   *
   * Returns something to SAY, never something to enforce. See the note at the
   * top: the sale proceeds whatever this returns.
   */
  shortfall(variantId: string, wanted: string): StockWarning | null {
    const held = this.held.get(variantId);
    if (!held) return null;

    const onHand = Number(held.onHand);
    const need = Number(wanted);
    if (!Number.isFinite(onHand) || !Number.isFinite(need)) return null;

    // Comparisons only — never arithmetic that is stored. A float is good
    // enough to decide "is this less than that" for shop quantities, and the
    // number that gets KEPT is added as a decimal string in sqlite.ts.
    if (onHand <= 0) {
      return { kind: 'none-left', onHand: held.onHand, asOf: held.asOf };
    }
    if (onHand < need) {
      return { kind: 'not-enough', onHand: held.onHand, asOf: held.asOf };
    }

    const reorder = Number(held.reorderLevel);
    if (Number.isFinite(reorder) && reorder > 0 && onHand - need < reorder) {
      return { kind: 'below-reorder', onHand: held.onHand, asOf: held.asOf };
    }
    return null;
  }
}
