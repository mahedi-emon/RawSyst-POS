'use client';

// One debit note.
//
// The two figures side by side, because the whole point of keeping them apart
// is that somebody can see both: the supplier is claimed what they billed, the
// shelf released what the stock was carrying, and the difference is a real gain
// or loss rather than a rounding artefact.

import Link from 'next/link';
import { use } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Figure, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { PurchaseReturn, PurchaseReturnLine } from '@/lib/purchasing/returns';

function ReturnScreen({ returnID }: { returnID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<PurchaseReturn>(
    scope ? `/purchasing/returns/${returnID}` : null,
    scope ?? undefined,
  );

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.pret.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.pret.title')} />
        <TableSkeleton columns={5} />
      </>
    );
  }

  const money = (v: string) =>
    formatMoney(v, { currency: data.currency || currency, market });

  const columns: Column<PurchaseReturnLine>[] = [
    {
      key: 'item',
      header: t('nx.pret.colItem'),
      primary: true,
      cell: (l) => l.description,
    },
    {
      key: 'qty',
      header: t('nx.pret.colReturning'),
      numeric: true,
      width: 'w-24',
      cell: (l) => <span className="num">{l.qty}</span>,
    },
    {
      key: 'price',
      header: t('nx.pret.colPrice'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (l) => <span className="num text-muted">{money(l.unit_cost)}</span>,
    },
    {
      key: 'claimed',
      header: t('nx.pret.colClaimed'),
      numeric: true,
      width: 'w-32',
      cell: (l) => <span className="num font-medium">{money(l.gross_amount)}</span>,
    },
    {
      key: 'stock',
      header: t('nx.pret.stockReleased'),
      numeric: true,
      width: 'w-32',
      // What the shelf gave up for this line. Beside the claim, so the
      // difference is visible per line and not only in a total.
      cell: (l) => <span className="num text-muted">{money(l.stock_value)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={<span className="num">{data.return_no}</span>}
        description={[
          t('nx.pret.claimedFrom', { supplier: data.supplier }),
          data.bill_ref ? t('nx.pret.againstBill', { ref: data.bill_ref }) : null,
          data.returned_on,
          data.warehouse,
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/buying/returns"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.pret.back')}
          </Link>
        }
      />

      <div className="mb-6 grid gap-4 sm:grid-cols-3">
        <Panel>
          <Figure label={t('nx.pret.colClaimed')} value={money(data.total_inclusive)} />
        </Panel>
        <Panel>
          <Figure label={t('nx.pret.stockReleased')} value={money(data.stock_value)} />
        </Panel>
        <Panel>
          <Figure
            label={t('nx.pret.variance')}
            value={money(data.variance)}
            tone={isZero(data.variance) ? undefined : 'critical'}
          />
        </Panel>
      </div>

      {!isZero(data.variance) ? (
        <p className="mb-6 max-w-prose text-caption text-muted">
          {t('nx.pret.varianceHint')}
        </p>
      ) : null}

      <Panel flush>
        <DataTable
          caption={t('nx.pret.linesCaption')}
          columns={columns}
          rows={data.lines ?? []}
          rowKey={(l) => `${l.bill_line_id}-${l.line_no}`}
          className="rounded-none border-0"
        />
        <dl className="flex flex-col gap-1 border-t border-line px-4 py-3 text-body">
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t('nx.pret.net')}</dt>
            <dd className="num">{money(data.subtotal_net)}</dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t('nx.pret.tax')}</dt>
            <dd className="num">{money(data.tax_total)}</dd>
          </div>
          <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
            <dt>{t('nx.pret.total')}</dt>
            <dd className="num">{money(data.total_inclusive)}</dd>
          </div>
        </dl>
      </Panel>

      <p className="mt-4 max-w-prose text-caption text-muted">{data.reason}</p>
    </>
  );
}

export default function PurchaseReturnPage({
  params,
}: {
  params: Promise<{ returnID: string }>;
}) {
  const { returnID } = use(params);
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <ReturnScreen returnID={returnID} />
    </RequirePermission>
  );
}
