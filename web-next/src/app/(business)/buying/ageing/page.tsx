'use client';

// What the shop owes its suppliers, and for how long.
//
// # Aged from the DUE date, and the screen says so
//
// B6, and the Go comment is worth repeating: a 60-day bill raised 45 days ago
// is not overdue, and ageing it from the bill date would say it was — which
// would have a buyer chasing a supplier who is owed nothing yet. The distinction
// is invisible in a table of five columns, so it is written down once.
//
// # Zero is an em dash
//
// A supplier owed nothing in the 61–90 bucket should not draw the eye with a
// "0.00". Five columns of zeros with one figure among them is a table nobody
// can read; five em dashes with one figure among them is a table that answers
// the question from across the room.
//
// # The overdue columns carry weight, the not-due one does not
//
// Everything to the right of "not due yet" is late, and lateness is the reason
// somebody opened this. So the first column is quiet and the rest are not.

import { CheckCircle2 } from 'lucide-react';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PageHeader, Panel } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

interface AgeingRow {
  supplier_id: string;
  supplier: string;
  not_due: string;
  days_0_30: string;
  days_31_60: string;
  days_61_90: string;
  days_90_plus: string;
  total: string;
}

interface Ageing {
  as_of: string;
  rows: AgeingRow[];
  total: string;
  base_currency: string;
}

function AgeingScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<Ageing>(
    scope ? '/purchasing/ageing' : null,
    scope ?? undefined,
  );

  // The report names its own currency, which is the company's base. Reading it
  // off the payload rather than the company context means the figures and the
  // symbol can never disagree.
  const currency = data?.base_currency ?? '';
  const money = (v: string) =>
    isZero(v) ? null : formatMoney(v, { currency, market });

  /** A bucket cell: quiet when empty, and heavier the later it is. */
  function bucket(value: string, late: boolean) {
    const shown = money(value);
    if (!shown) return <span className="text-subtle">—</span>;
    return (
      <span className={late ? 'num font-medium text-caution-fg' : 'num'}>
        {shown}
      </span>
    );
  }

  const columns: Column<AgeingRow>[] = [
    {
      key: 'supplier',
      header: t('nx.age.colSupplier'),
      primary: true,
      cell: (r) => r.supplier,
    },
    {
      key: 'not_due',
      header: t('nx.age.colNotDue'),
      numeric: true,
      secondary: true,
      cell: (r) => bucket(r.not_due, false),
    },
    {
      key: 'd030',
      header: t('nx.age.col030'),
      numeric: true,
      cell: (r) => bucket(r.days_0_30, true),
    },
    {
      key: 'd3160',
      header: t('nx.age.col3160'),
      numeric: true,
      cell: (r) => bucket(r.days_31_60, true),
    },
    {
      key: 'd6190',
      header: t('nx.age.col6190'),
      numeric: true,
      cell: (r) => bucket(r.days_61_90, true),
    },
    {
      key: 'd90',
      header: t('nx.age.col90'),
      numeric: true,
      // The one that means somebody has stopped answering the phone.
      cell: (r) => {
        const shown = money(r.days_90_plus);
        return shown ? (
          <span className="num font-semibold text-critical-fg">{shown}</span>
        ) : (
          <span className="text-subtle">—</span>
        );
      },
    },
    {
      key: 'total',
      header: t('nx.age.colTotal'),
      numeric: true,
      cell: (r) => <span className="num font-medium">{money(r.total) ?? '—'}</span>,
    },
  ];

  const rows = data?.rows ?? [];

  return (
    <>
      <PageHeader
        title={t('nx.age.title')}
        description={t('nx.age.subtitle')}
        actions={
          data ? (
            <span className="text-label text-muted">
              {t('nx.age.asOf', { date: data.as_of })}
            </span>
          ) : null
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      {isLoading && rows.length === 0 ? <TableSkeleton columns={7} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={CheckCircle2}
          title={t('nx.age.emptyTitle')}
          description={t('nx.age.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 && data ? (
        <>
          <p className="mb-3 text-caption text-muted">{t('nx.age.overdueHint')}</p>
          <Panel flush>
            <DataTable
              caption={t('nx.age.caption')}
              columns={columns}
              rows={rows}
              rowKey={(r) => r.supplier_id}
              className="rounded-none border-0"
            />
            <div className="flex items-baseline justify-between gap-4 border-t-[3px] border-double border-line-strong px-4 py-3">
              <span className="text-body font-semibold">
                {t('nx.age.everything')}
              </span>
              <span className="num text-body font-semibold">
                {formatMoney(data.total, { currency, market })}
              </span>
            </div>
          </Panel>
          {/* Written once, under the table, because the difference between
              ageing from the due date and from the bill date is invisible in
              five columns and changes who gets chased. */}
          <p className="mt-4 max-w-prose text-caption text-muted">
            {t('nx.age.dueBasis')}
          </p>
        </>
      ) : null}
    </>
  );
}

export default function AgeingPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <AgeingScreen />
      </Suspense>
    </RequirePermission>
  );
}
