'use client';

// What customers have asked for and not taken away yet.
//
// # An order is not a sale
//
// Something bought over the counter goes through the till and is finished. An
// order is the other case: agreed now, picked later, delivered after that, and
// invoiced at the end. Seven states, and the one it is in says whose turn it is.
//
// # A quotation past its date is not cancelled
//
// `expired` is derived from the validity date rather than stored, because "a
// quote does not become a different row at midnight". So it reads as out of
// date rather than as dead: the price simply stops being one the shop has
// promised.

import { ClipboardList } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { ResourceList } from '@/components/data/resource-list';
import { Select } from '@/components/ui/field';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { ORDER_STATE, ORDER_STATES, type Order } from '@/lib/orders/orders';
import { useUrlState } from '@/lib/url-state';

/** Where the order came from. Four the product names; anything else as sent. */
const CHANNEL: Record<string, Key> = {
  walk_in: 'nx.ord.chWalkIn',
  phone: 'nx.ord.chPhone',
  online: 'nx.ord.chOnline',
  wholesale: 'nx.ord.chWholesale',
};

function OrdersScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const [state, setState] = useUrlState('state');

  const columns: Column<Order>[] = [
    {
      key: 'number',
      header: t('nx.ord.colNumber'),
      primary: true,
      width: 'w-56',
      cell: (o) => {
        const shown = ORDER_STATE[o.state];
        return (
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{o.order_no}</span>
            {shown ? <Badge tone={shown.tone}>{t(shown.key)}</Badge> : null}
            {/* Out of date, not dead. */}
            {o.expired ? <Badge tone="caution">{t('nx.ord.expired')}</Badge> : null}
          </span>
        );
      },
    },
    {
      key: 'customer',
      header: t('nx.ord.colCustomer'),
      cell: (o) => o.customer || <span className="text-subtle">{t('nx.ord.walkIn')}</span>,
    },
    {
      key: 'channel',
      header: t('nx.ord.colChannel'),
      secondary: true,
      width: 'w-36',
      cell: (o) => {
        const named = CHANNEL[o.channel];
        return named ? (
          <span className="text-muted">{t(named)}</span>
        ) : (
          <span className="num text-muted">{o.channel}</span>
        );
      },
    },
    {
      key: 'when',
      header: t('nx.ord.colWhen'),
      secondary: true,
      width: 'w-32',
      cell: (o) => (
        <time dateTime={o.created_at} className="num text-muted">
          {o.created_at.slice(0, 10)}
        </time>
      ),
    },
    {
      key: 'total',
      header: t('nx.ord.colTotal'),
      numeric: true,
      width: 'w-36',
      cell: (o) => (
        <span className="num font-medium">
          {formatMoney(o.total, { currency: o.currency || currency, market })}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.ord.title')}
        description={t('nx.ord.subtitle')}
        actions={
          grants.can('order.manage') ? (
            <Button variant="primary" onClick={() => router.push('/orders/new')}>
              {t('nx.ord.raise')}
            </Button>
          ) : null
        }
      />

      <ResourceList<Order>
        path={scope ? '/orders' : null}
        query={{ ...scope, state: state || undefined }}
        columns={columns}
        rowKey={(o) => o.id}
        onOpenRow={(o) => router.push(`/orders/${o.id}`)}
        caption={t('nx.ord.caption')}
        searchPlaceholder={t('nx.ord.search')}
        searchLabel={t('nx.ord.searchLabel')}
        noun={t('nx.ord.noun')}
        filterRow={(o, term) =>
          o.order_no.toLowerCase().includes(term) ||
          (o.customer ?? '').toLowerCase().includes(term)
        }
        filters={
          <Select
            value={state}
            onChange={(e) => setState(e.target.value)}
            aria-label={t('nx.req.filterLabel')}
            className="h-10 w-auto"
          >
            <option value="">{t('nx.ord.filterAll')}</option>
            {ORDER_STATES.map((s) => (
              <option key={s} value={s}>
                {t(ORDER_STATE[s]!.key)}
              </option>
            ))}
          </Select>
        }
        emptyState={
          <EmptyState
            icon={ClipboardList}
            title={t('nx.ord.emptyTitle')}
            description={t('nx.ord.emptyDesc')}
          />
        }
      />
    </>
  );
}

export default function OrdersPage() {
  return (
    <RequirePermission anyOf={['order.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <OrdersScreen />
      </Suspense>
    </RequirePermission>
  );
}
