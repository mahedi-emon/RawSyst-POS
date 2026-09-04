'use client';

// Empty, loading and failed.
//
// # An empty screen is an invitation, not an apology
//
// "No data" tells somebody nothing they did not already know. Every empty state
// here says what the screen is for and offers the action that fills it, because
// the moment a list is empty is the moment a person most needs to know what to
// do next.
//
// # A failure says what happened and what to do
//
// The API writes its refusals for the person reading them, so `ErrorState`
// shows the server's own sentence in whatever language the server wrote it.
// What this layer adds is the REMEDY, which depends on the code and is
// translated: a 403 needs a different next step from a 409, and "Something
// went wrong" serves neither.

import {
  CircleSlash,
  Inbox,
  Lock,
  RefreshCw,
  ServerCrash,
  TriangleAlert,
  WifiOff,
} from 'lucide-react';
import type { ReactNode } from 'react';

import { ApiError, isNetworkError } from '@/lib/api/errors';
import { useT, type Key } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

import { Button } from './button';

export function EmptyState({
  icon: Icon = Inbox,
  title,
  description,
  action,
  className,
}: {
  icon?: typeof Inbox;
  title: string;
  description: string;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-3 rounded-md',
        'border border-dashed border-line-strong bg-surface px-6 py-12 text-center',
        className,
      )}
    >
      <Icon className="size-7 text-disabled" aria-hidden="true" />
      <div className="max-w-[52ch]">
        <p className="text-card-title font-semibold text-fg">{title}</p>
        <p className="mt-1 text-body text-muted">{description}</p>
      </div>
      {action && <div className="mt-1">{action}</div>}
    </div>
  );
}

/**
 * The empty state for a filtered list, which is a different situation.
 *
 * The list is not empty; the filter is too narrow. Offering "Add a product"
 * here would be wrong -- the product may well exist, three filters away.
 */
export function NoMatches({
  onClear,
  what,
}: {
  onClear?: () => void;
  /** Translated plural noun for the thing being filtered, e.g. "products". */
  what?: string;
}) {
  const t = useT();
  return (
    <EmptyState
      icon={CircleSlash}
      title={t('nx.empty.noMatches.title')}
      description={t('nx.empty.noMatches.desc', {
        what: what ?? t('nx.empty.records'),
      })}
      action={
        onClear && (
          <Button variant="secondary" onClick={onClear}>
            {t('nx.empty.clearFilters')}
          </Button>
        )
      }
    />
  );
}

interface Remedy {
  icon: typeof TriangleAlert;
  title: Key;
  /** What to do about it. Shown under the server's own message. */
  advice: Key;
  retryable: boolean;
}

/** What kind of failure this is, and what a person can do about it. */
function remedyFor(error: unknown): Remedy {
  if (isNetworkError(error)) {
    return {
      icon: WifiOff,
      title: 'nx.err.offline.title',
      advice: 'nx.err.offline.advice',
      retryable: true,
    };
  }

  if (!(error instanceof ApiError)) {
    return {
      icon: TriangleAlert,
      title: 'nx.err.unknown.title',
      advice: 'nx.err.unknown.advice',
      retryable: true,
    };
  }

  switch (error.status) {
    case 403:
      return {
        icon: Lock,
        title: 'nx.err.forbidden.title',
        advice: 'nx.err.forbidden.advice',
        retryable: false,
      };
    case 404:
      // The API answers 404 rather than 403 for another business's record,
      // deliberately -- a 403 would confirm the record exists. So this
      // sentence has to cover both without implying either.
      return {
        icon: CircleSlash,
        title: 'nx.err.notFound.title',
        advice: 'nx.err.notFound.advice',
        retryable: false,
      };
    case 402:
      return {
        icon: Lock,
        title: 'nx.err.plan.title',
        advice: 'nx.err.plan.advice',
        retryable: false,
      };
    case 409:
      return {
        icon: TriangleAlert,
        title: 'nx.err.conflict.title',
        advice: 'nx.err.conflict.advice',
        retryable: true,
      };
    case 422:
      // A compliance refusal is not a validation error to correct. The
      // request was well formed; the legal situation refused it.
      return error.isComplianceRefusal
        ? {
            icon: TriangleAlert,
            title: 'nx.err.compliance.title',
            advice: 'nx.err.compliance.advice',
            retryable: false,
          }
        : {
            icon: TriangleAlert,
            title: 'nx.err.unprocessable.title',
            advice: 'nx.err.unprocessable.advice',
            retryable: false,
          };
    case 429:
      return {
        icon: TriangleAlert,
        title: 'nx.err.rateLimit.title',
        advice: 'nx.err.rateLimit.advice',
        retryable: true,
      };
    case 503:
      return {
        icon: ServerCrash,
        title: 'nx.err.unavailable.title',
        advice: 'nx.err.unavailable.advice',
        retryable: true,
      };
    default:
      return {
        icon: ServerCrash,
        title: 'nx.err.server.title',
        advice: 'nx.err.server.advice',
        retryable: true,
      };
  }
}

export function ErrorState({
  error,
  onRetry,
  className,
}: {
  error: unknown;
  onRetry?: () => void;
  className?: string;
}) {
  const t = useT();
  const remedy = remedyFor(error);
  const title = t(remedy.title);
  const serverMessage = error instanceof ApiError ? error.message : null;
  const requestId = error instanceof ApiError ? error.requestId : undefined;

  return (
    <div
      role="alert"
      className={cn(
        'flex flex-col items-center justify-center gap-3 rounded-md',
        'border border-line bg-surface px-6 py-12 text-center',
        className,
      )}
    >
      <remedy.icon className="size-7 text-critical" aria-hidden="true" />
      <div className="max-w-[60ch]">
        <p className="text-card-title font-semibold text-fg">{title}</p>
        {/* The server's own words first: they are specific to what was
            refused, and nothing here can improve on them. */}
        {serverMessage && serverMessage !== title && (
          <p className="mt-1 text-body text-fg">{serverMessage}</p>
        )}
        <p className="mt-1 text-body text-muted">{t(remedy.advice)}</p>
      </div>

      {remedy.retryable && onRetry && (
        <Button variant="secondary" onClick={onRetry} className="mt-1">
          <RefreshCw aria-hidden="true" />
          {t('action.retry')}
        </Button>
      )}

      {/* Correlates with the server log. Shown small, because it is for the
          support conversation and not for the person's benefit. */}
      {requestId && (
        <p className="mt-1 text-caption text-subtle">
          {t('nx.err.reference', { id: requestId })}
        </p>
      )}
    </div>
  );
}

/**
 * The permission refusal, as a whole page.
 *
 * Reached when a URL is opened that the person's permissions do not cover --
 * typed, bookmarked, or followed from a link somebody else sent them. The route
 * guard renders this INSTEAD of the screen, so no unauthorised data is
 * requested at all; the backend would refuse it too, but the screen should not
 * ask.
 */
export function AccessDenied({
  /** The permission that would have opened it, named plainly. */
  needed,
  backHref = '/dashboard',
}: {
  needed?: string;
  backHref?: string;
}) {
  const t = useT();
  return (
    <div className="mx-auto flex max-w-lg flex-col items-center gap-4 py-20 text-center">
      <div className="grid size-12 place-items-center rounded-full bg-surface-sunken">
        <Lock className="size-5 text-muted" aria-hidden="true" />
      </div>
      <div>
        <h1 className="text-page font-semibold text-fg">{t('nx.denied.title')}</h1>
        <p className="mt-2 text-body text-muted">{t('nx.denied.body')}</p>
        {needed && (
          // The permission string is NOT translated: it is an identifier the
          // roles screen shows verbatim, and a person repeating it to whoever
          // grants it needs the same word in both places.
          <p className="mt-3 text-label text-subtle">
            {t('nx.denied.ask', { permission: needed })}
          </p>
        )}
      </div>
      <Button asChild variant="secondary">
        <a href={backHref}>{t('nx.denied.back')}</a>
      </Button>
    </div>
  );
}

/** A block of loading text, shaped like the content it stands in for. */
export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      className={cn('animate-pulse rounded-xs bg-surface-sunken', className)}
      aria-hidden="true"
    />
  );
}
