// The HTTP client.
//
// # Money never becomes a number
//
// Amounts and quantities cross this boundary as STRINGS and stay strings all
// the way to the screen. JavaScript's `number` is a float64 and cannot hold
// 0.15 exactly; a till that parsed a total and re-rendered it would eventually
// disagree with the invoice the customer was handed. Nothing here calls
// `parseFloat` on a money value, and nothing downstream should either. Use
// `decimal.js` where arithmetic is genuinely needed on the client.
//
// # Where the session lives
//
// The access token is held in MEMORY only. The durable half of the session is
// an httpOnly cookie the page cannot read, so a script -- an extension, an XSS,
// a compromised dependency -- has nothing to steal that outlives the tab. That
// is the backend's deliberate design (`internal/api/refresh_cookie.go`) and
// this client is built to it rather than around it.
//
// The consequence is that a full page load starts with no access token and must
// silently refresh from the cookie before it can fetch anything. `bootstrap()`
// is that step, and it is why data fetching in this app happens on the client
// rather than in Server Components: a Server Component has no way to hold the
// in-memory token, and copying the token into a second cookie so it could would
// undo the protection above.
//
// # Same origin, deliberately
//
// The refresh cookie is `SameSite=Strict` and the CSRF cookie has to be
// readable by `document.cookie` on the page that echoes it. Both work only if
// the browser believes the API is this site, so `next.config.mjs` rewrites
// `/api/v1/*` to the Go service instead of the browser calling it cross-origin.
// That also means no CORS configuration is involved at all.

import { ApiError, NetworkError, type ApiErrorBody } from './errors';

/** Where the browser sends API calls. Same origin; see the note above. */
const API_BASE = '/api/v1';

const CSRF_COOKIE = 'rawsyst_csrf';
const CSRF_HEADER = 'X-CSRF-Token';

/** A collection response. Single resources are returned bare. */
export interface Page<T> {
  data: T[];
  page?: {
    cursor: string | null;
    has_more: boolean;
    limit: number;
  };
}

export interface RequestOptions {
  /** Query parameters. `undefined` and `null` values are dropped, not sent empty. */
  query?: Record<string, string | number | boolean | undefined | null>;
  /**
   * Makes a retry safe. Required by the API on `/sync/push` and on anything the
   * POS calls, because the POS retries after a network failure and must not
   * post a sale twice. A replay returns the original response.
   */
  idempotencyKey?: string;
  signal?: AbortSignal;
  /** Skips the silent refresh-and-retry. Used by the auth calls themselves. */
  noRetry?: boolean;
}

type Listener = (signedIn: boolean) => void;

/**
 * Reads a cookie the page is allowed to read.
 *
 * Only ever used for the CSRF token, which is deliberately not httpOnly: the
 * client has to echo it in a header, and that echo IS the mechanism. It is not
 * a secret -- proving the request came from a page on this origin is exactly
 * what a cross-site attacker cannot arrange.
 */
function readCookie(name: string): string | null {
  if (typeof document === 'undefined') return null;
  const prefix = `${name}=`;
  for (const part of document.cookie.split('; ')) {
    if (part.startsWith(prefix)) return decodeURIComponent(part.slice(prefix.length));
  }
  return null;
}

class RawsystClient {
  /** In memory, never in storage. See the header note. */
  private accessToken: string | null = null;

  /**
   * One refresh at a time.
   *
   * Several requests routinely fail with 401 together -- a dashboard fires four
   * in parallel and the access token expires between them. Without this each
   * would start its own refresh, and because refresh tokens ROTATE, the second
   * would present a token the first had already spent. The server reads that as
   * reuse, which is indistinguishable from theft, and revokes every session the
   * user has. Opening a dashboard would sign them out.
   */
  private refreshing: Promise<boolean> | null = null;

  private listeners = new Set<Listener>();

  get signedIn(): boolean {
    return this.accessToken !== null;
  }

  setAccessToken(token: string | null): void {
    const was = this.accessToken !== null;
    this.accessToken = token;
    if (was !== (token !== null)) {
      for (const l of this.listeners) l(token !== null);
    }
  }

  /** Notifies the shell when the session appears or disappears. */
  subscribe(l: Listener): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }

  /**
   * Recovers a session on page load, from the cookie the page cannot read.
   *
   * Returns false when there is nothing to recover, which is the ordinary state
   * of a first visit and not an error.
   */
  async bootstrap(): Promise<boolean> {
    return this.refresh();
  }

  private async refresh(): Promise<boolean> {
    if (this.refreshing) return this.refreshing;

    this.refreshing = (async () => {
      try {
        const csrf = readCookie(CSRF_COOKIE);
        const headers: Record<string, string> = {
          'Content-Type': 'application/json',
        };
        // Absent on a first visit, when there is no session to refresh. Sending
        // the request anyway is how the app discovers that.
        if (csrf) headers[CSRF_HEADER] = csrf;

        const res = await fetch(`${API_BASE}/auth/refresh`, {
          method: 'POST',
          headers,
          body: '{}',
          credentials: 'same-origin',
        });
        if (!res.ok) {
          this.setAccessToken(null);
          return false;
        }
        const body = (await res.json()) as { access_token?: string };
        if (!body.access_token) {
          this.setAccessToken(null);
          return false;
        }
        this.setAccessToken(body.access_token);
        return true;
      } catch {
        // A network failure is not a signed-out session. Leaving the token in
        // place lets the next call succeed when the connection comes back,
        // rather than throwing the person out because a train went into a
        // tunnel.
        return this.accessToken !== null;
      } finally {
        this.refreshing = null;
      }
    })();

    return this.refreshing;
  }

  private url(path: string, query?: RequestOptions['query']): string {
    const url = `${API_BASE}${path}`;
    if (!query) return url;
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(query)) {
      // Dropped rather than sent empty: `?store_id=` is a filter on the empty
      // string, which matches nothing, and is not what an unset filter means.
      if (v === undefined || v === null || v === '') continue;
      params.set(k, String(v));
    }
    const qs = params.toString();
    return qs ? `${url}?${qs}` : url;
  }

  private async send<T>(
    method: string,
    path: string,
    body: unknown,
    opts: RequestOptions = {},
  ): Promise<T> {
    const headers: Record<string, string> = {};
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (this.accessToken) headers['Authorization'] = `Bearer ${this.accessToken}`;
    if (opts.idempotencyKey) headers['Idempotency-Key'] = opts.idempotencyKey;

    let res: Response;
    try {
      res = await fetch(this.url(path, opts.query), {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        credentials: 'same-origin',
        signal: opts.signal,
      });
    } catch (cause) {
      // An aborted request is the caller's own doing -- a search box moving on
      // to the next keystroke -- and must not be reported as the server being
      // unreachable.
      if (opts.signal?.aborted) throw cause;
      throw new NetworkError(cause);
    }

    // One silent retry on 401: the access token lives 15 minutes and expiring
    // mid-session is routine, not a sign-out. `noRetry` stops the auth calls
    // from recursing into themselves.
    if (res.status === 401 && !opts.noRetry) {
      const recovered = await this.refresh();
      if (recovered) {
        return this.send<T>(method, path, body, { ...opts, noRetry: true });
      }
      this.setAccessToken(null);
    }

    if (res.status === 204) return undefined as T;

    const text = await res.text();
    let parsed: unknown = null;
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch {
        // A body that is not JSON came from something between here and the API
        // -- a proxy, a gateway. It has no envelope to read, so the status has
        // to speak for it.
        parsed = null;
      }
    }

    if (!res.ok) {
      const envelope = (parsed as { error?: ApiErrorBody } | null)?.error;
      throw new ApiError(
        res.status,
        envelope ?? {
          code: res.status >= 500 ? 'internal' : 'invalid_input',
          message:
            res.status >= 500
              ? 'The server could not complete that. Try again shortly.'
              : 'That request could not be completed.',
        },
      );
    }

    return parsed as T;
  }

  get<T>(path: string, opts?: RequestOptions): Promise<T> {
    return this.send<T>('GET', path, undefined, opts);
  }

  post<T>(path: string, body?: unknown, opts?: RequestOptions): Promise<T> {
    return this.send<T>('POST', path, body ?? {}, opts);
  }

  put<T>(path: string, body?: unknown, opts?: RequestOptions): Promise<T> {
    return this.send<T>('PUT', path, body ?? {}, opts);
  }

  patch<T>(path: string, body?: unknown, opts?: RequestOptions): Promise<T> {
    return this.send<T>('PATCH', path, body ?? {}, opts);
  }

  delete<T>(path: string, opts?: RequestOptions): Promise<T> {
    return this.send<T>('DELETE', path, undefined, opts);
  }

  /** Signs in. The refresh cookie is set by the server on the way back. */
  async login(input: {
    email: string;
    password: string;
    tenant_id?: string;
    mfa_code?: string;
  }): Promise<LoginOutcome> {
    const res = await this.send<LoginResponse>('POST', '/auth/login', input, {
      noRetry: true,
    });

    // A 200 with no access token is a CHALLENGE, not a failure: the password
    // was right and the server needs one more thing. Two of them exist, and a
    // client that ignored the flags would find no token and be unable to
    // proceed regardless.
    if (res.tenant_choice_required) {
      return { kind: 'choose_business', businesses: res.tenants ?? [] };
    }
    if (res.mfa_required) {
      return { kind: 'need_code' };
    }

    this.setAccessToken(res.access_token);
    return {
      kind: 'signed_in',
      mustChangePassword: res.must_change_password === true,
    };
  }

  async logout(): Promise<void> {
    try {
      await this.send('POST', '/auth/logout', {}, { noRetry: true });
    } finally {
      // Cleared whatever the server said. A sign-out that leaves the person
      // signed in because the network blinked is the wrong failure.
      this.setAccessToken(null);
    }
  }
}

export interface BusinessChoice {
  tenant_id: string;
  name: string;
}

interface LoginResponse {
  access_token: string;
  expires_at: string;
  must_change_password?: boolean;
  tenant_choice_required?: boolean;
  tenants?: BusinessChoice[];
  mfa_required?: boolean;
}

/** What a sign-in attempt produced: a session, or one more question. */
export type LoginOutcome =
  | { kind: 'signed_in'; mustChangePassword: boolean }
  | { kind: 'choose_business'; businesses: BusinessChoice[] }
  | { kind: 'need_code' };

/**
 * One client for the tab.
 *
 * A module-level instance rather than a React context value, because the token
 * and the single-flight refresh must be shared by every caller including ones
 * outside React -- a WebSocket reconnect, a service worker message. React reads
 * it through `useSession`.
 */
export const api = new RawsystClient();
