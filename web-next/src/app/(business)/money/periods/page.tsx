'use client';

// The accounting calendar: which months are open, and which are settled.
//
// # Closing is in order, and the screen says why when it is not offered
//
// Closing March while February is open leaves a hole somebody can still post
// into, and the trial balance for the quarter you just reported keeps moving.
// So the button appears on the earliest open period and nowhere else — with the
// reason beside the ones where it does not, because "you cannot close March"
// and "close February first" are different sentences and only one tells
// somebody what to do.
//
// # Reopening needs a reason, and a different permission
//
// `accounting.close_period` closes; `accounting.reopen_period` reopens, and C10
// puts the second at Owner level because reopening changes figures somebody has
// already reported to somebody else. The reason is required by the server and is
// asked for here rather than collected as a refusal.
//
// # A month with entries in it is not the same as an empty one
//
// The count is on the row. Closing a month that holds nothing is bookkeeping
// hygiene; closing one with four hundred entries is a decision, and the screen
// should not make them look alike.

import { CalendarDays } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  allPeriods,
  closeBlock,
  localDay,
  periodOn,
  yearExists,
  type FiscalYear,
  type Period,
} from '@/lib/settings/business';

const STATE_LABEL: Record<string, Key> = {
  open: 'nx.per.open',
  closed: 'nx.per.closed',
  locked: 'nx.per.locked',
};

const STATE_TONE: Record<string, 'positive' | 'neutral' | 'caution'> = {
  open: 'positive',
  closed: 'neutral',
  locked: 'caution',
};

function PeriodsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayClose = grants.can('accounting.close_period');
  const mayReopen = grants.can('accounting.reopen_period');

  const { data, isLoading, error, refetch } = useApi<{ years: FiscalYear[] }>(
    scope ? '/accounting/periods' : null,
    scope ?? undefined,
  );

  const [year, setYear] = useState(() => String(new Date().getFullYear()));
  const [reopening, setReopening] = useState<string | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  // Closing a year is its own confirmation, because it is the one action on
  // this screen that cannot be undone. `mayClose` is `accounting.close_period`;
  // the route takes `accounting.reopen_period`, the more restricted of the two,
  // so the button is offered on the stricter grant rather than on the one that
  // merely closes a month.
  const [closingYear, setClosingYear] = useState(false);

  // The year-end figures come back from the server as decimal strings, and the
  // sentence reporting them is read by somebody who is about to file accounts.
  const money = (v: string) => formatMoney(v, { currency, market });

  const years = data?.years ?? [];
  const periods = allPeriods(years);
  const today = localDay();
  const current = periodOn(periods, today);
  const open = periods.filter((p) => p.state === 'open').length;

  async function openYear() {
    if (!scope) return;
    const n = Number(year);
    if (!Number.isInteger(n) || n < 2000 || n > 2100) return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      const out = await api.post<{ fiscal_year: number; periods_created: number }>(
        `/accounting/periods?company_id=${scope.company_id}`,
        { fiscal_year: n },
      );
      // How many were actually created, not just that it worked. Opening a
      // year that already exists is not an error — two people can press it on
      // the same morning — and the two cases read differently.
      setNote(
        out.periods_created > 0
          ? t('nx.per.opened', {
              count: String(out.periods_created),
              year: String(out.fiscal_year),
            })
          : t('nx.per.alreadyOpen', { year: String(out.fiscal_year) }),
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function close(period: Period) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await api.post(
        `/accounting/periods/${period.id}/close?company_id=${scope.company_id}`,
        {},
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function reopen(period: Period) {
    if (!scope || reason.trim() === '') return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await api.post(
        `/accounting/periods/${period.id}/reopen?company_id=${scope.company_id}`,
        { reason: reason.trim() },
      );
      setReopening(null);
      setReason('');
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function closeYear() {
    if (!scope) return;
    const n = Number(year);
    if (!Number.isInteger(n)) return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      const out = await api.post<{
        fiscal_year: number;
        revenue_closed: string;
        expenses_closed: string;
        profit_to_retained_earnings: string;
        already_closed?: boolean;
      }>(`/accounting/year-end?company_id=${scope.company_id}`, { fiscal_year: n });
      // Replaying a close is not an error -- two people can press it on the
      // same morning -- and the two cases read differently.
      setNote(
        out.already_closed
          ? t('nx.per.yearAlreadyClosed', { year: String(out.fiscal_year) })
          : t('nx.per.yearClosed', {
              year: String(out.fiscal_year),
              revenue: money(out.revenue_closed),
              expenses: money(out.expenses_closed),
              profit: money(out.profit_to_retained_earnings),
            }),
      );
      setClosingYear(false);
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Period>[] = [
    {
      key: 'period',
      header: t('nx.per.colPeriod'),
      primary: true,
      width: 'w-52',
      cell: (p) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">
            {p.starts_on.slice(0, 7)}
            {current?.id === p.id ? (
              <span className="ms-2 align-middle">
                <Badge tone="info">{t('nx.per.thisMonth')}</Badge>
              </span>
            ) : null}
          </span>
          <span className="num text-caption text-muted">
            {p.starts_on} — {p.ends_on}
          </span>
        </span>
      ),
    },
    {
      key: 'entries',
      header: t('nx.per.colEntries'),
      numeric: true,
      width: 'w-32',
      // Closing an empty month is hygiene; closing one with four hundred
      // entries is a decision. They should not look alike.
      cell: (p) => <span className="num">{p.entries}</span>,
    },
    {
      key: 'state',
      header: t('nx.per.colState'),
      width: 'w-32',
      cell: (p) => (
        <Badge tone={STATE_TONE[p.state] ?? 'neutral'}>
          {t(STATE_LABEL[p.state] ?? 'nx.per.open')}
        </Badge>
      ),
    },
    {
      key: 'actions',
      header: t('nx.per.colActions'),
      width: 'w-72',
      cell: (p) => {
        const block = closeBlock(periods, p);
        return (
          <span className="flex flex-col gap-2">
            <span className="flex flex-wrap gap-2">
              {mayClose && block === 'none' ? (
                <Button size="sm" disabled={busy} onClick={() => void close(p)}>
                  {t('nx.per.close')}
                </Button>
              ) : null}
              {mayClose && block === 'earlier_open' ? (
                // The reason, not a disabled button. "Close February first"
                // tells somebody what to do; a greyed button does not.
                <span className="self-center text-caption text-muted">
                  {t('nx.per.closeEarlierFirst')}
                </span>
              ) : null}
              {mayReopen && p.state !== 'open' ? (
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy}
                  onClick={() => {
                    setReopening(reopening === p.id ? null : p.id);
                    setReason('');
                  }}
                >
                  {t('nx.per.reopen')}
                </Button>
              ) : null}
            </span>

            {reopening === p.id ? (
              <span className="flex flex-col gap-2">
                <Field
                  name="reason"
                  label={t('nx.per.reopenReason')}
                  hint={t('nx.per.reopenReasonHint')}
                  required
                >
                  <Input
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                  />
                </Field>
                <span className="flex gap-2">
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={busy || reason.trim() === ''}
                    onClick={() => void reopen(p)}
                  >
                    {t('nx.per.confirmReopen')}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setReopening(null)}>
                    {t('nx.per.cancel')}
                  </Button>
                </span>
              </span>
            ) : null}
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.per.title')} description={t('nx.per.subtitle')} />

      <FormError message={actionError} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={4} /> : null}

      {periods.length > 0 ? (
        <div className="mb-5 grid gap-4 sm:grid-cols-3">
          <Panel>
            <Figure
              label={t('nx.per.openCount')}
              value={String(open)}
              caption={t('nx.per.openHint')}
            />
          </Panel>
          <Panel>
            <Figure
              label={t('nx.per.thisMonthIs')}
              value={
                current
                  ? t(STATE_LABEL[current.state] ?? 'nx.per.open')
                  : t('nx.per.notOnCalendar')
              }
              tone={current?.state === 'open' ? 'positive' : undefined}
            />
          </Panel>
          <Panel>
            <Figure
              label={t('nx.per.yearsOnCalendar')}
              value={String(years.length)}
            />
          </Panel>
        </div>
      ) : null}

      {mayClose ? (
        <Panel
          className="mb-5"
          title={t('nx.per.openYearTitle')}
          description={t('nx.per.openYearHint')}
        >
          <div className="flex flex-wrap items-end gap-3">
            <Field name="fiscal_year" label={t('nx.per.year')}>
              <Input
                value={year}
                onChange={(e) => setYear(e.target.value)}
                inputMode="numeric"
                className="num w-32 text-end"
              />
            </Field>
            <Button
              variant="primary"
              disabled={busy || yearExists(years, Number(year))}
              onClick={() => void openYear()}
            >
              {t('nx.per.openYear')}
            </Button>
            {yearExists(years, Number(year)) ? (
              <p className="pb-2 text-caption text-muted">
                {t('nx.per.yearAlready', { year })}
              </p>
            ) : null}
          </div>
        </Panel>
      ) : null}

      {mayReopen ? (
        <Panel
          className="mb-5"
          title={t('nx.per.closeYearTitle')}
          description={t('nx.per.closeYearHint')}
        >
          {closingYear ? (
            <>
              <p className="text-body text-fg">
                {t('nx.per.closeYearWarn', { year })}
              </p>
              <div className="mt-4 flex flex-wrap gap-2">
                <Button
                  variant="destructive"
                  disabled={busy}
                  onClick={() => void closeYear()}
                >
                  {t('nx.per.closeYearConfirm', { year })}
                </Button>
                <Button
                  variant="ghost"
                  disabled={busy}
                  onClick={() => setClosingYear(false)}
                >
                  {t('nx.per.closeYearCancel')}
                </Button>
              </div>
            </>
          ) : (
            <Button disabled={busy} onClick={() => setClosingYear(true)}>
              {t('nx.per.closeYear')}
            </Button>
          )}
        </Panel>
      ) : null}

      {!isLoading && !error && periods.length === 0 ? (
        <EmptyState
          icon={CalendarDays}
          title={t('nx.per.emptyTitle')}
          description={t('nx.per.emptyDesc')}
        />
      ) : null}

      {periods.length > 0 ? (
        <DataTable
          caption={t('nx.per.title')}
          columns={columns}
          rows={periods}
          rowKey={(p) => p.id}
        />
      ) : null}
    </>
  );
}

export default function PeriodsPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <PeriodsScreen />
      </Suspense>
    </RequirePermission>
  );
}
