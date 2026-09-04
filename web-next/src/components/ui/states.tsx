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
// shows the server's own sentence. What it adds is the remedy, which depends on
// the code: a 403 needs a different next step from a 409, and "Something went
// wrong" serves neither.

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
  what = 'records',
}: {
  onClear?: () => void;
  what?: string;
}) {
  return (
    <EmptyState
      icon={CircleSlash}
      title="Nothing matches those filters"
      description={`There are ${what} here, but none of them match what you have narrowed to.`}
      action={
        onClear && (
          <Button variant="secondary" onClick={onClear}>
            Clear the filters
          </Button>
        )
      }
    />
  );
}

interface Remedy {
  icon: typeof TriangleAlert;
  title: string;
  /** What to do about it. Shown under the server's own message. */
  advice: string;
  retryable: boolean;
}

/** What kind of failure this is, and what a person can do about it. */
function remedyFor(error: unknown): Remedy {
  if (isNetworkError(error)) {
    return {
      icon: WifiOff,
      title: 'RawSyst cannot reach the server',
      advice:
        'Check the connection. Nothing has been lost — this screen will load once the connection is back.',
      retryable: true,
    };
  }

  if (!(error instanceof ApiError)) {
    return {
      icon: TriangleAlert,
      title: 'Something went wrong',
      advice: 'Try again. If it keeps happening, tell support what you were doing.',
      retryable: true,
    };
  }

  switch (error.status) {
    case 403:
      return {
        icon: Lock,
        title: 'You do not have access to this',
        advice:
          'Your account is missing the permission this screen needs. The business owner can grant it under Users and roles.',
        retryable: false,
      };
    case 404:
      return {
        icon: CircleSlash,
        title: 'That is not here',
        // The API answers 404 rather than 403 for another business's record,
        // deliberately -- a 403 would confirm the record exists. So this
        // sentence has to cover both without implying either.
        advice:
          'It may have been removed, or the link may be pointing somewhere that is not part of this business.',
        retryable: false,
      };
    case 402:
      return {
        icon: Lock,
        title: 'Your plan does not include this',
        advice:
          'This module is sold separately. Plan and billing, under Settings, shows what the current plan covers.',
        retryable: false,
      };
    case 409:
      return {
        icon: TriangleAlert,
        title: 'Something changed underneath this',
        advice:
          'Somebody else has edited this, or the period it belongs to has been closed. Reload and look at the current state before trying again.',
        retryable: true,
      };
    case 422:
      return {
        icon: TriangleAlert,
        // A compliance refusal is not a validation error to correct. The
        // request was well formed; the legal situation refused it.
        title: error.isComplianceRefusal
          ? 'This is not allowed here'
          : 'That could not be accepted',
        advice: error.isComplianceRefusal
          ? 'This is a rule the product cannot set aside. The reason above is the whole of it.'
          : 'Correct the highlighted values and try again.',
        retryable: false,
      };
    case 429:
      return {
        icon: TriangleAlert,
        title: 'Too many requests, too quickly',
        advice: 'Wait a few seconds and try again.',
        retryable: true,
      };
    case 503:
      return {
        icon: ServerCrash,
        title: 'A part of RawSyst is unavailable',
        advice: 'This is usually brief. Try again shortly.',
        retryable: true,
      };
    default:
      return {
        icon: ServerCrash,
        title: 'The server could not complete that',
        advice:
          'This one is our fault, and it has been recorded. Try again shortly.',
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
  const remedy = remedyFor(error);
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
        <p className="text-card-title font-semibold text-fg">{remedy.title}</p>
        {/* The server's own words first: they are specific to what was
            refused, and nothing here can improve on them. */}
        {serverMessage && serverMessage !== remedy.title && (
          <p className="mt-1 text-body text-fg">{serverMessage}</p>
        )}
        <p className="mt-1 text-body text-muted">{remedy.advice}</p>
      </div>

      {remedy.retryable && onRetry && (
        <Button variant="secondary" onClick={onRetry} className="mt-1">
          <RefreshCw aria-hidden="true" />
          Try again
        </Button>
      )}

      {/* Correlates with the server log. Shown small, because it is for the
          support conversation and not for the person's benefit. */}
      {requestId && (
        <p className="mt-1 text-caption text-subtle">
          Reference {requestId}
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
  return (
    <div className="mx-auto flex max-w-lg flex-col items-center gap-4 py-20 text-center">
      <div className="grid size-12 place-items-center rounded-full bg-surface-sunken">
        <Lock className="size-5 text-muted" aria-hidden="true" />
      </div>
      <div>
        <h1 className="text-page font-semibold text-fg">
          You do not have access to this
        </h1>
        <p className="mt-2 text-body text-muted">
          Your account does not include the permission this screen needs. This
          is not a fault — it is how access is set up for your role.
        </p>
        {needed && (
          <p className="mt-3 text-label text-subtle">
            Ask the business owner to grant{' '}
            <code className="rounded-xs bg-surface-sunken px-1 py-0.5 text-fg">
              {needed}
            </code>{' '}
            under Users and roles.
          </p>
        )}
      </div>
      <Button asChild variant="secondary">
        <a href={backHref}>Go back to where you can work</a>
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
