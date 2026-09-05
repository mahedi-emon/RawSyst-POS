'use client';

// Hand-written adjustments.
//
// Almost everything in this product posts itself: a sale, a bill, a payment, a
// stock movement. A manual journal is for what none of those covers — an
// accrual, a depreciation charge, a correction to something posted before — and
// the empty state says so, because a register that is empty in a working
// business is the ordinary case rather than a missing setup step.
//
// # The reason is a column, not a detail
//
// C10 requires one on every entry, and the service says why: it is "what
// somebody reading the ledger a year from now has to go on". A register that
// showed numbers and dates and made you open each row to find out why would
// have thrown that away.

import { NotebookPen } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import type { Journal } from '@/lib/accounting/journals';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

function JournalsScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApiList<Journal>(
    scope ? '/accounting/journals' : null,
    scope ?? undefined,
  );

  const rows = data?.data ?? [];

  const columns: Column<Journal>[] = [
    {
      key: 'number',
      header: t('nx.jnl.colNumber'),
      primary: true,
      width: 'w-44',
      cell: (j) => (
        <span className="flex flex-wrap items-center gap-2">
          <span className="num">{j.journal_no}</span>
          {/* Two different facts, and a register that showed neither would
              have somebody reading a correction as an original. */}
          {j.reversed_by ? <Badge tone="caution">{t('nx.jnl.reversed')}</Badge> : null}
          {j.reverses_id ? <Badge tone="info">{t('nx.jnl.isReversal')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'date',
      header: t('nx.jnl.colDate'),
      secondary: true,
      width: 'w-32',
      cell: (j) => (
        <time dateTime={j.entry_date} className="num text-muted">
          {j.entry_date}
        </time>
      ),
    },
    {
      key: 'reason',
      header: t('nx.jnl.colReason'),
      cell: (j) => (
        <span className="flex flex-col gap-0.5">
          <span>{j.reason}</span>
          {j.memo ? (
            <span className="text-caption text-muted">{j.memo}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'who',
      header: t('nx.jnl.colWho'),
      secondary: true,
      width: 'w-40',
      cell: (j) => <span className="text-muted">{j.created_by || '—'}</span>,
    },
    {
      key: 'total',
      header: t('nx.jnl.colTotal'),
      numeric: true,
      width: 'w-36',
      cell: (j) => (
        <span className="num font-medium">
          {formatMoney(j.total, { currency: j.currency || currency, market })}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.jnl.title')}
        description={t('nx.jnl.subtitle')}
        actions={
          grants.can('accounting.create') ? (
            <Button
              variant="primary"
              onClick={() => router.push('/money/journals/new')}
            >
              {t('nx.jnl.write')}
            </Button>
          ) : null
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={NotebookPen}
          title={t('nx.jnl.emptyTitle')}
          description={t('nx.jnl.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.jnl.caption')}
          columns={columns}
          rows={rows}
          rowKey={(j) => j.id}
          onOpenRow={(j) => router.push(`/money/journals/${j.id}`)}
        />
      ) : null}
    </>
  );
}

export default function JournalsPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <JournalsScreen />
      </Suspense>
    </RequirePermission>
  );
}
