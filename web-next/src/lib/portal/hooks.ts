'use client';

// Fetching, for portal screens.
//
// A thin wrapper over React Query, the same shape as the staff `useApi`, but
// keyed on the SHOP as well as the path: two shops open in two tabs must not
// share a cache entry, and the token differs per shop too.
//
// Nothing fetches until there is a token. A portal screen rendered without one
// would fire a wave of 401s on the way to the sign-in redirect.

import { useRouter } from 'next/navigation';
import { useEffect } from 'react';

import { useQuery, type UseQueryOptions } from '@tanstack/react-query';

import { portalApi, type PortalList } from './client';
import { usePortalSession } from './session';

export function usePortal<T>(
  path: string | null,
  query?: Record<string, string | number | boolean | undefined | null>,
  options?: Partial<UseQueryOptions<T, Error>>,
) {
  const { shop, token } = usePortalSession();
  return useQuery<T, Error>({
    queryKey: ['portal', shop?.tenantId, shop?.companyId, path, query ?? {}],
    queryFn: ({ signal }) =>
      portalApi.get<T>(path as string, {
        shop: shop as NonNullable<typeof shop>,
        token,
        query,
        signal,
      }),
    enabled: Boolean(shop) && Boolean(token) && path !== null,
    ...options,
  });
}

/** A portal collection, which arrives under `data`. */
export function usePortalList<T>(
  path: string | null,
  query?: Record<string, string | number | boolean | undefined | null>,
  options?: Partial<UseQueryOptions<PortalList<T>, Error>>,
) {
  return usePortal<PortalList<T>>(path, query, options);
}

/**
 * Sends a portal screen to sign-in when it has no session.
 *
 * Returns true while the answer is still unknown OR while the redirect is in
 * flight, so a screen renders its quiet state rather than flashing an empty
 * list on the way out. Redirecting in an effect rather than during render:
 * navigating while React is rendering is a side effect in the wrong place, and
 * it warns in development for the reason that it can run twice.
 */
export function usePortalGuard(signInHref: string): boolean {
  const { token, ready } = usePortalSession();
  const router = useRouter();

  useEffect(() => {
    // Replace rather than push: the back button should leave the portal, not
    // bounce between a screen and the sign-in it just redirected from.
    if (ready && !token) router.replace(signInHref);
  }, [ready, token, router, signInHref]);

  return !ready || !token;
}
