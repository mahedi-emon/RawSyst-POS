'use client';

// The client-side context the whole product sits in.
//
// Three providers, in an order that matters: the query client has to exist
// before anything fetches, the session has to resolve before a screen can ask
// what somebody may do, and the locale wraps both so a loading state is already
// in the right language.

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';

import { ApiError } from '@/lib/api/errors';
import { SessionProvider } from '@/lib/auth/session';
import { LocaleProvider } from '@/lib/i18n/locale';

function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Thirty seconds. An ERP screen is read, acted on, and read again; data
        // that refetches on every focus change makes a table jump under
        // somebody's cursor, and data cached for minutes shows a stock figure
        // that has since been sold.
        staleTime: 30_000,
        refetchOnWindowFocus: false,
        retry: (failureCount, error) => {
          // A refusal is not a transient failure. Retrying a 403 three times
          // produces three audit entries and the same answer.
          if (error instanceof ApiError) {
            if (error.status < 500 && error.status !== 429) return false;
          }
          return failureCount < 2;
        },
      },
      mutations: {
        // Never automatic. A write that retries itself is how a sale gets
        // posted twice; where a retry is genuinely safe the caller sends an
        // Idempotency-Key and asks for it explicitly.
        retry: false,
      },
    },
  });
}

export function Providers({ children }: { children: ReactNode }) {
  // Created in state rather than at module scope, so a server render and a
  // client render do not share one cache between different users.
  const [queryClient] = useState(makeQueryClient);

  return (
    <QueryClientProvider client={queryClient}>
      <LocaleProvider>
        <SessionProvider>{children}</SessionProvider>
      </LocaleProvider>
    </QueryClientProvider>
  );
}
