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
  refreshToken: string;
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
  constructor(
    private readonly baseUrl: string,
    private session: Session | null = null,
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
      { authenticated: false },
    );

    if (body.tenant_choice_required) {
      // Deliberately does NOT set this.session: there is no session yet, and a
      // client left holding a half-authenticated state is how a screen ends up
      // showing a signed-in shell with no token behind it.
      return { kind: 'choose_tenant', tenants: body.tenants ?? [] };
    }

    const session = {
      accessToken: body.access_token ?? '',
      refreshToken: body.refresh_token ?? '',
    };
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
    opts: { authenticated?: boolean } = {},
  ): Promise<T> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
    };
    if (opts.authenticated !== false && this.session) {
      headers.Authorization = `Bearer ${this.session.accessToken}`;
    }

    let response: Response;
    try {
      response = await fetch(this.baseUrl + path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    } catch {
      // A transport failure, not a refusal. The POS treats the two very
      // differently: one queues the sale and carries on, the other stops it.
      throw new Offline();
    }

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
