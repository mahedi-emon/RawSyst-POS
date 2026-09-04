'use client';

// Lots, and when they stop being sellable.
//
// # Soonest to expire first, because that is the order they get used
//
// The route already returns them that way, and it is FEFO: first expired, first
// out. Sorting by product would be sorting by the thing nobody is worried
// about.
//
// # A date that has passed is not the same as one that is close
//
// Both need somebody, and they need different somebodies — one needs the stock
// pulled off the shelf today, the other needs it sold this month. So they read
// differently rather than sharing an "attention" colour.

import { Layers } from 'lucide-react';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { expiryState, type Batch } from '@/lib/stock/expiry';

function BatchesScreen() {
  const t = useT();
  const scope = useCompanyScope();

  const { data, isLoading, error, refetch } = useApiList<Batch>(
    scope ? '/stock/batches' : null,
    scope ? { ...scope, limit: 200 } : undefined,
  );

  const rows = data?.data ?? [];

  const columns: Column<Batch>[] = [
    {
      key: 'batch',
      header: t('nx.bat.colBatch'),
      primary: true,
      width: 'w-48',
      // The number printed on the carton, which is how somebody standing at the
      // shelf will match it. Shown exactly as the supplier wrote it.
      cell: (b) => <span className="num">{b.batch_no}</span>,
    },
    {
      key: 'product',
      header: t('nx.bat.colProduct'),
      cell: (b) => (
        <span className="flex flex-col gap-0.5">
          <span>{b.product ?? '—'}</span>
          {b.sku ? <span className="num text-caption text-muted">{b.sku}</span> : null}
        </span>
      ),
    },
    {
      key: 'made',
      header: t('nx.bat.colMade'),
      secondary: true,
      width: 'w-32',
      cell: (b) =>
        b.manufactured_on ? (
          <time dateTime={b.manufactured_on} className="num text-muted">
            {b.manufactured_on}
          </time>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'expires',
      header: t('nx.bat.colExpires'),
      width: 'w-52',
      cell: (b) => {
        const state = expiryState(b.expires_on);
        if (state === 'none') {
          return <span className="text-subtle">{t('nx.bat.noExpiry')}</span>;
        }
        return (
          <span className="flex flex-wrap items-center gap-2">
            <time dateTime={b.expires_on} className="num">
              {b.expires_on}
            </time>
            {state === 'expired' ? (
              <Badge tone="critical">{t('nx.bat.expired')}</Badge>
            ) : state === 'soon' ? (
              <Badge tone="caution">{t('nx.bat.expiringSoon')}</Badge>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'left',
      header: t('nx.bat.colLeft'),
      numeric: true,
      width: 'w-28',
      cell: (b) => <span className="num font-medium">{formatQuantity(b.qty_remaining)}</span>,
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.bat.title')} description={t('nx.bat.subtitle')} />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={5} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Layers}
          title={t('nx.bat.emptyTitle')}
          description={t('nx.bat.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.bat.caption')}
          columns={columns}
          rows={rows}
          rowKey={(b) => b.id ?? `${b.variant_id}-${b.batch_no}`}
          className="rows-lazy"
        />
      ) : null}
    </>
  );
}

export default function BatchesPage() {
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <BatchesScreen />
      </Suspense>
    </RequirePermission>
  );
}
