// Is the server actually reachable?
//
// Not the same question as `navigator.onLine`, which answers "does this
// machine have a network interface that thinks it is up". A till on a shop
// wifi whose uplink is dead, or behind a captive portal at a mall, or pointed
// at a RawSyst deployment that is itself down, reports `true` throughout. Two
// of those are ordinary Saturday-afternoon conditions in retail.
//
// So we ask the server directly, and we ask it the useful question. The probe
// hits GET /api/v1/meta/ping, which is authenticated: what a till needs to
// know before it drains a day of takings is not "is there a network" but "can
// I sync right now", and those differ exactly when it matters — an expired
// session is unreachable for every purpose the terminal has.
//
// # What it must not do
//
// It must not poll hard. One probe per second per terminal, times every till
// in every shop, is a denial of service the vendor pays for. Online, the
// interval is slow; offline, it backs off; and `navigator.onLine` flipping is
// used as a *hint to probe now*, not as an answer.
//
// It must not touch a business route. Probing with a product search would put
// real query load on the database purely to learn whether a socket opens.
//
// It must never sit between a cashier and a completed sale. Nothing here is
// awaited on the selling path: the queue writes to SQLite and returns, and
// this monitor is only ever consulted for what it already knows. A probe that
// hangs for its full timeout delays nothing.

/** Configuration. Every timing is injectable — a shop on satellite backhaul
 *  and a shop on fibre should not be forced onto the same cadence, and the
 *  tests need to drive it deterministically. */
export interface ConnectivityConfig {
  /** Gap between probes while reachable. */
  onlineIntervalMs: number;
  /** First gap after losing the server. Doubles from here. */
  offlineBaseMs: number;
  /** The ceiling that doubling stops at. */
  offlineMaxMs: number;
  /** How long a probe may take before it counts as unreachable. A till with
   *  no network fails fast; this bounds the slow-but-alive case. */
  timeoutMs: number;
}

export const DEFAULT_CONNECTIVITY: ConnectivityConfig = {
  // Half a minute online: fast enough that a returning connection drains the
  // queue while the customer is still at the counter, slow enough to be
  // nothing on a server that also handles selling.
  onlineIntervalMs: 30_000,
  offlineBaseMs: 5_000,
  // Five minutes at the top. An outage lasting hours should not produce
  // thousands of probes, and the browser/OS online event covers the case where
  // the network returns mid-wait.
  offlineMaxMs: 300_000,
  timeoutMs: 5_000,
};

export interface ConnectivityState {
  /** Whether the last probe reached the server AND was accepted. */
  reachable: boolean;
  /** False before the first probe settles. Distinguishes "we know we are
   *  offline" from "we have not looked yet", which the UI shows differently:
   *  claiming offline during startup would alarm a cashier for no reason. */
  checked: boolean;
  /** Set when the server answered but rejected us — an expired session, not a
   *  dead network. Reachable is false either way, but only one is fixed by
   *  signing in again, and the cashier deserves to be told which. */
  unauthenticated: boolean;
  /** Consecutive failures. Drives the backoff and is worth showing. */
  failures: number;
  /** When the last probe settled, however it settled. */
  lastCheckedAt: number | null;
}

export type ConnectivityListener = (state: ConnectivityState) => void;

/** What the monitor needs to make a request. Deliberately not the full Client:
 *  the probe is a bare fetch with its own timeout, and it must not inherit any
 *  retry or offline-wrapping behaviour that the business client applies. */
export interface Prober {
  /** Resolves `ok` when the server answered acceptably. `authenticated: false`
   *  means it answered and refused. Rejects only on transport failure. */
  probe(signal: AbortSignal): Promise<{ ok: boolean; authenticated: boolean }>;
}

export class ConnectivityMonitor {
  private state: ConnectivityState = {
    reachable: false,
    checked: false,
    unauthenticated: false,
    failures: 0,
    lastCheckedAt: null,
  };

  private listeners = new Set<ConnectivityListener>();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private inFlight = false;
  private stopped = false;

  constructor(
    private readonly prober: Prober,
    private readonly config: ConnectivityConfig = DEFAULT_CONNECTIVITY,
    /** Called on the transition to reachable, and only on the transition.
     *  This is what drains the queue the moment the network returns rather
     *  than at the next flush tick — a till that reconnects at 17:58 should
     *  not still be holding the day's takings at closing time. */
    private readonly onRestored: () => void = () => {},
  ) {}

  current(): ConnectivityState {
    return { ...this.state };
  }

  subscribe(listener: ConnectivityListener): () => void {
    this.listeners.add(listener);
    listener(this.current());
    return () => this.listeners.delete(listener);
  }

  /** Begins probing. The first probe runs immediately so the UI is honest
   *  within a second of the till starting, not one interval later. */
  start(): void {
    this.stopped = false;
    void this.check();
  }

  stop(): void {
    this.stopped = true;
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }

  /**
   * Probes now, cancelling any scheduled probe.
   *
   * Called when the OS reports the network back, and when a business request
   * fails in a way that suggests the server has gone. Treating those as hints
   * to look rather than as answers is the point: the OS event is often early,
   * and a single failed request is often just one failed request.
   */
  async check(): Promise<ConnectivityState> {
    if (this.stopped) return this.current();

    // One probe at a time. Without this, an OS online event arriving during a
    // slow probe would start a second, and both would schedule a timer.
    if (this.inFlight) return this.current();
    this.inFlight = true;

    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }

    const wasReachable = this.state.reachable;
    const controller = new AbortController();
    const abort = setTimeout(() => controller.abort(), this.config.timeoutMs);

    try {
      const result = await this.prober.probe(controller.signal);
      if (result.ok) {
        this.settle({ reachable: true, unauthenticated: false, failures: 0 });
      } else {
        // The server answered and said no. Reachable in the TCP sense and
        // useless in every sense the terminal cares about.
        this.settle({
          reachable: false,
          unauthenticated: !result.authenticated,
          failures: this.state.failures + 1,
        });
      }
    } catch {
      this.settle({
        reachable: false,
        unauthenticated: false,
        failures: this.state.failures + 1,
      });
    } finally {
      clearTimeout(abort);
      this.inFlight = false;
    }

    this.schedule();

    // Fired after the state is published and the next probe is booked, so a
    // slow or throwing sync callback cannot delay either.
    if (!wasReachable && this.state.reachable) {
      try {
        this.onRestored();
      } catch {
        // The queue's problem, not the monitor's. Connectivity is still up and
        // saying otherwise because a flush threw would be wrong.
      }
    }

    return this.current();
  }

  /** The wait before the next probe: fixed while up, doubling while down. */
  private delay(): number {
    if (this.state.reachable) return this.config.onlineIntervalMs;
    const steps = Math.max(0, this.state.failures - 1);
    const grown = this.config.offlineBaseMs * 2 ** Math.min(steps, 20);
    return Math.min(grown, this.config.offlineMaxMs);
  }

  private schedule(): void {
    if (this.stopped || this.timer !== null) return;
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.check();
    }, this.delay());
  }

  private settle(
    patch: Pick<
      ConnectivityState,
      'reachable' | 'unauthenticated' | 'failures'
    >,
  ): void {
    this.state = { ...this.state, ...patch, checked: true, lastCheckedAt: Date.now() };
    for (const l of this.listeners) l(this.current());
  }
}

/** Builds a prober from the API client.
 *
 * The client owns the probe because it owns the access token, and a monitor
 * that had to be handed the raw JWT to do its job would be one more place a
 * credential lives. See Client.ping for why the check is a 204 and not merely
 * a successful response.
 */
export function clientProber(client: {
  ping(signal: AbortSignal): Promise<{ ok: boolean; authenticated: boolean }>;
}): Prober {
  return { probe: (signal) => client.ping(signal) };
}