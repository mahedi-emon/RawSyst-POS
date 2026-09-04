'use client';

// Stock changed by hand, and why.
//
// # This screen is a control, not a log
//
// The route note says it plainly: "reading what was written off is how a
// manager notices somebody writing too much off; gating it behind the verb that
// DOES the writing would hide it from exactly the person checking." So the list
// is `inventory.view` while raising one is `inventory.adjust_stock`, and the
// value column is the point of the screen rather than a detail on it.
//
// # Write-offs read differently from corrections
//
// Both are adjustments and only one is a loss. The kind carries the tone, so a
// page of corrections and a page of write-offs do not look the same from across
// a desk.

import { PencilRuler } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { PageHeader, Badge } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  ADJUSTMENT_KIND,
  ADJUSTMENT_REASONS,
  ADJUSTMENT_STATUS,
  type Adjustment,
} from '@/lib/stock/stock';

function AdjustmentsScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApiList<Adjustment>(
    scope ? '/stock/adjustments' : null,
    scope ? { ...scope, limit: 100 } : undefined,
  );

  const rows = data?.data ?? [];

  const columns: Column<Adjustment>[] = [
    {
      key: 'number',
      header: t('nx.adj.colNumber'),
      primary: true,
      width: 'w-52',
      cell: (a) => {
        const kind = ADJUSTMENT_KIND[a.kind];
        const status = ADJUSTMENT_STATUS[a.status];
        return (
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{a.adjustment_no}</span>
            {kind ? <Badge tone={kind.tone}>{t(kind.key)}</Badge> : null}
            {/* An open count is not yet a fact about the stock, so it says so
                rather than sitting silently among the posted ones. */}
            {status && a.status !== 'posted' ? (
              <Badge tone={status.tone}>{t(status.key)}</Badge>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'reason',
      header: t('nx.adj.colReason'),
      cell: (a) => {
        const named = ADJUSTMENT_REASONS.find((r) => r.value === a.reason);
        return (
          <span className="flex flex-col gap-0.5">
            <span>{named ? t(named.key) : a.reason}</span>
            {a.note ? (
              <span className="text-caption text-muted">{a.note}</span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'where',
      header: t('nx.adj.colWhere'),
      secondary: true,
      cell: (a) => <span className="text-muted">{a.location}</span>,
    },
    {
      key: 'who',
      header: t('nx.adj.colWho'),
      secondary: true,
      width: 'w-40',
      cell: (a) => <span className="text-muted">{a.created_by || '—'}</span>,
    },
    {
      key: 'when',
      header: t('nx.adj.colWhen'),
      secondary: true,
      width: 'w-32',
      cell: (a) => (
        <time dateTime={a.created_at} className="num text-muted">
          {a.created_at.slice(0, 10)}
        </time>
      ),
    },
    {
      key: 'value',
      header: t('nx.adj.colValue'),
      numeric: true,
      width: 'w-32',
      // The figure the control exists for.
      cell: (a) =>
        isZero(a.value) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num font-medium">
            {formatMoney(a.value, { currency: a.currency || currency, market })}
          </span>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.adj.title')}
        description={t('nx.adj.subtitle')}
        actions={
          // Reading is inventory.view; writing is inventory.adjust_stock, and
          // the button is absent rather than refused without it.
          grants.can('inventory.adjust_stock') ? (
            <div className="flex gap-2">
              <Button
                variant="secondary"
                onClick={() => router.push('/stock/counts/new')}
              >
                {t('nx.cnt.start')}
              </Button>
              <Button
                variant="primary"
                onClick={() => router.push('/stock/adjustments/new')}
              >
                {t('nx.adj.newTitle')}
              </Button>
            </div>
          ) : null
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={6} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={PencilRuler}
          title={t('nx.adj.emptyTitle')}
          description={t('nx.adj.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.adj.caption')}
          columns={columns}
          rows={rows}
          rowKey={(a) => a.id}
          onOpenRow={(a) =>
            router.push(
              a.kind === 'count' && a.status === 'draft'
                ? `/stock/counts/${a.id}`
                : `/stock/adjustments/${a.id}`,
            )
          }
        />
      ) : null}
    </>
  );
}

export default function AdjustmentsPage() {
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <AdjustmentsScreen />
      </Suspense>
    </RequirePermission>
  );
}
