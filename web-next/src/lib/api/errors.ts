// What went wrong, in a form a screen can act on.
//
// The Go API writes its refusals for the person reading them -- "This discount
// is SAR 120.00, above your limit of SAR 50.00. Ask a manager to approve it."
// Those words are better than anything this layer could substitute, so they are
// carried through unchanged and shown. What this layer adds is the STABLE CODE,
// which is what a screen branches on.

/** The server's error envelope, exactly as `docs/system-design/07` defines it. */
export interface ApiErrorBody {
  code: string;
  message: string;
  /** Field-level messages, keyed by the field name the form used. */
  fields?: Record<string, string>;
  request_id?: string;
}

/**
 * The codes the API can return. Screens branch on these; they never parse the
 * message, which is prose and may be reworded.
 */
export const ERROR_CODES = [
  'invalid_input',
  'unauthenticated',
  'forbidden',
  'amount_limit_exceeded',
  'not_found',
  'conflict',
  'immutable',
  'period_closed',
  'plan_limit_reached',
  'compliance_blocked',
  'unverified_regulatory_rule',
  'rate_limited',
  'internal',
  'unavailable',
] as const;

export type ErrorCode = (typeof ERROR_CODES)[number];

/** A refusal from the API, carrying the server's own words. */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly fields?: Record<string, string>;
  readonly requestId?: string;

  constructor(status: number, body: ApiErrorBody) {
    super(body.message);
    this.name = 'ApiError';
    this.status = status;
    this.code = body.code;
    this.fields = body.fields;
    this.requestId = body.request_id;
  }

  /** The session is gone. The shell signs the person out rather than retrying. */
  get isUnauthenticated(): boolean {
    return this.status === 401 || this.code === 'unauthenticated';
  }

  /**
   * Permitted to be here, not permitted to do this.
   *
   * Note that a record belonging to another business answers 404, not 403 --
   * a 403 would confirm the record exists. So a 404 is not always "no such
   * thing"; it can be "not yours", and the copy says so.
   */
  get isForbidden(): boolean {
    return this.status === 403;
  }

  /** The plan does not include this module. Distinct from a permission refusal. */
  get isPlanLimited(): boolean {
    return this.status === 402 || this.code === 'plan_limit_reached';
  }

  /**
   * Refused for a legal reason, or because a legal value has not been verified.
   * The reason is shown as written; it is not a validation hint to correct.
   */
  get isComplianceRefusal(): boolean {
    return (
      this.code === 'compliance_blocked' ||
      this.code === 'unverified_regulatory_rule'
    );
  }

  /** Something in the world moved underneath this request. Re-read, re-try. */
  get isConflict(): boolean {
    return this.status === 409;
  }
}

/** The API could not be reached at all, as distinct from refusing. */
export class NetworkError extends Error {
  constructor(cause?: unknown) {
    super('RawSyst cannot reach the server.');
    this.name = 'NetworkError';
    this.cause = cause;
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

export function isNetworkError(e: unknown): e is NetworkError {
  return e instanceof NetworkError;
}

/**
 * A sentence to show when nothing more specific is known.
 *
 * Only ever reached for an error with no envelope -- a proxy timing out, a
 * gateway returning HTML. Anything the API itself refused arrives with a
 * message written for this exact situation, and that one is used instead,
 * in whatever language the server wrote it.
 *
 * Takes a translator rather than holding one. This module has no locale and
 * cannot be given one: a `t` captured at import time goes stale the moment the
 * reader switches language, which is the one thing the locale provider exists
 * to prevent. Callers inside React pass `useT()`; the English is the answer for
 * a caller that has no locale at all.
 */
export function messageFor(
  e: unknown,
  // Narrowed to the two keys this function can ask for, so a caller's `t` --
  // which accepts every key in the catalogue -- is assignable to it. Typed as
  // `(key: string)` it would not be: a function accepting fewer inputs cannot
  // stand in for one accepting more.
  translate?: (key: 'nx.err.networkPlain' | 'nx.err.generic') => string,
): string {
  if (isApiError(e)) return e.message;
  if (isNetworkError(e)) {
    return (
      translate?.('nx.err.networkPlain') ??
      'RawSyst cannot reach the server. Check the connection and try again.'
    );
  }
  return (
    translate?.('nx.err.generic') ??
    'Something went wrong. Try again, and tell support if it keeps happening.'
  );
}
