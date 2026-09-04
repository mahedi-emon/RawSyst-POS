'use client';

// What the shop has asked suppliers for.
//
// # A status filter, not a set of tabs
//
// Six states, and the one a buyer wants is rarely "all of them" -- it is
// "what have I sent that has not turned up". Tabs across six would take a row
// of the screen to say what a select says in one control, and the sixth would
// wrap on a phone.
//
// # The list carries no lines
//
// `ListOrders` selects the order columns only. So the total is on the row and
// the items are one click away, which is the right way round: a buyer scanning
// this is looking for an order, not reading one.

import { ClipboardList } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Select } from '@/components/ui/field';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { ORDER_STATUS, ORDER_STATUSES, type Order } from '@/lib/purchasing/orders';
import { useUrlState } from '@/lib/url-state';

function OrdersScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const [status, setStatus] = useUrlState('status');

  const columns: Column<Order>[] = [
    {
      key: 'po_number',
      header: t('nx.po.colNumber'),
      primary: true,
      width: 'w-44',
      cell: (o) => {
        // A status the table does not allow means the backend has changed
        // under this screen; the number is still worth showing.
        const shown = ORDER_STATUS[o.status];
        return (
          <span className="flex items-center gap-2">
            {/* Assigned by the shop and printed on the order; shown as written. */}
            <span className="num">{o.po_number}</span>
            {shown ? <Badge tone={shown.tone}>{t(shown.key)}</Badge> : null}
          </span>
        );
      },
    },
    {
      key: 'supplier',
      header: t('nx.po.colSupplier'),
      cell: (o) => o.supplier,
    },
    {
      key: 'ordered_on',
      header: t('nx.po.colOrdered'),
      secondary: true,
      width: 'w-32',
      cell: (o) => (
        <time dateTime={o.ordered_on} className="num text-muted">
          {o.ordered_on}
        </time>
      ),
    },
    {
      key: 'expected_on',
      header: t('nx.po.colExpected'),
      secondary: true,
      width: 'w-32',
      cell: (o) =>
        o.expected_on ? (
          <time dateTime={o.expected_on} className="num">
            {o.expected_on}
          </time>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'total',
      header: t('nx.po.colTotal'),
      numeric: true,
      width: 'w-40',
      cell: (o) => (
        <span className="num font-medium">
          {/* An order is in the SUPPLIER's currency, which need not be the
              company's -- so the row says which rather than assuming. */}
          {formatMoney(o.total_inclusive, { currency: o.currency || currency, market })}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.po.title')} description={t('nx.po.subtitle')} />

      <ResourceList<Order>
        path={scope ? '/purchasing/orders' : null}
        query={{ ...scope, status: status || undefined }}
        columns={columns}
        rowKey={(o) => o.id}
        onOpenRow={(o) => router.push(`/buying/orders/${o.id}`)}
        caption={t('nx.po.caption')}
        searchPlaceholder={t('nx.po.search')}
        searchLabel={t('nx.po.searchLabel')}
        noun={t('nx.po.noun')}
        // `ListOrders` takes a status and a limit, and no search term -- so the
        // rows it sends are all of them, and the box filters where they are.
        filterRow={(o, term) =>
          o.po_number.toLowerCase().includes(term) ||
          o.supplier.toLowerCase().includes(term)
        }
        filters={
          <Select
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            aria-label={t('nx.po.filterLabel')}
            className="h-10 w-auto"
          >
            <option value="">{t('nx.po.filterAll')}</option>
            {ORDER_STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(ORDER_STATUS[s]!.key)}
              </option>
            ))}
          </Select>
        }
        emptyState={
          <EmptyState
            icon={ClipboardList}
            title={t('nx.po.emptyTitle')}
            description={t('nx.po.emptyDesc')}
          />
        }
      />
    </>
  );
}

export default function OrdersPage() {
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <OrdersScreen />
      </Suspense>
    </RequirePermission>
  );
}
