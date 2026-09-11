'use client';

// Point-in-Time Recovery.
//
// # The one question this screen answers
//
// "Can I get back to 14:32 last Tuesday, and what happens if I try?" Everything
// here is arranged around that: the window is shown before anything else, the
// moment is checked against it BEFORE the button is offered, and the answer to
// the check is the server's sentence rather than a colour this screen chose.
//
// # Why the check is a separate step from the recovery
//
// Because a recovery takes minutes to hours and the commonest way to waste them
// is to aim at a moment the archive cannot reach. `GET /platform/pitr/window`
// with an `at` answers that in one round trip, in words — "that is before the
// recovery window, which starts at ... because the oldest base backup still
// kept finished then" — and the Recover button stays disabled until it has said
// yes. The agent checks again when it starts, because the window moves.
//
// # Why the confirmation is a typed word
//
// A recovery produces a readable copy of every business on this server as they
// were at the chosen moment, on the staging volume. That is not a production
// incident and it is not nothing. Typing RECOVER is what makes it a decision
// rather than a mis-click, and it is the same shape of gate the production
// restore uses — the difference being that the production one asks for the
// snapshot id, because it is the dangerous one.
//
// # What this screen cannot do
//
// Replace production. There is no button here for that and no route behind it:
// a point-in-time recovery lands in a PostgreSQL of its own on a socket nothing
// else can reach. Replacing the live database is still the restore-production
// flow on the History tab, which takes a dump, a rehearsal that passed, a fresh
// verified backup and a write freeze.

import {
  AlertTriangle,
  Database,
  Download,
  Play,
  Search,
  ShieldCheck,
} from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, Panel } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { saveAs } from '@/lib/download';
import { useT, type Key } from '@/lib/i18n/locale';
import { fileSize } from '@/lib/oversight/records';
import {
  ARCHIVE_LABEL,
  ARCHIVE_TONE,
  BASE_LABEL,
  BASE_TONE,
  RECOVERY_STATUS_LABEL,
  RECOVERY_STATUS_TONE,
  TARGET_HELP,
  TARGET_KINDS,
  TARGET_LABEL,
  CONFIRM_WORD,
  momentToISO,
  needsMoment,
  needsValue,
  nowForInput,
  type ArchiveFailure,
  type BaseBackup,
  type PITRStatus,
  type RecoveryRecord,
  type TargetKind,
  type WindowAnswer,
} from '@/lib/platform/pitr';

export function Recovery({
  running,
  onAsk,
  busy,
}: {
  running: boolean;
  onAsk: (path: string, body?: unknown) => Promise<void>;
  busy: boolean;
}) {
  const t = useT();

  // Polled while something is running, slowly otherwise. The archive readout
  // is written by the agent once a minute, so asking more often than that
  // would be load for an answer that has not changed.
  const status = useApi<PITRStatus>('/platform/pitr', undefined, {
    refetchInterval: running ? 5_000 : 60_000,
    staleTime: 0,
  });
  const recoveries = useApiList<RecoveryRecord>(
    '/platform/pitr/recoveries',
    undefined,
    { refetchInterval: running ? 6_000 : false },
  );

  if (status.isLoading || !status.data) {
    return <TableSkeleton rows={4} columns={3} />;
  }

  const s = status.data;
  const a = s.archive;

  return (
    <div className="grid gap-4">
      <Standing status={s} />
      <Plan
        status={s}
        busy={busy}
        running={running}
        onAsk={onAsk}
        onDone={() => {
          void status.refetch();
          void recoveries.refetch();
        }}
      />
      <BaseBackups
        rows={s.base_backups}
        busy={busy}
        running={running}
        onAsk={onAsk}
      />
      <Recoveries rows={recoveries.data?.data ?? []} />
      <Maintenance busy={busy} running={running} onAsk={onAsk} />

      {/*
        Last, because it is the least urgent thing on the page and the most
        likely to be the answer when something above is amber.
      */}
      <Panel title={t('nx.pitr.archiveTitle')}>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Figure
            label={t('nx.pitr.fig.segments')}
            value={String(a.archive_segments)}
            caption={fileSize(a.archive_bytes)}
          />
          <Figure
            label={t('nx.pitr.fig.localWal')}
            value={fileSize(a.pg_wal_bytes)}
            caption={t('nx.pitr.fig.localWalCaption')}
            tone={a.pg_wal_bytes > 2 * 1024 ** 3 ? 'critical' : undefined}
          />
          <Figure
            label={t('nx.pitr.fig.attempts')}
            value={String(a.archived_total)}
            caption={t('nx.pitr.fig.failedTotal', { n: a.failed_total })}
            tone={a.failed_total > 0 ? 'critical' : undefined}
          />
          <Figure
            label={t('nx.pitr.fig.gaps')}
            value={String(a.archive_gaps)}
            tone={a.archive_gaps > 0 ? 'critical' : undefined}
          />
        </div>
        {a.store_error && (
          <p className="mt-4 rounded-sm border border-critical/25 bg-critical-subtle p-3 text-label text-critical-fg">
            {t('nx.pitr.storeError')}: {a.store_error}
          </p>
        )}
      </Panel>
    </div>
  );
}

// --- where this stands ------------------------------------------------------

function Standing({ status }: { status: PITRStatus }) {
  const t = useT();
  const a = status.archive;
  const health = a.health ?? 'red';

  return (
    <Panel
      title={t('nx.pitr.standing')}
      actions={
        <Badge tone={ARCHIVE_TONE[health] ?? 'neutral'}>
          {t(ARCHIVE_LABEL[health] ?? 'nx.pitr.health.red')}
        </Badge>
      }
    >
      {/* Printed as the server wrote it, like the backup health sentence. */}
      <p className="max-w-[68ch] text-body text-fg">
        {a.summary || t('nx.pitr.neverObserved')}
      </p>

      {a.stale && (
        <p className="mt-3 rounded-sm border border-warning/25 bg-warning-subtle p-3 text-label text-warning-fg">
          {t('nx.pitr.staleReading')}
        </p>
      )}

      <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Figure
          label={t('nx.pitr.fig.windowStart')}
          value={a.recovery_window_start ? when(a.recovery_window_start) : '—'}
          caption={t('nx.pitr.fig.windowStartCaption', {
            n: status.retention.window_days,
          })}
        />
        <Figure
          label={t('nx.pitr.fig.windowEnd')}
          value={a.recovery_window_end ? when(a.recovery_window_end) : '—'}
          caption={a.last_archived_segment}
        />
        <Figure
          label={t('nx.pitr.fig.lag')}
          value={t('nx.pitr.fig.lagValue', { n: a.lag_segments })}
          caption={t('nx.pitr.fig.lagCaption', { n: a.lag_seconds })}
          tone={a.lag_segments > 4 ? 'critical' : undefined}
        />
        <Figure
          label={t('nx.pitr.fig.available')}
          value={status.available ? t('nx.pitr.yes') : t('nx.pitr.no')}
          tone={status.available ? 'positive' : 'critical'}
        />
      </div>
    </Panel>
  );
}

// --- choosing a moment ------------------------------------------------------

function Plan({
  status,
  busy,
  running,
  onAsk,
  onDone,
}: {
  status: PITRStatus;
  busy: boolean;
  running: boolean;
  onAsk: (path: string, body?: unknown) => Promise<void>;
  onDone: () => void;
}) {
  const t = useT();
  const [kind, setKind] = useState<TargetKind>('before_time');
  const [moment, setMoment] = useState(() => nowForInput());
  const [value, setValue] = useState('');
  const [confirm, setConfirm] = useState('');
  const [checking, setChecking] = useState(false);
  const [answer, setAnswer] = useState<WindowAnswer | null>(null);
  const [error, setError] = useState<string | null>(null);

  // The check is only meaningful for a target that is a moment. `latest` and
  // `immediate` are defined by the archive rather than by a clock, so there is
  // nothing to check them against and the button is offered directly.
  const wantsMoment = needsMoment(kind);
  const wantsValue = needsValue(kind);
  const checked = !wantsMoment || answer?.recoverable === true;
  const ready =
    status.available &&
    checked &&
    confirm.trim() === CONFIRM_WORD &&
    (!wantsValue || value.trim() !== '') &&
    !busy &&
    !running;

  // Any change to what is being asked for invalidates the answer. Leaving a
  // green "recoverable" up while the moment underneath it changed would be the
  // screen agreeing to something nobody checked.
  function change(next: () => void) {
    setAnswer(null);
    setError(null);
    next();
  }

  async function check() {
    const iso = momentToISO(moment);
    if (!iso) {
      setError(t('nx.pitr.badMoment'));
      return;
    }
    setChecking(true);
    setError(null);
    try {
      const got = await api.get<WindowAnswer>(
        `/platform/pitr/window?at=${encodeURIComponent(iso)}`,
      );
      setAnswer(got);
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setChecking(false);
    }
  }

  async function recover() {
    const body: Record<string, unknown> = {
      target: kind,
      confirm: CONFIRM_WORD,
    };
    if (wantsMoment) {
      const iso = momentToISO(moment);
      if (!iso) {
        setError(t('nx.pitr.badMoment'));
        return;
      }
      body.at = iso;
    }
    if (wantsValue) body.value = value.trim();

    await onAsk('/platform/pitr/restore', body);
    setConfirm('');
    onDone();
  }

  return (
    <Panel title={t('nx.pitr.planTitle')} description={t('nx.pitr.planIntro')}>
      {error && <FormError message={error} className="mb-4" />}

      {!status.available && (
        <p className="mb-4 rounded-sm border border-critical/25 bg-critical-subtle p-3 text-label text-critical-fg">
          {t('nx.pitr.notAvailable')}
        </p>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          name="target"
          label={t('nx.pitr.field.target')}
          hint={t(TARGET_HELP[kind])}
        >
          <Select
            value={kind}
            onChange={(e) =>
              change(() => setKind(e.target.value as TargetKind))
            }
          >
            {TARGET_KINDS.map((k) => (
              <option key={k} value={k}>
                {t(TARGET_LABEL[k])}
              </option>
            ))}
          </Select>
        </Field>

        {wantsMoment && (
          <Field
            name="at"
            label={t('nx.pitr.field.moment')}
            hint={t('nx.pitr.field.momentHint')}
          >
            <Input
              type="datetime-local"
              value={moment}
              onChange={(e) => change(() => setMoment(e.target.value))}
            />
          </Field>
        )}

        {wantsValue && (
          <Field
            name="value"
            label={t(
              kind === 'lsn' ? 'nx.pitr.field.lsn' : 'nx.pitr.field.name',
            )}
            hint={t(
              kind === 'lsn'
                ? 'nx.pitr.field.lsnHint'
                : 'nx.pitr.field.nameHint',
            )}
          >
            <Input
              value={value}
              onChange={(e) => change(() => setValue(e.target.value))}
            />
          </Field>
        )}
      </div>

      {wantsMoment && (
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button onClick={check} disabled={checking || busy}>
            <Search aria-hidden />
            {t('nx.pitr.check')}
          </Button>
          {/*
            Announced: the answer arrives after a round trip and is the thing
            the operator is waiting for, and it decides whether the button
            below is offered at all.
          */}
          <div role="status" aria-live="polite" className="min-w-0">
            {answer && answer.recoverable === true && (
              <p className="text-label text-positive-fg">
                {t('nx.pitr.recoverable', {
                  base: answer.base_backup ?? '—',
                })}
              </p>
            )}
            {answer && answer.recoverable === false && (
              <p className="max-w-[68ch] text-label text-critical-fg">
                {answer.because}
              </p>
            )}
          </div>
        </div>
      )}

      {/*
        The warning, always, and never only when something is wrong. What this
        produces is a readable copy of every business on the server at an
        earlier moment, and an operator should read that sentence every time
        rather than the first time.
      */}
      <div className="mt-5 rounded-sm border border-warning/25 bg-warning-subtle p-3">
        <p className="flex items-start gap-2 text-label text-warning-fg">
          <AlertTriangle aria-hidden className="mt-0.5 shrink-0" />
          <span className="max-w-[68ch]">{t('nx.pitr.warning')}</span>
        </p>
      </div>

      <div className="mt-4 flex flex-wrap items-end gap-3">
        <Field
          name="confirm"
          label={t('nx.pitr.field.confirm', { word: CONFIRM_WORD })}
          hint={t('nx.pitr.field.confirmHint')}
          className="min-w-[16rem]"
        >
          <Input
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="off"
          />
        </Field>
        <Button variant="primary" onClick={recover} disabled={!ready}>
          <Play aria-hidden />
          {t('nx.pitr.recover')}
        </Button>
      </div>

      {running && (
        <p className="mt-3 text-caption text-subtle">
          {t('nx.pitr.oneAtATime')}
        </p>
      )}
    </Panel>
  );
}

// --- the physical copies ----------------------------------------------------

function BaseBackups({
  rows,
  busy,
  running,
  onAsk,
}: {
  rows: BaseBackup[];
  busy: boolean;
  running: boolean;
  onAsk: (path: string, body?: unknown) => Promise<void>;
}) {
  const t = useT();
  const [pulling, setPulling] = useState('');
  const [error, setError] = useState<string | null>(null);

  // Pulls the four files of one base backup, one request each.
  //
  // They come down SEALED where encryption is on, because that is what the
  // store holds and the API does not have the key. That is said on the screen
  // rather than discovered when somebody tries to open one — see
  // `nx.pitr.downloadNote`.
  async function download(base: string) {
    setPulling(base);
    setError(null);
    try {
      for (const part of ['manifest', 'pg-manifest', 'base', 'wal'] as const) {
        const { blob, filename } = await api.download(
          `/platform/pitr/base-backups/${base}/download/${part}`,
        );
        saveAs(blob, filename);
      }
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setPulling('');
    }
  }

  const columns: Column<BaseBackup>[] = [
    {
      key: 'taken',
      header: t('nx.pitr.col.taken'),
      primary: true,
      cell: (r) => (
        <div className="min-w-0">
          <p className="truncate font-medium text-fg">
            {when(r.completed_at || r.started_at)}
          </p>
          <p className="truncate text-caption text-subtle">
            {r.base_backup_id}
          </p>
        </div>
      ),
    },
    {
      key: 'status',
      header: t('nx.pitr.col.state'),
      cell: (r) => (
        <Badge tone={BASE_TONE[r.status] ?? 'neutral'}>
          {t(BASE_LABEL[r.status] ?? 'nx.pitr.base.stored')}
        </Badge>
      ),
    },
    {
      key: 'where',
      header: t('nx.pitr.col.where'),
      cell: (r) => (
        <div className="min-w-0">
          <p className="truncate text-label text-fg">
            {t('nx.pitr.timeline', { n: r.timeline ?? 0 })}
          </p>
          <p className="truncate text-caption text-subtle">
            {r.start_wal_segment || '—'}
          </p>
        </div>
      ),
    },
    {
      key: 'size',
      header: t('nx.pitr.col.size'),
      cell: (r) => (
        <div className="min-w-0">
          <p className="text-label text-fg">{fileSize(r.size_bytes ?? 0)}</p>
          <p className="text-caption text-subtle">
            {r.encrypted ? t('nx.pitr.sealed') : t('nx.pitr.notSealed')}
          </p>
        </div>
      ),
    },
    {
      key: 'download',
      header: t('nx.pitr.col.copy'),
      cell: (r) => (
        <Button
          variant="ghost"
          // Only a copy that finished can be carried away. `running` and
          // `uploading` have no manifest yet, and the manifest is what proves
          // every piece reached the store.
          disabled={
            pulling !== '' ||
            (r.status !== 'stored' && r.status !== 'verified')
          }
          onClick={() => download(r.base_backup_id)}
        >
          <Download aria-hidden />
          {pulling === r.base_backup_id
            ? t('nx.pitr.downloading')
            : t('nx.pitr.download')}
        </Button>
      ),
    },
  ];

  return (
    <Panel
      title={t('nx.pitr.basesTitle')}
      description={t('nx.pitr.basesIntro')}
      actions={
        <Button
          onClick={() => onAsk('/platform/pitr/base-backups')}
          disabled={busy || running}
        >
          <Database aria-hidden />
          {t('nx.pitr.takeBase')}
        </Button>
      }
    >
      {error && <FormError message={error} className="mb-4" />}
      {rows.length === 0 ? (
        <EmptyState
          icon={ShieldCheck}
          title={t('nx.pitr.noBases')}
          description={t('nx.pitr.noBasesWhy')}
        />
      ) : (
        <>
          <DataTable
            caption={t('nx.pitr.basesCaption')}
            columns={columns}
            rows={rows}
            rowKey={(r) => r.id}
          />
          <p className="mt-3 max-w-[68ch] text-caption text-subtle">
            {t('nx.pitr.downloadNote')}
          </p>
        </>
      )}
    </Panel>
  );
}

// --- who recovered what, and to when ----------------------------------------

function Recoveries({ rows }: { rows: RecoveryRecord[] }) {
  const t = useT();

  const columns: Column<RecoveryRecord>[] = [
    {
      key: 'when',
      header: t('nx.pitr.col.asked'),
      primary: true,
      cell: (r) => (
        <div className="min-w-0">
          <p className="truncate font-medium text-fg">{when(r.started_at)}</p>
          <p className="truncate text-caption text-subtle">
            {r.requested_by || t('nx.pitr.byTheSchedule')}
          </p>
        </div>
      ),
    },
    {
      key: 'target',
      header: t('nx.pitr.col.target'),
      cell: (r) => (
        <div className="min-w-0">
          <p className="truncate text-label text-fg">
            {t(
              TARGET_LABEL[r.target_kind as TargetKind] ??
                ('nx.pitr.target.latest' as Key),
            )}
          </p>
          <p className="truncate text-caption text-subtle">
            {r.target_value || '—'}
          </p>
        </div>
      ),
    },
    {
      key: 'reached',
      header: t('nx.pitr.col.reached'),
      cell: (r) => (
        <div className="min-w-0">
          <p className="truncate text-label text-fg">
            {r.reached_at ? when(r.reached_at) : '—'}
          </p>
          <p className="truncate text-caption text-subtle">
            {r.reached_lsn || ''}
          </p>
        </div>
      ),
    },
    {
      key: 'status',
      header: t('nx.pitr.col.state'),
      cell: (r) => (
        <Badge tone={RECOVERY_STATUS_TONE[r.status] ?? 'neutral'}>
          {t(RECOVERY_STATUS_LABEL[r.status] ?? 'nx.pitr.recovery.running')}
        </Badge>
      ),
    },
  ];

  return (
    <Panel
      title={t('nx.pitr.historyTitle')}
      description={t('nx.pitr.historyIntro')}
    >
      {rows.length === 0 ? (
        <EmptyState
          icon={ShieldCheck}
          title={t('nx.pitr.noRecoveries')}
          description={t('nx.pitr.noRecoveriesWhy')}
        />
      ) : (
        <DataTable
          caption={t('nx.pitr.historyCaption')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
        />
      )}
    </Panel>
  );
}

// --- keeping the archive honest ---------------------------------------------

// Maintenance is the two jobs somebody runs at the archive rather than at a
// recovery: proving it is intact, and removing what nothing can still need.
//
// The retention preview is offered before the deletion and is the default,
// which is the opposite of the convention everywhere else in this product. It
// is deliberate: this is the only button here that removes the last copy of
// something.
function Maintenance({
  busy,
  running,
  onAsk,
}: {
  busy: boolean;
  running: boolean;
  onAsk: (path: string, body?: unknown) => Promise<void>;
}) {
  const t = useT();
  const failures = useApiList<ArchiveFailure>(
    '/platform/pitr/failures',
    undefined,
    { refetchInterval: false },
  );
  const rows = failures.data?.data ?? [];

  const columns: Column<ArchiveFailure>[] = [
    {
      key: 'segment',
      header: t('nx.pitr.col.segment'),
      primary: true,
      cell: (r) => (
        <p className="truncate font-medium text-fg">{r.segment || '—'}</p>
      ),
    },
    {
      key: 'when',
      header: t('nx.pitr.col.failedAt'),
      cell: (r) => (
        <p className="truncate text-label text-fg">{when(r.failed_at)}</p>
      ),
    },
    {
      key: 'reason',
      header: t('nx.pitr.col.reason'),
      cell: (r) => (
        <p className="max-w-[52ch] text-caption text-subtle">{r.reason || '—'}</p>
      ),
    },
  ];

  return (
    <Panel title={t('nx.pitr.maintTitle')} description={t('nx.pitr.maintIntro')}>
      <div className="flex flex-wrap gap-3">
        <Button
          onClick={() => onAsk('/platform/pitr/verify', { deep_sample: 8 })}
          disabled={busy || running}
        >
          <Search aria-hidden />
          {t('nx.pitr.checkArchive')}
        </Button>
        <Button
          onClick={() => onAsk('/platform/pitr/prune', { apply: false })}
          disabled={busy}
        >
          {t('nx.pitr.previewRetention')}
        </Button>
        <Button
          variant="destructive"
          onClick={() => onAsk('/platform/pitr/prune', { apply: true })}
          disabled={busy}
        >
          {t('nx.pitr.applyRetention')}
        </Button>
      </div>
      <p className="mt-3 max-w-[68ch] text-caption text-subtle">
        {t('nx.pitr.retentionHint')}
      </p>

      <h3 className="mt-6 text-label font-medium text-fg">
        {t('nx.pitr.failuresTitle')}
      </h3>
      {rows.length === 0 ? (
        <p className="mt-2 text-caption text-subtle">
          {t('nx.pitr.noFailures')}
        </p>
      ) : (
        <div className="mt-2">
          <DataTable
            caption={t('nx.pitr.failuresCaption')}
            columns={columns}
            rows={rows}
            rowKey={(r, i) => `${r.segment ?? 'x'}-${r.failed_at ?? i}`}
          />
        </div>
      )}
    </Panel>
  );
}

/** A timestamp a person can read, in their own machine's format. */
function when(iso?: string): string {
  if (!iso) return '—';
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
