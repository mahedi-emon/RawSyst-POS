'use client';

// One finished batch, and what it cost to make.
//
// # The three costs are shown separately before they are added
//
// Components, labour, packaging. A single total would answer "what did it cost"
// and not "why did it cost that", and the second is the question somebody asks
// when the unit cost moves between batches — usually because labour did.
//
// # The components are shown at what they cost, not at what they sell for
//
// They came out of stock at their cost layer, which is the figure that went
// into this batch. Showing a retail price here would be showing a number that
// played no part in the arithmetic.

import Link from 'next/link';
import { use } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { ProductionBatch, ProductionUsage } from '@/lib/stock/production';

function ProductionBatchScreen({ productionID }: { productionID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<ProductionBatch>(
    scope ? `/stock/production/${productionID}` : null,
    scope ?? undefined,
  );

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.prd.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.prd.title')} />
        <TableSkeleton columns={3} />
      </>
    );
  }

  const money = (v: string) =>
    formatMoney(v, { currency: data.currency || currency, market });

  const columns: Column<ProductionUsage>[] = [
    {
      key: 'what',
      header: t('nx.prd.materials'),
      primary: true,
      cell: (u) => u.variant || u.variant_id.slice(0, 8),
    },
    {
      key: 'qty',
      header: t('nx.prd.colQty'),
      numeric: true,
      width: 'w-28',
      cell: (u) => <span className="num">{formatQuantity(u.qty)}</span>,
    },
    {
      key: 'cost',
      header: t('nx.prd.colTotal'),
      numeric: true,
      width: 'w-36',
      cell: (u) => <span className="num">{money(u.cost)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{data.batch_no}</span>
            {data.variant}
          </span>
        }
        description={[
          `${formatQuantity(data.qty_produced)} · ${data.produced_on}`,
          data.note,
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/stock/production"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.prd.back')}
          </Link>
        }
      />

      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title={t('nx.prd.whatWentIn')} flush>
          <DataTable
            caption={t('nx.prd.inputsCaption')}
            columns={columns}
            rows={data.inputs ?? []}
            rowKey={(u) => u.variant_id}
            className="rounded-none border-0"
          />
        </Panel>

        <Panel>
          <dl className="flex flex-col gap-1 text-body">
            {/* Separately, because "why did it cost that" is the question
                somebody asks when the unit cost moves between batches. */}
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.prd.materials')}</dt>
              <dd className="num">{money(data.material_cost)}</dd>
            </div>
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.prd.labour')}</dt>
              <dd className="num">{money(data.labour_cost)}</dd>
            </div>
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.prd.packaging')}</dt>
              <dd className="num">{money(data.packaging_cost)}</dd>
            </div>
            <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
              <dt>{t('nx.prd.total')}</dt>
              <dd className="num">{money(data.total_cost)}</dd>
            </div>
            <div className="mt-2 flex justify-between gap-4 font-medium">
              <dt>{t('nx.prd.perUnit')}</dt>
              <dd className="num">{money(data.unit_cost)}</dd>
            </div>
          </dl>

          {data.paid_from && !isZero(data.labour_cost) ? (
            <p className="mt-4 text-caption text-muted">
              {t('nx.prd.paidFrom', { account: data.paid_from })}
            </p>
          ) : null}
        </Panel>
      </div>
    </>
  );
}

export default function ProductionBatchPage({
  params,
}: {
  params: Promise<{ productionID: string }>;
}) {
  const { productionID } = use(params);
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <ProductionBatchScreen productionID={productionID} />
    </RequirePermission>
  );
}
