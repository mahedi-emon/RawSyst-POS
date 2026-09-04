'use client';

// One adjustment voucher.
//
// # What the system believed is on the line
//
// `system_qty` is what the stock said when the document was raised, beside the
// change that was made to it. That pairing is the evidence: an adjustment of -2
// against a system quantity of 74 is a breakage, and the same -2 against a
// system quantity of 2 is a shelf that was already empty. The second is worth
// asking about and the first is not.

import Link from 'next/link';
import { use } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  ADJUSTMENT_KIND,
  ADJUSTMENT_REASONS,
  ADJUSTMENT_STATUS,
  isIncoming,
  type Adjustment,
  type AdjustmentLine,
} from '@/lib/stock/stock';

function AdjustmentScreen({ adjustmentID }: { adjustmentID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<Adjustment>(
    scope ? `/stock/adjustments/${adjustmentID}` : null,
    scope ?? undefined,
  );

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.adj.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.adj.title')} />
        <TableSkeleton columns={4} />
      </>
    );
  }

  const kind = ADJUSTMENT_KIND[data.kind];
  const status = ADJUSTMENT_STATUS[data.status];
  const namedReason = ADJUSTMENT_REASONS.find((r) => r.value === data.reason);

  const columns: Column<AdjustmentLine>[] = [
    {
      key: 'what',
      header: t('nx.npo.colProduct'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>{l.product}</span>
          <span className="num text-caption text-muted">{l.sku}</span>
        </span>
      ),
    },
    {
      key: 'system',
      header: t('nx.adj.colSystem'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      cell: (l) => <span className="num text-muted">{formatQuantity(l.system_qty)}</span>,
    },
    {
      key: 'change',
      header: t('nx.adj.colChange'),
      numeric: true,
      width: 'w-28',
      cell: (l) =>
        // Empty on a line of an open count nobody has reached yet.
        l.delta === '' ? (
          <span className="text-subtle">{t('nx.cnt.notCounted')}</span>
        ) : (
          <span
            className={
              isIncoming(l.delta)
                ? 'num font-medium text-positive-fg'
                : 'num font-medium text-critical-fg'
            }
          >
            {isIncoming(l.delta) ? '+' : ''}
            {formatQuantity(l.delta)}
          </span>
        ),
    },
    {
      key: 'value',
      header: t('nx.adj.colValue'),
      numeric: true,
      width: 'w-32',
      cell: (l) =>
        l.value === '' || isZero(l.value) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num">
            {formatMoney(l.value, { currency: data.currency || currency, market })}
          </span>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{data.adjustment_no}</span>
            {kind ? <Badge tone={kind.tone}>{t(kind.key)}</Badge> : null}
            {status ? <Badge tone={status.tone}>{t(status.key)}</Badge> : null}
          </span>
        }
        description={[
          namedReason ? t(namedReason.key) : data.reason,
          data.location,
          data.note,
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/stock/adjustments"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.adj.back')}
          </Link>
        }
      />

      <Panel flush>
        <DataTable
          caption={t('nx.adj.linesCaption')}
          columns={columns}
          rows={data.lines ?? []}
          rowKey={(l) => l.variant_id}
          className="rounded-none border-0"
        />
        {!isZero(data.value) ? (
          <div className="flex items-baseline justify-between gap-4 border-t-[3px] border-double border-line-strong px-4 py-3">
            <span className="text-body font-semibold">{t('nx.adj.colValue')}</span>
            <span className="num text-body font-semibold">
              {formatMoney(data.value, {
                currency: data.currency || currency,
                market,
              })}
            </span>
          </div>
        ) : null}
      </Panel>

      {data.posted_at ? (
        <p className="mt-4 text-caption text-muted">
          {t('nx.adj.postedBy', {
            who: data.created_by || '—',
            date: data.posted_at.slice(0, 10),
          })}
        </p>
      ) : null}
    </>
  );
}

export default function AdjustmentPage({
  params,
}: {
  params: Promise<{ adjustmentID: string }>;
}) {
  const { adjustmentID } = use(params);
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <AdjustmentScreen adjustmentID={adjustmentID} />
    </RequirePermission>
  );
}
