'use client';

// Things the shop makes rather than buys.
//
// # The unit cost is the answer, and it is computed rather than typed
//
// A finished batch costs what its components cost coming out of stock, plus
// labour and packaging. Nobody types the unit cost: the route works it out, and
// that is the whole reason to record a batch at all — a shirt made in-house has
// no supplier invoice to read a cost off.
//
// # Cost to make, and cost each, are both on the row
//
// The total is what left the bank and the shelves; the unit cost is what every
// sale of it will be measured against. A buyer reads the second and an
// accountant reads the first.

import { Factory } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { ProductionBatch } from '@/lib/stock/production';

function ProductionScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const { data, isLoading, error, refetch } = useApiList<ProductionBatch>(
    scope ? '/stock/production' : null,
    scope ? { ...scope, limit: 100 } : undefined,
  );

  const rows = data?.data ?? [];

  const columns: Column<ProductionBatch>[] = [
    {
      key: 'batch',
      header: t('nx.prd.colBatch'),
      width: 'w-44',
      cell: (b) => <span className="num">{b.batch_no}</span>,
    },
    {
      key: 'what',
      header: t('nx.prd.colWhat'),
      primary: true,
      cell: (b) => b.variant || '—',
    },
    {
      key: 'qty',
      header: t('nx.prd.colQty'),
      numeric: true,
      width: 'w-28',
      cell: (b) => <span className="num">{formatQuantity(b.qty_produced)}</span>,
    },
    {
      key: 'when',
      header: t('nx.prd.colWhen'),
      secondary: true,
      width: 'w-32',
      cell: (b) => (
        <time dateTime={b.produced_on} className="num text-muted">
          {b.produced_on}
        </time>
      ),
    },
    {
      key: 'total',
      header: t('nx.prd.colTotal'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (b) => (
        <span className="num text-muted">
          {formatMoney(b.total_cost, { currency: b.currency || currency, market })}
        </span>
      ),
    },
    {
      key: 'unit',
      header: t('nx.prd.colUnit'),
      numeric: true,
      width: 'w-32',
      // What every sale of it will be measured against.
      cell: (b) => (
        <span className="num font-medium">
          {formatMoney(b.unit_cost, { currency: b.currency || currency, market })}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.prd.title')} description={t('nx.prd.subtitle')} />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={6} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Factory}
          title={t('nx.prd.emptyTitle')}
          description={t('nx.prd.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.prd.caption')}
          columns={columns}
          rows={rows}
          rowKey={(b) => b.id}
          onOpenRow={(b) => router.push(`/stock/production/${b.id}`)}
        />
      ) : null}
    </>
  );
}

export default function ProductionPage() {
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ProductionScreen />
      </Suspense>
    </RequirePermission>
  );
}
