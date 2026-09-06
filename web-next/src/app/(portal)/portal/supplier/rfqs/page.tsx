'use client';

// Requests for quotation — F3.
//
// # It lists them, and does not take a quotation
//
// F3 asks for "submit quotations in response to an RFQ, feeding the comparison
// screen". `GET /portal/supplier/rfqs` lists what a supplier has been asked to
// price and whether they have answered; there is no portal route that ACCEPTS
// a quotation. `POST /purchasing/quotes` is a staff route behind
// `purchasing.manage_rfq`, and a supplier holds no staff permission at all.
//
// So this screen shows what it can and says where the rest goes, rather than
// rendering a form whose submit button has nothing to call. Recorded in
// IMPLEMENTATION_PROGRESS.md as the one half of F3 that is genuinely backend
// work rather than a missing screen — a portal quotation route has to decide
// how a supplier's prices reach the comparison without giving a supplier the
// permission that writes them, and inventing that here would be inventing a
// contract.

import { FileText } from 'lucide-react';

import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useT } from '@/lib/i18n/locale';
import { usePortalGuard, usePortalList } from '@/lib/portal/hooks';
import type { SupplierRFQ } from '@/lib/portal/types';

export default function SupplierRFQsPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal/supplier');
  const { data, isLoading, error, refetch } = usePortalList<SupplierRFQ>(
    waiting ? null : '/portal/supplier/rfqs',
  );

  const rows = data?.data ?? [];

  const columns: Column<SupplierRFQ>[] = [
    {
      key: 'number',
      header: t('nx.sp.rfqNo'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col">
          <span className="num">{r.rfq_no}</span>
          {r.note ? (
            <span className="text-caption text-muted">{r.note}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'closes',
      header: t('nx.sp.closesOn'),
      cell: (r) => <span className="num text-muted">{r.closes_on ?? '—'}</span>,
    },
    {
      key: 'status',
      header: t('nx.sp.status'),
      cell: (r) => <Badge>{r.status}</Badge>,
    },
    {
      key: 'quoted',
      header: t('nx.sp.yourQuote'),
      cell: (r) =>
        r.quoted ? (
          <span className="flex flex-col">
            <Badge tone="positive">{t('nx.sp.quoted')}</Badge>
            {r.quote_total ? (
              <span className="num text-caption text-muted">{r.quote_total}</span>
            ) : null}
            {r.quoted_on ? (
              <span className="num text-caption text-muted">{r.quoted_on}</span>
            ) : null}
          </span>
        ) : (
          <Badge tone="caution">{t('nx.sp.notQuoted')}</Badge>
        ),
    },
  ];

  return (
    <div className="flex flex-col gap-4">
      <Panel title={t('nx.sp.rfqsTitle')} description={t('nx.sp.rfqsLead')} flush>
        {error ? (
          <div className="p-4">
            <ErrorState error={error} onRetry={() => void refetch()} />
          </div>
        ) : null}
        {(waiting || isLoading) && !data ? <TableSkeleton columns={4} /> : null}
        {!waiting && !isLoading && !error && rows.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={FileText}
              title={t('nx.sp.noRfqsTitle')}
              description={t('nx.sp.noRfqsBody')}
            />
          </div>
        ) : null}
        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.sp.rfqsTitle')}
            columns={columns}
            rows={rows}
            rowKey={(r) => r.id}
            className="rounded-none border-0"
          />
        ) : null}
      </Panel>

      {/* Said plainly rather than by a button that would do nothing. */}
      <p className="text-caption text-muted">{t('nx.sp.quoteHowTo')}</p>
    </div>
  );
}
