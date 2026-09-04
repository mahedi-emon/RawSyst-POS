'use client';

// The query layer.
//
// Thin on purpose: React Query holds the cache, the API client holds the
// transport, and this file only names the keys so two screens asking for the
// same thing share one request rather than each fetching it.

import { useQuery, type UseQueryOptions } from '@tanstack/react-query';

import { api, type Page } from './client';
import { useSession } from '../auth/session';

/**
 * A GET, cached by its path and query.
 *
 * The key is the path and the parameters, so `/customers?status=active` and
 * `/customers?status=all` are two entries rather than one that keeps replacing
 * itself.
 */
export function useApi<T>(
  path: string | null,
  query?: Record<string, string | number | boolean | undefined | null>,
  options?: Partial<UseQueryOptions<T, Error>>,
) {
  const { status } = useSession();
  return useQuery<T, Error>({
    queryKey: [path, query ?? {}],
    queryFn: ({ signal }) => api.get<T>(path as string, { query, signal }),
    // Nothing is fetched until there is a session. Firing during the resolve
    // produces a wave of 401s and a refresh storm on every page load.
    enabled: status === 'signed-in' && path !== null,
    ...options,
  });
}

/** A collection endpoint, which the API wraps with pagination metadata. */
export function useApiList<T>(
  path: string | null,
  query?: Record<string, string | number | boolean | undefined | null>,
  options?: Partial<UseQueryOptions<Page<T>, Error>>,
) {
  return useApi<Page<T>>(path, query, options);
}

export interface CompanyRecord {
  id: string;
  legal_name: string;
  trade_name?: string;
  /** ISO country. Drives grouping, date format and tax vocabulary. */
  country: string;
  /** ISO currency. There is no default; every figure is shown in this. */
  base_currency: string;
}

/**
 * The companies this person can see.
 *
 * Every money figure in the product is denominated in one of these, so this is
 * fetched once near the root and read from cache by everything that formats an
 * amount. A screen that hardcoded a currency would be wrong in two of the three
 * markets the product sells into.
 */
export function useCompanies() {
  return useApiList<CompanyRecord>('/companies', undefined, {
    // Companies change when somebody adds one, which is rare. Refetching this
    // on every screen would be a request per navigation for an answer that has
    // not moved since the business was set up.
    staleTime: 10 * 60 * 1000,
  });
}

export interface Entitlement {
  feature: string;
  /** What the plan tier includes. */
  in_plan: boolean;
  /** The effective answer, after any exception granted to this business. */
  allowed: boolean;
  /** Why an exception was granted. Empty unless there is one. */
  reason?: string;
  expires_on?: string;
}

/**
 * The plan modules this business may reach.
 *
 * `allowed` rather than `in_plan`: a business can be granted a module its tier
 * does not include, and navigation should offer what the business can actually
 * use rather than what the price list says.
 *
 * Navigation reads this to say "not included in your plan" instead of offering
 * a link the backend answers 402 to. When the call fails the set is undefined,
 * and navigation treats that as everything included -- showing a link that
 * turns out to be refused is a smaller failure than hiding a module the
 * business is paying for.
 */
export function usePlanFeatures(): ReadonlySet<string> | undefined {
  const { data, isError } = useApiList<Entitlement>(
    '/subscription/entitlements',
    undefined,
    { staleTime: 10 * 60 * 1000, retry: false },
  );
  if (isError || !data?.data) return undefined;
  return new Set(data.data.filter((e) => e.allowed).map((e) => e.feature));
}
