'use client';

// Backup & Recovery.
//
// # The one sentence this screen exists to say
//
// A backup that ran is not a backup that restores. The register has kept those
// two claims in separate columns since 0093 and this screen keeps them in
// separate words: a run that finished says "Not checked", and only a snapshot
// that has been pulled down, restored into a temporary database and compared
// table by table against its own manifest says "Proved to restore". The green
// word is never given for the cheaper claim.
//
// # Nothing here is a percentage
//
// `pg_dump` does not report progress and neither does an upload of a file to a
// bucket, so there is no honest percentage to show. What the agent writes is
// the name of the thing currently happening — Dumping, Uploading, Verifying,
// Restoring — and this screen prints that. A progress bar computed from a guess
// is a lie with an animation on it.
//
// # The health sentence is the server's
//
// `/platform/backups/health` answers with a state and a sentence in its own
// words. It leads the screen and is printed as written. Recomposing it from the
// parts would give the product two opinions about a question a business needs
// one answer to.
//
// # Why this is a platform screen and not a business one
//
// A dump of this database is every business on the server at once. There is no
// tenant permission that could safely reach it — one an owner could grant
// themselves would be one that lets a shop download another shop's books. The
// routes are all `AccessSuperAdmin` and answer 404 to everybody else; this
// screen lives under `/platform` for the same reason.

import {
  AlertTriangle,
  CheckCircle2,
  Download,
  HardDriveDownload,
  Play,
  RotateCcw,
  ShieldCheck,
  Upload,
} from 'lucide-react';
import { Suspense, useRef, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { Tabs } from '@/components/ui/tabs';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useT, type Key } from '@/lib/i18n/locale';
import { saveAs } from '@/lib/download';
import { fileSize } from '@/lib/oversight/records';
import { useUrlState } from '@/lib/url-state';

import { Recovery } from './pitr';
import {
  ARTIFACT_NAMES,
  PHASE_LABEL,
  PHASE_TONE,
  RECOVERY_DOC,
  STAGE_LABEL,
  type BackupHealth,
  type BackupRecord,
  type BackupTask,
} from '@/lib/platform/backups';

type View = 'overview' | 'recovery' | 'history' | 'upload' | 'operations';

// Named here so an unknown `?view=` in a shared link falls back rather than
// rendering nothing at all.
const VIEWS: readonly View[] = [
  'overview',
  'recovery',
  'history',
  'upload',
  'operations',
];

function BackupsScreen() {
  const t = useT();
  const [view, setView] = useUrlState('view', 'overview');
  const tab = (VIEWS.includes(view as View) ? view : 'overview') as View;
  const [selected, setSelected] = useUrlState('snapshot', '');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Polled while something is running, still while nothing is. A screen that
  // asked every five seconds for ever would be load on a server sized for two
  // cores, to answer a question whose answer changes twice a day.
  const health = useApi<BackupHealth>('/platform/backups/health', undefined, {
    refetchInterval: (query) =>
      query.state.data?.active_task ? 4_000 : 60_000,
    staleTime: 0,
  });
  const running = health.data?.active_task;

  const backups = useApiList<BackupRecord>('/platform/backups', undefined, {
    refetchInterval: running ? 6_000 : false,
  });
  const tasks = useApiList<BackupTask>('/platform/backups/tasks', undefined, {
    refetchInterval: running ? 6_000 : false,
  });

  const rows = backups.data?.data ?? [];
  const chosen = rows.find((r) => r.snapshot_id === selected);

  async function ask(path: string, body?: unknown) {
    setBusy(true);
    setError(null);
    try {
      await api.post(path, body ?? {});
      await Promise.all([
        health.refetch(),
        backups.refetch(),
        tasks.refetch(),
      ]);
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.pbk.title')}
        description={t('nx.pbk.subtitle')}
        actions={
          <Button
            variant="primary"
            onClick={() => ask('/platform/backups')}
            disabled={busy || Boolean(running)}
          >
            <Play aria-hidden />
            {t('nx.pbk.create')}
          </Button>
        }
      />

      {error && <FormError message={error} className="mb-4" />}

      {/*
        Announced, because this is the one thing on the page that changes on
        its own. A backup takes minutes and moves through named stages; a
        sighted operator watches the sentence change and a screen reader user,
        without this, is told nothing at all between pressing the button and
        the row appearing in the history. `polite` rather than `assertive`:
        the stages are progress, not an emergency, and they should wait for a
        gap in what is being read rather than interrupt it.

        The error above announces itself already — `FormError` is `role="alert"`.
      */}
      {running && (
        <div role="status" aria-live="polite">
          <Panel className="mb-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="min-w-0">
                <p className="text-label text-muted">
                  {t(`nx.pbk.kind.${running.kind}` as Key)}
                </p>
                <p className="mt-0.5 text-lede font-medium text-fg">
                  {t(STAGE_LABEL[running.stage] ?? 'nx.pbk.stage.working')}
                </p>
              </div>
              <Badge tone="info">{t('nx.pbk.running')}</Badge>
            </div>
            <p className="mt-2 max-w-[68ch] text-caption text-subtle">
              {t('nx.pbk.noProgressBar')}
            </p>
          </Panel>
        </div>
      )}

      <Tabs
        label={t('nx.pbk.title')}
        value={tab}
        onChange={(v) => setView(v)}
        items={[
          { id: 'overview', label: t('nx.pbk.tab.overview') },
          { id: 'recovery', label: t('nx.pbk.tab.recovery') },
          {
            id: 'history',
            label: t('nx.pbk.tab.history'),
            badge: rows.length ? String(rows.length) : undefined,
          },
          { id: 'upload', label: t('nx.pbk.tab.upload') },
          { id: 'operations', label: t('nx.pbk.tab.operations') },
        ]}
        className="mb-4"
      />

      {tab === 'overview' && (
        <Overview health={health.data} loading={health.isLoading} onAsk={ask} busy={busy} />
      )}

      {tab === 'recovery' && (
        <Recovery running={Boolean(running)} onAsk={ask} busy={busy} />
      )}

      {tab === 'history' && (
        <History
          rows={rows}
          loading={backups.isLoading}
          error={backups.error}
          onRetry={() => backups.refetch()}
          selected={selected}
          onSelect={setSelected}
          chosen={chosen}
          onAsk={ask}
          busy={busy || Boolean(running)}
          onError={setError}
        />
      )}

      {tab === 'upload' && (
        <UploadPanel
          onDone={() => {
            void backups.refetch();
            void health.refetch();
            setView('history');
          }}
        />
      )}

      {tab === 'operations' && (
        <Operations rows={tasks.data?.data ?? []} loading={tasks.isLoading} />
      )}
    </>
  );
}

// --- overview ---------------------------------------------------------------

const HEALTH_TONE: Record<string, Tone> = {
  green: 'positive',
  amber: 'caution',
  red: 'critical',
};

const HEALTH_LABEL: Record<string, Key> = {
  green: 'nx.pbk.health.green',
  amber: 'nx.pbk.health.amber',
  red: 'nx.pbk.health.red',
};

function Overview({
  health,
  loading,
  onAsk,
  busy,
}: {
  health?: BackupHealth;
  loading: boolean;
  onAsk: (path: string, body?: unknown) => Promise<void>;
  busy: boolean;
}) {
  const t = useT();
  if (loading || !health) return <TableSkeleton rows={4} columns={3} />;

  const h = health.health;
  const store = health.storage;

  return (
    <div className="grid gap-4">
      <Panel
        title={t('nx.pbk.standing')}
        actions={
          <Badge tone={HEALTH_TONE[h.state] ?? 'neutral'}>
            {t(HEALTH_LABEL[h.state] ?? 'nx.pbk.health.red')}
          </Badge>
        }
      >
        {/* Printed as the server wrote it. See the file note. */}
        <p className="max-w-[68ch] text-body text-fg">{h.summary}</p>

        <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Figure
            label={t('nx.pbk.lastProved')}
            value={h.last_verified_at ? whenShort(h.last_verified_at) : t('nx.pbk.never')}
            caption={h.last_verified_snapshot}
          />
          <Figure
            label={t('nx.pbk.lastRun')}
            value={h.last_run_at ? whenShort(h.last_run_at) : t('nx.pbk.never')}
            caption={h.last_run_status}
          />
          <Figure
            label={t('nx.pbk.provedCount')}
            value={String(h.verified_count)}
            caption={t('nx.pbk.unprovedCount', { n: h.unverified_count })}
          />
          <Figure
            label={t('nx.pbk.failuresWeek')}
            value={String(h.failed_last_week)}
            tone={h.failed_last_week > 0 ? 'critical' : undefined}
          />
        </div>

        {h.last_failure_reason && (
          <p className="mt-4 rounded-sm border border-critical/25 bg-critical-subtle p-3 text-label text-critical-fg">
            {t('nx.pbk.lastFailure')}: {h.last_failure_reason}
          </p>
        )}
      </Panel>

      <div className="grid gap-4 lg:grid-cols-2">
        <Panel title={t('nx.pbk.storage')}>
          {store.configured ? (
            <dl className="grid gap-2 text-body">
              <Row label={t('nx.pbk.provider')} value={store.endpoint_host ?? ''} />
              <Row label={t('nx.pbk.bucket')} value={store.bucket ?? ''} />
              <Row label={t('nx.pbk.prefix')} value={store.prefix ?? ''} />
              {store.region && <Row label={t('nx.pbk.region')} value={store.region} />}
            </dl>
          ) : (
            <p className="text-body text-critical-fg">{t('nx.pbk.noStore')}</p>
          )}
          <p className="mt-3 max-w-[68ch] text-caption text-subtle">
            {t('nx.pbk.storageNote')}
          </p>
        </Panel>

        <Panel
          title={t('nx.pbk.retention')}
          actions={
            <Button
              variant="secondary"
              onClick={() => onAsk('/platform/backups/prune')}
              disabled={busy}
            >
              {t('nx.pbk.prune')}
            </Button>
          }
        >
          <dl className="grid gap-2 text-body">
            <Row label={t('nx.pbk.daily')} value={String(health.retention.daily)} />
            <Row label={t('nx.pbk.weekly')} value={String(health.retention.weekly)} />
            <Row label={t('nx.pbk.monthly')} value={String(health.retention.monthly)} />
          </dl>
          <p className="mt-3 max-w-[68ch] text-caption text-subtle">
            {t('nx.pbk.retentionNote')}
          </p>
        </Panel>
      </div>

      <Maintenance state={health.maintenance} />

      <Panel title={t('nx.pbk.recovery')}>
        <p className="max-w-[68ch] text-body text-muted">
          {t('nx.pbk.recoveryNote')}
        </p>
        {!health.production_restore_enabled && (
          <p className="mt-3 max-w-[68ch] rounded-sm border border-line bg-surface-sunken p-3 text-label text-muted">
            {t('nx.pbk.restoreDisabled')}
          </p>
        )}
      </Panel>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <dt className="text-label text-muted">{label}</dt>
      <dd className="min-w-0 truncate text-body text-fg">{value || '—'}</dd>
    </div>
  );
}

// --- maintenance ------------------------------------------------------------

function Maintenance({ state }: { state?: BackupHealth['maintenance'] }) {
  const t = useT();
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [current, setCurrent] = useState(state);

  const active = current?.active ?? false;

  async function set(next: boolean) {
    setBusy(true);
    setError(null);
    try {
      const updated = await api.put<BackupHealth['maintenance']>(
        '/platform/maintenance',
        { active: next, reason: reason.trim(), allow_reads: true },
      );
      setCurrent(updated);
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel
      title={t('nx.pbk.freeze')}
      actions={
        <Badge tone={active ? 'caution' : 'neutral'}>
          {t(active ? 'nx.pbk.freezeOn' : 'nx.pbk.freezeOff')}
        </Badge>
      }
    >
      <p className="max-w-[68ch] text-body text-muted">{t('nx.pbk.freezeNote')}</p>
      {error && <FormError message={error} className="mt-3" />}

      {active ? (
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <p className="min-w-0 flex-1 text-body text-fg">{current?.reason}</p>
          <Button variant="secondary" onClick={() => set(false)} disabled={busy}>
            {t('nx.pbk.freezeEnd')}
          </Button>
        </div>
      ) : (
        <div className="mt-4 flex flex-wrap items-end gap-3">
          <Field label={t('nx.pbk.freezeReason')} className="min-w-64 flex-1">
            <Input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t('nx.pbk.freezeReasonHint')}
            />
          </Field>
          <Button variant="secondary" onClick={() => set(true)} disabled={busy}>
            {t('nx.pbk.freezeBegin')}
          </Button>
        </div>
      )}
    </Panel>
  );
}

// --- history ----------------------------------------------------------------

function History({
  rows,
  loading,
  error,
  onRetry,
  selected,
  onSelect,
  chosen,
  onAsk,
  busy,
  onError,
}: {
  rows: readonly BackupRecord[];
  loading: boolean;
  error: Error | null;
  onRetry: () => void;
  selected: string;
  onSelect: (id: string) => void;
  chosen?: BackupRecord;
  onAsk: (path: string, body?: unknown) => Promise<void>;
  busy: boolean;
  onError: (message: string | null) => void;
}) {
  const t = useT();

  const columns: Column<BackupRecord>[] = [
    {
      key: 'taken',
      header: t('nx.pbk.col.taken'),
      primary: true,
      cell: (r) => (
        <div className="min-w-0">
          <p className="truncate font-medium text-fg">{whenShort(r.started_at)}</p>
          <p className="truncate text-caption text-subtle">{r.snapshot_id || '—'}</p>
        </div>
      ),
    },
    {
      key: 'phase',
      header: t('nx.pbk.col.state'),
      cell: (r) => (
        <Badge tone={PHASE_TONE[r.phase] ?? 'neutral'}>
          {t(PHASE_LABEL[r.phase] ?? 'nx.pbk.phase.unknown')}
        </Badge>
      ),
    },
    {
      key: 'source',
      header: t('nx.pbk.col.source'),
      secondary: true,
      cell: (r) => t(r.source === 'upload' ? 'nx.pbk.fromUpload' : 'nx.pbk.fromServer'),
    },
    {
      key: 'size',
      header: t('nx.pbk.col.size'),
      numeric: true,
      cell: (r) => (r.size_bytes ? fileSize(r.size_bytes) : '—'),
    },
    {
      key: 'schema',
      header: t('nx.pbk.col.schema'),
      numeric: true,
      secondary: true,
      cell: (r) => (r.schema_version ? String(r.schema_version) : '—'),
    },
    {
      key: 'sealed',
      header: t('nx.pbk.col.sealed'),
      secondary: true,
      cell: (r) => t(r.encrypted ? 'nx.pbk.sealed' : 'nx.pbk.notSealed'),
    },
  ];

  if (loading) return <TableSkeleton rows={6} columns={6} />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (rows.length === 0) {
    return (
      <EmptyState
        icon={HardDriveDownload}
        title={t('nx.pbk.emptyTitle')}
        description={t('nx.pbk.emptyDesc')}
      />
    );
  }

  return (
    <div className="grid gap-4">
      <DataTable
        caption={t('nx.pbk.tableCaption')}
        columns={columns}
        rows={rows}
        rowKey={(r) => r.id}
        // A row with no snapshot id is a run that failed before it produced
        // one. It is listed — hiding a failure is how a broken arrangement
        // stays broken — and it has nothing to open.
        onOpenRow={(r) =>
          onSelect(!r.snapshot_id || r.snapshot_id === selected ? '' : r.snapshot_id)
        }
        isSelected={(r) => Boolean(r.snapshot_id) && r.snapshot_id === selected}
      />
      {chosen && (
        <Detail record={chosen} onAsk={onAsk} busy={busy} onError={onError} />
      )}
    </div>
  );
}

// --- one backup -------------------------------------------------------------

function Detail({
  record,
  onAsk,
  busy,
  onError,
}: {
  record: BackupRecord;
  onAsk: (path: string, body?: unknown) => Promise<void>;
  busy: boolean;
  onError: (message: string | null) => void;
}) {
  const t = useT();
  const id = record.snapshot_id;
  const detail = useApi<BackupRecord>(id ? `/platform/backups/${id}` : null);
  const [confirm, setConfirm] = useState('');
  const [downloading, setDownloading] = useState(false);

  const verified = Boolean(record.verified_at);
  const ready = record.phase === 'restore_ready';
  const report = detail.data?.verify_report;

  async function download(part: 'dump' | 'manifest' | 'checksum') {
    setDownloading(true);
    onError(null);
    try {
      const { blob, filename } = await api.download(
        `/platform/backups/${id}/download/${part}`,
      );
      saveAs(blob, filename);
    } catch (e) {
      onError(messageFor(e, t));
    } finally {
      setDownloading(false);
    }
  }

  return (
    <Panel
      title={id}
      description={t('nx.pbk.detailNote')}
      actions={
        <Badge tone={PHASE_TONE[record.phase] ?? 'neutral'}>
          {t(PHASE_LABEL[record.phase] ?? 'nx.pbk.phase.unknown')}
        </Badge>
      }
    >
      <dl className="grid gap-2 sm:grid-cols-2">
        <Row label={t('nx.pbk.col.taken')} value={whenShort(record.started_at)} />
        <Row
          label={t('nx.pbk.provedOn')}
          value={record.verified_at ? whenShort(record.verified_at) : t('nx.pbk.never')}
        />
        <Row
          label={t('nx.pbk.col.size')}
          value={record.size_bytes ? fileSize(record.size_bytes) : '—'}
        />
        <Row label={t('nx.pbk.col.schema')} value={String(record.schema_version ?? '')} />
        <Row label={t('nx.pbk.checksum')} value={record.checksum ?? ''} />
        <Row label={t('nx.pbk.where')} value={record.location ?? ''} />
        <Row label={t('nx.pbk.builtBy')} value={record.app_version ?? ''} />
        <Row label={t('nx.pbk.retentionClass')} value={record.retention_class ?? ''} />
        <Row label={t('nx.pbk.requestedBy')} value={record.requested_by ?? ''} />
      </dl>

      {record.verify_error && (
        <p className="mt-4 rounded-sm border border-critical/25 bg-critical-subtle p-3 text-label text-critical-fg">
          {record.verify_error}
        </p>
      )}

      {report && <VerificationReport report={report} />}

      <div className="mt-5 flex flex-wrap gap-2">
        <Button
          variant="secondary"
          onClick={() => onAsk(`/platform/backups/${id}/verify`)}
          disabled={busy}
        >
          <ShieldCheck aria-hidden />
          {t('nx.pbk.verify')}
        </Button>
        <Button
          variant="secondary"
          onClick={() => onAsk(`/platform/backups/${id}/validate-restore`)}
          disabled={busy}
        >
          <RotateCcw aria-hidden />
          {t('nx.pbk.validateRestore')}
        </Button>
        {record.source !== 'upload' && (
          <>
            <Button
              variant="secondary"
              onClick={() => download('dump')}
              disabled={downloading || !verified}
            >
              <Download aria-hidden />
              {t('nx.pbk.downloadDump')}
            </Button>
            <Button
              variant="ghost"
              onClick={() => download('manifest')}
              disabled={downloading}
            >
              {t('nx.pbk.downloadManifest')}
            </Button>
            <Button
              variant="ghost"
              onClick={() => download('checksum')}
              disabled={downloading}
            >
              {t('nx.pbk.downloadChecksum')}
            </Button>
          </>
        )}
      </div>

      {!verified && record.source !== 'upload' && (
        <p className="mt-3 max-w-[68ch] text-caption text-subtle">
          {t('nx.pbk.downloadNeedsProof')}
        </p>
      )}

      {/* The dangerous one. Present only once a rehearsal has passed, so the
          button that replaces a live database cannot be the first thing
          anybody presses. */}
      <div className="mt-6 border-t border-line pt-5">
        <h3 className="flex items-center gap-2 text-card-title font-semibold text-fg">
          <AlertTriangle className="size-4 text-critical-fg" aria-hidden />
          {t('nx.pbk.toProduction')}
        </h3>
        <p className="mt-1 max-w-[68ch] text-body text-muted">
          {t('nx.pbk.toProductionNote')}
        </p>
        {ready ? (
          <div className="mt-4 flex flex-wrap items-end gap-3">
            <Field
              label={t('nx.pbk.typeId')}
              hint={t('nx.pbk.typeIdHint')}
              className="min-w-72 flex-1"
            >
              <Input
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                placeholder={id}
                autoComplete="off"
              />
            </Field>
            <Button
              variant="destructive"
              disabled={busy || confirm !== id}
              onClick={() =>
                onAsk(`/platform/backups/${id}/restore-production`, {
                  confirm,
                })
              }
            >
              {t('nx.pbk.replaceLive')}
            </Button>
          </div>
        ) : (
          <p className="mt-3 max-w-[68ch] rounded-sm border border-line bg-surface-sunken p-3 text-label text-muted">
            {t('nx.pbk.needsRehearsal')}
          </p>
        )}
      </div>
    </Panel>
  );
}

function VerificationReport({ report }: { report: NonNullable<BackupRecord['verify_report']> }) {
  const t = useT();
  return (
    <div className="mt-4 rounded-sm border border-line bg-surface-sunken p-3">
      <p className="flex items-center gap-2 text-label font-medium text-fg">
        {report.passed ? (
          <CheckCircle2 className="size-4 text-positive-fg" aria-hidden />
        ) : (
          <AlertTriangle className="size-4 text-critical-fg" aria-hidden />
        )}
        {t(report.passed ? 'nx.pbk.reportPassed' : 'nx.pbk.reportFailed')}
      </p>
      <dl className="mt-3 grid gap-2 sm:grid-cols-3">
        <Row label={t('nx.pbk.tables')} value={String(report.tables_restored ?? 0)} />
        <Row label={t('nx.pbk.rows')} value={String(report.rows_restored ?? 0)} />
        <Row
          label={t('nx.pbk.businesses')}
          value={String(report.businesses_restored ?? 0)}
        />
      </dl>
      {report.checked?.length ? (
        <ul className="mt-3 grid gap-1">
          {report.checked.map((c) => (
            <li key={c} className="flex items-start gap-2 text-caption text-muted">
              <CheckCircle2
                className="mt-0.5 size-3.5 shrink-0 text-positive-fg"
                aria-hidden
              />
              {c}
            </li>
          ))}
        </ul>
      ) : null}
      {report.findings?.length ? (
        <ul className="mt-3 grid gap-1">
          {report.findings.map((f) => (
            <li key={f} className="text-caption text-critical-fg">
              {f}
            </li>
          ))}
        </ul>
      ) : null}
      {report.complete_inventory === false && (
        <p className="mt-3 text-caption text-caution-fg">
          {t('nx.pbk.partialManifest')}
        </p>
      )}
    </div>
  );
}

// --- upload -----------------------------------------------------------------

function UploadPanel({ onDone }: { onDone: () => void }) {
  const t = useT();
  const dump = useRef<HTMLInputElement>(null);
  const manifest = useRef<HTMLInputElement>(null);
  const checksum = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  async function send() {
    const dumpFile = dump.current?.files?.[0];
    const manifestFile = manifest.current?.files?.[0];
    if (!dumpFile || !manifestFile) {
      setError(t('nx.pbk.uploadNeedsBoth'));
      return;
    }
    setBusy(true);
    setError(null);
    setDone(null);

    const form = new FormData();
    form.append('dump', dumpFile);
    form.append('manifest', manifestFile);
    const checksumFile = checksum.current?.files?.[0];
    if (checksumFile) form.append('checksum', checksumFile);

    try {
      const out = await api.upload<{ snapshot_id: string }>(
        '/platform/backups/upload',
        form,
      );
      setDone(out.snapshot_id);
      onDone();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel title={t('nx.pbk.uploadTitle')} description={t('nx.pbk.uploadNote')}>
      {error && <FormError message={error} className="mb-4" />}
      {done && (
        <p className="mb-4 rounded-sm border border-caution/25 bg-caution-subtle p-3 text-label text-caution-fg">
          {t('nx.pbk.uploadDone', { id: done })}
        </p>
      )}

      {/* The filename patterns sit beside the fields rather than inside the
          hints. A filename is the same in every language because it is the
          same on disk, and one translated into Arabic would send somebody
          looking for a file that does not exist. */}
      <div className="grid gap-4">
        <Field label={t('nx.pbk.uploadDump')} hint={t('nx.pbk.uploadDumpHint')}>
          <input ref={dump} type="file" className="text-body" />
          <FileName name={ARTIFACT_NAMES.dump} />
        </Field>
        <Field label={t('nx.pbk.uploadManifest')} hint={t('nx.pbk.uploadManifestHint')}>
          <input ref={manifest} type="file" accept=".json" className="text-body" />
          <FileName name={ARTIFACT_NAMES.manifest} />
        </Field>
        <Field label={t('nx.pbk.uploadChecksum')} hint={t('nx.pbk.uploadChecksumHint')}>
          <input ref={checksum} type="file" className="text-body" />
          <FileName name={ARTIFACT_NAMES.checksum} />
        </Field>
      </div>

      <Button
        className="mt-5"
        onClick={send}
        disabled={busy}
      >
        <Upload aria-hidden />
        {t('nx.pbk.uploadSend')}
      </Button>

      <p className="mt-4 max-w-[68ch] text-caption text-subtle">
        {t('nx.pbk.uploadLimit')} <FileName name={RECOVERY_DOC} />
      </p>
    </Panel>
  );
}

// --- operations -------------------------------------------------------------

function Operations({
  rows,
  loading,
}: {
  rows: readonly BackupTask[];
  loading: boolean;
}) {
  const t = useT();

  const columns: Column<BackupTask>[] = [
    {
      key: 'kind',
      header: t('nx.pbk.col.operation'),
      primary: true,
      cell: (r) => (
        <div className="min-w-0">
          <p className="truncate font-medium text-fg">
            {t(`nx.pbk.kind.${r.kind}` as Key)}
          </p>
          <p className="truncate text-caption text-subtle">{r.snapshot_id || '—'}</p>
        </div>
      ),
    },
    {
      key: 'state',
      header: t('nx.pbk.col.state'),
      cell: (r) => (
        <Badge
          tone={
            r.state === 'done'
              ? 'positive'
              : r.state === 'failed'
                ? 'critical'
                : 'info'
          }
        >
          {t(`nx.pbk.taskState.${r.state}` as Key)}
        </Badge>
      ),
    },
    {
      key: 'stage',
      header: t('nx.pbk.col.stage'),
      secondary: true,
      cell: (r) => t(STAGE_LABEL[r.stage] ?? 'nx.pbk.stage.working'),
    },
    {
      key: 'who',
      header: t('nx.pbk.col.who'),
      secondary: true,
      cell: (r) => r.requested_by || t('nx.pbk.scheduled'),
    },
    {
      key: 'when',
      header: t('nx.pbk.col.when'),
      cell: (r) => whenShort(r.requested_at),
    },
  ];

  if (loading) return <TableSkeleton rows={5} columns={5} />;
  if (rows.length === 0) {
    return (
      <EmptyState
        icon={HardDriveDownload}
        title={t('nx.pbk.noOperations')}
        description={t('nx.pbk.noOperationsDesc')}
      />
    );
  }

  return (
    <div className="grid gap-4">
      <DataTable
        caption={t('nx.pbk.operationsCaption')}
        columns={columns}
        rows={rows}
        rowKey={(r) => r.id}
      />
      {rows
        .filter((r) => r.state === 'failed' && r.error)
        .slice(0, 3)
        .map((r) => (
          <Panel key={r.id} title={t(`nx.pbk.kind.${r.kind}` as Key)}>
            <p className="text-body text-critical-fg">{r.error}</p>
          </Panel>
        ))}
    </div>
  );
}

/**
 * A filename or a path, shown as what it is.
 *
 * Left-to-right whatever the page direction, because a path read right-to-left
 * is a path somebody types wrongly. `dir="ltr"` on the element is what does
 * that; the surrounding sentence stays in the reader's own direction.
 */
function FileName({ name }: { name: string }) {
  return (
    <code dir="ltr" className="text-caption text-muted">
      {name}
    </code>
  );
}

// --- shared -----------------------------------------------------------------

/**
 * A timestamp a person can read.
 *
 * The browser's own locale, deliberately: this is a platform operator's screen
 * and the only question it answers is "how long ago", which is answered best in
 * whatever format that person's machine already uses.
 */
function whenShort(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export default function BackupsPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<TableSkeleton rows={6} columns={5} />}>
        <BackupsScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
