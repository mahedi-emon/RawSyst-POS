// The HTTP client.
//
// Money and quantities cross this boundary as STRINGS, never numbers, and they
// stay strings all the way to the screen. JavaScript's `number` is a float64
// and cannot hold 0.15 exactly; a till that parsed a total and re-rendered it
// would eventually disagree with the invoice the customer was handed. Nothing
// here calls parseFloat on a money value, and nothing should.

export interface ApiError {
  code: string;
  message: string;
  fields?: Record<string, string>;
  request_id?: string;
}

/** An error carrying the server's own words. */
export class RequestFailed extends Error {
  readonly code: string;
  readonly status: number;
  readonly fields?: Record<string, string>;
  readonly requestId?: string;

  constructor(status: number, err: ApiError) {
    // The server writes its messages for the person reading them, so they are
    // shown as-is. Rewording a refusal here would lose the one place that knows
    // why it happened.
    super(err.message);
    this.name = 'RequestFailed';
    this.status = status;
    this.code = err.code;
    this.fields = err.fields;
    this.requestId = err.request_id;
  }
}

/** Raised when the network is unreachable, as distinct from a refusal. */
export class Offline extends Error {
  constructor() {
    super('The till cannot reach the server.');
    this.name = 'Offline';
  }
}

/** One business a signed-in email can belong to. */
export interface TenantChoice {
  tenant_id: string;
  name: string;
}

/** What a sign-in attempt produced: a session, or a question. */
export type LoginOutcome =
  | { kind: 'signed_in'; session: Session }
  | { kind: 'choose_tenant'; tenants: TenantChoice[] };

export interface Session {
  accessToken: string;

  /**
   * Only ever set in the NATIVE app.
   *
   * In a browser the refresh token lives in an httpOnly cookie the page cannot
   * read, so there is nothing to put here and nothing for a script -- an
   * extension, an XSS, a compromised dependency -- to steal. It used to be
   * returned in the sign-in body and kept in localStorage, which is the
   * durable half of a session sitting somewhere any script could reach it.
   */
  refreshToken?: string;
}

export interface Me {
  user_id: string;
  tenant_id?: string;
  is_super_admin: boolean;
  /** Sorted by the server, so two responses can be compared meaningfully. */
  permissions: string[];
  /** A decimal STRING. Never widened through a float on its way to a check. */
  amount_limit?: string;
}

export class Client {
  /**
   * One refresh at a time.
   *
   * Several requests routinely fail with 401 together -- a dashboard fires
   * four in parallel and the access token expires between them. Without this
   * each would start its own refresh, and because refresh tokens ROTATE, the
   * second would present a token the first had already spent. The server reads
   * that as reuse, which is indistinguishable from theft, and revokes every
   * session the user has. A screen refresh would sign them out.
   */
  private refreshing: Promise<boolean> | null = null;

  constructor(
    private readonly baseUrl: string,
    private session: Session | null = null,
    /**
     * True in the Tauri POS, which has no browser cookie jar and no other
     * origin sharing it, so it receives the refresh token in the body and
     * keeps it in the application's own storage.
     *
     * The default is the browser, so a caller that says nothing gets the safe
     * behaviour rather than the convenient one.
     */
    private readonly native: boolean = false,
  ) {}

  setSession(s: Session | null) {
    this.session = s;
  }

  get authenticated(): boolean {
    return this.session !== null;
  }

  /**
   * Signs in, or reports that the server needs to know which business.
   *
   * One email can legitimately belong to several businesses — a bookkeeper
   * serving two shops, an owner with two companies. When the password opens
   * accounts in more than one, the server issues no tokens and returns the
   * choices instead; the caller asks the person and signs in again naming one.
   *
   * `tenantId` is only ever a filter on the server's lookup. Naming a business
   * the account is not in is refused exactly like a wrong password, so nothing
   * here is trusted on the client's word.
   */
  async login(
    email: string,
    password: string,
    tenantId?: string,
  ): Promise<LoginOutcome> {
    const body = await this.send<{
      access_token?: string;
      refresh_token?: string;
      tenant_choice_required?: boolean;
      tenants?: TenantChoice[];
    }>(
      'POST',
      '/api/v1/auth/login',
      tenantId ? { email, password, tenant_id: tenantId } : { email, password },
      { authenticated: false, credentials: true },
    );

    if (body.tenant_choice_required) {
      // Deliberately does NOT set this.session: there is no session yet, and a
      // client left holding a half-authenticated state is how a screen ends up
      // showing a signed-in shell with no token behind it.
      return { kind: 'choose_tenant', tenants: body.tenants ?? [] };
    }

    // refresh_token is absent for a browser by design: the server put it in an
    // httpOnly cookie instead. Only the native app asks for it in the body.
    const session: Session = { accessToken: body.access_token ?? '' };
    if (body.refresh_token) session.refreshToken = body.refresh_token;
    this.session = session;
    return { kind: 'signed_in', session };
  }

  async me(): Promise<Me> {
    return this.send<Me>('GET', '/api/v1/auth/me');
  }

  async logout(): Promise<void> {
    try {
      await this.send('POST', '/api/v1/auth/logout', {});
    } finally {
      // Cleared even if the call failed. A cashier who pressed sign-out must
      // not be left signed in because the network chose that moment to drop.
      this.session = null;
    }
  }

  async send<T>(
    method: string,
    path: string,
    body?: unknown,
    opts: { authenticated?: boolean; credentials?: boolean; retry?: boolean } = {},
  ): Promise<T> {
    const response = await this.fire(method, path, body, opts);

    // An expired access token, once. The token lives fifteen minutes and a
    // shift lasts eight hours, so without this every session died mid-morning
    // and the only cure was signing in again -- which is exactly what happened
    // before, because nothing ever called the refresh endpoint at all.
    //
    // Only once: if the retry is refused too, the session is genuinely over,
    // and looping would turn one refusal into a storm of them.
    if (response.status === 401 && opts.retry !== false && opts.authenticated !== false) {
      if (await this.refreshOnce()) {
        return this.unwrap<T>(
          await this.fire(method, path, body, { ...opts, retry: false }),
        );
      }
    }
    return this.unwrap<T>(response);
  }

  /** One request, with no retry logic wrapped around it. */
  private async fire(
    method: string,
    path: string,
    body: unknown,
    opts: { authenticated?: boolean; credentials?: boolean; retry?: boolean },
  ): Promise<Response> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
    };
    if (opts.authenticated !== false && this.session) {
      headers.Authorization = `Bearer ${this.session.accessToken}`;
    }
    if (this.native) headers['X-Client-Kind'] = 'native';

    // The double-submit echo. That cookie is readable on purpose: proving the
    // page can read it is precisely what a cross-site attacker cannot arrange.
    if (opts.credentials) {
      const csrf = readCookie('rawsyst_csrf');
      if (csrf) headers['X-CSRF-Token'] = csrf;
    }

    try {
      return await fetch(this.baseUrl + path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        // Cookies travel only on the auth routes, which is where the refresh
        // cookie is scoped. Every other route authenticates with the Bearer
        // token, and sending cookies there would widen the surface for nothing.
        credentials: opts.credentials ? 'include' : 'same-origin',
      });
    } catch {
      // A transport failure, not a refusal. The POS treats the two very
      // differently: one queues the sale and carries on, the other stops it.
      throw new Offline();
    }
  }

  private async unwrap<T>(response: Response): Promise<T> {
    if (response.status === 204) {
      return undefined as T;
    }

    const text = await response.text();
    const payload = text ? JSON.parse(text) : {};

    if (!response.ok) {
      const err: ApiError = payload.error ?? {
        code: 'internal',
        message: 'Something went wrong and the server did not say what.',
      };
      throw new RequestFailed(response.status, err);
    }
    return payload as T;
  }

  /**
   * Exchanges the refresh token for a new access token.
   *
   * Collapsed to one attempt for every caller. See the note on `refreshing`:
   * parallel refreshes spend a rotating token twice, the server reads the
   * second as reuse -- which is indistinguishable from theft -- and the user
   * is signed out of everything.
   */
  async refreshOnce(): Promise<boolean> {
    if (this.refreshing) return this.refreshing;

    this.refreshing = (async () => {
      try {
        const body = await this.send<{ access_token?: string; refresh_token?: string }>(
          'POST',
          '/api/v1/auth/refresh',
          // A browser sends nothing: the cookie carries it. The native app
          // sends the token it holds.
          this.native && this.session?.refreshToken
            ? { refresh_token: this.session.refreshToken }
            : {},
          { authenticated: false, credentials: true, retry: false },
        );

        if (!body.access_token) return false;

        const next: Session = { accessToken: body.access_token };
        if (body.refresh_token) next.refreshToken = body.refresh_token;
        this.session = next;
        this.onSession?.(next);
        return true;
      } catch {
        // Any refusal means the session is over. The caller turns that into a
        // sign-in prompt rather than a screen full of failed panels.
        this.session = null;
        this.onSession?.(null);
        return false;
      } finally {
        this.refreshing = null;
      }
    })();

    return this.refreshing;
  }

  /** Told when a refresh replaces or ends the session, so storage keeps up. */
  onSession?: (s: Session | null) => void;

  /**
   * Fetches a binary response — today, a company's logo.
   *
   * Separate from `send` because that method assumes JSON and would throw on an
   * image. Kept on the Client for the same reason `ping` is: the access token
   * never leaves this object, and a caller building its own fetch would need it
   * in hand.
   *
   * This is why a logo cannot simply be an `<img src>` pointing at the route —
   * a browser does not attach an Authorization header to an image request, so
   * the bytes are fetched here and handed on as a blob.
   */
  async sendBlob(method: string, path: string): Promise<Blob> {
    const headers: Record<string, string> = {};
    if (this.session) {
      headers.Authorization = `Bearer ${this.session.accessToken}`;
    }

    let response: Response;
    try {
      response = await fetch(this.baseUrl + path, { method, headers });
    } catch {
      throw new Offline();
    }

    if (!response.ok) {
      // The error envelope is still JSON even when the success path is not, so
      // a refusal reads the same way it does everywhere else.
      const text = await response.text();
      let payload: { error?: ApiError } = {};
      try {
        payload = text ? JSON.parse(text) : {};
      } catch {
        payload = {};
      }
      throw new RequestFailed(
        response.status,
        payload.error ?? {
          code: 'internal',
          message: 'That file could not be fetched.',
        },
      );
    }
    return response.blob();
  }

  /**
   * The reachability probe, kept on the Client so the access token never
   * leaves it.
   *
   * Deliberately NOT written in terms of `send`. That method wraps every
   * transport failure in `Offline` and every rejection in `RequestFailed`,
   * which is right for a business call and wrong here: the monitor needs to
   * tell "the server never answered" from "the server answered and refused
   * me", and those two collapse into one exception on the way through.
   *
   * The 204 is checked rather than merely `res.ok`. A captive portal — a mall
   * wifi splash screen, the ordinary case for a till on shared infrastructure
   * — answers every request with a 200 and a login page, so "it did not throw"
   * is evidence of nothing. An empty 204 is not something a portal produces by
   * accident.
   */
  async ping(signal: AbortSignal): Promise<{ ok: boolean; authenticated: boolean }> {
    if (!this.session) return { ok: false, authenticated: false };

    const res = await fetch(this.baseUrl + '/api/v1/meta/ping', {
      method: 'GET',
      headers: { Authorization: `Bearer ${this.session.accessToken}` },
      // A cached 204 would report a dead server as reachable for as long as
      // the entry lived.
      cache: 'no-store',
      signal,
    });

    if (res.status === 401 || res.status === 403) {
      return { ok: false, authenticated: false };
    }
    return { ok: res.status === 204, authenticated: true };
  }
}

/**
 * Reads a readable cookie.
 *
 * Only ever used for the CSRF token, which is readable on purpose. The refresh
 * cookie is httpOnly and this cannot see it, which is the whole point.
 */
function readCookie(name: string): string | null {
  if (typeof document === 'undefined') return null;
  for (const part of document.cookie.split(';')) {
    const [k, ...rest] = part.trim().split('=');
    if (k === name) return decodeURIComponent(rest.join('='));
  }
  return null;
}
