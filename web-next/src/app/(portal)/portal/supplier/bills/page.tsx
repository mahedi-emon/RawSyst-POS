'use client';

// What the shop owes, and what is late — F3's "payment status and history".
//
// The other half of why a supplier signs in. Overdue is a flag the server
// computed against the due date rather than a comparison made here, so the
// portal and the shop's own ageing report cannot disagree about which invoices
// are late — which is exactly the disagreement that turns a payment query into
// an argument.

import { Banknote } from 'lucide-react';

import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useT } from '@/lib/i18n/locale';
import { usePortalGuard, usePortalList } from '@/lib/portal/hooks';
import type { SupplierBill } from '@/lib/portal/types';

export default function SupplierBillsPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal/supplier');
  const { data, isLoading, error, refetch } = usePortalList<SupplierBill>(
    waiting ? null : '/portal/supplier/bills',
  );

  const rows = data?.data ?? [];

  const columns: Column<SupplierBill>[] = [
    {
      key: 'ref',
      header: t('nx.sp.yourInvoice'),
      primary: true,
      cell: (b) => (
        <span className="num">{b.supplier_ref ?? b.id.slice(0, 8)}</span>
      ),
    },
    {
      key: 'dated',
      header: t('nx.sp.billDate'),
      cell: (b) => <span className="num text-muted">{b.bill_date}</span>,
    },
    {
      key: 'due',
      header: t('nx.sp.dueOn'),
      cell: (b) => (
        <span className="flex items-center gap-2">
          <span className="num text-muted">{b.due_on ?? '—'}</span>
          {/* A word as well as a colour. */}
          {b.overdue ? <Badge tone="critical">{t('nx.sp.late')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'gross',
      header: t('nx.sp.total'),
      numeric: true,
      cell: (b) => (
        <span className="num">
          {b.gross_total} {b.currency}
        </span>
      ),
    },
    {
      key: 'outstanding',
      header: t('nx.sp.stillOwed'),
      numeric: true,
      cell: (b) => (
        <span
          className={
            b.outstanding === '0.00' ? 'num text-muted' : 'num font-medium'
          }
        >
          {b.outstanding} {b.currency}
        </span>
      ),
    },
  ];

  return (
    <Panel title={t('nx.sp.billsTitle')} description={t('nx.sp.billsLead')} flush>
      {error ? (
        <div className="p-4">
          <ErrorState error={error} onRetry={() => void refetch()} />
        </div>
      ) : null}
      {(waiting || isLoading) && !data ? <TableSkeleton columns={5} /> : null}
      {!waiting && !isLoading && !error && rows.length === 0 ? (
        <div className="p-4">
          <EmptyState
            icon={Banknote}
            title={t('nx.sp.noBillsTitle')}
            description={t('nx.sp.noBillsBody')}
          />
        </div>
      ) : null}
      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.sp.billsTitle')}
          columns={columns}
          rows={rows}
          rowKey={(b) => b.id}
          className="rounded-none border-0"
        />
      ) : null}
    </Panel>
  );
}
