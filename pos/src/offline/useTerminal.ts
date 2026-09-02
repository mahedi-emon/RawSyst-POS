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
import { useT } from '@rawsyst/shared/i18n/locale';
import { pushBatch } from '../api/pos';
import { SaleQueue, type OfflineSalePayload, type QueueCounts } from './queue';
import { Catalogue } from './catalogue';
import { Customers } from './customers';
import { HeldCarts } from '../pos/held';
import {
  ConnectivityMonitor,
  DEFAULT_CONNECTIVITY,
  clientProber,
  type ConnectivityConfig,
  type ConnectivityState,
} from './connectivity';
import { openLocalStore } from './sqlite';
import { StockCache } from './stock';
import { useLive, type LiveMessage } from '@rawsyst/shared/live/useLive';
import { Stationery, receiptStationery, type ReceiptStationery } from './stationery';

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

  /** The shop's own words for a receipt (I2), as the till currently holds
   *  them. Always usable: a terminal that has never been online gets the
   *  RawSyst default rather than nothing. */
  stationery: ReceiptStationery;
  /** The customer book, so a sale can be attached to somebody offline. */
  customers: Customers | null;
  /** Carts parked mid-sale. Local only and never synced. */
  held: HeldCarts | null;

  /**
   * The till's cached view of the main stock ledger.
   *
   * Null until local storage opens. Holds nothing at all for a cashier
   * without `inventory.view`, which is a legitimate answer rather than a
   * fault — the till then says nothing about stock, exactly as it did
   * before this existed.
   *
   * A CACHE. It is stale the moment another till sells something, and
   * design 03 forbids it from refusing a sale. See offline/stock.ts.
   */
  stock: StockCache | null;

  record(payload: OfflineSalePayload): Promise<void>;
  flushNow(): Promise<void>;
  refreshCatalogue(): Promise<void>;
}

export function useTerminal(
  config: ConnectivityConfig = DEFAULT_CONNECTIVITY,
): TerminalState {
  const { client, status } = useAuth();
  const t = useT();

  const [queue, setQueue] = useState<SaleQueue | null>(null);
  // Why the local store could not be opened, when it could not. Null while it
  // is working, which is every till in a shop.
  const [storeFailure, setStoreFailure] = useState<string | null>(null);
  const [catalogue, setCatalogue] = useState<Catalogue | null>(null);
  const [stationery, setStationery] = useState<Stationery | null>(null);
  const [receiptWords, setReceiptWords] = useState<ReceiptStationery>(
    receiptStationery(null),
  );
  const [customers, setCustomers] = useState<Customers | null>(null);
  const [held, setHeld] = useState<HeldCarts | null>(null);
  const [stock, setStock] = useState<StockCache | null>(null);
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
        setCustomers(new Customers(stores.customers, client));

        // Loaded before any network is attempted, so the first receipt of the
        // day prints on the shop's own words even on a till that opened with
        // no connection.
        const st = new Stationery(stores.stationery);
        await st.load();
        setStationery(st);
        setReceiptWords(receiptStationery(st.current()));

        // Read off disk before any network is attempted, so a till that
        // opens with no connection still knows what it last agreed —
        // dated, so a screen can say how old the figure is rather than
        // present it as current.
        const stk = new StockCache(stores.stock);
        await stk.hydrate();
        setStock(stk);

        const h = new HeldCarts(stores.held);
        setHeld(h);
        // Sweep on open, so Monday's list is not last week's ghosts.
        void h.purgeExpired();

        setCounts(await q.counts());
        setCached(await c.size());
      })
      .catch((err: unknown) => {
        // Two very different situations reach here and this used to treat them
        // as one.
        //
        // A browser during development has no SQL plugin at all, and the
        // counter still works against the live API — nothing durable, nothing
        // cached, and saying so is better than pretending.
        //
        // An INSTALLED TILL reaching here cannot sell. The store is the queue a
        // sale is written to before a receipt prints, so its absence stops
        // trading, and the reason matters: a denied capability, a locked file
        // and a failed migration are three different problems with three
        // different answers. Swallowing the error left a shopkeeper with "this
        // terminal has no local storage" and nobody with any way to find out
        // why.
        if (cancelled) return;
        setQueue(null);
        setStoreFailure(err instanceof Error ? err.message : String(err));
        // Logged as well as kept, because the shop's own screen is not where a
        // developer reads a stack and the WebView2 console is.
        console.error('the terminal store could not be opened:', err);
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

      // The customer book rides along on the same schedule and the same
      // reconnect trigger. Its own timer would be a second thing to reason
      // about, and a till whose prices were fresh but whose credit figures
      // were a day old is a worse position than having both equally current.
      //
      // Awaited separately so a customer pull that fails does not discard a
      // catalogue pull that succeeded.
      try {
        await customers?.sync();
      } catch {
        // Same bargain as below: the book the till already holds is untouched.
      }

      // The stock figures ride along on the same schedule and the same
      // reconnect trigger, for the same reason the customer book does: a
      // till whose prices were fresh and whose quantities were a day old
      // is a worse position than having both equally current.
      //
      // A refusal is expected and silent. A cashier without
      // `inventory.view` gets one every time, and it is not a fault.
      try {
        await stock?.pull(client);
      } catch {
        // What the till holds is untouched, and every row is dated.
      }

      // The stationery rides along too. A failure leaves what the terminal was
      // already holding, which is the point: a till that loses the network
      // mid-shift keeps printing the shop's words rather than reverting to the
      // default in the middle of a queue of customers.
      try {
        if (stationery) {
          await stationery.refresh(client);
          setReceiptWords(receiptStationery(stationery.current()));
        }
      } catch {
        // Kept as held.
      }
    } catch {
      // Offline, or the server refused. The cache the till already holds is
      // untouched and still sellable, which is the entire point of holding it.
    } finally {
      pulling.current = false;
      setSyncing(false);
    }
  }, [catalogue, customers]);

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

  // The main stock ledger announcing that it moved.
  //
  // This is what keeps the cached quantities current BETWEEN pulls: another
  // till three metres away sells the last one, the server writes it to the one
  // ledger that is true, and this terminal hears about it in seconds rather
  // than at its next refresh.
  //
  // Best-effort by construction. A missed message is not a fault — the socket
  // reconnects, the next pull replaces everything, and design 03 forbids
  // anything from depending on the figure anyway. A delta for a variant this
  // till never pulled is ignored rather than invented; see offline/stock.ts.
  const onLive = useCallback(
    (m: LiveMessage) => {
      if (m.kind !== 'stock.moved' || !stock) return;
      const variantId = String(m.payload?.variant_id ?? '');
      const warehouseId = String(m.payload?.warehouse_id ?? '');
      const delta = String(m.payload?.delta ?? '');
      if (!variantId || !warehouseId || !delta) return;
      void stock.apply(variantId, warehouseId, delta);
    },
    [stock],
  );

  useLive(client, { enabled: signedIn && stock !== null, onMessage: onLive });

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
        // The reason, where there is one. A cashier cannot act on it and
        // the person they telephone can, and that person is not standing in
        // front of the screen.
        throw new Error(
          storeFailure
            ? t('till.noStoreWhy', { reason: storeFailure })
            : t('till.noStore'),
        );
      }
      await queue.record(payload);
      setCounts(await queue.counts());
      // Attempted immediately, but the sale is already safe either way. The
      // await is deliberately NOT on the caller's path to printing a receipt.
      void flushNow();
    },
    [queue, flushNow, storeFailure],
  );

  return {
    ready: queue !== null,
    counts,
    sending,
    network,
    cached,
    syncingCatalogue,
    catalogue,
    stationery: receiptWords,
    customers,
    held,
    stock,
    record,
    flushNow,
    refreshCatalogue,
  };
}
