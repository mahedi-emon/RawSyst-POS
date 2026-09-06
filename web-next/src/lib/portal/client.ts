// Calling the portal routes.
//
// Every portal route takes `tenant_id` and `company_id` in the query and a
// portal bearer token in the header. Both are supplied here rather than by
// each caller, because forgetting either produces the same refusal — "That
// sign-in link is not complete" for the first, a 401 for the second — and
// neither message would point at the screen that forgot.
//
// The error envelope is the API's own, so `ApiError` and `messageFor` from the
// staff client are reused. That much IS shared, because it is the server's
// contract rather than the staff session's.

import { ApiError, NetworkError, type ApiErrorBody } from '../api/errors';
import type { Shop } from './session';

const BASE = '/api/v1';

export interface PortalRequest {
  shop: Shop;
  token?: string | null;
  query?: Record<string, string | number | boolean | undefined | null>;
  signal?: AbortSignal;
}

async function send<T>(
  method: string,
  path: string,
  { shop, token, query, signal }: PortalRequest,
  body?: unknown,
): Promise<T> {
  const url = new URL(BASE + path, window.location.origin);
  url.searchParams.set('tenant_id', shop.tenantId);
  url.searchParams.set('company_id', shop.companyId);
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value === undefined || value === null || value === '') continue;
    url.searchParams.set(key, String(value));
  }

  let res: Response;
  try {
    res = await fetch(url.toString(), {
      method,
      headers: {
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (cause) {
    throw new NetworkError(cause);
  }

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  let parsed: unknown = null;
  try {
    parsed = text ? JSON.parse(text) : null;
  } catch {
    parsed = null;
  }

  if (!res.ok) {
    // The envelope is nested under `error`, exactly as it is for staff routes.
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

export const portalApi = {
  get: <T>(path: string, req: PortalRequest) => send<T>('GET', path, req),
  post: <T>(path: string, req: PortalRequest, body?: unknown) =>
    send<T>('POST', path, req, body),
  put: <T>(path: string, req: PortalRequest, body?: unknown) =>
    send<T>('PUT', path, req, body),
  del: <T>(path: string, req: PortalRequest) => send<T>('DELETE', path, req),
};

/** A collection, as the portal routes wrap one. */
export interface PortalList<T> {
  data: T[];
}
