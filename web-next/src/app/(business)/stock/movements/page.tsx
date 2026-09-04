'use client';

// Everything that has changed the stock, in the order it happened.
//
// # A ledger, not a balance
//
// `/stock/on-hand` says where you are. This says how you got there, and it is
// the screen somebody opens when the two disagree with the shelf. So it is
// ordered by when rather than by product, and nothing is aggregated: two
// deliveries of the same thing on the same day are two lines, because the
// question being asked is which one was wrong.
//
// # The sign is the whole point
//
// A movement of -2 and a movement of 2 are opposite events, and a column of
// unsigned numbers with a separate "in/out" column makes the reader do the
// arithmetic. The figure carries its own sign and its own colour.

import { ArrowLeftRight } from 'lucide-react';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Select } from '@/components/ui/field';
import { PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity, isZero } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { isIncoming, type Movement, type StockLocation } from '@/lib/stock/stock';
import { useUrlState } from '@/lib/url-state';

/**
 * Why stock moved, in words.
 *
 * The backend names these, and an unknown one is shown as written rather than
 * guessed at — a reason this screen has not heard of is still a fact about the
 * movement, and hiding it would leave a row with no explanation at all.
 */
const REASON: Record<string, Key> = {
  grn: 'nx.mov.rGrn',
  sale: 'nx.mov.rSale',
  return: 'nx.mov.rReturn',
  adjustment: 'nx.mov.rAdjustment',
  wastage: 'nx.mov.rWastage',
  count: 'nx.mov.rCount',
  transfer_out: 'nx.mov.rTransferOut',
  transfer_in: 'nx.mov.rTransferIn',
  production: 'nx.mov.rProduction',
};

function MovementsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const [locationId, setLocationId] = useUrlState('location');

  const locations = useApiList<StockLocation>(
    scope ? '/stock/locations' : null,
    scope ?? undefined,
  );

  const { data, isLoading, error, refetch } = useApiList<Movement>(
    scope ? '/stock/movements' : null,
    scope
      ? { ...scope, warehouse_id: locationId || undefined, limit: 100 }
      : undefined,
  );

  const rows = data?.data ?? [];

  const columns: Column<Movement>[] = [
    {
      key: 'when',
      header: t('nx.mov.colWhen'),
      width: 'w-40',
      secondary: true,
      cell: (m) => (
        <time dateTime={m.occurred_at} className="num text-muted">
          {m.occurred_at.slice(0, 16).replace('T', ' ')}
        </time>
      ),
    },
    {
      key: 'what',
      header: t('nx.mov.colWhat'),
      primary: true,
      cell: (m) => (
        <span className="flex flex-col gap-0.5">
          <span>{m.product}</span>
          <span className="num text-caption text-muted">{m.sku}</span>
        </span>
      ),
    },
    {
      key: 'where',
      header: t('nx.mov.colWhere'),
      secondary: true,
      cell: (m) => <span className="text-muted">{m.location}</span>,
    },
    {
      key: 'why',
      header: t('nx.mov.colWhy'),
      cell: (m) => {
        const named = REASON[m.reason];
        return named ? t(named) : <span className="num">{m.reason}</span>;
      },
    },
    {
      key: 'change',
      header: t('nx.mov.colChange'),
      numeric: true,
      width: 'w-28',
      // Signed and coloured, because -2 and 2 are opposite events and a reader
      // should not have to look at another column to tell them apart.
      cell: (m) => (
        <span
          className={
            isIncoming(m.delta)
              ? 'num font-medium text-positive-fg'
              : 'num font-medium text-critical-fg'
          }
        >
          {isIncoming(m.delta) ? '+' : ''}
          {formatQuantity(m.delta)}
        </span>
      ),
    },
    {
      key: 'value',
      header: t('nx.mov.colValue'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      cell: (m) =>
        isZero(m.value) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num text-muted">
            {formatMoney(m.value, { currency, market })}
          </span>
        ),
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.mov.title')} description={t('nx.mov.subtitle')} />

      <div className="mb-4">
        <Select
          value={locationId}
          onChange={(e) => setLocationId(e.target.value)}
          aria-label={t('nx.mov.colWhere')}
          className="h-10 w-auto"
        >
          <option value="">{t('nx.mov.filterLocation')}</option>
          {(locations.data?.data ?? []).map((l) => (
            <option key={l.id} value={l.id}>
              {l.name}
            </option>
          ))}
        </Select>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={6} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={ArrowLeftRight}
          title={t('nx.mov.emptyTitle')}
          description={t('nx.mov.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.mov.caption')}
          columns={columns}
          rows={rows}
          // A movement has no id of its own -- it is a line in a ledger, not a
          // document -- so the key is what makes it distinct.
          rowKey={(m) => `${m.occurred_at}-${m.sku}-${m.delta}-${m.reason}`}
        />
      ) : null}
    </>
  );
}

export default function MovementsPage() {
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <MovementsScreen />
      </Suspense>
    </RequirePermission>
  );
}
