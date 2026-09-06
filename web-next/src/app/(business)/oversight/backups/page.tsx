'use client';

// Backups.
//
// # A backup that ran is not a backup that restores
//
// The backend pins that sentence with a test of the same name, and it is the
// whole reason this screen exists rather than a list of green ticks. A file
// nobody has proved can be read is not a backup; it is a file. So "finished"
// and "verified" are different words here, and a run that finished without
// being verified is reported as work still to do.
//
// # The risk sentence is the server's
//
// `/backups/health` answers with `at_risk` and a `summary` in its own words —
// today, "No backup has ever been verified". That sentence leads the screen and
// is printed as written. Recomposing it from the parts would produce a second
// opinion about a question the business needs one answer to.
//
// # Taking the dump is not this product's job
//
// The service records and verifies; something outside it writes the file. The
// screen says so rather than implying a button here is what stands between the
// business and losing everything.

import { DatabaseBackup } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  backupState,
  fileSize,
  restorable,
  type Backup,
  type BackupHealth,
  type BackupState,
} from '@/lib/oversight/records';

const STATE_TONE: Record<BackupState, Tone> = {
  verified: 'positive',
  unverified: 'caution',
  failed: 'critical',
  running: 'info',
};

const STATE_LABEL: Record<BackupState, Key> = {
  verified: 'nx.bak.verified',
  unverified: 'nx.bak.unverified',
  failed: 'nx.bak.failed',
  running: 'nx.bak.running',
};

function BackupsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayRun = grants.can('backup.run');

  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const health = useApi<BackupHealth>(scope ? '/backups/health' : null, scope ?? undefined);
  const backups = useApiList<Backup>(scope ? '/backups' : null, scope ?? undefined);
  const rows = backups.data?.data ?? [];

  async function verify(backup: Backup) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(`/backups/${backup.id}/verify?company_id=${scope.company_id}`, {});
      await backups.refetch();
      await health.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Backup>[] = [
    {
      key: 'when',
      header: t('nx.bak.colWhen'),
      primary: true,
      cell: (b) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">
            {b.started_at.slice(0, 16).replace('T', ' ')}
          </span>
          <span className="text-caption text-muted">
            {b.kind}
            {b.requested_by ? ` · ${b.requested_by}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'state',
      header: t('nx.bak.colState'),
      width: 'w-56',
      cell: (b) => {
        const state = backupState(b);
        return (
          <span className="flex flex-col gap-1">
            <Badge tone={STATE_TONE[state]}>{t(STATE_LABEL[state])}</Badge>
            {/* The reason, as the server gave it. A failure with no reason is
                a failure somebody has to reproduce to understand. */}
            {b.verify_error || b.error ? (
              <span className="text-caption text-critical-fg">
                {b.verify_error || b.error}
              </span>
            ) : b.verified_at ? (
              <span className="num text-caption text-muted">
                {t('nx.bak.checkedOn', { on: b.verified_at.slice(0, 10) })}
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'size',
      header: t('nx.bak.colSize'),
      secondary: true,
      width: 'w-32',
      cell: (b) => (
        // Absent is not empty: a run still going has no size yet, and "0 B"
        // would read as a backup that captured nothing.
        <span className="num text-muted">
          {typeof b.size_bytes === 'number' ? fileSize(b.size_bytes) : '—'}
        </span>
      ),
    },
    {
      key: 'where',
      header: t('nx.bak.colWhere'),
      secondary: true,
      cell: (b) => <span className="num text-muted break-all">{b.location || '—'}</span>,
    },
    ...(mayRun
      ? [
          {
            key: 'act',
            header: t('nx.bak.colAction'),
            width: 'w-28',
            cell: (b: Backup) =>
              backupState(b) === 'unverified' ? (
                <Button variant="ghost" disabled={busy} onClick={() => void verify(b)}>
                  {t('nx.bak.verify')}
                </Button>
              ) : (
                <span className="text-caption text-muted">—</span>
              ),
          },
        ]
      : []),
  ];

  const h = health.data;
  const proven = restorable(rows);

  return (
    <>
      <PageHeader title={t('nx.bak.title')} description={t('nx.bak.subtitle')} />

      {error ? (
        <p className="mb-4 text-body text-critical-fg" role="alert">
          {error}
        </p>
      ) : null}

      {h ? (
        <Panel
          className="mb-6"
          title={t('nx.bak.standingTitle')}
          actions={
            <Badge tone={h.at_risk ? 'critical' : 'positive'}>
              {t(h.at_risk ? 'nx.bak.atRisk' : 'nx.bak.covered')}
            </Badge>
          }
        >
          {/* The server's sentence, printed as written. */}
          <p className="max-w-prose text-body text-fg">{h.summary}</p>

          <dl className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-label text-muted">{t('nx.bak.lastRun')}</dt>
              <dd className="num mt-0.5 text-body">
                {h.last_run_at ? h.last_run_at.slice(0, 16).replace('T', ' ') : t('nx.bak.never')}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.bak.lastVerified')}</dt>
              <dd className="num mt-0.5 text-body">
                {h.last_verified_at
                  ? h.last_verified_at.slice(0, 16).replace('T', ' ')
                  : t('nx.bak.never')}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.bak.recentFailures')}</dt>
              <dd className="num mt-0.5 text-body">{h.recent_failures}</dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.bak.provenLabel')}</dt>
              {/* Counted from the rows on this screen, and named as such: it
                  answers "how many of these could I restore from", which is a
                  different question from the server's health verdict. */}
              <dd className="num mt-0.5 text-body">
                {t('nx.bak.provenOf', { n: String(proven), total: String(rows.length) })}
              </dd>
            </div>
          </dl>

          <p className="mt-4 max-w-prose text-caption text-muted">
            {t('nx.bak.whoTakesIt')}
          </p>
        </Panel>
      ) : (
        <div className="mb-6">
          <TableSkeleton columns={4} />
        </div>
      )}

      {backups.error ? (
        <ErrorState error={backups.error} onRetry={() => void backups.refetch()} />
      ) : null}
      {backups.isLoading && !backups.data ? <TableSkeleton columns={5} /> : null}

      {!backups.isLoading && !backups.error && rows.length === 0 ? (
        <EmptyState
          icon={DatabaseBackup}
          title={t('nx.bak.emptyTitle')}
          description={t('nx.bak.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.bak.title')}
          columns={columns}
          rows={rows}
          rowKey={(b) => b.id}
        />
      ) : null}
    </>
  );
}

export default function BackupsPage() {
  return (
    <RequirePermission anyOf={['backup.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <BackupsScreen />
      </Suspense>
    </RequirePermission>
  );
}
