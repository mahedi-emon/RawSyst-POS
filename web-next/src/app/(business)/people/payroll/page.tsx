'use client';

// The payroll runs.
//
// # A draft is a calculation, not a payment
//
// C6 makes preparing and approving separate acts and the backend makes them
// separate permissions: `payroll.run` computes a month and posts nothing,
// `payroll.approve` commits the business to those figures. The screen keeps
// them apart too — preparing a month lands on the run, where the numbers can
// be read before anybody signs them off.
//
// # The month offered is the one just finished
//
// A month is paid after it is worked, so the period somebody opens this to run
// is almost never the one they are standing in. The default is last month, and
// it is a real month picker rather than a fixed value, because a shop catching
// up runs two.
//
// # The same month cannot be run twice
//
// The server refuses a second run for a month that already has one, and it is
// right to: a second run pays everybody again. The refusal is shown as it
// comes; the list is what stops somebody trying.

import { Coins } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, localeTagFor } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { runTone, type PayrollRun } from '@/lib/people/payroll';
import { lastFullMonth, monthLabel } from '@/lib/people/staff';

const STATUS_LABEL: Record<string, Key> = {
  draft: 'nx.prl.draft',
  approved: 'nx.prl.approved',
  paid: 'nx.prl.paid',
  cancelled: 'nx.prl.cancelled',
};

function PayrollScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  // The market's tag, which is how this product already localises money.
  // Every one of them is an `en-*`, deliberately: the month reads in the
  // interface language and the digits stay Latin, as they do in every figure
  // on every other screen.
  const tag = localeTagFor(market);

  const [period, setPeriod] = useState(() => lastFullMonth());
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const { data, isLoading, error: loadError, refetch } = useApiList<PayrollRun>(
    scope ? '/payroll' : null,
    scope ?? undefined,
  );
  const rows = data?.data ?? [];

  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || currency, market });

  async function prepare() {
    if (!scope || !period) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const out = await api.post<PayrollRun>(
        `/payroll?company_id=${scope.company_id}`,
        { period, note: note.trim() },
      );
      router.push(`/people/payroll/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  const columns: Column<PayrollRun>[] = [
    {
      key: 'run',
      header: t('nx.prl.colRun'),
      primary: true,
      width: 'w-44',
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="num">{r.run_no}</span>
          <span className="text-caption text-muted">{monthLabel(r.period, tag)}</span>
        </span>
      ),
    },
    {
      key: 'status',
      header: t('nx.prl.colStatus'),
      width: 'w-32',
      cell: (r) => (
        <Badge tone={runTone(r.status)}>
          {t(STATUS_LABEL[r.status] ?? 'nx.prl.draft')}
        </Badge>
      ),
    },
    {
      key: 'gross',
      header: t('nx.prl.colGross'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (r) => (
        <span className="num text-muted">{money(r.gross_total, r.currency)}</span>
      ),
    },
    {
      key: 'net',
      header: t('nx.prl.colNet'),
      numeric: true,
      width: 'w-36',
      cell: (r) => (
        <span className="num font-medium">{money(r.net_total, r.currency)}</span>
      ),
    },
    {
      key: 'paid',
      header: t('nx.prl.colPayDate'),
      secondary: true,
      width: 'w-32',
      cell: (r) =>
        r.pay_date ? (
          <time dateTime={r.pay_date} className="num text-muted">
            {r.pay_date}
          </time>
        ) : (
          <span className="text-muted">—</span>
        ),
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.prl.title')} description={t('nx.prl.subtitle')} />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      {grants.can('payroll.run') ? (
        <Panel
          className="mb-6"
          title={t('nx.prl.prepareTitle')}
          description={t('nx.prl.prepareHint')}
        >
          <div className="grid gap-4 sm:grid-cols-[12rem_minmax(0,1fr)_auto] sm:items-end">
            <Field name="period" label={t('nx.prl.period')} error={fieldErrors.period}>
              <Input
                type="month"
                value={period}
                onChange={(e) => setPeriod(e.target.value)}
              />
            </Field>
            <Field name="note" label={t('nx.prl.note')} error={fieldErrors.note}>
              <Input value={note} onChange={(e) => setNote(e.target.value)} />
            </Field>
            <Button
              variant="primary"
              onClick={() => void prepare()}
              disabled={busy || !period}
            >
              {busy ? t('nx.prl.preparing') : t('nx.prl.prepare')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {loadError ? (
        <ErrorState error={loadError} onRetry={() => void refetch()} />
      ) : null}
      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {!isLoading && !loadError && rows.length === 0 ? (
        <EmptyState
          icon={Coins}
          title={t('nx.prl.emptyTitle')}
          description={t('nx.prl.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.prl.caption')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
          onOpenRow={(r) => router.push(`/people/payroll/${r.id}`)}
        />
      ) : null}
    </>
  );
}

export default function PayrollPage() {
  return (
    <RequirePermission anyOf={['payroll.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <PayrollScreen />
      </Suspense>
    </RequirePermission>
  );
}
