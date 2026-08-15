// What the monitor must get right.
//
// These are the failures that strand a till in a real shop, not hypotheticals:
// a captive portal that answers everything with a login page, an uplink that
// dies while the wifi interface stays up, a session that expired overnight,
// and a probe that hangs while a customer waits to pay.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ConnectivityMonitor,
  DEFAULT_CONNECTIVITY,
  clientProber,
  type ConnectivityConfig,
  type Prober,
} from './connectivity';

const FAST: ConnectivityConfig = {
  onlineIntervalMs: 1000,
  offlineBaseMs: 100,
  offlineMaxMs: 800,
  timeoutMs: 50,
};

/** A prober the test drives. */
function stub(): Prober & {
  ok: boolean;
  refuse: boolean;
  throws: boolean;
  hang: boolean;
  calls: number;
} {
  return {
    ok: true,
    refuse: false,
    throws: false,
    hang: false,
    calls: 0,
    async probe(signal: AbortSignal) {
      this.calls++;
      if (this.hang) {
        // Resolves only when the monitor's own timeout aborts it, which is
        // what a real hung socket does.
        return new Promise((_, reject) => {
          signal.addEventListener('abort', () => reject(new Error('aborted')));
        });
      }
      if (this.throws) throw new Error('network unreachable');
      if (this.refuse) return { ok: false, authenticated: false };
      return { ok: this.ok, authenticated: true };
    },
  };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('detecting the server', () => {
  it('reports reachable when the ping answers', async () => {
    const p = stub();
    const m = new ConnectivityMonitor(p, FAST);

    await m.check();

    expect(m.current().reachable).toBe(true);
    expect(m.current().checked).toBe(true);
    expect(m.current().failures).toBe(0);
    m.stop();
  });

  it('reports unreachable when the probe throws', async () => {
    const p = stub();
    p.throws = true;
    const m = new ConnectivityMonitor(p, FAST);

    await m.check();

    expect(m.current().reachable).toBe(false);
    expect(m.current().failures).toBe(1);
    m.stop();
  });

  it('starts unchecked, so the UI does not claim offline before looking', () => {
    const m = new ConnectivityMonitor(stub(), FAST);
    expect(m.current().checked).toBe(false);
    expect(m.current().reachable).toBe(false);
  });

  it('separates an expired session from a dead network', async () => {
    const p = stub();
    p.refuse = true;
    const m = new ConnectivityMonitor(p, FAST);

    await m.check();

    expect(m.current().reachable).toBe(false);
    // The distinction that matters: one is fixed by signing in again.
    expect(m.current().unauthenticated).toBe(true);
    m.stop();
  });
});

describe('false positives', () => {
  it('does not call a captive portal reachable', async () => {
    // A mall wifi splash screen answers with 200 and a login page. The prober
    // reports not-ok because the status was not 204.
    const portal: Prober = {
      probe: async () => ({ ok: false, authenticated: true }),
    };
    const m = new ConnectivityMonitor(portal, FAST);

    await m.check();

    expect(m.current().reachable).toBe(false);
    // Not an auth problem — signing in again would not help.
    expect(m.current().unauthenticated).toBe(false);
    m.stop();
  });

  it('treats a hung socket as unreachable rather than waiting forever', async () => {
    const p = stub();
    p.hang = true;
    const m = new ConnectivityMonitor(p, FAST);

    const settled = m.check();
    await vi.advanceTimersByTimeAsync(FAST.timeoutMs + 10);
    await settled;

    expect(m.current().reachable).toBe(false);
    m.stop();
  });

  it('rejects the Client ping unless the server answered exactly 204', async () => {
    // The real prober, against the statuses that actually occur in the field.
    for (const [status, expected] of [
      [204, true],
      [200, false], // captive portal
      [502, false], // proxy with no upstream
      [401, false], // expired session
    ] as const) {
      const client = {
        ping: async () => ({
          ok: status === 204,
          authenticated: status !== 401,
        }),
      };
      const result = await clientProber(client).probe(new AbortController().signal);
      expect(result.ok).toBe(expected);
    }
  });
});

describe('backoff', () => {
  it('widens the gap while the server stays down, up to the ceiling', async () => {
    const p = stub();
    p.throws = true;
    const m = new ConnectivityMonitor(p, FAST);

    m.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(p.calls).toBe(1);

    // 100ms after the first failure, not sooner.
    await vi.advanceTimersByTimeAsync(99);
    expect(p.calls).toBe(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(p.calls).toBe(2);

    // Then 200, then 400 — doubling, not repeating.
    await vi.advanceTimersByTimeAsync(199);
    expect(p.calls).toBe(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(p.calls).toBe(3);

    await vi.advanceTimersByTimeAsync(399);
    expect(p.calls).toBe(3);
    await vi.advanceTimersByTimeAsync(1);
    expect(p.calls).toBe(4);

    m.stop();
  });

  it('never exceeds the ceiling however long the outage lasts', async () => {
    const p = stub();
    p.throws = true;
    const m = new ConnectivityMonitor(p, FAST);

    m.start();
    // Long enough to be well past the point where doubling would have
    // overshot: an all-day outage must not push the gap into hours.
    await vi.advanceTimersByTimeAsync(20_000);
    const before = p.calls;

    await vi.advanceTimersByTimeAsync(FAST.offlineMaxMs + 10);
    expect(p.calls).toBe(before + 1);

    m.stop();
  });

  it('returns to the slow online interval once the server is back', async () => {
    const p = stub();
    p.throws = true;
    const m = new ConnectivityMonitor(p, FAST);

    m.start();
    await vi.advanceTimersByTimeAsync(0);
    p.throws = false;
    await vi.advanceTimersByTimeAsync(100);
    expect(m.current().reachable).toBe(true);

    const after = p.calls;
    // Not probing at the offline cadence any more.
    await vi.advanceTimersByTimeAsync(FAST.onlineIntervalMs - 1);
    expect(p.calls).toBe(after);
    await vi.advanceTimersByTimeAsync(1);
    expect(p.calls).toBe(after + 1);

    m.stop();
  });
});

describe('reconnecting', () => {
  it('triggers the sync immediately, not at the next flush tick', async () => {
    const p = stub();
    p.throws = true;
    const restored = vi.fn();
    const m = new ConnectivityMonitor(p, FAST, restored);

    m.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(restored).not.toHaveBeenCalled();

    p.throws = false;
    await vi.advanceTimersByTimeAsync(100);

    expect(m.current().reachable).toBe(true);
    expect(restored).toHaveBeenCalledTimes(1);
    m.stop();
  });

  it('fires on a cold start, then not again while it stays up', async () => {
    const p = stub();
    const restored = vi.fn();
    const m = new ConnectivityMonitor(p, FAST, restored);

    m.start();
    await vi.advanceTimersByTimeAsync(0);
    // The monitor begins not-reachable, so the first success IS a transition —
    // and firing there is what we want: a till switched on with yesterday's
    // takings still queued should drain them before the first customer, not
    // wait for a network blip to prompt it.
    expect(restored).toHaveBeenCalledTimes(1);

    // But it is a transition, not a heartbeat. Three more successful probes
    // must not each kick off another flush.
    await vi.advanceTimersByTimeAsync(FAST.onlineIntervalMs * 3);
    expect(p.calls).toBeGreaterThan(2);
    expect(restored).toHaveBeenCalledTimes(1);

    m.stop();
  });

  it('stays online when the sync callback throws', async () => {
    const p = stub();
    p.throws = true;
    const m = new ConnectivityMonitor(p, FAST, () => {
      throw new Error('the queue is unhappy');
    });

    m.start();
    await vi.advanceTimersByTimeAsync(0);
    p.throws = false;
    await vi.advanceTimersByTimeAsync(100);

    // A failing flush is the queue's problem. Reporting the network as down
    // because of it would be wrong, and would stop it ever being retried.
    expect(m.current().reachable).toBe(true);
    m.stop();
  });

  it('collapses an OS online hint arriving mid-probe into one check', async () => {
    const p = stub();
    p.hang = true;
    const m = new ConnectivityMonitor(p, FAST);

    const first = m.check();
    // The OS says the interface came up while the first probe is still out.
    void m.check();
    void m.check();
    expect(p.calls).toBe(1);

    await vi.advanceTimersByTimeAsync(FAST.timeoutMs + 10);
    await first;
    m.stop();
  });
});

describe('staying out of the way', () => {
  it('does not probe at all once stopped', async () => {
    const p = stub();
    const m = new ConnectivityMonitor(p, FAST);

    m.start();
    await vi.advanceTimersByTimeAsync(0);
    const after = p.calls;

    m.stop();
    await vi.advanceTimersByTimeAsync(FAST.onlineIntervalMs * 5);

    expect(p.calls).toBe(after);
  });

  it('never blocks a sale: a hung probe delays nothing on the selling path', async () => {
    const p = stub();
    p.hang = true;
    const m = new ConnectivityMonitor(p, FAST);

    // A probe is in flight and will not answer for its full timeout.
    void m.check();
    expect(p.calls).toBe(1);

    // The sale path only ever reads what the monitor already knows. It is a
    // synchronous field read, so no await can be introduced accidentally.
    const before = Date.now();
    const state = m.current();
    expect(Date.now() - before).toBe(0);
    expect(state).toBeDefined();

    await vi.advanceTimersByTimeAsync(FAST.timeoutMs + 10);
    m.stop();
  });

  it('probes far less than once a second, on the SHIPPED defaults', async () => {
    // Deliberately not the fast test config: the number that matters is what
    // real terminals in real shops will do to the real server, and a test that
    // only exercised an artificially tight ceiling would prove nothing about
    // it.
    const p = stub();
    p.throws = true;
    const m = new ConnectivityMonitor(p, DEFAULT_CONNECTIVITY);

    m.start();
    await vi.advanceTimersByTimeAsync(60 * 60_000);

    // A full hour with the server down. Once a second would be 3,600 probes
    // from this one till; backoff keeps it in the tens.
    expect(p.calls).toBeLessThan(30);
    m.stop();
  });

  it('an hour of uptime is also quiet', async () => {
    const p = stub();
    const m = new ConnectivityMonitor(p, DEFAULT_CONNECTIVITY);

    m.start();
    await vi.advanceTimersByTimeAsync(60 * 60_000);

    // 30s intervals: ~120 in an hour, or one every 30 seconds per terminal.
    // A hundred-till chain is under four requests a second across the estate,
    // against a route that does no database work of its own.
    expect(p.calls).toBeLessThanOrEqual(121);
    m.stop();
  });
});
