'use client';

// What this supplier has quoted before.
//
// # B5.1's archive, and why it was unreachable
//
// `GET /purchasing/suppliers/{id}/quotes` is described in the route table as
// "B5.1's archive: what this supplier has quoted before, won or lost, so the
// next negotiation starts from a fact". It was live and reachable from
// nothing, so the next negotiation started from whatever anybody remembered.
//
// # Won and lost are both shown
//
// A supplier who quotes low and never wins is a different supplier from one
// who quotes high and always does. Filtering to the awarded ones would leave a
// buyer with a price history that flatters everybody equally.
//
// # A superseded revision is not a second quote
//
// "A second reply supersedes the first rather than overwriting it." The
// revision is shown beside the number so a run of three rows for one request
// reads as one negotiation rather than three offers.

import { Scale } from 'lucide-react';

import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { formatMoney, type MarketCode } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { Quote } from '@/lib/purchasing/sourcing';

export function SupplierQuoteHistory({
  companyId,
  supplierId,
  supplierName,
  currency,
  market,
}: {
  companyId: string;
  supplierId: string;
  supplierName: string;
  currency: string;
  market: MarketCode;
}) {
  const t = useT();
  const { data, isLoading, error, refetch } = useApi<{ quotes: Quote[] }>(
    `/purchasing/suppliers/${supplierId}/quotes`,
    { company_id: companyId },
  );

  const rows = data?.quotes ?? [];

  const columns: Column<Quote>[] = [
    {
      key: 'quote',
      header: t('nx.sup.qColQuote'),
      primary: true,
      cell: (q) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{q.quote_number || q.id.slice(0, 8)}</span>
          {q.revision > 1 ? (
            <span className="text-caption text-muted">
              {t('nx.sup.qRevision', { n: String(q.revision) })}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'received',
      header: t('nx.sup.qColReceived'),
      width: 'w-32',
      cell: (q) => <time dateTime={q.received_on}>{q.received_on}</time>,
    },
    {
      key: 'terms',
      header: t('nx.sup.qColTerms'),
      secondary: true,
      // The two things that routinely outweigh price, which is exactly why
      // B5.1 refuses to call the cheapest quote the best one.
      cell: (q) => (
        <span className="flex flex-col gap-0.5 text-caption text-muted">
          <span>
            {q.lead_time_days !== undefined
              ? t('nx.sup.qLeadTime', { n: String(q.lead_time_days) })
              : t('nx.sup.qNoLeadTime')}
          </span>
          <span>
            {q.payment_terms_days !== undefined
              ? t('nx.sup.qPaymentTerms', { n: String(q.payment_terms_days) })
              : t('nx.sup.qNoTerms')}
          </span>
        </span>
      ),
    },
    {
      key: 'state',
      header: t('nx.sup.qColState'),
      width: 'w-36',
      cell: (q) => (
        <span className="flex flex-col items-start gap-1">
          <Badge tone={q.status === 'awarded' ? 'positive' : 'neutral'}>
            {q.status === 'awarded' ? t('nx.sup.qWon') : t('nx.sup.qNotWon')}
          </Badge>
          {q.expired ? (
            <span className="text-caption text-muted">{t('nx.sup.qExpired')}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'total',
      header: t('nx.sup.qColTotal'),
      numeric: true,
      width: 'w-36',
      cell: (q) => formatMoney(q.total_inclusive, { currency: q.currency || currency, market }),
    },
  ];

  return (
    <Panel
      className="mt-5"
      title={t('nx.sup.qTitle', { name: supplierName })}
      description={t('nx.sup.qHint')}
    >
      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <Skeleton className="h-28" /> : null}
      {data && rows.length === 0 ? (
        <EmptyState
          icon={Scale}
          title={t('nx.sup.qEmptyTitle')}
          description={t('nx.sup.qEmptyDesc')}
        />
      ) : null}
      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.sup.qTitle', { name: supplierName })}
          columns={columns}
          rows={rows}
          rowKey={(q) => q.id}
        />
      ) : null}
    </Panel>
  );
}
