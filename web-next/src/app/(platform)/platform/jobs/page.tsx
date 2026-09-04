'use client';

// Background work that did not finish.
//
// # A dead job has no retry, and that is the point
//
// The route says so plainly, and a live call confirms it: retrying a DEAD job
// answers 409 with "exhausted its attempts on something retrying will not fix".
// So the retry is absent for those rows rather than present and refused -- an
// operator should not learn that from an error after pressing it.
//
// # The kind is shown exactly as the backend writes it
//
// `zatca.submit`, `stock.low_sweep`, `accounting.tie_out`. Opening the
// underscores would make `stock.low_sweep` read as `stock.low sweep`, which is
// a string that appears in no log and cannot be searched for. An identifier is
// more useful accurate than pretty.
//
// # No business means the platform's own work
//
// `tenant_id` is nullable and the sweeps run without one. An em dash there
// would leave an operator wondering whose job it was; the answer is nobody's
// but ours, which changes who has to fix it.
//
// # The error is the reason anybody opened this screen
//
// It is truncated at the database, not in the browser, because a stack trace
// in a list is a list nobody can read. What arrives is what is shown.

import { CheckCircle2, RotateCw } from 'lucide-react';
import { useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface FailedJob {
  id: string;
  /** Absent for the platform's own sweeps. */
  tenant_id?: string;
  tenant?: string;
  kind: string;
  status: string;
  attempts: number;
  last_error?: string;
  failed_at: string;
}

function JobsScreen() {
  const t = useT();
  const { data, isLoading, error, refetch } = useApiList<FailedJob>(
    '/platform/jobs/failed',
    undefined,
    // An operator watching a queue drain wants it current without pressing
    // anything; thirty seconds is short enough to notice and long enough not
    // to be load of its own.
    { refetchInterval: 30_000, staleTime: 0 },
  );

  const [retrying, setRetrying] = useState<string | null>(null);
  const [retried, setRetried] = useState<Set<string>>(new Set());
  // A retry can still fail even with the dead rows excluded: the worker may
  // have picked the job up, another operator may have queued it already, or
  // the network may be down. Swallowing that would leave a button that looks
  // pressed and did nothing.
  const [retryError, setRetryError] = useState<string | null>(null);

  const jobs = data?.data ?? [];

  async function retry(id: string) {
    setRetrying(id);
    setRetryError(null);
    try {
      await api.post(`/platform/jobs/${id}/retry`);
      setRetried((s) => new Set(s).add(id));
      await refetch();
    } catch (e) {
      setRetryError(messageFor(e, t));
      // The list is the evidence for what just happened -- if the job moved
      // under us, the operator should see the row it moved to.
      await refetch();
    } finally {
      setRetrying(null);
    }
  }

  const columns: Column<FailedJob>[] = [
    {
      key: 'kind',
      header: t('nx.plat.colJob'),
      primary: true,
      cell: (j) => <span className="num">{j.kind}</span>,
    },
    {
      key: 'tenant',
      header: t('nx.plat.colOwner'),
      secondary: true,
      cell: (j) =>
        j.tenant ? (
          <span className="text-muted">{j.tenant}</span>
        ) : (
          <span className="text-subtle italic">{t('nx.plat.platformJob')}</span>
        ),
    },
    {
      key: 'attempts',
      header: t('nx.plat.colAttempts'),
      numeric: true,
      width: 'w-20',
      cell: (j) => j.attempts,
    },
    {
      key: 'failed_at',
      header: t('nx.plat.colFailedAt'),
      secondary: true,
      width: 'w-40',
      cell: (j) => (
        <time dateTime={j.failed_at} className="num text-muted">
          {j.failed_at.slice(0, 16).replace('T', ' ')}
        </time>
      ),
    },
    {
      key: 'error',
      header: t('nx.plat.colWhy'),
      cell: (j) => (
        <span className="line-clamp-2 text-caption text-muted">
          {j.last_error || '—'}
        </span>
      ),
    },
    {
      key: 'action',
      header: '',
      width: 'w-36',
      cell: (j) => {
        if (j.status === 'dead') {
          return (
            <span className="text-caption text-subtle">
              {t('nx.plat.deadNoRetry')}
            </span>
          );
        }
        if (retried.has(j.id)) {
          return <Badge tone="positive">{t('nx.plat.retried')}</Badge>;
        }
        return (
          <Button
            variant="secondary"
            size="sm"
            busy={retrying === j.id}
            disabled={retrying !== null}
            onClick={() => void retry(j.id)}
          >
            <RotateCw aria-hidden="true" />
            {t('nx.plat.retry')}
          </Button>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.plat.jobsTitle')}
        description={t('nx.plat.jobsSubtitle')}
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      <FormError message={retryError} className="mb-4" />

      {isLoading && jobs.length === 0 ? <TableSkeleton columns={6} /> : null}

      {!isLoading && !error && jobs.length === 0 ? (
        <EmptyState
          icon={CheckCircle2}
          title={t('nx.plat.jobsEmptyTitle')}
          description={t('nx.plat.jobsEmptyDesc')}
        />
      ) : null}

      {jobs.length > 0 ? (
        <Panel flush>
          <DataTable
            caption={t('nx.plat.jobsCaption')}
            columns={columns}
            rows={jobs}
            rowKey={(j) => j.id}
            className="rounded-none border-0"
          />
        </Panel>
      ) : null}
    </>
  );
}

export default function JobsPage() {
  return (
    <RequireWorkspace workspace="platform">
      <JobsScreen />
    </RequireWorkspace>
  );
}
