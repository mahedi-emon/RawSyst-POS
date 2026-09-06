'use client';

// Privacy: the registers a business has to keep, and the two clocks on them.
//
// # What is running out comes before what is on file
//
// Six registers is a filing cabinet, and a filing cabinet does not tell anybody
// what to do this morning. A subject request has a statutory deadline and a
// breach has a notification window, so anything counting down leads the screen
// and the registers sit behind tabs underneath it. Nothing is hidden — the
// queue is a way in, not a summary.
//
// # Neither clock is ours
//
// `days_left` and `hours_left` are counted by the server and read here. A
// deadline this product worked out itself would be a second answer to a
// regulatory question, free to disagree with the register the request was filed
// against. The same reason `under_warranty` is never recomputed, with more at
// stake.
//
// # Withdrawal is a record, not a deletion
//
// A withdrawn consent keeps its row and gains a `withdrawn_at`, so the register
// shows that permission was given and then taken away. Both dates are printed
// for that reason: the withdrawal is the record, and a row showing only the
// grant would read as though it still stood.

import { ShieldCheck } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { TabPanel, Tabs } from '@/components/ui/tabs';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  activityGaps,
  blockedBy,
  consentStands,
  dueOn,
  holdStands,
  incidentClock,
  pressing,
  requestClock,
  uncovered,
  type Activity,
  type Clock,
  type Consent,
  type Hold,
  type Incident,
  type Request,
  type Retention,
} from '@/lib/privacy/governance';
import { useUrlState } from '@/lib/url-state';

const REGISTERS = [
  'requests',
  'incidents',
  'consents',
  'register',
  'retention',
  'disclosure',
] as const;
type RegisterTab = (typeof REGISTERS)[number];

const TAB_LABEL: Record<RegisterTab, Key> = {
  requests: 'nx.pri.tabRequests',
  incidents: 'nx.pri.tabIncidents',
  consents: 'nx.pri.tabConsents',
  register: 'nx.pri.tabRegister',
  retention: 'nx.pri.tabRetention',
  disclosure: 'nx.pri.tabDisclosure',
};

const CLOCK_TONE: Record<Clock, Tone> = {
  overdue: 'critical',
  due_soon: 'caution',
  waiting_on_subject: 'info',
  running: 'neutral',
  settled: 'positive',
};

const CLOCK_LABEL: Record<Clock, Key> = {
  overdue: 'nx.pri.overdue',
  due_soon: 'nx.pri.dueSoon',
  waiting_on_subject: 'nx.pri.waitingOnSubject',
  running: 'nx.pri.running',
  settled: 'nx.pri.settled',
};

/** A short date. The registers carry timestamps; a reader wants the day. */
function day(iso?: string): string {
  return iso ? iso.slice(0, 10) : '—';
}

function PrivacyScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayManage = grants.can('privacy.manage');

  // Validated rather than cast: the value comes off the URL, so a hand-typed
  // or stale link must land on a real tab instead of an empty panel.
  const [rawTab, setTab] = useUrlState('on', 'requests');
  const tab = (REGISTERS as readonly string[]).includes(rawTab)
    ? (rawTab as RegisterTab)
    : 'requests';
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const requests = useApiList<Request>(scope ? '/privacy/requests' : null, scope ?? undefined);
  const incidents = useApiList<Incident>(scope ? '/privacy/incidents' : null, scope ?? undefined);
  const consents = useApiList<Consent>(scope ? '/privacy/consents' : null, scope ?? undefined);
  const activities = useApiList<Activity>(scope ? '/privacy/activities' : null, scope ?? undefined);
  const retentions = useApiList<Retention>(scope ? '/privacy/retention' : null, scope ?? undefined);
  const holds = useApiList<Hold>(scope ? '/privacy/holds' : null, scope ?? undefined);

  const requestRows = requests.data?.data ?? [];
  const incidentRows = incidents.data?.data ?? [];
  const queue = pressing(requestRows, incidentRows);

  /** Runs a write, then re-reads whatever it changed. */
  async function act(run: () => Promise<unknown>, ...after: { refetch: () => unknown }[]) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await run();
      for (const q of after) await q.refetch();
    } catch (e) {
      // A refusal here is often a legal one — an erasure under a hold, a
      // deadline that cannot be extended twice. It is shown as written; it is
      // not a validation hint to correct.
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const q = `?company_id=${scope?.company_id ?? ''}`;

  return (
    <>
      <PageHeader title={t('nx.pri.title')} description={t('nx.pri.subtitle')} />

      <FormError message={error} className="mb-4" />

      {/* What is counting down, before what is merely on file. */}
      {queue.length > 0 ? (
        <Panel className="mb-6" title={t('nx.pri.pressingTitle')}>
          <ul className="flex flex-col divide-y divide-line">
            {queue.map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-3 py-2 first:pt-0">
                <Badge tone={CLOCK_TONE[p.clock]}>{t(CLOCK_LABEL[p.clock])}</Badge>
                <span className="num text-caption text-muted">{p.reference}</span>
                <span className="min-w-0 flex-1 truncate text-body">{p.what}</span>
                <span className="num text-caption text-muted">
                  {/* The server's count in the server's unit. An incident's
                      hours are never shown as a fraction of a day. */}
                  {p.left < 0
                    ? t(p.unit === 'days' ? 'nx.pri.daysOver' : 'nx.pri.hoursOver', {
                        n: String(Math.abs(p.left)),
                      })
                    : t(p.unit === 'days' ? 'nx.pri.daysLeft' : 'nx.pri.hoursLeft', {
                        n: String(p.left),
                      })}
                </span>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}

      <Tabs<RegisterTab>
        label={t('nx.pri.title')}
        value={tab}
        onChange={setTab}
        items={REGISTERS.map((r) => ({ id: r, label: t(TAB_LABEL[r]) }))}
      />

      <TabPanel id={tab}>
        {tab === 'requests' ? (
          <RequestsPanel
            query={requests}
            rows={requestRows}
            mayManage={mayManage}
            busy={busy}
            onExtend={(id, reason) =>
              act(
                () => api.post(`/privacy/requests/${id}/extend${q}`, { reason }),
                requests,
              )
            }
            onClose={(id, outcome, note) =>
              act(
                () => api.post(`/privacy/requests/${id}/close${q}`, { outcome, note }),
                requests,
              )
            }
          />
        ) : null}

        {tab === 'incidents' ? (
          <IncidentsPanel
            query={incidents}
            rows={incidentRows}
            mayManage={mayManage}
            busy={busy}
            onNotify={(id, who) =>
              act(() => api.post(`/privacy/incidents/${id}/notify${q}`, { who }), incidents)
            }
            onClose={(id, containment) =>
              act(
                () => api.post(`/privacy/incidents/${id}/close${q}`, { containment }),
                incidents,
              )
            }
          />
        ) : null}

        {tab === 'consents' ? (
          <ConsentsPanel
            query={consents}
            mayManage={mayManage}
            busy={busy}
            onWithdraw={(id) =>
              act(() => api.post(`/privacy/consents/${id}/withdraw${q}`, {}), consents)
            }
          />
        ) : null}

        {tab === 'register' ? (
          <RegisterPanel
            query={activities}
            retentions={retentions.data?.data ?? []}
          />
        ) : null}

        {tab === 'retention' ? (
          <RetentionPanel
            retentions={retentions}
            holds={holds}
            mayManage={mayManage}
            busy={busy}
            onRelease={(id) =>
              act(() => api.post(`/privacy/holds/${id}/release${q}`, {}), holds)
            }
          />
        ) : null}

        {tab === 'disclosure' ? <DisclosurePanel /> : null}
      </TabPanel>
    </>
  );
}

// --- the panels ------------------------------------------------------------

/** Shared shape of the list hooks these panels read. */
interface ListQuery<T> {
  data?: { data: T[] };
  error: unknown;
  isLoading: boolean;
  refetch: () => unknown;
}

function Loading<T>({
  query,
  columns,
  empty,
  children,
}: {
  query: ListQuery<T>;
  columns: number;
  empty: React.ReactNode;
  children: React.ReactNode;
}) {
  const rows = query.data?.data ?? [];
  if (query.error) {
    return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  }
  if (query.isLoading && !query.data) return <TableSkeleton columns={columns} />;
  if (rows.length === 0) return <>{empty}</>;
  return <>{children}</>;
}

function RequestsPanel({
  query,
  rows,
  mayManage,
  busy,
  onExtend,
  onClose,
}: {
  query: ListQuery<Request>;
  rows: Request[];
  mayManage: boolean;
  busy: boolean;
  onExtend: (id: string, reason: string) => void;
  onClose: (id: string, outcome: string, note: string) => void;
}) {
  const t = useT();
  const [open, setOpen] = useState<Request | null>(null);
  const [reason, setReason] = useState('');
  const [outcome, setOutcome] = useState('fulfilled');
  const [note, setNote] = useState('');

  const columns: Column<Request>[] = [
    {
      key: 'ref',
      header: t('nx.pri.colRequest'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{r.request_no}</span>
          <span className="text-caption text-muted">{r.subject_name}</span>
        </span>
      ),
    },
    {
      key: 'kind',
      header: t('nx.pri.colKind'),
      width: 'w-32',
      cell: (r) => <span>{t(`nx.pri.kind.${r.kind}` as Key)}</span>,
    },
    {
      key: 'clock',
      header: t('nx.pri.colDeadline'),
      width: 'w-56',
      cell: (r) => {
        const clock = requestClock(r);
        return (
          <span className="flex flex-col gap-1">
            <Badge tone={CLOCK_TONE[clock]}>{t(CLOCK_LABEL[clock])}</Badge>
            <span className="num text-caption text-muted">
              {day(dueOn(r))}
              {r.extended_to ? ` · ${t('nx.pri.extended')}` : ''}
            </span>
            {blockedBy(r) ? (
              // Saying so on the row is the difference between a queue
              // somebody can work and one where the same request is picked up
              // and put down every day.
              <span className="text-caption text-caution-fg">
                {t('nx.pri.underHold')}
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'status',
      header: t('nx.pri.colState'),
      secondary: true,
      width: 'w-40',
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span>{t(`nx.pri.dsr.${r.status}` as Key)}</span>
          {r.outcome ? (
            <span className="text-caption text-muted">
              {t(`nx.pri.outcome.${r.outcome}` as Key)}
            </span>
          ) : null}
        </span>
      ),
    },
    ...(mayManage
      ? [
          {
            key: 'act',
            header: t('nx.pri.colAction'),
            width: 'w-28',
            cell: (r: Request) =>
              requestClock(r) === 'settled' ? (
                <span className="text-caption text-muted">—</span>
              ) : (
                <Button
                  variant="ghost"
                  onClick={() => {
                    setOpen(r);
                    setReason('');
                    setNote('');
                    setOutcome('fulfilled');
                  }}
                >
                  {t('nx.pri.answer')}
                </Button>
              ),
          },
        ]
      : []),
  ];

  return (
    <>
      <Loading
        query={query}
        columns={5}
        empty={
          <EmptyState
            icon={ShieldCheck}
            title={t('nx.pri.noRequestsTitle')}
            description={t('nx.pri.noRequestsDesc')}
          />
        }
      >
        <DataTable
          caption={t('nx.pri.tabRequests')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
        />
      </Loading>

      {open ? (
        <Panel
          className="mt-6"
          title={t('nx.pri.answering', { ref: open.request_no })}
          description={t('nx.pri.answeringHint')}
          actions={<Button variant="ghost" onClick={() => setOpen(null)}>{t('nx.pri.cancel')}</Button>}
        >
          <div className="grid gap-5 lg:grid-cols-2">
            <div className="flex flex-col gap-3">
              <Field name="outcome" label={t('nx.pri.outcomeLabel')}>
                <Select value={outcome} onChange={(e) => setOutcome(e.target.value)}>
                  <option value="fulfilled">{t('nx.pri.outcome.fulfilled')}</option>
                  <option value="refused">{t('nx.pri.outcome.refused')}</option>
                  <option value="partially_fulfilled">
                    {t('nx.pri.outcome.partially_fulfilled')}
                  </option>
                </Select>
              </Field>
              <Field
                name="note"
                label={t('nx.pri.outcomeNote')}
                hint={t('nx.pri.outcomeNoteHint')}
              >
                <Textarea rows={3} value={note} onChange={(e) => setNote(e.target.value)} />
              </Field>
              <div>
                <Button
                  variant="primary"
                  disabled={busy || note.trim() === ''}
                  onClick={() => {
                    onClose(open.id, outcome, note.trim());
                    setOpen(null);
                  }}
                >
                  {t('nx.pri.recordAnswer')}
                </Button>
              </div>
            </div>

            <div className="flex flex-col gap-3">
              <Field
                name="reason"
                label={t('nx.pri.extendLabel')}
                hint={t('nx.pri.extendHint')}
              >
                <Textarea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} />
              </Field>
              <div>
                <Button
                  disabled={busy || reason.trim() === '' || Boolean(open.extended_to)}
                  onClick={() => {
                    onExtend(open.id, reason.trim());
                    setOpen(null);
                  }}
                >
                  {t('nx.pri.extend')}
                </Button>
                {open.extended_to ? (
                  <p className="mt-2 max-w-prose text-caption text-muted">
                    {t('nx.pri.alreadyExtended', { on: day(open.extended_to) })}
                  </p>
                ) : null}
              </div>
            </div>
          </div>
        </Panel>
      ) : null}
    </>
  );
}

function IncidentsPanel({
  query,
  rows,
  mayManage,
  busy,
  onNotify,
  onClose,
}: {
  query: ListQuery<Incident>;
  rows: Incident[];
  mayManage: boolean;
  busy: boolean;
  onNotify: (id: string, who: string) => void;
  onClose: (id: string, containment: string) => void;
}) {
  const t = useT();
  const [open, setOpen] = useState<Incident | null>(null);
  const [who, setWho] = useState('authority');
  const [containment, setContainment] = useState('');

  const columns: Column<Incident>[] = [
    {
      key: 'ref',
      header: t('nx.pri.colIncident'),
      primary: true,
      cell: (i) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{i.incident_no}</span>
          <span className="text-caption text-muted">{i.title}</span>
        </span>
      ),
    },
    {
      key: 'severity',
      header: t('nx.pri.colSeverity'),
      width: 'w-28',
      cell: (i) => (
        <Badge tone={i.severity === 'critical' || i.severity === 'high' ? 'critical' : 'neutral'}>
          {t(`nx.pri.severity.${i.severity}` as Key)}
        </Badge>
      ),
    },
    {
      key: 'window',
      header: t('nx.pri.colWindow'),
      width: 'w-52',
      cell: (i) => {
        const clock = incidentClock(i);
        return (
          <span className="flex flex-col gap-1">
            <Badge tone={CLOCK_TONE[clock]}>{t(CLOCK_LABEL[clock])}</Badge>
            <span className="num text-caption text-muted">
              {i.sdaia_notified_at
                ? t('nx.pri.notifiedOn', { on: day(i.sdaia_notified_at) })
                : day(i.notify_due_at)}
            </span>
          </span>
        );
      },
    },
    {
      key: 'who',
      header: t('nx.pri.colAffected'),
      secondary: true,
      width: 'w-40',
      cell: (i) => (
        <span className="flex flex-col gap-0.5">
          {/* Absent is not none: an incident under investigation may not yet
              know how many people it touched, and printing 0 would say it
              touched nobody. */}
          <span className="num">
            {typeof i.subjects_affected === 'number'
              ? i.subjects_affected
              : t('nx.pri.countUnknown')}
          </span>
          <span className="text-caption text-muted">{i.data_categories}</span>
        </span>
      ),
    },
    ...(mayManage
      ? [
          {
            key: 'act',
            header: t('nx.pri.colAction'),
            width: 'w-28',
            cell: (i: Incident) =>
              i.status === 'closed' ? (
                <span className="text-caption text-muted">—</span>
              ) : (
                <Button
                  variant="ghost"
                  onClick={() => {
                    setOpen(i);
                    setContainment(i.containment ?? '');
                    setWho('authority');
                  }}
                >
                  {t('nx.pri.handle')}
                </Button>
              ),
          },
        ]
      : []),
  ];

  return (
    <>
      <Loading
        query={query}
        columns={5}
        empty={
          <EmptyState
            icon={ShieldCheck}
            title={t('nx.pri.noIncidentsTitle')}
            description={t('nx.pri.noIncidentsDesc')}
          />
        }
      >
        <DataTable
          caption={t('nx.pri.tabIncidents')}
          columns={columns}
          rows={rows}
          rowKey={(i) => i.id}
        />
      </Loading>

      {open ? (
        <Panel
          className="mt-6"
          title={t('nx.pri.handling', { ref: open.incident_no })}
          actions={<Button variant="ghost" onClick={() => setOpen(null)}>{t('nx.pri.cancel')}</Button>}
        >
          <p className="mb-4 max-w-prose text-body text-muted">{open.what_happened}</p>

          <div className="grid gap-5 lg:grid-cols-2">
            <div className="flex flex-col gap-3">
              <Field
                name="who"
                label={t('nx.pri.notifyLabel')}
                hint={t('nx.pri.notifyHint')}
              >
                <Select value={who} onChange={(e) => setWho(e.target.value)}>
                  <option value="authority">{t('nx.pri.notifyAuthority')}</option>
                  <option value="subjects">{t('nx.pri.notifySubjects')}</option>
                </Select>
              </Field>
              <div>
                <Button
                  variant="primary"
                  disabled={busy}
                  onClick={() => {
                    onNotify(open.id, who);
                    setOpen(null);
                  }}
                >
                  {t('nx.pri.recordNotification')}
                </Button>
              </div>
            </div>

            <div className="flex flex-col gap-3">
              <Field
                name="containment"
                label={t('nx.pri.containmentLabel')}
                hint={t('nx.pri.containmentHint')}
              >
                <Textarea
                  rows={3}
                  value={containment}
                  onChange={(e) => setContainment(e.target.value)}
                />
              </Field>
              <div>
                <Button
                  disabled={busy || containment.trim() === ''}
                  onClick={() => {
                    onClose(open.id, containment.trim());
                    setOpen(null);
                  }}
                >
                  {t('nx.pri.closeIncident')}
                </Button>
              </div>
            </div>
          </div>
        </Panel>
      ) : null}
    </>
  );
}

function ConsentsPanel({
  query,
  mayManage,
  busy,
  onWithdraw,
}: {
  query: ListQuery<Consent>;
  mayManage: boolean;
  busy: boolean;
  onWithdraw: (id: string) => void;
}) {
  const t = useT();
  const rows = query.data?.data ?? [];

  const columns: Column<Consent>[] = [
    {
      key: 'subject',
      header: t('nx.pri.colSubject'),
      primary: true,
      cell: (c) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{c.subject_name || c.subject_id}</span>
          <span className="text-caption text-muted">
            {t(`nx.pri.purpose.${c.purpose}` as Key)} · {t(`nx.pri.channel.${c.channel}` as Key)}
          </span>
        </span>
      ),
    },
    {
      key: 'basis',
      header: t('nx.pri.colBasis'),
      width: 'w-44',
      cell: (c) => <span>{t(`nx.pri.basis.${c.lawful_basis}` as Key)}</span>,
    },
    {
      key: 'stands',
      header: t('nx.pri.colStanding'),
      width: 'w-48',
      cell: (c) => (
        <span className="flex flex-col gap-1">
          <Badge tone={consentStands(c) ? 'positive' : 'neutral'}>
            {t(consentStands(c) ? 'nx.pri.consentStands' : 'nx.pri.consentWithdrawn')}
          </Badge>
          <span className="num text-caption text-muted">
            {/* Both dates, because the register's job is to show that
                permission was given and then taken away. */}
            {day(c.granted_at)}
            {c.withdrawn_at ? ` → ${day(c.withdrawn_at)}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'proof',
      header: t('nx.pri.colProof'),
      secondary: true,
      cell: (c) => <span className="text-muted">{c.proof}</span>,
    },
    ...(mayManage
      ? [
          {
            key: 'act',
            header: t('nx.pri.colAction'),
            width: 'w-32',
            cell: (c: Consent) =>
              consentStands(c) ? (
                <Button variant="ghost" disabled={busy} onClick={() => onWithdraw(c.id)}>
                  {t('nx.pri.withdraw')}
                </Button>
              ) : (
                <span className="text-caption text-muted">—</span>
              ),
          },
        ]
      : []),
  ];

  return (
    <Loading
      query={query}
      columns={5}
      empty={
        <EmptyState
          icon={ShieldCheck}
          title={t('nx.pri.noConsentsTitle')}
          description={t('nx.pri.noConsentsDesc')}
        />
      }
    >
      <DataTable
        caption={t('nx.pri.tabConsents')}
        columns={columns}
        rows={rows}
        rowKey={(c) => c.id}
      />
    </Loading>
  );
}

function RegisterPanel({
  query,
  retentions,
}: {
  query: ListQuery<Activity>;
  retentions: Retention[];
}) {
  const t = useT();
  const rows = query.data?.data ?? [];
  const orphaned = uncovered(rows, retentions);

  const columns: Column<Activity>[] = [
    {
      key: 'name',
      header: t('nx.pri.colActivity'),
      primary: true,
      cell: (a) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{a.name}</span>
          <span className="text-caption text-muted">{a.purpose}</span>
        </span>
      ),
    },
    {
      key: 'basis',
      header: t('nx.pri.colBasis'),
      width: 'w-44',
      cell: (a) => <span>{t(`nx.pri.basis.${a.lawful_basis}` as Key)}</span>,
    },
    {
      key: 'data',
      header: t('nx.pri.colData'),
      secondary: true,
      cell: (a) => (
        <span className="flex flex-col gap-0.5">
          <span>{a.data_categories}</span>
          <span className="text-caption text-muted">{a.subject_categories}</span>
        </span>
      ),
    },
    {
      key: 'transfer',
      header: t('nx.pri.colTransfer'),
      width: 'w-48',
      cell: (a) =>
        a.cross_border ? (
          <span className="flex flex-col gap-0.5">
            <span>{a.destination_country || '—'}</span>
            <span className="text-caption text-muted">{a.transfer_safeguard || '—'}</span>
          </span>
        ) : (
          <span className="text-muted">{t('nx.pri.staysHere')}</span>
        ),
    },
    {
      key: 'gaps',
      header: t('nx.pri.colGaps'),
      width: 'w-44',
      cell: (a) => {
        const gaps = activityGaps(a);
        if (gaps.length === 0) return <Badge tone="positive">{t('nx.pri.complete')}</Badge>;
        return (
          <span className="flex flex-wrap gap-1">
            {gaps.map((g) => (
              <Badge key={g} tone="caution">
                {t(`nx.pri.gap.${g}` as Key)}
              </Badge>
            ))}
          </span>
        );
      },
    },
  ];

  return (
    <>
      {orphaned.length > 0 ? (
        // The gap PDPL is actually about: something is collected, and no
        // policy ever disposes of it, so it is kept for ever by default.
        <Panel className="mb-5" title={t('nx.pri.uncoveredTitle')}>
          <p className="max-w-prose text-body text-muted">
            {t('nx.pri.uncoveredDesc')}
          </p>
          <ul className="mt-3 flex flex-wrap gap-1.5">
            {orphaned.map((c) => (
              <li key={c}>
                <Badge tone="caution">{c}</Badge>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}

      <Loading
        query={query}
        columns={5}
        empty={
          <EmptyState
            icon={ShieldCheck}
            title={t('nx.pri.noActivitiesTitle')}
            description={t('nx.pri.noActivitiesDesc')}
          />
        }
      >
        <DataTable
          caption={t('nx.pri.tabRegister')}
          columns={columns}
          rows={rows}
          rowKey={(a) => a.id}
        />
      </Loading>
    </>
  );
}

function RetentionPanel({
  retentions,
  holds,
  mayManage,
  busy,
  onRelease,
}: {
  retentions: ListQuery<Retention>;
  holds: ListQuery<Hold>;
  mayManage: boolean;
  busy: boolean;
  onRelease: (id: string) => void;
}) {
  const t = useT();
  const policyRows = retentions.data?.data ?? [];
  const holdRows = holds.data?.data ?? [];

  const policyColumns: Column<Retention>[] = [
    {
      key: 'category',
      header: t('nx.pri.colCategory'),
      primary: true,
      cell: (r) => <span className="font-medium">{r.data_category}</span>,
    },
    {
      key: 'keep',
      header: t('nx.pri.colKeepFor'),
      width: 'w-40',
      cell: (r) => (
        <span className="num">{t('nx.pri.months', { n: String(r.retain_months) })}</span>
      ),
    },
    {
      key: 'action',
      header: t('nx.pri.colThen'),
      width: 'w-40',
      cell: (r) => <span>{t(`nx.pri.action.${r.action}` as Key)}</span>,
    },
    {
      key: 'active',
      header: t('nx.pri.colStanding'),
      width: 'w-40',
      cell: (r) => (
        <span className="flex flex-col gap-1">
          <Badge tone={r.is_active ? 'positive' : 'neutral'}>
            {t(r.is_active ? 'nx.pri.policyOn' : 'nx.pri.policyOff')}
          </Badge>
          {/* An "off" policy deletes nothing. Saying when it last ran is what
              separates a policy that works from one that is merely written. */}
          <span className="num text-caption text-muted">
            {r.last_run_at ? day(r.last_run_at) : t('nx.pri.neverRun')}
          </span>
        </span>
      ),
    },
  ];

  const holdColumns: Column<Hold>[] = [
    {
      key: 'name',
      header: t('nx.pri.colHold'),
      primary: true,
      cell: (h) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{h.name}</span>
          <span className="text-caption text-muted">{h.reason}</span>
        </span>
      ),
    },
    {
      key: 'scope',
      header: t('nx.pri.colCategory'),
      secondary: true,
      width: 'w-48',
      cell: (h) => <span className="text-muted">{h.data_category || '—'}</span>,
    },
    {
      key: 'standing',
      header: t('nx.pri.colStanding'),
      width: 'w-44',
      cell: (h) => (
        <span className="flex flex-col gap-1">
          <Badge tone={holdStands(h) ? 'caution' : 'neutral'}>
            {t(holdStands(h) ? 'nx.pri.holdStands' : 'nx.pri.holdReleased')}
          </Badge>
          <span className="num text-caption text-muted">
            {day(h.placed_at)}
            {h.released_at ? ` → ${day(h.released_at)}` : ''}
          </span>
        </span>
      ),
    },
    ...(mayManage
      ? [
          {
            key: 'act',
            header: t('nx.pri.colAction'),
            width: 'w-28',
            cell: (h: Hold) =>
              holdStands(h) ? (
                <Button variant="ghost" disabled={busy} onClick={() => onRelease(h.id)}>
                  {t('nx.pri.release')}
                </Button>
              ) : (
                <span className="text-caption text-muted">—</span>
              ),
          },
        ]
      : []),
  ];

  return (
    <div className="flex flex-col gap-8">
      <section>
        <h2 className="mb-3 text-card-title font-semibold text-fg">
          {t('nx.pri.policiesTitle')}
        </h2>
        <Loading
          query={retentions}
          columns={4}
          empty={
            <EmptyState
              icon={ShieldCheck}
              title={t('nx.pri.noPoliciesTitle')}
              description={t('nx.pri.noPoliciesDesc')}
            />
          }
        >
          <DataTable
            caption={t('nx.pri.policiesTitle')}
            columns={policyColumns}
            rows={policyRows}
            rowKey={(r) => r.id}
          />
        </Loading>
      </section>

      <section>
        <h2 className="mb-1 text-card-title font-semibold text-fg">
          {t('nx.pri.holdsTitle')}
        </h2>
        <p className="mb-3 max-w-prose text-caption text-muted">
          {t('nx.pri.holdsDesc')}
        </p>
        <Loading
          query={holds}
          columns={4}
          empty={
            <EmptyState
              icon={ShieldCheck}
              title={t('nx.pri.noHoldsTitle')}
              description={t('nx.pri.noHoldsDesc')}
            />
          }
        >
          <DataTable
            caption={t('nx.pri.holdsTitle')}
            columns={holdColumns}
            rows={holdRows}
            rowKey={(h) => h.id}
          />
        </Loading>
      </section>
    </div>
  );
}

interface Settings {
  dpo_external: boolean;
  data_region: string;
  dpo_name?: string;
  dpo_email?: string;
}

interface Disclosure {
  contact_email?: string;
  cr_number?: string;
  vat_number?: string;
  missing?: string[];
}

interface Subprocessor {
  id: string;
  name: string;
  purpose: string;
  country: string;
  safeguard?: string;
}

function DisclosurePanel() {
  const t = useT();
  const scope = useCompanyScope();

  const settings = useApi<{ settings: Settings }>(
    scope ? '/privacy/settings' : null,
    scope ?? undefined,
  );
  const disclosure = useApi<{ disclosure: Disclosure }>(
    scope ? '/privacy/disclosure' : null,
    scope ?? undefined,
  );
  const subprocessors = useApiList<Subprocessor>(
    scope ? '/privacy/subprocessors' : null,
    scope ?? undefined,
  );

  const s = settings.data?.settings;
  const d = disclosure.data?.disclosure;
  const missing = d?.missing ?? [];

  return (
    <div className="flex flex-col gap-6">
      <Panel title={t('nx.pri.settingsTitle')}>
        {s ? (
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <div>
              <dt className="text-label text-muted">{t('nx.pri.dataRegion')}</dt>
              <dd className="mt-0.5 text-body uppercase">{s.data_region}</dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.pri.dpo')}</dt>
              <dd className="mt-0.5 text-body">
                {s.dpo_name || t(s.dpo_external ? 'nx.pri.dpoExternal' : 'nx.pri.dpoNone')}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.pri.dpoContact')}</dt>
              <dd className="mt-0.5 text-body">{s.dpo_email || '—'}</dd>
            </div>
          </dl>
        ) : (
          <TableSkeleton columns={3} />
        )}
      </Panel>

      <Panel
        title={t('nx.pri.disclosureTitle')}
        description={t('nx.pri.disclosureDesc')}
      >
        {missing.length > 0 ? (
          // The notice cannot be published while these are blank, so they lead
          // rather than sitting under the fields that are filled in.
          <div className="mb-4">
            <p className="max-w-prose text-body text-fg">{t('nx.pri.disclosureBlocked')}</p>
            <ul className="mt-2 flex flex-wrap gap-1.5">
              {missing.map((m) => (
                <li key={m}>
                  <Badge tone="caution">{t(`nx.pri.field.${m}` as Key)}</Badge>
                </li>
              ))}
            </ul>
          </div>
        ) : (
          <p className="mb-4 max-w-prose text-body text-muted">
            {t('nx.pri.disclosureReady')}
          </p>
        )}

        <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <dt className="text-label text-muted">{t('nx.pri.contactEmail')}</dt>
            <dd className="mt-0.5 text-body">{d?.contact_email || '—'}</dd>
          </div>
          <div>
            <dt className="text-label text-muted">{t('nx.pri.crNumber')}</dt>
            <dd className="num mt-0.5 text-body">{d?.cr_number || '—'}</dd>
          </div>
          <div>
            <dt className="text-label text-muted">{t('nx.pri.vatNumber')}</dt>
            <dd className="num mt-0.5 text-body">{d?.vat_number || '—'}</dd>
          </div>
        </dl>
      </Panel>

      <section>
        <h2 className="mb-1 text-card-title font-semibold text-fg">
          {t('nx.pri.subprocessorsTitle')}
        </h2>
        <p className="mb-3 max-w-prose text-caption text-muted">
          {t('nx.pri.subprocessorsDesc')}
        </p>
        <Loading
          query={subprocessors}
          columns={4}
          empty={
            <EmptyState
              icon={ShieldCheck}
              title={t('nx.pri.noSubprocessorsTitle')}
              description={t('nx.pri.noSubprocessorsDesc')}
            />
          }
        >
          <DataTable
            caption={t('nx.pri.subprocessorsTitle')}
            columns={[
              {
                key: 'name',
                header: t('nx.pri.colSubprocessor'),
                primary: true,
                cell: (p: Subprocessor) => <span className="font-medium">{p.name}</span>,
              },
              {
                key: 'purpose',
                header: t('nx.pri.colPurpose'),
                cell: (p: Subprocessor) => <span className="text-muted">{p.purpose}</span>,
              },
              {
                key: 'country',
                header: t('nx.pri.colCountry'),
                width: 'w-32',
                cell: (p: Subprocessor) => <span className="uppercase">{p.country}</span>,
              },
              {
                key: 'safeguard',
                header: t('nx.pri.colSafeguard'),
                secondary: true,
                cell: (p: Subprocessor) => (
                  <span className="text-muted">{p.safeguard || '—'}</span>
                ),
              },
            ]}
            rows={subprocessors.data?.data ?? []}
            rowKey={(p) => p.id}
          />
        </Loading>
      </section>
    </div>
  );
}

export default function PrivacyPage() {
  return (
    <RequirePermission anyOf={['privacy.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <PrivacyScreen />
      </Suspense>
    </RequirePermission>
  );
}
