// The offline queue.
//
// What must hold: a sale is durable before anything touches the network, a
// retry never rings it up twice, a failed sale is kept rather than dropped,
// and a blocked one stays in line rather than jumping ahead.

import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  SaleQueue,
  batchKey,
  type OfflineSalePayload,
  type Pusher,
  type QueueCounts,
  type QueueStore,
  type QueuedSale,
  type SettledState,
} from './queue';

/** An in-memory store, so the queue's behaviour is tested without SQLite. */
class MemoryStore implements QueueStore {
  rows: QueuedSale[] = [];
  private seq = 0;

  async nextSeq() {
    return ++this.seq;
  }
  async put(entry: QueuedSale) {
    this.rows.push({ ...entry });
  }
  async outstanding(limit: number) {
    return this.rows
      .filter((r) => r.state === 'pending')
      .sort((a, b) => a.seq - b.seq)
      .slice(0, limit)
      .map((r) => ({ ...r }));
  }
  async markSettled(seqs: number[], state: SettledState) {
    for (const r of this.rows) if (seqs.includes(r.seq)) r.state = state;
  }
  async markFailed(seq: number, reason: string) {
    for (const r of this.rows)
      if (r.seq === seq) {
        r.state = 'failed';
        r.error = reason;
      }
  }
  async counts(): Promise<QueueCounts> {
    return {
      pending: this.rows.filter((r) => r.state === 'pending').length,
      failed: this.rows.filter((r) => r.state === 'failed').length,
    };
  }
}

function sale(uuid: string): OfflineSalePayload {
  return {
    invoice_uuid: uuid,
    doc_type: 'simplified',
    issued_at: '2026-08-16T10:00:00Z',
    lines: [
      {
        variant_id: 'v1',
        description: 'Abaya',
        qty: '1',
        unit_price: '115.00',
        tax_treatment: 'standard',
      },
    ],
    tenders: [{ method: 'cash', amount: '115.00' }],
  };
}

/** A pusher that answers however a test needs, and records what it saw. */
function pusherReturning(
  reply: (items: Array<{ seq: number }>) => Parameters<Pusher['push']> extends never
    ? never
    : Awaited<ReturnType<Pusher['push']>>,
): Pusher & { calls: Array<{ key: string; seqs: number[] }> } {
  const calls: Array<{ key: string; seqs: number[] }> = [];
  return {
    calls,
    async push(key, items) {
      calls.push({ key, seqs: items.map((i) => i.seq) });
      return reply(items);
    },
  };
}

const allApplied = (items: Array<{ seq: number }>) => ({
  applied: items.length,
  duplicates: 0,
  failed: 0,
  blocked: 0,
  cursor: items[items.length - 1]?.seq ?? 0,
  items: items.map((i) => ({ seq: i.seq, state: 'applied' as const })),
});

describe('recording a sale', () => {
  let store: MemoryStore;

  beforeEach(() => {
    store = new MemoryStore();
  });

  it('is durable before the network is touched', async () => {
    // The pusher throws if called. Recording must not reach it: a cashier
    // presses Finish and the sale is safe, network or no network.
    const pusher: Pusher = {
      push: vi.fn(async () => {
        throw new Error('the network must not be on the path to finishing a sale');
      }),
    };
    const queue = new SaleQueue(store, pusher);

    const entry = await queue.record(sale('inv-1'));

    expect(entry.state).toBe('pending');
    expect(store.rows).toHaveLength(1);
    expect(pusher.push).not.toHaveBeenCalled();
  });

  it('numbers sales per terminal, in order', async () => {
    const queue = new SaleQueue(store, pusherReturning(allApplied));
    const a = await queue.record(sale('inv-1'));
    const b = await queue.record(sale('inv-2'));
    const c = await queue.record(sale('inv-3'));

    // The server applies in this order and stalls the rest if one fails,
    // because the ZATCA chain is per terminal.
    expect([a.seq, b.seq, c.seq]).toEqual([1, 2, 3]);
  });
});

describe('flushing', () => {
  let store: MemoryStore;

  beforeEach(() => {
    store = new MemoryStore();
  });

  it('sends nothing when there is nothing outstanding', async () => {
    const pusher = pusherReturning(allApplied);
    const queue = new SaleQueue(store, pusher);

    const { sent } = await queue.flush();

    expect(sent).toBe(0);
    expect(pusher.calls).toHaveLength(0);
  });

  it('sends outstanding sales oldest first', async () => {
    const pusher = pusherReturning(allApplied);
    const queue = new SaleQueue(store, pusher);
    await queue.record(sale('inv-1'));
    await queue.record(sale('inv-2'));

    await queue.flush();

    expect(pusher.calls[0]?.seqs).toEqual([1, 2]);
  });

  it('does not resend what has already landed', async () => {
    const pusher = pusherReturning(allApplied);
    const queue = new SaleQueue(store, pusher);
    await queue.record(sale('inv-1'));

    await queue.flush();
    const second = await queue.flush();

    expect(second.sent).toBe(0);
    expect(pusher.calls).toHaveLength(1);
  });

  it('treats a duplicate as settled, not as a problem', async () => {
    // The expected outcome of a healthy retry: the server already had this
    // sale. Leaving it pending would make the terminal resend it forever.
    const pusher = pusherReturning((items) => ({
      applied: 0,
      duplicates: items.length,
      failed: 0,
      blocked: 0,
      cursor: items[items.length - 1]?.seq ?? 0,
      items: items.map((i) => ({ seq: i.seq, state: 'duplicate' as const })),
    }));
    const queue = new SaleQueue(store, pusher);
    await queue.record(sale('inv-1'));

    await queue.flush();

    expect((await queue.counts()).pending).toBe(0);
    expect((await queue.counts()).failed).toBe(0);
  });

  it('keeps a refused sale rather than discarding it', async () => {
    // Real takings the server refused. Dropping it would lose money silently
    // and leave a gap in this terminal's chain.
    const pusher = pusherReturning((items) => ({
      applied: 0,
      duplicates: 0,
      failed: items.length,
      blocked: 0,
      cursor: 0,
      items: items.map((i) => ({
        seq: i.seq,
        state: 'failed' as const,
        error: 'The payments come to 114.99 against a total of 115.',
      })),
    }));
    const queue = new SaleQueue(store, pusher);
    await queue.record(sale('inv-1'));

    await queue.flush();

    const counts = await queue.counts();
    expect(counts.failed).toBe(1);
    expect(store.rows[0]?.error).toContain('114.99');
  });

  it('leaves a blocked sale in line rather than dropping or settling it', async () => {
    // An earlier sale has not landed. This one must not jump ahead of it, and
    // must still be there to retry once the earlier one is fixed.
    const pusher = pusherReturning((items) => ({
      applied: 1,
      duplicates: 0,
      failed: 1,
      blocked: items.length - 2,
      cursor: 1,
      items: [
        { seq: 1, state: 'applied' as const },
        { seq: 2, state: 'failed' as const, error: 'underpaid' },
        ...items.slice(2).map((i) => ({ seq: i.seq, state: 'blocked' as const })),
      ],
    }));
    const queue = new SaleQueue(store, pusher);
    for (const id of ['inv-1', 'inv-2', 'inv-3', 'inv-4']) {
      await queue.record(sale(id));
    }

    await queue.flush();

    const counts = await queue.counts();
    expect(counts.failed).toBe(1);
    // Two still pending: they were blocked, not settled and not lost.
    expect(counts.pending).toBe(2);
  });

  it('sends the same batch under the same key when retried', async () => {
    // A retry of identical contents must be recognised as the same batch, or
    // the cashier is told their takings landed twice.
    const failing: Pusher = {
      push: vi.fn(async () => {
        throw new Error('network down');
      }),
    };
    const queue = new SaleQueue(store, failing);
    await queue.record(sale('inv-1'));
    await queue.record(sale('inv-2'));

    await expect(queue.flush()).rejects.toThrow('network down');
    await expect(queue.flush()).rejects.toThrow('network down');

    const keys = (failing.push as ReturnType<typeof vi.fn>).mock.calls.map(
      (c) => c[0],
    );
    expect(keys[0]).toBe(keys[1]);
  });

  it('respects the batch size so a week offline does not arrive at once', async () => {
    const pusher = pusherReturning(allApplied);
    const queue = new SaleQueue(store, pusher);
    for (let i = 0; i < 10; i++) await queue.record(sale(`inv-${i}`));

    await queue.flush(4);

    expect(pusher.calls[0]?.seqs).toHaveLength(4);
    expect((await queue.counts()).pending).toBe(6);
  });
});

describe('batch keys', () => {
  it('are stable for the same contents', () => {
    const rows: QueuedSale[] = [
      {
        seq: 1,
        invoiceUuid: 'aaaaaaaa-1111',
        payload: sale('aaaaaaaa-1111'),
        state: 'pending',
        recordedAt: '',
      },
      {
        seq: 2,
        invoiceUuid: 'bbbbbbbb-2222',
        payload: sale('bbbbbbbb-2222'),
        state: 'pending',
        recordedAt: '',
      },
    ];
    expect(batchKey(rows)).toBe(batchKey([...rows]));
  });

  it('differ when the contents differ', () => {
    const one: QueuedSale = {
      seq: 1,
      invoiceUuid: 'aaaaaaaa-1111',
      payload: sale('aaaaaaaa-1111'),
      state: 'pending',
      recordedAt: '',
    };
    const two: QueuedSale = { ...one, seq: 9, invoiceUuid: 'cccccccc-3333' };
    expect(batchKey([one])).not.toBe(batchKey([two]));
  });
});
