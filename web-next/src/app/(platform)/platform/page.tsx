'use client';

// Service health.
//
// # Counts, not lists
//
// The platform overview is deliberately made of numbers. An operator needs the
// SHAPE of the load -- how many businesses, how much is queued, how much is
// stuck -- and the names of the businesses would be an exposure that answers no
// question they are asking. The Go route says so in its own comment, and this
// screen does not go looking for the names.
//
// # Two failures that look alike and are not
//
// A queue with a backlog and a queue that is failing need different responses,
// so they are separate figures with separate thresholds. Same for a business
// that has never been backed up versus one whose backup has never been
// verified: only the second is a number an operator can do something about
// today.

import { Activity, Database, HardDrive, LifeBuoy, Server } from 'lucide-react';

import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { useApi } from '@/lib/api/hooks';
import { cn } from '@/lib/utils';

interface Health {
  database_ok: boolean;
  database_latency_ms: number;
  tenants: number;
  active_tenants: number;
  companies: number;
  users: number;
  active_users_30d: number;
  terminals: number;
  jobs_queued: number;
  jobs_running: number;
  jobs_failed_24h: number;
  jobs_dead: number;
  submissions_pending: number;
  submissions_failed: number;
  tenants_with_verified_backup: number;
  tenants_without_verified_backup: number;
  sync_failures_24h: number;
  tickets_open: number;
  tickets_waiting_on_support: number;
  checked_at: string;
}

export default function PlatformHealthPage() {
  const { data, isLoading, error, refetch } = useApi<Health>('/platform/health', undefined, {
    // The point of this screen is that it is current. Thirty seconds is short
    // enough to notice a queue backing up and long enough not to be a load
    // generator of its own.
    refetchInterval: 30_000,
    staleTime: 0,
  });

  return (
    <>
      <PageHeader
        title="Service health"
        description="The shape of the platform right now: what is running, what is queued and what is stuck."
      />

      {error && <ErrorState error={error} onRetry={() => void refetch()} />}

      {isLoading && !data && (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-28 w-full" />
          ))}
        </div>
      )}

      {data && (
        <div className="flex flex-col gap-6">
          <section
            aria-label="Right now"
            className="grid gap-px overflow-hidden rounded-md border border-line bg-line sm:grid-cols-2 xl:grid-cols-4"
          >
            <Stat
              icon={Database}
              label="Database"
              value={data.database_ok ? `${data.database_latency_ms} ms` : 'Not answering'}
              caption={
                data.database_ok
                  ? 'Answered this check'
                  : 'The one figure here that is measured, and it failed'
              }
              tone={
                !data.database_ok
                  ? 'critical'
                  : data.database_latency_ms > 250
                    ? 'caution'
                    : 'ok'
              }
            />
            <Stat
              icon={Server}
              label="Businesses trading"
              value={`${data.active_tenants} of ${data.tenants}`}
              // Counting a signup as active is how a platform tells itself a
              // story. The route defines active as "traded in the last thirty
              // days", and the caption says so rather than leaving the reader
              // to assume the flattering meaning.
              caption="Sold something in the last 30 days"
            />
            <Stat
              icon={Activity}
              label="Queue"
              value={`${data.jobs_queued} waiting`}
              caption={`${data.jobs_running} running · ${data.jobs_failed_24h} failed in 24h`}
              tone={data.jobs_dead > 0 ? 'caution' : 'ok'}
            />
            <Stat
              icon={LifeBuoy}
              label="Tickets"
              value={String(data.tickets_open)}
              caption={`${data.tickets_waiting_on_support} waiting on us`}
              tone={data.tickets_waiting_on_support > 0 ? 'caution' : 'ok'}
            />
          </section>

          <div className="grid gap-5 lg:grid-cols-3">
            <Panel
              title="Stuck work"
              description="Things that will not clear without somebody"
            >
              <ul className="flex flex-col">
                <Row
                  label="Dead jobs"
                  value={data.jobs_dead}
                  // A DEAD job is deliberately not retriable: it exhausted its
                  // attempts on something retrying cannot fix. Saying that here
                  // stops an operator hunting for a retry button.
                  hint="Exhausted their attempts. Retrying will not fix these."
                  bad={data.jobs_dead > 0}
                  href="/platform/jobs"
                />
                <Row
                  label="Submissions failed"
                  value={data.submissions_failed}
                  hint="E-invoicing documents refused by the authority"
                  bad={data.submissions_failed > 0}
                />
                <Row
                  label="Submissions pending"
                  value={data.submissions_pending}
                  hint="In flight, retrying"
                />
                <Row
                  label="Sync failures, 24h"
                  value={data.sync_failures_24h}
                  hint="Terminals that could not push"
                  bad={data.sync_failures_24h > 0}
                />
              </ul>
            </Panel>

            <Panel
              title="Backups"
              description="Verified, not merely taken"
            >
              <div className="flex flex-col gap-3">
                <p className="num text-figure font-semibold leading-none tabular-nums">
                  {data.tenants_without_verified_backup}
                </p>
                <p className="text-body text-muted">
                  {data.tenants_without_verified_backup === 0
                    ? 'Every business has proved it can restore.'
                    : `${data.tenants_without_verified_backup === 1 ? 'One business has' : `${data.tenants_without_verified_backup} businesses have`} never proved a restore. A backup nobody has restored is a file, not a backup.`}
                </p>
                <div className="rule pt-3">
                  <p className="text-label text-muted">
                    <HardDrive
                      className="me-1.5 inline size-3.5 align-[-2px]"
                      aria-hidden="true"
                    />
                    {data.tenants_with_verified_backup} verified of {data.tenants}
                  </p>
                </div>
              </div>
            </Panel>

            <Panel title="Scale" description="What the platform is carrying">
              <ul className="flex flex-col">
                <Row label="Businesses" value={data.tenants} />
                <Row label="Companies" value={data.companies} />
                <Row
                  label="Users"
                  value={data.users}
                  hint={`${data.active_users_30d} signed in within 30 days`}
                />
                <Row label="Terminals" value={data.terminals} />
              </ul>
            </Panel>
          </div>

          <p className="text-caption text-subtle">
            Checked <time dateTime={data.checked_at}>{data.checked_at}</time>.
            Refreshes every 30 seconds.
          </p>
        </div>
      )}
    </>
  );
}

function Stat({
  icon: Icon,
  label,
  value,
  caption,
  tone = 'ok',
}: {
  icon: typeof Activity;
  label: string;
  value: string;
  caption: string;
  tone?: 'ok' | 'caution' | 'critical';
}) {
  return (
    <div className="bg-surface p-4">
      <p className="flex items-center gap-1.5 text-label text-muted">
        <Icon className="size-3.5" aria-hidden="true" />
        {label}
      </p>
      <p
        className={cn(
          'num mt-1 text-display font-semibold tabular-nums tracking-tight',
          tone === 'caution' && 'text-caution-fg',
          tone === 'critical' && 'text-critical-fg',
        )}
      >
        {value}
      </p>
      <p className="mt-1 text-caption text-subtle">{caption}</p>
    </div>
  );
}

function Row({
  label,
  value,
  hint,
  bad,
  href,
}: {
  label: string;
  value: number;
  hint?: string;
  bad?: boolean;
  href?: string;
}) {
  const body = (
    <>
      <span className="min-w-0">
        <span className="block text-body text-fg">{label}</span>
        {hint && <span className="block text-caption text-subtle">{hint}</span>}
      </span>
      <span className="shrink-0">
        {bad ? (
          <Badge tone="caution">{value}</Badge>
        ) : (
          <span className="num text-body font-medium tabular-nums">{value}</span>
        )}
      </span>
    </>
  );

  return (
    <li className="border-b border-line last:border-b-0">
      {href ? (
        <a
          href={href}
          className="-mx-2 flex items-start justify-between gap-3 rounded-sm px-2 py-2.5 hover:bg-surface-hover"
        >
          {body}
        </a>
      ) : (
        <div className="flex items-start justify-between gap-3 py-2.5">{body}</div>
      )}
    </li>
  );
}
