'use client';

// Moving stock between places.
//
// # Four states, and each one is waiting for a different person
//
// Requested waits for an approver, approved waits for whoever packs it,
// in transit waits for whoever unpacks it. The status is therefore not
// decoration — it is the answer to "whose turn is it" — so it sits beside the
// number rather than in a column of its own at the far right.

import { Truck } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { TRANSFER_STATUS, type Transfer } from '@/lib/stock/stock';

function TransfersScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApiList<Transfer>(
    scope ? '/stock/transfers' : null,
    scope ? { ...scope, limit: 100 } : undefined,
  );

  const rows = data?.data ?? [];

  const columns: Column<Transfer>[] = [
    {
      key: 'number',
      header: t('nx.trf.colNumber'),
      primary: true,
      width: 'w-60',
      cell: (x) => {
        const shown = TRANSFER_STATUS[x.status];
        return (
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{x.transfer_no}</span>
            {shown ? <Badge tone={shown.tone}>{t(shown.key)}</Badge> : null}
          </span>
        );
      },
    },
    { key: 'from', header: t('nx.trf.colFrom'), cell: (x) => x.from },
    { key: 'to', header: t('nx.trf.colTo'), cell: (x) => x.to },
    {
      key: 'who',
      header: t('nx.trf.colWho'),
      secondary: true,
      width: 'w-40',
      cell: (x) => <span className="text-muted">{x.requested_by || '—'}</span>,
    },
    {
      key: 'when',
      header: t('nx.trf.colWhen'),
      secondary: true,
      width: 'w-32',
      cell: (x) => (
        <time dateTime={x.requested_at} className="num text-muted">
          {x.requested_at.slice(0, 10)}
        </time>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.trf.title')}
        description={t('nx.trf.subtitle')}
        actions={
          grants.can('inventory.transfer_stock') ? (
            <Button variant="primary" onClick={() => router.push('/stock/transfers/new')}>
              {t('nx.trf.newTitle')}
            </Button>
          ) : null
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={5} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Truck}
          title={t('nx.trf.emptyTitle')}
          description={t('nx.trf.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.trf.caption')}
          columns={columns}
          rows={rows}
          rowKey={(x) => x.id}
          onOpenRow={(x) => router.push(`/stock/transfers/${x.id}`)}
        />
      ) : null}
    </>
  );
}

export default function TransfersPage() {
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <TransfersScreen />
      </Suspense>
    </RequirePermission>
  );
}
