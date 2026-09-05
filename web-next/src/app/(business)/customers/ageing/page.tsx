'use client';

// What customers owe, and for how long.
//
// The mirror of `/buying/ageing`, and the same table: what suppliers are owed
// and what customers owe are one report pointed in opposite directions. The
// buckets are shared in `components/money/ageing.tsx`; what differs is the
// first column and the words around it.
//
// # A counter sale is never here
//
// It was paid when it was rung up. Everything on this screen is an invoice on
// account, which is the only kind that can be late — and saying so in the empty
// state stops a shop reading "nobody owes anything" as "nothing was sold".

import { CheckCircle2 } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import {
  AgeingTable,
  type AgeingBuckets,
  type AgeingReport,
} from '@/components/money/ageing';
import { Button } from '@/components/ui/button';
import { PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { TableSkeleton } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';

interface CustomerAgeing extends AgeingBuckets {
  customer_id: string;
  customer: string;
}

function ReceivablesScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApi<AgeingReport<CustomerAgeing>>(
    scope ? '/receivables/ageing' : null,
    scope ?? undefined,
  );

  const rows = data?.rows ?? [];

  return (
    <>
      <PageHeader
        title={t('nx.rage.title')}
        description={t('nx.rage.subtitle')}
        actions={
          <span className="flex flex-wrap items-center gap-3">
            {data ? (
              <span className="text-label text-muted">
                {t('nx.age.asOf', { date: data.as_of })}
              </span>
            ) : null}
            {grants.can('sales.receive_payment') ? (
              <Button
                variant="primary"
                onClick={() => router.push('/money/receipts')}
              >
                {t('nx.rage.takePayment')}
              </Button>
            ) : null}
          </span>
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      {isLoading && rows.length === 0 ? <TableSkeleton columns={7} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={CheckCircle2}
          title={t('nx.rage.emptyTitle')}
          description={t('nx.rage.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 && data ? (
        <>
          <p className="mb-3 text-caption text-muted">{t('nx.rage.overdueHint')}</p>
          <AgeingTable
            report={data}
            market={market}
            rowKey={(r) => r.customer_id}
            caption={t('nx.rage.caption')}
            totalLabel={t('nx.rage.everything')}
            party={{
              key: 'customer',
              header: t('nx.rage.colCustomer'),
              primary: true,
              cell: (r) => r.customer,
            }}
          />
          <p className="mt-4 max-w-prose text-caption text-muted">
            {t('nx.rage.dueBasis')}
          </p>
        </>
      ) : null}
    </>
  );
}

export default function ReceivablesPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ReceivablesScreen />
      </Suspense>
    </RequirePermission>
  );
}
