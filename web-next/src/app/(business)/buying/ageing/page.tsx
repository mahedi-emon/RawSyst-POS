'use client';

// What the shop owes its suppliers, and for how long.
//
// The table itself is `components/money/ageing.tsx`, shared with the customer
// side: what suppliers are owed and what customers owe are the same report
// pointed in opposite directions, and the buckets, the em dashes and the
// weighting are identical. What differs is the first column and the words.
//
// # Aged from the DUE date, and the screen says so
//
// B6, and the Go comment is worth repeating: a 60-day bill raised 45 days ago
// is not overdue, and ageing it from the bill date would say it was — which
// would have a buyer chasing a supplier who is owed nothing yet. The distinction
// is invisible in a table of five columns, so it is written down once.

import { CheckCircle2 } from 'lucide-react';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { AgeingTable, type AgeingBuckets, type AgeingReport } from '@/components/money/ageing';
import { PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { TableSkeleton } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';

interface SupplierAgeing extends AgeingBuckets {
  supplier_id: string;
  supplier: string;
}

function AgeingScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<AgeingReport<SupplierAgeing>>(
    scope ? '/purchasing/ageing' : null,
    scope ?? undefined,
  );

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
          <AgeingTable
            report={data}
            market={market}
            rowKey={(r) => r.supplier_id}
            caption={t('nx.age.caption')}
            totalLabel={t('nx.age.everything')}
            party={{
              key: 'supplier',
              header: t('nx.age.colSupplier'),
              primary: true,
              cell: (r) => r.supplier,
            }}
          />
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
