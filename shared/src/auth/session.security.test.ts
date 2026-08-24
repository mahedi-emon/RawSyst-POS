import { beforeEach, describe, expect, it } from 'vitest';

import { Client } from '../api/client';
import { storedRefreshToken } from './session';

// The browser must never end up holding a refresh token.
//
// It used to: sign-in returned one in the body and the client wrote the whole
// session object to localStorage, where an extension, a successful XSS or a
// compromised dependency could read it. A refresh token is the durable half of
// a session — stealing one is stealing the account until somebody notices.
//
// The server now withholds it from a browser and sets an httpOnly cookie
// instead. These tests guard the client half of that bargain: that nothing
// here puts one back, and that the native app still gets the behaviour it
// needs.

// These run in Node, where there is no DOM. A few lines of in-memory storage
// is cheaper and clearer than pulling jsdom in for one file, and it exercises
// the same code paths: what is under test is what the CLIENT decides to keep,
// not the browser's implementation of a key-value store.
const memory = new Map<string, string>();
globalThis.localStorage = {
  getItem: (k: string) => memory.get(k) ?? null,
  setItem: (k: string, v: string) => void memory.set(k, String(v)),
  removeItem: (k: string) => void memory.delete(k),
  clear: () => memory.clear(),
  key: (i: number) => Array.from(memory.keys())[i] ?? null,
  get length() {
    return memory.size;
  },
} as Storage;

/** A fetch stand-in that answers with whatever the server would. */
function respondWith(body: unknown, status = 200) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fake = (url: string, init: RequestInit = {}) => {
    calls.push({ url, init });
    return Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
  };
  return { fake, calls };
}

describe('what the browser is allowed to keep', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('keeps no refresh token when the server sends none', async () => {
    // What a browser sign-in actually returns now: no refresh_token at all.
    const { fake } = respondWith({ access_token: 'access-only' });
    globalThis.fetch = fake as unknown as typeof fetch;

    const client = new Client('http://api.test');
    const outcome = await client.login('owner@example.test', 'a long passphrase');

    expect(outcome.kind).toBe('signed_in');
    if (outcome.kind !== 'signed_in') return;

    expect(outcome.session.accessToken).toBe('access-only');
    expect(outcome.session.refreshToken).toBeUndefined();

    // And nothing reachable by script is holding one.
    localStorage.setItem('rawsyst.session', JSON.stringify(outcome.session));
    expect(storedRefreshToken()).toBeNull();
  });

  it('announces itself as native only when it is', async () => {
    const browser = respondWith({ access_token: 'a' });
    globalThis.fetch = browser.fake as unknown as typeof fetch;
    await new Client('http://api.test').login('x@y.test', 'passphrase');

    const browserHeaders = (browser.calls[0]?.init.headers ?? {}) as Record<string, string>;
    expect(browserHeaders['X-Client-Kind']).toBeUndefined();

    const native = respondWith({ access_token: 'a', refresh_token: 'r' });
    globalThis.fetch = native.fake as unknown as typeof fetch;
    await new Client('http://api.test', null, true).login('x@y.test', 'passphrase');

    const nativeHeaders = (native.calls[0]?.init.headers ?? {}) as Record<string, string>;
    expect(nativeHeaders['X-Client-Kind']).toBe('native');
  });

  it('sends cookies on the auth routes and nowhere else', async () => {
    const { fake, calls } = respondWith({ access_token: 'a' });
    globalThis.fetch = fake as unknown as typeof fetch;

    const client = new Client('http://api.test');
    await client.login('x@y.test', 'passphrase');
    expect(calls[0]?.init.credentials).toBe('include');

    // An ordinary call must not drag the session cookie along: every other
    // route authenticates with the Bearer token, and sending cookies there
    // would widen the surface for nothing.
    calls.length = 0;
    await client.send('GET', '/api/v1/catalog/products');
    expect(calls[0]?.init.credentials).toBe('same-origin');
  });

  it('keeps the native refresh token, because that app has no cookie jar', async () => {
    const { fake } = respondWith({ access_token: 'a', refresh_token: 'r' });
    globalThis.fetch = fake as unknown as typeof fetch;

    const outcome = await new Client('http://api.test', null, true).login(
      'x@y.test',
      'passphrase',
    );
    expect(outcome.kind).toBe('signed_in');
    if (outcome.kind !== 'signed_in') return;
    expect(outcome.session.refreshToken).toBe('r');
  });
});

describe('recovering from an expired access token', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('refreshes once and retries the call that failed', async () => {
    const seen: string[] = [];
    let refreshed = false;

    globalThis.fetch = ((url: string) => {
      seen.push(url);
      if (url.endsWith('/api/v1/auth/refresh')) {
        refreshed = true;
        return Promise.resolve(
          new Response(JSON.stringify({ access_token: 'fresh' }), { status: 200 }),
        );
      }
      // The first business call fails as expired; the retry succeeds.
      if (!refreshed) {
        return Promise.resolve(new Response(JSON.stringify({}), { status: 401 }));
      }
      return Promise.resolve(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    }) as unknown as typeof fetch;

    const client = new Client('http://api.test', { accessToken: 'stale' });
    const out = await client.send<{ ok: boolean }>('GET', '/api/v1/catalog/products');

    expect(out.ok).toBe(true);
    expect(seen.filter((u) => u.endsWith('/auth/refresh'))).toHaveLength(1);
    // The original call, the refresh, then the retry.
    expect(seen).toHaveLength(3);
  });

  it('refreshes ONCE for several calls failing together', async () => {
    // The case that matters: a dashboard fires four requests and the token
    // expires between them. Without a single-flight guard each starts its own
    // refresh, the second presents a token the first already spent, the server
    // reads that as reuse — which is indistinguishable from theft — and every
    // session the user has is revoked. A screen refresh would sign them out.
    let refreshes = 0;
    let refreshed = false;

    globalThis.fetch = ((url: string) => {
      if (url.endsWith('/api/v1/auth/refresh')) {
        refreshes++;
        return new Promise((resolve) =>
          setTimeout(() => {
            refreshed = true;
            resolve(
              new Response(JSON.stringify({ access_token: 'fresh' }), { status: 200 }),
            );
          }, 10),
        );
      }
      if (!refreshed) {
        return Promise.resolve(new Response(JSON.stringify({}), { status: 401 }));
      }
      return Promise.resolve(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    }) as unknown as typeof fetch;

    const client = new Client('http://api.test', { accessToken: 'stale' });
    await Promise.all([
      client.send('GET', '/api/v1/a'),
      client.send('GET', '/api/v1/b'),
      client.send('GET', '/api/v1/c'),
      client.send('GET', '/api/v1/d'),
    ]);

    expect(refreshes).toBe(1);
  });

  it('gives up after one refresh rather than looping', async () => {
    let refreshes = 0;
    globalThis.fetch = ((url: string) => {
      if (url.endsWith('/api/v1/auth/refresh')) {
        refreshes++;
        return Promise.resolve(
          new Response(JSON.stringify({ access_token: 'fresh' }), { status: 200 }),
        );
      }
      // Refused even after a successful refresh: the session is genuinely over.
      return Promise.resolve(new Response(JSON.stringify({}), { status: 401 }));
    }) as unknown as typeof fetch;

    const client = new Client('http://api.test', { accessToken: 'stale' });
    await expect(client.send('GET', '/api/v1/anything')).rejects.toThrow();
    expect(refreshes).toBe(1);
  });

  it('drops the session when the refresh itself is refused', async () => {
    globalThis.fetch = ((url: string) => {
      if (url.endsWith('/api/v1/auth/refresh')) {
        return Promise.resolve(new Response(JSON.stringify({}), { status: 401 }));
      }
      return Promise.resolve(new Response(JSON.stringify({}), { status: 401 }));
    }) as unknown as typeof fetch;

    const client = new Client('http://api.test', { accessToken: 'stale' });
    let ended: unknown = 'untouched';
    client.onSession = (s) => {
      ended = s;
    };

    await expect(client.send('GET', '/api/v1/anything')).rejects.toThrow();
    expect(ended).toBeNull();
    expect(client.authenticated).toBe(false);
  });
});
