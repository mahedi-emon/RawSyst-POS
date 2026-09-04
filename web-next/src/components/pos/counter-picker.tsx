'use client';

// Which counter am I about to stand at?
//
// The list is already filtered by the server to counters that would actually
// work -- active, and the kind a browser may open. A counter paired to a
// desktop till is absent rather than shown-and-refused, because listing an
// option that fails on selection teaches cashiers to try things.
//
// Targets are large and there are few of them. This is the first screen of a
// shift, often on a tablet, often by somebody who has just walked in.

import { Monitor, Store } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { useApiList } from '@/lib/api/hooks';
import { useCounter, type Counter } from '@/lib/pos/counter';
import { cn } from '@/lib/utils';

export function CounterPicker() {
  const { data, isLoading, error, refetch } = useApiList<Counter>('/pos/counters');
  const { open, state } = useCounter();

  const counters = data?.data ?? [];
  const opening = state.kind === 'opening';

  return (
    <div className="mx-auto flex min-h-dvh max-w-2xl flex-col justify-center gap-6 px-4 py-10">
      <div>
        <h1 className="text-page font-semibold text-fg">Open a counter</h1>
        <p className="mt-1 text-body text-muted">
          Everything you ring up will be recorded against the counter you choose,
          and against its own drawer and shift.
        </p>
      </div>

      {state.kind === 'failed' && (
        <ErrorState error={state.error} onRetry={() => void refetch()} />
      )}

      {error && <ErrorState error={error} onRetry={() => void refetch()} />}

      {isLoading && (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      )}

      {!isLoading && !error && counters.length === 0 && (
        <EmptyState
          icon={Monitor}
          title="There is no counter here you can open"
          description="A counter has to be set up before it can sell, and a counter paired to a desktop till is opened on that machine rather than in a browser. Tills and devices, under Settings, is where they are added."
          action={
            <Button asChild variant="secondary">
              <a href="/settings/devices">Go to tills and devices</a>
            </Button>
          }
        />
      )}

      {counters.length > 0 && (
        <ul className="flex flex-col gap-2">
          {counters.map((counter) => (
            <li key={counter.id}>
              <button
                type="button"
                disabled={opening}
                onClick={() => void open(counter.id)}
                className={cn(
                  'flex min-h-16 w-full items-center gap-3 rounded-md border px-4',
                  'border-line-strong bg-surface text-start',
                  'hover:border-primary hover:bg-surface-selected',
                  'disabled:pointer-events-none disabled:opacity-55',
                )}
              >
                <Store className="size-5 shrink-0 text-muted" aria-hidden="true" />
                <span className="min-w-0 flex-1">
                  <span className="block text-lede font-semibold text-fg">
                    {counter.terminal_label}
                  </span>
                  <span className="block text-label text-muted">
                    {counter.store}
                    {counter.egs_unit ? ` · ${counter.egs_unit}` : ''}
                  </span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      <a
        href="/dashboard"
        className="text-center text-label text-muted underline underline-offset-4 hover:text-fg"
      >
        Go back to the back office instead
      </a>
    </div>
  );
}
