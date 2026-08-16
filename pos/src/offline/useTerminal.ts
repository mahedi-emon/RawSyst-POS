// Everything the terminal keeps and does on its own.
//
// One hook, because the three things are not independent: the connectivity
// monitor is what tells the queue the network is back, and the catalogue pull
// is only worth attempting when it is. Wiring them separately would mean each
// discovering the network state for itself, three times over.
//
// # Network status is not queue status
//
// They are reported separately and they mean different things. `network` is
// whether the server is reachable right now. `queue.counts` is how many sales
// this till is still holding. A terminal can be online with a backlog (still
// draining), or offline with nothing pending (a quiet morning), and a cashier
// closing up needs to distinguish those — collapsing them into one indicator
// would make "all sent" and "no signal" look identical.
//
// # Nothing here is on the selling path
//
// `record` writes to SQLite and returns. The flush it kicks off afterwards is
// deliberately not awaited, and the connectivity monitor is never consulted
// before a sale is recorded. A probe hanging for its full timeout delays no
// cashier.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { useAuth } from '@rawsyst/shared/auth/session';
import { pushBatch } from '../api/pos';
import { SaleQueue, type OfflineSalePayload, type QueueCounts } from './queue';
import { Catalogue } from './catalogue';
import { HeldCarts } from '../pos/held';
import {
  ConnectivityMonitor,
  DEFAULT_CONNECTIVITY,
  clientProber,
  type ConnectivityConfig,
  type ConnectivityState,
} from './connectivity';
import { openLocalStore } from './sqlite';

/** The backstop flush. The connectivity monitor triggers a drain the moment
 *  the server returns, so this only covers the case where a push failed for a
 *  reason that was never a network problem. */
const FLUSH_EVERY_MS = 60_000;

/** How often to refresh the catalogue while online. Prices change rarely and
 *  the pull is a delta, so this is cheap — but a shop that repriced at 9am
 *  should not still be selling yesterday's prices at noon. */
const CATALOGUE_EVERY_MS = 15 * 60_000;

export interface TerminalState {
  /** Local storage is open. False in a browser during development. */
  ready: boolean;

  /** What this till is still holding. */
  counts: QueueCounts;
  sending: boolean;

  /** Whether the server is reachable. Separate from the counts above. */
  network: ConnectivityState;

  /** How many variants can be sold with no network at all. */
  cached: number;
  /** True while the catalogue is being pulled. */
  syncingCatalogue: boolean;

  catalogue: Catalogue | null;
  /** Carts parked mid-sale. Local only and never synced. */
  held: HeldCarts | null;

  record(payload: OfflineSalePayload): Promise<void>;
  flushNow(): Promise<void>;
  refreshCatalogue(): Promise<void>;
}

export function useTerminal(
  config: ConnectivityConfig = DEFAULT_CONNECTIVITY,
): TerminalState {
  const { client, status } = useAuth();

  const [queue, setQueue] = useState<SaleQueue | null>(null);
  const [catalogue, setCatalogue] = useState<Catalogue | null>(null);
  const [held, setHeld] = useState<HeldCarts | null>(null);
  const [counts, setCounts] = useState<QueueCounts>({ pending: 0, failed: 0 });
  const [sending, setSending] = useState(false);
  const [cached, setCached] = useState(0);
  const [syncingCatalogue, setSyncing] = useState(false);
  const [network, setNetwork] = useState<ConnectivityState>({
    reachable: false,
    checked: false,
    unauthenticated: false,
    failures: 0,
    lastCheckedAt: null,
  });

  // Guards against two flushes overlapping. Without it, a slow push and the
  // monitor's reconnect trigger would send the same sales twice — harmless,
  // because the server deduplicates, but it doubles the traffic on the worst
  // network, which is exactly when it can least afford it.
  const flushing = useRef(false);
  const pulling = useRef(false);

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
      .then(async (stores) => {
        if (cancelled) return;
        const q = new SaleQueue(stores.queue, pusher);
        const c = new Catalogue(stores.catalogue, client);
        setQueue(q);
        setCatalogue(c);

        const h = new HeldCarts(stores.held);
        setHeld(h);
        // Sweep on open, so Monday's list is not last week's ghosts.
        void h.purgeExpired();

        setCounts(await q.counts());
        setCached(await c.size());
      })
      .catch(() => {
        // Running outside Tauri, in a browser during development. The counter
        // still works against the live API; nothing is durable and nothing is
        // cached. Better to say so than to pretend.
        if (!cancelled) setQueue(null);
      });
    return () => {
      cancelled = true;
    };
  }, [pusher, client]);

  const flushNow = useCallback(async () => {
    if (!queue || flushing.current) return;
    flushing.current = true;
    setSending(true);
    try {
      await queue.flush();
    } catch {
      // A transport failure, not a refusal. Nothing is lost: the sales are
      // still queued, and the connectivity monitor is the thing that decides
      // when to try again — this handler does not need an opinion.
    } finally {
      flushing.current = false;
      setSending(false);
      setCounts(await queue.counts());
    }
  }, [queue]);

  const refreshCatalogue = useCallback(async () => {
    if (!catalogue || pulling.current) return;
    pulling.current = true;
    setSyncing(true);
    try {
      await catalogue.sync();
      setCached(await catalogue.size());
    } catch {
      // Offline, or the server refused. The cache the till already holds is
      // untouched and still sellable, which is the entire point of holding it.
    } finally {
      pulling.current = false;
      setSyncing(false);
    }
  }, [catalogue]);

  // Both callbacks live in a ref so the monitor can be built once and still
  // call the current versions. Rebuilding the monitor whenever a callback
  // changed identity would reset the backoff on every render — a till would
  // probe at the base interval forever and never actually back off.
  const onRestored = useRef<() => void>(() => {});
  useEffect(() => {
    onRestored.current = () => {
      void flushNow();
      void refreshCatalogue();
    };
  }, [flushNow, refreshCatalogue]);

  // Probing only once signed in. A monitor running against a signed-out
  // client would report "unreachable" for the entirely wrong reason, and the
  // login screen has its own, more direct, way of finding out.
  const signedIn = status === 'signed_in';

  useEffect(() => {
    if (!signedIn) return;

    const monitor = new ConnectivityMonitor(
      clientProber(client),
      config,
      () => onRestored.current(),
    );
    const unsubscribe = monitor.subscribe(setNetwork);
    monitor.start();

    // The OS event is a HINT to probe now, not an answer. It fires when an
    // interface comes up, which on shop wifi routinely precedes the uplink
    // being usable by several seconds — and it never fires at all when the
    // uplink dies while the interface stays up, which is the failure that
    // actually strands a till.
    const hint = () => void monitor.check();
    window.addEventListener('online', hint);

    return () => {
      window.removeEventListener('online', hint);
      unsubscribe();
      monitor.stop();
    };
  }, [signedIn, client, config]);

  // The backstop. Reconnection is handled by the monitor, so this is slow on
  // purpose: it exists for a push that failed for some reason other than the
  // network, where no connectivity transition will ever come to retry it.
  useEffect(() => {
    if (!queue) return;
    const timer = setInterval(() => void flushNow(), FLUSH_EVERY_MS);
    void flushNow();
    return () => clearInterval(timer);
  }, [queue, flushNow]);

  // First pull on startup, then periodically. A terminal set up this morning
  // has an empty cache and cannot scan anything until this lands.
  useEffect(() => {
    if (!catalogue) return;
    const timer = setInterval(() => void refreshCatalogue(), CATALOGUE_EVERY_MS);
    void refreshCatalogue();
    return () => clearInterval(timer);
  }, [catalogue, refreshCatalogue]);

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

  return {
    ready: queue !== null,
    counts,
    sending,
    network,
    cached,
    syncingCatalogue,
    catalogue,
    held,
    record,
    flushNow,
    refreshCatalogue,
  };
}
