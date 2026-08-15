// The queue, as the interface sees it.
//
// Opens the local database once, keeps a flush running on a timer, and exposes
// the counts a cashier needs. The timer is why a till that was offline all
// morning drains itself when the network returns without anyone pressing
// anything — reconnection is not an event the app waits for, it is simply the
// next attempt succeeding.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { useAuth } from '../auth/session';
import { pushBatch } from '../api/pos';
import { SaleQueue, type OfflineSalePayload, type QueueCounts } from './queue';
import { openLocalStore } from './sqlite';

/** How often to try. Short enough that a reconnection is noticed quickly,
 *  long enough that a till with no network is not retrying constantly. */
const FLUSH_EVERY_MS = 15_000;

export interface QueueState {
  ready: boolean;
  counts: QueueCounts;
  /** True while a push is in flight, so the UI can say "sending" honestly. */
  sending: boolean;
  /** Whether the last attempt reached the server at all. */
  online: boolean;

  record(payload: OfflineSalePayload): Promise<void>;
  flushNow(): Promise<void>;
}

export function useQueue(): QueueState {
  const { client } = useAuth();

  const [queue, setQueue] = useState<SaleQueue | null>(null);
  const [counts, setCounts] = useState<QueueCounts>({ pending: 0, failed: 0 });
  const [sending, setSending] = useState(false);
  const [online, setOnline] = useState(true);

  // Guards against two flushes overlapping. Without it a slow push and the
  // timer's next tick would send the same sales twice — harmless, because the
  // server deduplicates, but it doubles the traffic on the worst network.
  const flushing = useRef(false);

  const pusher = useMemo(
    () => ({
      push: (key: string, items: Parameters<typeof pushBatch>[2]) =>
        pushBatch(client, key, items),
    }),
    [client],
  );

  useEffect(() => {
    let cancelled = false;
    openLocalStore()
      .then((store) => {
        if (cancelled) return;
        const q = new SaleQueue(store, pusher);
        setQueue(q);
        return q.counts().then(setCounts);
      })
      .catch(() => {
        // Running outside Tauri, in a browser during development. The counter
        // still works; nothing is durable. Better to say so than to pretend.
        if (!cancelled) setQueue(null);
      });
    return () => {
      cancelled = true;
    };
  }, [pusher]);

  const flushNow = useCallback(async () => {
    if (!queue || flushing.current) return;
    flushing.current = true;
    setSending(true);
    try {
      await queue.flush();
      setOnline(true);
    } catch {
      // A transport failure, not a refusal. Nothing is lost: the sales are
      // still queued and the next tick tries again.
      setOnline(false);
    } finally {
      flushing.current = false;
      setSending(false);
      setCounts(await queue.counts());
    }
  }, [queue]);

  useEffect(() => {
    if (!queue) return;
    const timer = setInterval(() => void flushNow(), FLUSH_EVERY_MS);
    void flushNow();
    return () => clearInterval(timer);
  }, [queue, flushNow]);

  const record = useCallback(
    async (payload: OfflineSalePayload) => {
      if (!queue) {
        throw new Error(
          'This terminal has no local storage, so a sale cannot be recorded safely.',
        );
      }
      await queue.record(payload);
      setCounts(await queue.counts());
      // Attempted immediately, but the sale is already safe either way. The
      // await is deliberately NOT on the caller's path to printing a receipt.
      void flushNow();
    },
    [queue, flushNow],
  );

  return { ready: queue !== null, counts, sending, online, record, flushNow };
}
