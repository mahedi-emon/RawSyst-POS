'use client';

// Goods going back to a supplier.
//
// # Two figures on every row, and they are not the same figure
//
// "Claimed" is the price the supplier billed at the tax they charged — their
// document, and what they will argue with. "Stock released" is what the shelf
// was carrying, which the costing method decides and which differs whenever
// freight was added on delivery or a cheaper batch has been bought since.
//
// Showing one and calling it the other is how a stock report stops agreeing
// with a balance sheet, so both are here and the difference has a name.
//
// # Against a bill, not a delivery
//
// Goods refused at the door never entered stock: the receipt records them as
// rejected and there is nothing to send back. This is the other case, a fault
// found after the invoice arrived — and a debit note has no meaning without the
// invoice it corrects.

import { PackageX } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { PurchaseReturn } from '@/lib/purchasing/returns';

function ReturnsScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApiList<PurchaseReturn>(
    scope ? '/purchasing/returns' : null,
    scope ?? undefined,
  );

  const rows = data?.data ?? [];
  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || currency, market });

  const columns: Column<PurchaseReturn>[] = [
    {
      key: 'number',
      header: t('nx.pret.colNumber'),
      primary: true,
      width: 'w-44',
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="num">{r.return_no}</span>
          {r.bill_ref ? (
            <span className="num text-caption text-muted">{r.bill_ref}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'supplier',
      header: t('nx.pret.colSupplier'),
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span>{r.supplier}</span>
          <span className="text-caption text-muted">{r.reason}</span>
        </span>
      ),
    },
    {
      key: 'date',
      header: t('nx.pret.colDate'),
      secondary: true,
      width: 'w-32',
      cell: (r) => (
        <time dateTime={r.returned_on} className="num text-muted">
          {r.returned_on}
        </time>
      ),
    },
    {
      key: 'claimed',
      header: t('nx.pret.colClaimed'),
      numeric: true,
      width: 'w-36',
      cell: (r) => (
        <span className="num font-medium">
          {money(r.total_inclusive, r.currency)}
        </span>
      ),
    },
    {
      key: 'stock',
      header: t('nx.pret.colStock'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      // The other figure. Quieter than the claim, because the claim is what
      // the supplier is being asked for and this is what the books gave up.
      cell: (r) => (
        <span className="num text-muted">{money(r.stock_value, r.currency)}</span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.pret.title')}
        description={t('nx.pret.subtitle')}
        actions={
          grants.can('purchasing.return_goods') ? (
            <Button
              variant="primary"
              onClick={() => router.push('/buying/returns/new')}
            >
              {t('nx.pret.raise')}
            </Button>
          ) : null
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={PackageX}
          title={t('nx.pret.emptyTitle')}
          description={t('nx.pret.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.pret.caption')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
          onOpenRow={(r) => router.push(`/buying/returns/${r.id}`)}
        />
      ) : null}
    </>
  );
}

export default function PurchaseReturnsPage() {
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ReturnsScreen />
      </Suspense>
    </RequirePermission>
  );
}
