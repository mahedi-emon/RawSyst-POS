// The offline queue.
//
// # Local first, always
//
// A finished sale is written to local storage BEFORE any network call, and the
// network is never on the path to completing one. That ordering is the whole
// offline-first design: a cashier presses Finish, the sale is durable, the
// receipt prints. Whether the server heard about it is a separate question
// answered later, possibly much later.
//
// Doing it the other way round — try the server, fall back to local — looks
// equivalent and is not. It makes every sale wait on a timeout when the
// network is merely slow, and it loses the sale entirely if the process dies
// between a successful send and the local write.
//
// # This queue duplicates no business logic
//
// It holds the payload the terminal recorded and hands it to
// POST /api/v1/sync/push. Pricing, costing, the invoice chain, stock and the
// journal are all the server's, reached through the sale service that an
// online sale uses too. The terminal decides WHAT was sold; it never decides
// what that means.
//
// # Sequence numbers are per terminal
//
// Each queued sale takes the next seq for this device. The server applies them
// in that order and stalls the rest of this terminal's queue if one fails,
// because the ZATCA chain is per terminal and a gap in it is the exact signal
// tamper detection looks for.

/** Anything that can hold rows durably. SQLite in the app; a map in tests. */
export interface QueueStore {
  nextSeq(): Promise<number>;
  put(entry: QueuedSale): Promise<void>;
  /** Unsent entries, oldest sequence first. */
  outstanding(limit: number): Promise<QueuedSale[]>;
  markSettled(seqs: number[], state: SettledState): Promise<void>;
  markFailed(seq: number, reason: string): Promise<void>;
  counts(): Promise<QueueCounts>;
}

export type SettledState = 'applied' | 'duplicate';

export interface QueueCounts {
  pending: number;
  failed: number;
}

/** One sale as the terminal recorded it. */
export interface QueuedSale {
  seq: number;
  /** Assigned on THIS device before any network call. It is what makes a
   *  retry idempotent: the same sale arriving twice carries the same id and
   *  the server recognises it rather than ringing it up again. */
  invoiceUuid: string;
  payload: OfflineSalePayload;
  state: 'pending' | 'applied' | 'duplicate' | 'failed';
  error?: string;
  recordedAt: string;
}

/** The shape POST /api/v1/sync/push expects, per the backend's applier. */
export interface OfflineSalePayload {
  invoice_uuid: string;
  doc_type: string;
  issued_at: string;
  cashier_id?: string;
  warehouse_id?: string;
  prices_include_tax?: boolean;
  invoice_discount?: string;
  lines: Array<{
    variant_id: string;
    description: string;
    description_ar?: string;
    qty: string;
    unit_price: string;
    line_discount?: string;
    tax_treatment: string;
  }>;
  tenders: Array<{ method: string; amount: string; reference?: string }>;
}

/** What a push attempt reported, per item. */
interface PushItemResult {
  seq: number;
  state: 'applied' | 'duplicate' | 'failed' | 'blocked';
  error?: string;
}

interface PushResult {
  applied: number;
  duplicates: number;
  failed: number;
  blocked: number;
  cursor: number;
  items: PushItemResult[];
}

/** Sends batches to the server. Injected so the queue is testable offline. */
export interface Pusher {
  push(
    idempotencyKey: string,
    items: Array<{
      seq: number;
      entity_uuid: string;
      entity_type: string;
      payload: OfflineSalePayload;
    }>,
  ): Promise<PushResult>;
}

export class SaleQueue {
  constructor(
    private readonly store: QueueStore,
    private readonly pusher: Pusher,
  ) {}

  /**
   * Records a finished sale locally. Never touches the network.
   *
   * Returns once the sale is durable, which is the moment a receipt may be
   * printed and a customer may leave.
   */
  async record(payload: OfflineSalePayload): Promise<QueuedSale> {
    const seq = await this.store.nextSeq();
    const entry: QueuedSale = {
      seq,
      invoiceUuid: payload.invoice_uuid,
      payload,
      state: 'pending',
      recordedAt: new Date().toISOString(),
    };
    await this.store.put(entry);
    return entry;
  }

  /**
   * Sends what is outstanding, oldest first.
   *
   * Safe to call at any time and as often as one likes: the server
   * deduplicates on the invoice id, so a sale already applied comes back as a
   * duplicate rather than being rung up twice. That is why this can run on a
   * timer without any locking.
   */
  async flush(batchSize = 50): Promise<{ sent: number; result?: PushResult }> {
    const outstanding = await this.store.outstanding(batchSize);
    if (outstanding.length === 0) return { sent: 0 };

    // The batch key is derived from what is IN the batch, so a retry of the
    // same contents is recognised as the same batch. A random key each time
    // would make every retry look like new work to the batch bookkeeping —
    // harmless for the sales themselves, which the invoice id protects, but it
    // would tell the cashier their takings had landed twice.
    const idempotencyKey = batchKey(outstanding);

    const result = await this.pusher.push(
      idempotencyKey,
      outstanding.map((e) => ({
        seq: e.seq,
        entity_uuid: e.invoiceUuid,
        entity_type: 'sales_invoice',
        payload: e.payload,
      })),
    );

    const settled: Record<SettledState, number[]> = { applied: [], duplicate: [] };
    for (const item of result.items ?? []) {
      if (item.state === 'applied' || item.state === 'duplicate') {
        settled[item.state].push(item.seq);
      } else if (item.state === 'failed') {
        // Kept, not discarded. A failed sale is real takings the server has
        // refused for a reason someone must see — dropping it would lose money
        // silently and leave a gap in this terminal's chain.
        await this.store.markFailed(item.seq, item.error ?? 'The server refused this sale.');
      }
      // 'blocked' stays pending: an earlier sale has not landed, and this one
      // must not jump ahead of it.
    }

    for (const state of ['applied', 'duplicate'] as const) {
      if (settled[state].length > 0) {
        await this.store.markSettled(settled[state], state);
      }
    }

    return { sent: outstanding.length, result };
  }

  counts(): Promise<QueueCounts> {
    return this.store.counts();
  }
}

/**
 * A stable key for a batch's contents.
 *
 * The same sales in the same order produce the same key, so a retry after a
 * lost response is recognised. Built from the sequence range and the invoice
 * ids rather than a hash, because it has to be reproducible on a device that
 * may have restarted in between.
 */
export function batchKey(entries: QueuedSale[]): string {
  const first = entries[0];
  const last = entries[entries.length - 1];
  if (!first || !last) return 'empty';
  return `${first.seq}-${last.seq}:${first.invoiceUuid.slice(0, 8)}:${entries.length}`;
}
