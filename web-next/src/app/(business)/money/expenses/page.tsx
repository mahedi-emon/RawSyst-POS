'use client';

// What the business spent, and how much of the tax on it comes back.
//
// # This is a period, not a page of rows
//
// `GET /expenses` answers with totals for a date range and the expenses inside
// it, because "what did we spend last month" is the question and a list alone
// cannot answer it. So the figures sit above the list rather than being
// something the reader adds up.
//
// # Two tax figures, and the second one is a cost
//
// E2.3 restricts input VAT recovery by category: entertainment, some vehicles
// and fuel carry tax that cannot be reclaimed, and it is absorbed into the
// expense "so the VAT return is not overstated". One combined tax figure would
// hide the half that is real money gone.

import { Wallet } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/field';
import { Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { Expense, ExpensePeriod } from '@/lib/money/expenses';
import { useUrlState } from '@/lib/url-state';

function ExpensesScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const [from, setFrom] = useUrlState('from');
  const [to, setTo] = useUrlState('to');

  const { data, isLoading, error, refetch } = useApi<ExpensePeriod>(
    scope ? '/expenses' : null,
    scope ? { ...scope, from: from || undefined, to: to || undefined } : undefined,
  );

  const rows = data?.expenses ?? [];
  const money = (v: string) => formatMoney(v, { currency, market });

  const columns: Column<Expense>[] = [
    {
      key: 'number',
      header: t('nx.exp.colNumber'),
      primary: true,
      width: 'w-40',
      cell: (e) => <span className="num">{e.expense_no}</span>,
    },
    {
      key: 'date',
      header: t('nx.exp.colDate'),
      secondary: true,
      width: 'w-32',
      cell: (e) => (
        <time dateTime={e.expense_date} className="num text-muted">
          {e.expense_date}
        </time>
      ),
    },
    {
      key: 'who',
      header: t('nx.exp.colWho'),
      cell: (e) => e.description || <span className="text-subtle">—</span>,
    },
    {
      key: 'from',
      header: t('nx.exp.colFrom'),
      secondary: true,
      cell: (e) => <span className="text-muted">{e.paid_from || '—'}</span>,
    },
    {
      key: 'total',
      header: t('nx.exp.colTotal'),
      numeric: true,
      width: 'w-36',
      cell: (e) => (
        <span className="num font-medium">
          {formatMoney(e.total_inclusive, { currency: e.currency || currency, market })}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.exp.title')}
        description={t('nx.exp.subtitle')}
        actions={
          grants.can('expense.record') ? (
            <Button variant="primary" onClick={() => router.push('/money/expenses/new')}>
              {t('nx.exp.record')}
            </Button>
          ) : null
        }
      />

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted">{t('nx.exp.period', { from: '', to: '' }).trim()}</span>
          <span className="flex items-center gap-2">
            <Input
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              aria-label={t('nx.exp.colDate')}
              className="w-auto"
            />
            <Input
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              aria-label={t('nx.exp.colDate')}
              className="w-auto"
            />
          </span>
        </label>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {data ? (
        <>
          <div className="mb-6 grid gap-4 sm:grid-cols-3">
            <Panel>
              <Figure label={t('nx.exp.spent')} value={money(data.total)} />
            </Panel>
            <Panel>
              <Figure
                label={t('nx.exp.recoverable')}
                value={money(data.tax_recoverable)}
              />
            </Panel>
            <Panel>
              {/* The half that is real money gone. */}
              <Figure label={t('nx.exp.absorbed')} value={money(data.tax_absorbed)} />
            </Panel>
          </div>

          {!isZero(data.tax_absorbed) ? (
            <p className="mb-6 max-w-prose text-caption text-muted">
              {t('nx.exp.absorbedHint')}
            </p>
          ) : null}

          {data.by_head.length > 0 ? (
            <Panel className="mb-6" title={t('nx.exp.byHead')}>
              <ul className="flex flex-col gap-1">
                {data.by_head.map((h) => (
                  <li key={h.head_id} className="flex justify-between gap-4 text-body">
                    <span className="text-muted">{h.head}</span>
                    <span className="num">{money(h.amount)}</span>
                  </li>
                ))}
              </ul>
            </Panel>
          ) : null}
        </>
      ) : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Wallet}
          title={t('nx.exp.emptyTitle')}
          description={t('nx.exp.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.exp.caption')}
          columns={columns}
          rows={rows}
          rowKey={(e) => e.id}
          onOpenRow={(e) => router.push(`/money/expenses/${e.id}`)}
        />
      ) : null}
    </>
  );
}

export default function ExpensesPage() {
  return (
    <RequirePermission anyOf={['expense.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ExpensesScreen />
      </Suspense>
    </RequirePermission>
  );
}
