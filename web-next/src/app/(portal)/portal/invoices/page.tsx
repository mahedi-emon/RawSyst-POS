'use client';

// The customer's own receipts — F2, "view and download past invoices".
//
// The list is the caller's alone: `/portal/invoices` filters on the customer
// id in the session and takes no parameter that could name anybody else.
//
// # No download button, and that is honest
//
// F2 asks for download "with the ZATCA QR intact". The QR is generated for the
// printed receipt at the till, and there is no route that serves a customer a
// rendered invoice document — `GET /orders/{id}/documents/{kind}` is a staff
// route behind `order.view`. Offering a button that fetched a document the
// server will not give this caller would be a dead control, so the screen
// shows the figures it has and says where the paper comes from.

import { ReceiptText } from 'lucide-react';

import { Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useT } from '@/lib/i18n/locale';
import { usePortalGuard, usePortalList } from '@/lib/portal/hooks';
import type { PortalInvoice } from '@/lib/portal/types';

export default function PortalInvoicesPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal');
  const { data, isLoading, error, refetch } = usePortalList<PortalInvoice>(
    waiting ? null : '/portal/invoices',
  );

  const rows = data?.data ?? [];

  const columns: Column<PortalInvoice>[] = [
    {
      key: 'number',
      header: t('nx.sp.invoiceNo'),
      primary: true,
      cell: (i) => <span className="num">{i.human_number}</span>,
    },
    {
      key: 'issued',
      header: t('nx.sp.issued'),
      cell: (i) => <span className="num text-muted">{i.issued_at}</span>,
    },
    {
      key: 'total',
      header: t('nx.sp.total'),
      numeric: true,
      cell: (i) => (
        <span className="num">
          {i.total} {i.currency}
        </span>
      ),
    },
    {
      key: 'outstanding',
      header: t('nx.sp.stillToPay'),
      numeric: true,
      cell: (i) => (
        <span className={i.outstanding === '0.00' ? 'num text-muted' : 'num font-medium'}>
          {i.outstanding} {i.currency}
        </span>
      ),
    },
  ];

  return (
    <Panel title={t('nx.sp.invoicesTitle')} description={t('nx.sp.invoicesLead')} flush>
      {error ? (
        <div className="p-4">
          <ErrorState error={error} onRetry={() => void refetch()} />
        </div>
      ) : null}
      {(waiting || isLoading) && !data ? <TableSkeleton columns={4} /> : null}
      {!waiting && !isLoading && !error && rows.length === 0 ? (
        <div className="p-4">
          <EmptyState
            icon={ReceiptText}
            title={t('nx.sp.noInvoicesTitle')}
            description={t('nx.sp.noInvoicesBody')}
          />
        </div>
      ) : null}
      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.sp.invoicesTitle')}
          columns={columns}
          rows={rows}
          rowKey={(i) => i.id}
          className="rounded-none border-0"
        />
      ) : null}
    </Panel>
  );
}
