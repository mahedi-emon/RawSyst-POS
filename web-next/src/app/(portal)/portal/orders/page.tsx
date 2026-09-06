'use client';

// Where an order has got to — F2's "order status and delivery tracking".
//
// One list, not two. F2 names them as separate features and a customer does
// not experience them that way: "where is my order" is one question whose
// answer is sometimes a state and sometimes a driver's name. The route already
// carries both on the row, so the screen shows the delivery beneath the order
// rather than making somebody match two tables by number.

import { PackageSearch } from 'lucide-react';

import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useT } from '@/lib/i18n/locale';
import { usePortalGuard, usePortalList } from '@/lib/portal/hooks';
import type { PortalOrder } from '@/lib/portal/types';

export default function PortalOrdersPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal');
  const { data, isLoading, error, refetch } = usePortalList<PortalOrder>(
    waiting ? null : '/portal/orders',
  );

  const rows = data?.data ?? [];

  const columns: Column<PortalOrder>[] = [
    {
      key: 'number',
      header: t('nx.sp.orderNo'),
      primary: true,
      cell: (o) => <span className="num">{o.order_no}</span>,
    },
    {
      key: 'placed',
      header: t('nx.sp.placed'),
      cell: (o) => <span className="num text-muted">{o.placed_at}</span>,
    },
    {
      key: 'state',
      header: t('nx.sp.status'),
      // The state carries a word rather than only a colour, because roughly one
      // man in twelve cannot rely on the colour.
      cell: (o) => (
        <span className="flex flex-col gap-1">
          <Badge>{o.state}</Badge>
          {o.delivery_status ? (
            <span className="text-caption text-muted">
              {o.driver_name
                ? t('nx.sp.deliveryWithDriver', {
                    status: o.delivery_status,
                    driver: o.driver_name,
                  })
                : o.delivery_status}
            </span>
          ) : null}
          {o.delivered_at ? (
            <span className="num text-caption text-muted">
              {t('nx.sp.deliveredOn', { date: o.delivered_at })}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'total',
      header: t('nx.sp.total'),
      numeric: true,
      cell: (o) => (
        <span className="num">
          {o.total} {o.currency}
        </span>
      ),
    },
  ];

  return (
    <Panel title={t('nx.sp.ordersTitle')} flush>
      {error ? (
        <div className="p-4">
          <ErrorState error={error} onRetry={() => void refetch()} />
        </div>
      ) : null}
      {(waiting || isLoading) && !data ? <TableSkeleton columns={4} /> : null}
      {!waiting && !isLoading && !error && rows.length === 0 ? (
        <div className="p-4">
          <EmptyState
            icon={PackageSearch}
            title={t('nx.sp.noOrdersTitle')}
            description={t('nx.sp.noOrdersBody')}
          />
        </div>
      ) : null}
      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.sp.ordersTitle')}
          columns={columns}
          rows={rows}
          rowKey={(o) => o.id}
          className="rounded-none border-0"
        />
      ) : null}
    </Panel>
  );
}
