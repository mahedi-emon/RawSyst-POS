'use client';

// The bell, and the number on it.
//
// # Why this is its own request
//
// `GET /notifications` returns the list AND the count, and the route table says
// why the count also has a route of its own: "the bell is read on every screen
// and the list is not". Loading two hundred notices into every page of the
// product to draw a badge would be the wrong trade in both directions — the
// payload, and a cache that goes stale the moment somebody opens the list on
// another tab.
//
// # The number is the server's
//
// There is no client-side count. Read is a fact about this PERSON — the same
// announcement goes to several people and each reads it separately — so the
// only place that knows is the server, and a badge computed here would drift
// the moment a colleague cleared something on a shared till.
//
// # Silence until there is something to say
//
// No badge while the count is loading, and no badge at zero. A bell that
// flashes a spinner on every navigation is a bell people stop looking at, and
// "0" is a number somebody has to read before discovering it means nothing.
//
// # It refreshes on its own, slowly
//
// Sixty seconds, and again whenever the window regains focus. A notification is
// not a chat message; the cost of learning about a low-stock warning a minute
// late is nothing, and the cost of a request per second from every open till is
// a database nobody can explain.

import { Bell } from 'lucide-react';
import Link from 'next/link';

import { useApi } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

interface UnreadCount {
  unread: number;
}

/** Beyond this the exact number stops being useful and starts being wide. */
const TOO_MANY = 99;

export function NotificationBell() {
  const t = useT();
  const scope = useCompanyScope();

  // Every notification query names a company, because `notifyScope` resolves
  // one and answers 400 without it. Null until it is known, which `useApi`
  // reads as "not yet" rather than as a request to make.
  const { data, isError } = useApi<UnreadCount>(
    scope ? '/notifications/unread' : null,
    scope ?? undefined,
    {
      refetchInterval: 60_000,
      refetchOnWindowFocus: true,
      // A failed count must not retry in a loop behind a header that is on
      // every screen. The next interval will try again.
      retry: false,
      staleTime: 30_000,
    },
  );

  // A failure shows the bell with no number rather than an error: the header is
  // not the place to report that one background request did not answer, and the
  // notifications screen itself says so properly when somebody opens it.
  const unread = isError ? 0 : (data?.unread ?? 0);
  const shown = unread > TOO_MANY ? `${TOO_MANY}+` : String(unread);

  return (
    <Link
      href="/notifications"
      aria-label={
        unread > 0
          ? t('nx.bell.withUnread', { count: shown })
          : t('nx.bell.none')
      }
      className={cn(
        'relative grid size-10 place-items-center rounded-sm',
        'hover:bg-surface-hover',
      )}
    >
      <Bell className="size-5 text-muted" aria-hidden="true" />
      {unread > 0 && (
        <>
          <span
            aria-hidden="true"
            className={cn(
              // Sits on the corner of the icon rather than beside it, so the
              // control stays a 40px square and the header does not reflow
              // when the count arrives.
              'absolute -top-0.5 -end-0.5 min-w-4 rounded-full px-1',
              'bg-critical text-center text-caption leading-4 font-medium',
              'text-destructive-fg num',
            )}
          >
            {shown}
          </span>
          {/* Announced when it changes, so somebody using a screen reader
              learns of a new notice without opening the menu to check. */}
          <span className="sr-only" aria-live="polite">
            {t('nx.bell.withUnread', { count: shown })}
          </span>
        </>
      )}
    </Link>
  );
}
