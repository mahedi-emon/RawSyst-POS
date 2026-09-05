'use client';

// Reports somebody keeps, and the ones that arrive by email.
//
// # A saved report is a question, not a snapshot
//
// The window is stored as a relative phrase — "last month" — never as two
// dates. Run in October it means September; the same report run in November
// means October. Storing dates would make a saved report a photograph, and a
// schedule built on them would email the same figures for ever.
//
// The screen shows the phrase AND what it resolves to today, because "last
// month" is unambiguous to the person who wrote it and not to the person who
// inherits it.
//
// # A schedule sends figures out of the building
//
// Which is why keeping a report is `report.save` rather than `report.view`, and
// why a cadence with no recipients is refused: it would run every week and
// reach nobody. Monthly stops at the 28th, because a schedule set for the 31st
// skips February and a shop that asked for monthly figures quietly gets eleven.
//
// # Export is offered only where it exists
//
// Twelve kinds can be kept and eight can be exported, and the two lists spell
// themselves differently. `exportKindOf` returns null for the rest, and the
// screen shows no button rather than one that answers 400.

import { CalendarClock } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  CADENCES,
  exportKindOf,
  savedProblem,
  SAVED_KINDS,
  SAVED_PERIODS,
  type SavedReport,
} from '@/lib/reports/analytics';

const KIND_LABEL: Record<string, Key> = {
  trial_balance: 'nx.sav.kTrialBalance',
  profit_and_loss: 'nx.sav.kProfitLoss',
  balance_sheet: 'nx.sav.kBalanceSheet',
  cash_flow: 'nx.sav.kCashFlow',
  sales: 'nx.sav.kSales',
  expenses: 'nx.sav.kExpenses',
  stock: 'nx.sav.kStock',
  vat_return: 'nx.sav.kVatReturn',
  receivables: 'nx.sav.kReceivables',
  payables: 'nx.sav.kPayables',
  movers: 'nx.sav.kMovers',
  compliance: 'nx.sav.kCompliance',
};

const PERIOD_LABEL: Record<string, Key> = {
  today: 'nx.sav.pToday',
  this_week: 'nx.sav.pThisWeek',
  this_month: 'nx.sav.pThisMonth',
  last_month: 'nx.sav.pLastMonth',
  this_quarter: 'nx.sav.pThisQuarter',
  last_quarter: 'nx.sav.pLastQuarter',
  this_year: 'nx.sav.pThisYear',
  last_year: 'nx.sav.pLastYear',
};

const CADENCE_LABEL: Record<string, Key> = {
  daily: 'nx.sav.cDaily',
  weekly: 'nx.sav.cWeekly',
  monthly: 'nx.sav.cMonthly',
};

const DAYS: Key[] = [
  'nx.sav.dSun',
  'nx.sav.dMon',
  'nx.sav.dTue',
  'nx.sav.dWed',
  'nx.sav.dThu',
  'nx.sav.dFri',
  'nx.sav.dSat',
];

const PROBLEM: Record<string, Key> = {
  no_name: 'nx.sav.needName',
  no_recipients: 'nx.sav.needRecipients',
  no_day: 'nx.sav.needDay',
  no_date: 'nx.sav.needDate',
};

function SavedScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayKeep = grants.can('report.save');
  const mayExport = grants.can('report.export');

  const { data, isLoading, error, refetch } = useApiList<SavedReport>(
    scope ? '/reports/saved' : null,
    scope ?? undefined,
  );

  const [adding, setAdding] = useState(false);
  const [name, setName] = useState('');
  const [kind, setKind] = useState<string>('profit_and_loss');
  const [period, setPeriod] = useState<string>('last_month');
  const [cadence, setCadence] = useState('');
  const [dayOfWeek, setDayOfWeek] = useState('');
  const [dayOfMonth, setDayOfMonth] = useState('');
  const [recipients, setRecipients] = useState('');
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const rows = data?.data ?? [];
  const state = savedProblem({ name, cadence, recipients, dayOfWeek, dayOfMonth });

  async function keep() {
    if (!scope || state !== 'none') return;
    setBusy(true);
    setFormError(null);
    setFieldErrors({});
    try {
      await api.put(`/reports/saved?company_id=${scope.company_id}`, {
        name: name.trim(),
        kind,
        period,
        ...(cadence
          ? {
              cadence,
              recipients: recipients.trim(),
              ...(cadence === 'weekly' ? { day_of_week: Number(dayOfWeek) } : {}),
              ...(cadence === 'monthly' ? { day_of_month: Number(dayOfMonth) } : {}),
            }
          : {}),
      });
      setName('');
      setCadence('');
      setRecipients('');
      setAdding(false);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function remove(report: SavedReport) {
    if (!scope) return;
    setBusy(true);
    setFormError(null);
    try {
      await api.delete(
        `/reports/saved/${report.id}?company_id=${scope.company_id}`,
      );
      void refetch();
    } catch (e) {
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  function exportHref(report: SavedReport): string | null {
    const kindPath = exportKindOf(report.kind);
    if (!kindPath || !scope) return null;
    return (
      `/api/v1/reports/${kindPath}/export` +
      `?company_id=${scope.company_id}&from=${report.from}&to=${report.to}`
    );
  }

  const columns: Column<SavedReport>[] = [
    {
      key: 'name',
      header: t('nx.sav.colName'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{r.name}</span>
          <span className="text-caption text-muted">
            {t(KIND_LABEL[r.kind] ?? 'nx.sav.kSales')}
          </span>
        </span>
      ),
    },
    {
      key: 'period',
      header: t('nx.sav.colPeriod'),
      width: 'w-52',
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span>{t(PERIOD_LABEL[r.period] ?? 'nx.sav.pThisMonth')}</span>
          {/* What it means today. "Last month" is unambiguous to whoever
              wrote it and not to whoever inherits it. */}
          <span className="num text-caption text-muted">
            {r.from} — {r.to}
          </span>
        </span>
      ),
    },
    {
      key: 'schedule',
      header: t('nx.sav.colSchedule'),
      width: 'w-56',
      cell: (r) => {
        if (!r.cadence) {
          return <span className="text-muted">{t('nx.sav.byHand')}</span>;
        }
        const when =
          r.cadence === 'weekly' && r.day_of_week !== undefined
            ? t(DAYS[r.day_of_week] ?? 'nx.sav.dSun')
            : r.cadence === 'monthly' && r.day_of_month !== undefined
              ? t('nx.sav.onThe', { day: String(r.day_of_month) })
              : '';
        return (
          <span className="flex flex-col gap-1">
            <span>
              {t(CADENCE_LABEL[r.cadence] ?? 'nx.sav.cDaily')}
              {when ? ` — ${when}` : ''}
            </span>
            {r.recipients ? (
              <span className="text-caption text-muted">{r.recipients}</span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'lastRun',
      header: t('nx.sav.colLastRun'),
      secondary: true,
      width: 'w-44',
      cell: (r) => {
        if (r.last_run_error) {
          // The failure, not a blank. A schedule that has been silently
          // failing for a month is worse than one that never ran.
          return (
            <span className="flex flex-col gap-1">
              <Badge tone="critical">{t('nx.sav.failed')}</Badge>
              <span className="text-caption text-critical-fg">
                {r.last_run_error}
              </span>
            </span>
          );
        }
        return r.last_run_at ? (
          <time dateTime={r.last_run_at} className="num text-muted">
            {r.last_run_at.slice(0, 10)}
          </time>
        ) : (
          <span className="text-muted">{t('nx.sav.neverRun')}</span>
        );
      },
    },
    {
      key: 'actions',
      header: t('nx.sav.colActions'),
      width: 'w-52',
      cell: (r) => {
        const href = mayExport ? exportHref(r) : null;
        return (
          <span className="flex flex-wrap gap-2">
            {href ? (
              <a
                href={href}
                className="inline-flex min-h-11 items-center text-body text-primary underline underline-offset-2 hover:no-underline"
              >
                {t('nx.sav.download')}
              </a>
            ) : null}
            {mayKeep ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => void remove(r)}
              >
                {t('nx.sav.remove')}
              </Button>
            ) : null}
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.sav.title')}
        description={t('nx.sav.subtitle')}
        actions={
          mayKeep ? (
            <Button variant="primary" onClick={() => setAdding((v) => !v)}>
              {t('nx.sav.keep')}
            </Button>
          ) : null
        }
      />

      <FormError message={formError} fields={fieldErrors} className="mb-4" />

      {adding ? (
        <Panel
          className="mb-5"
          title={t('nx.sav.keepTitle')}
          description={t('nx.sav.keepHint')}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field name="name" label={t('nx.sav.name')} error={fieldErrors.name} required>
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field name="kind" label={t('nx.sav.kind')} required>
              <Select value={kind} onChange={(e) => setKind(e.target.value)}>
                {SAVED_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {t(KIND_LABEL[k] as Key)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="period"
              label={t('nx.sav.period')}
              hint={t('nx.sav.periodHint')}
              required
            >
              <Select value={period} onChange={(e) => setPeriod(e.target.value)}>
                {SAVED_PERIODS.map((p) => (
                  <option key={p} value={p}>
                    {t(PERIOD_LABEL[p] as Key)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="cadence"
              label={t('nx.sav.cadence')}
              hint={t('nx.sav.cadenceHint')}
            >
              <Select value={cadence} onChange={(e) => setCadence(e.target.value)}>
                <option value="">{t('nx.sav.byHand')}</option>
                {CADENCES.map((c) => (
                  <option key={c} value={c}>
                    {t(CADENCE_LABEL[c] as Key)}
                  </option>
                ))}
              </Select>
            </Field>
            {cadence === 'weekly' ? (
              <Field name="day_of_week" label={t('nx.sav.whichDay')} required>
                <Select
                  value={dayOfWeek}
                  onChange={(e) => setDayOfWeek(e.target.value)}
                >
                  <option value="">{t('nx.sav.chooseDay')}</option>
                  {DAYS.map((d, i) => (
                    <option key={d} value={String(i)}>
                      {t(d)}
                    </option>
                  ))}
                </Select>
              </Field>
            ) : null}
            {cadence === 'monthly' ? (
              <Field
                name="day_of_month"
                label={t('nx.sav.whichDate')}
                hint={t('nx.sav.whichDateHint')}
                required
              >
                <Input
                  value={dayOfMonth}
                  onChange={(e) => setDayOfMonth(e.target.value)}
                  inputMode="numeric"
                  className="num text-end"
                />
              </Field>
            ) : null}
            {cadence ? (
              <Field
                name="recipients"
                label={t('nx.sav.recipients')}
                hint={t('nx.sav.recipientsHint')}
                error={fieldErrors.recipients}
                required
                className="sm:col-span-2"
              >
                <Input
                  value={recipients}
                  onChange={(e) => setRecipients(e.target.value)}
                />
              </Field>
            ) : null}
          </div>
          <div className="mt-4 flex items-center gap-3">
            <Button
              variant="primary"
              disabled={busy || state !== 'none'}
              onClick={() => void keep()}
            >
              {t('nx.sav.save')}
            </Button>
            <Button variant="ghost" onClick={() => setAdding(false)}>
              {t('nx.sav.cancel')}
            </Button>
            {state !== 'none' ? (
              <p className="text-caption text-muted">{t(PROBLEM[state] as Key)}</p>
            ) : null}
          </div>
        </Panel>
      ) : null}

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={CalendarClock}
          title={t('nx.sav.emptyTitle')}
          description={t('nx.sav.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.sav.title')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
        />
      ) : null}
    </>
  );
}

export default function SavedReportsPage() {
  return (
    <RequirePermission anyOf={['report.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SavedScreen />
      </Suspense>
    </RequirePermission>
  );
}
