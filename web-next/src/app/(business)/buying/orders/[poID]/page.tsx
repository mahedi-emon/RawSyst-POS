'use client';

// One purchase order.
//
// # Sending it is the one thing here that cannot be undone
//
// `POST /issue` freezes the order and commits the shop to it: after that `PUT`
// answers 400 — "an issued order is a commitment the supplier can hold you to"
// — and a second issue answers 409. Everything else on this screen is a read.
// So this is the one place in the module with a confirmation step, and it names
// the amount and the supplier rather than asking "are you sure", because the
// number is what somebody should be sure about.
//
// # Four quantities per line, and they answer different questions
//
// Ordered, arrived, still due, invoiced. A buyer chasing a delivery reads the
// third; somebody checking an invoice reads the fourth. Collapsing them into
// "12 of 24" would answer neither.
//
// # How a line is taxed is shown
//
// Since migration 0125 it comes back at all. A zero-rated import beside a
// standard purchase is a question somebody asks about an order from abroad,
// and until that migration the answer was not there to give.

import { AlertTriangle } from 'lucide-react';
import Link from 'next/link';
import { use, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  TAX_TREATMENT,
  ORDER_STATUS,
  canReceive,
  type Order,
  type OrderLine,
} from '@/lib/purchasing/orders';

interface Receipt {
  id: string;
  grn_number: string;
  received_on: string;
  note?: string;
}

function OrderScreen({ poID }: { poID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const {
    data: order,
    isLoading,
    error,
    refetch,
  } = useApi<Order>(scope ? `/purchasing/orders/${poID}` : null, scope ?? undefined);

  const { data: receipts } = useApi<{ data: Receipt[] }>(
    scope ? `/purchasing/orders/${poID}/receipts` : null,
    scope ?? undefined,
  );

  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [issueError, setIssueError] = useState<string | null>(null);

  async function issue() {
    if (!scope) return;
    setBusy(true);
    setIssueError(null);
    try {
      await api.post(
        `/purchasing/orders/${poID}/issue?company_id=${scope.company_id}`,
      );
      setConfirming(false);
      await refetch();
    } catch (e) {
      // A 409 lands here when somebody else issued it first, which is exactly
      // the case a silent failure would hide.
      setIssueError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.po.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !order) {
    return (
      <>
        <PageHeader title={t('nx.po.title')} />
        <TableSkeleton columns={6} />
      </>
    );
  }

  const money = (v: string) =>
    formatMoney(v, { currency: order.currency || currency, market });
  const status = ORDER_STATUS[order.status];
  const lines = order.lines ?? [];

  const columns: Column<OrderLine>[] = [
    {
      key: 'item',
      header: t('nx.po.colItem'),
      primary: true,
      cell: (l) => {
        // Empty on an order raised before migration 0125 stored it, so the
        // line simply does not say -- which is honest, and better than
        // guessing "standard" for a zero-rated import.
        const treatment = TAX_TREATMENT[l.tax_treatment];
        return (
          <span className="flex flex-col gap-0.5">
            <span>{l.description || '—'}</span>
            {treatment ? (
              <span className="text-caption text-muted">{t(treatment)}</span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'ordered',
      header: t('nx.po.colOrderedQty'),
      numeric: true,
      width: 'w-24',
      cell: (l) => <span className="num">{formatQuantity(l.qty_ordered)}</span>,
    },
    {
      key: 'received',
      header: t('nx.po.colArrived'),
      numeric: true,
      width: 'w-24',
      cell: (l) => <span className="num">{formatQuantity(l.qty_received)}</span>,
    },
    {
      key: 'outstanding',
      header: t('nx.po.colStillDue'),
      numeric: true,
      width: 'w-24',
      // The figure a buyer chasing a delivery actually reads, so it carries
      // the weight the other two do not.
      cell: (l) => (
        <span className="num font-medium">{formatQuantity(l.qty_outstanding)}</span>
      ),
    },
    {
      key: 'billed',
      header: t('nx.po.colInvoiced'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      cell: (l) => <span className="num text-muted">{formatQuantity(l.qty_billed)}</span>,
    },
    {
      key: 'unit_cost',
      header: t('nx.po.colUnitCost'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (l) => <span className="num text-muted">{money(l.unit_cost)}</span>,
    },
    {
      key: 'line_total',
      header: t('nx.po.colLineTotal'),
      numeric: true,
      width: 'w-32',
      cell: (l) => <span className="num">{money(l.gross_amount)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{order.po_number}</span>
            {status ? <Badge tone={status.tone}>{t(status.key)}</Badge> : null}
          </span>
        }
        description={[
          order.supplier,
          t('nx.po.raisedOn', { date: order.ordered_on }),
          order.expected_on
            ? t('nx.po.expectedOn', { date: order.expected_on })
            : t('nx.po.noExpected'),
        ].join(' · ')}
        actions={
          <Link
            href="/buying/orders"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.po.backToOrders')}
          </Link>
        }
      />

      <FormError message={issueError} className="mb-4" />

      {order.status === 'draft' ? (
        <Panel className="mb-6">
          <p className="text-body text-muted">{t('nx.po.draftHint')}</p>

          {confirming ? (
            <div className="mt-3 rounded-sm border border-caution/40 bg-caution-subtle p-3">
              <p className="flex items-start gap-2 text-body font-medium text-fg">
                <AlertTriangle
                  className="mt-0.5 size-4 shrink-0 text-caution-fg"
                  aria-hidden="true"
                />
                {t('nx.po.sendConfirmTitle')}
              </p>
              <p className="mt-1 text-body text-muted">
                {t('nx.po.sendConfirmBody', {
                  total: money(order.total_inclusive),
                  supplier: order.supplier,
                })}
              </p>
              <div className="mt-3 flex flex-wrap gap-2">
                <Button variant="primary" busy={busy} onClick={() => void issue()}>
                  {t('nx.po.sendConfirm')}
                </Button>
                <Button
                  variant="ghost"
                  disabled={busy}
                  onClick={() => setConfirming(false)}
                >
                  {t('nx.po.sendCancel')}
                </Button>
              </div>
            </div>
          ) : (
            <Button
              variant="primary"
              className="mt-3"
              onClick={() => setConfirming(true)}
            >
              {t('nx.po.send')}
            </Button>
          )}
        </Panel>
      ) : null}

      {canReceive(order.status) ? (
        <p className="mb-6 text-body text-muted">{t('nx.po.sentAlready')}</p>
      ) : null}

      <Panel title={t('nx.po.lines')} flush>
        <DataTable
          caption={t('nx.po.linesCaption')}
          columns={columns}
          rows={lines}
          rowKey={(l) => l.id}
          className="rounded-none border-0"
        />
        <dl className="flex flex-col gap-1 border-t border-line px-4 py-3 text-body">
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t('nx.po.net')}</dt>
            <dd className="num">{money(order.subtotal_net)}</dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t('nx.po.tax')}</dt>
            <dd className="num">{money(order.tax_total)}</dd>
          </div>
          {/* The double rule under a total is the ledger's own convention, and
              it is what tells the eye which of three figures is the answer. */}
          <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
            <dt>{t('nx.po.total')}</dt>
            <dd className="num">{money(order.total_inclusive)}</dd>
          </div>
        </dl>
      </Panel>

      <div className="mt-6">
        <Panel title={t('nx.po.receipts')} flush>
          {receipts?.data?.length ? (
            <DataTable
              caption={t('nx.po.receiptsCaption')}
              columns={[
                {
                  key: 'grn',
                  header: t('nx.po.colGrn'),
                  primary: true,
                  cell: (r: Receipt) => <span className="num">{r.grn_number}</span>,
                },
                {
                  key: 'on',
                  header: t('nx.po.colReceivedOn'),
                  width: 'w-40',
                  cell: (r: Receipt) => (
                    <time dateTime={r.received_on} className="num text-muted">
                      {r.received_on}
                    </time>
                  ),
                },
                {
                  key: 'note',
                  header: t('nx.po.colNote'),
                  secondary: true,
                  cell: (r: Receipt) => (
                    <span className="text-muted">{r.note || '—'}</span>
                  ),
                },
              ]}
              rows={receipts.data}
              rowKey={(r) => r.id}
              className="rounded-none border-0"
            />
          ) : (
            <p className="px-4 py-6 text-body text-muted">{t('nx.po.noReceipts')}</p>
          )}
        </Panel>
      </div>
    </>
  );
}

export default function OrderPage({
  params,
}: {
  params: Promise<{ poID: string }>;
}) {
  const { poID } = use(params);
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <OrderScreen poID={poID} />
    </RequirePermission>
  );
}
