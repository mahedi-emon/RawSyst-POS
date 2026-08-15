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

  async login(email: string, password: string): Promise<Session> {
    const body = await this.send<{ access_token: string; refresh_token: string }>(
      'POST',
      '/api/v1/auth/login',
      { email, password },
      { authenticated: false },
    );
    const session = {
      accessToken: body.access_token,
      refreshToken: body.refresh_token,
    };
    this.session = session;
    return session;
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
}
