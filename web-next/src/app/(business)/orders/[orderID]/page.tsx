'use client';

// One order, and whatever it is waiting for.
//
// # Confirming is not something to press on somebody's behalf
//
// The route says it: "confirming is the customer's decision, and a route that
// could skip it would put 'the customer agreed' in the hands of whoever typed
// the order." The button exists because somebody has to record the agreement,
// and the line above it says what pressing it claims.
//
// # Picking holds the stock
//
// "Holds stock against an order so a second channel cannot sell the same unit."
// That is worth saying on the screen, because it is the difference between
// picking as paperwork and picking as a promise.
//
// # The delivery note has no prices, and cannot
//
// B11 requires it itemised without pricing, "and the type it is built from has
// no price fields at all so no screen can put them back". Nothing here tries.

import { FileText } from 'lucide-react';
import Link from 'next/link';
import { use, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { TableSkeleton } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity, isZero } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  ORDER_STATE,
  canCancel,
  canDeliver,
  canInvoice,
  canPick,
  nextState,
  type Order,
} from '@/lib/orders/orders';

const TENDERS: ReadonlyArray<{ value: string; key: Key }> = [
  { value: 'cash', key: 'nx.ord.mCash' },
  { value: 'visa', key: 'nx.ord.mCard' },
  { value: 'bank_transfer', key: 'nx.ord.mTransfer' },
];

function OrderScreen({ orderID }: { orderID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApi<Order>(
    scope ? `/orders/${orderID}` : null,
    scope ?? undefined,
  );

  const [qty, setQty] = useState<Record<string, string>>({});
  const [reason, setReason] = useState('');
  const [tender, setTender] = useState('cash');
  const [reference, setReference] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());

  // Defaulted to what is outstanding on each line, and editable: picking or
  // delivering less than the paperwork says is ordinary.
  useEffect(() => {
    if (!data?.lines) return;
    setQty((current) => {
      if (Object.keys(current).length > 0) return current;
      const next: Record<string, string> = {};
      for (const l of data.lines ?? []) next[l.id] = l.qty;
      return next;
    });
  }, [data]);

  async function act(what: string, body?: unknown) {
    if (!scope) return;
    setBusy(what);
    setActionError(null);
    setFieldErrors({});
    try {
      await api.post(
        `/orders/${orderID}/${what}?company_id=${scope.company_id}`,
        body ?? {},
      );
      if (what === 'invoice') setDocUUID(crypto.randomUUID());
      setReason('');
      await refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  const lineQuantities = () => ({
    lines: Object.entries(qty)
      .filter(([, v]) => v.trim() !== '' && v.trim() !== '0')
      .map(([line_id, v]) => ({ line_id, qty: v })),
  });

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.ord.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.ord.title')} />
        <TableSkeleton columns={5} />
      </>
    );
  }

  const state = ORDER_STATE[data.state];
  const next = nextState(data.state);
  const money = (v: string) =>
    formatMoney(v, { currency: data.currency || currency, market });
  const mayManage = grants.can('order.manage');

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{data.order_no}</span>
            {state ? <Badge tone={state.tone}>{t(state.key)}</Badge> : null}
            {data.expired ? <Badge tone="caution">{t('nx.ord.expired')}</Badge> : null}
          </span>
        }
        description={[
          data.customer,
          data.deliver_to,
          data.valid_until ? t('nx.rfq.validUntil', { date: data.valid_until }) : '',
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/orders"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.ord.back')}
          </Link>
        }
      />

      <FormError message={actionError} className="mb-4" />

      {data.cancel_reason ? (
        <Panel className="mb-6">
          <p className="text-body text-fg">
            {t('nx.ord.cancelled', { reason: data.cancel_reason })}
          </p>
        </Panel>
      ) : null}

      {data.invoice_no ? (
        <Panel className="mb-6">
          <p className="text-body text-fg">
            {t('nx.ord.invoicedAs', { number: data.invoice_no })}
          </p>
          <Link
            href={`/sales?invoice=${data.invoice_id ?? ''}`}
            className="mt-2 inline-flex h-10 items-center rounded-sm border border-line-strong bg-surface px-3 text-body font-medium hover:border-primary"
          >
            {t('nx.ord.openInvoice')}
          </Link>
        </Panel>
      ) : null}

      <Panel flush>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[42rem] border-collapse text-body">
            <caption className="sr-only">{t('nx.ord.linesCaption')}</caption>
            <thead>
              <tr className="border-b border-line">
                <th scope="col" className="px-4 py-2 text-start text-label text-muted">
                  {t('nx.npo.colProduct')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.ord.colQty')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.ord.colPrice')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.ord.colPicked')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.ord.colDelivered')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.ord.colLine')}
                </th>
              </tr>
            </thead>
            <tbody>
              {(data.lines ?? []).map((l) => (
                <tr key={l.id} className="border-b border-line last:border-0">
                  <th scope="row" className="px-4 py-2 text-start font-normal">
                    <span className="flex flex-col gap-0.5">
                      <span>{l.product}</span>
                      <span className="num text-caption text-muted">{l.sku}</span>
                    </span>
                  </th>
                  <td className="px-4 py-2 text-end">
                    <span className="num">{formatQuantity(l.qty)}</span>
                  </td>
                  <td className="px-4 py-2 text-end">
                    <span className="num text-muted">{money(l.unit_price)}</span>
                  </td>
                  <td className="px-4 py-2 text-end">
                    <span className="num text-muted">{formatQuantity(l.qty_picked)}</span>
                  </td>
                  <td className="px-4 py-2 text-end">
                    <span className="num text-muted">
                      {formatQuantity(l.qty_delivered)}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-end">
                    <span className="num font-medium">{money(l.line_total)}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <dl className="flex flex-col gap-1 border-t border-line px-4 py-3 text-body">
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t('nx.ord.subtotal')}</dt>
            <dd className="num">{money(data.subtotal)}</dd>
          </div>
          {!isZero(data.discount) ? (
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.ord.discount')}</dt>
              <dd className="num">{money(data.discount)}</dd>
            </div>
          ) : null}
          <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
            <dt>{t('nx.ord.total')}</dt>
            <dd className="num">{money(data.total)}</dd>
          </div>
        </dl>
        {/* Said once: an order carries no tax until it is invoiced, and the
            rate is read then rather than now. */}
        <p className="px-4 pb-3 text-caption text-muted">{t('nx.ord.taxLater')}</p>
      </Panel>

      {mayManage ? (
        <div className="mt-6 flex flex-col gap-6">
          {next ? (
            <Panel>
              {data.state === 'quotation' ? (
                <p className="mb-3 max-w-prose text-body text-muted">
                  {t('nx.ord.confirmHint')}
                </p>
              ) : null}
              <Button
                variant="primary"
                busy={busy === 'advance'}
                disabled={busy !== null}
                onClick={() => void act('advance')}
              >
                {t('nx.ord.advance', { state: t(ORDER_STATE[next]!.key) })}
              </Button>
            </Panel>
          ) : null}

          {canPick(data.state) || canDeliver(data.state) ? (
            <Panel title={canDeliver(data.state) ? t('nx.ord.deliver') : t('nx.ord.pick')}>
              <p className="mb-3 max-w-prose text-caption text-muted">
                {canDeliver(data.state)
                  ? t('nx.ord.deliverHint')
                  : t('nx.ord.pickHint')}
              </p>
              <ul className="flex flex-col divide-y divide-line">
                {(data.lines ?? []).map((l) => (
                  <li
                    key={l.id}
                    className="flex flex-wrap items-center justify-between gap-3 py-2"
                  >
                    <span className="min-w-0 truncate text-body text-fg">{l.product}</span>
                    <div className="w-28">
                      <Input
                        value={qty[l.id] ?? ''}
                        onChange={(e) =>
                          setQty((c) => ({ ...c, [l.id]: e.target.value }))
                        }
                        inputMode="decimal"
                        autoComplete="off"
                        aria-label={`${l.product}`}
                        className="text-end"
                      />
                    </div>
                  </li>
                ))}
              </ul>
              <div className="mt-3 flex flex-wrap gap-2">
                {canPick(data.state) ? (
                  <Button
                    variant="secondary"
                    busy={busy === 'pick'}
                    disabled={busy !== null}
                    onClick={() => void act('pick', lineQuantities())}
                  >
                    {t('nx.ord.pick')}
                  </Button>
                ) : null}
                {canDeliver(data.state) ? (
                  <Button
                    variant="primary"
                    busy={busy === 'deliver'}
                    disabled={busy !== null}
                    onClick={() => void act('deliver', lineQuantities())}
                  >
                    {t('nx.ord.deliver')}
                  </Button>
                ) : null}
              </div>
            </Panel>
          ) : null}

          {canInvoice(data.state) && grants.can('sales.create') ? (
            <Panel title={t('nx.ord.invoice')}>
              <p className="mb-3 max-w-prose text-caption text-muted">
                {t('nx.ord.invoiceHint')}
              </p>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label={t('nx.ord.howPaid')}>
                  <Select value={tender} onChange={(e) => setTender(e.target.value)}>
                    {TENDERS.map((m) => (
                      <option key={m.value} value={m.value}>
                        {t(m.key)}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field label={t('nx.ord.reference')}>
                  <Input
                    value={reference}
                    onChange={(e) => setReference(e.target.value)}
                    autoComplete="off"
                  />
                </Field>
              </div>
              <Button
                variant="primary"
                className="mt-3"
                busy={busy === 'invoice'}
                disabled={busy !== null}
                onClick={() =>
                  void act('invoice', {
                    // Client-assigned, so a retry after a lost response does
                    // not bill the order twice.
                    uuid: docUUID,
                    tenders: [
                      { method: tender, amount: data.total, reference },
                    ],
                  })
                }
              >
                {t('nx.ord.invoice')}
              </Button>
            </Panel>
          ) : null}

          {canCancel(data.state) ? (
            <Panel title={t('nx.ord.cancel')}>
              <Field
                label={t('nx.ord.cancelReason')}
                hint={t('nx.ord.cancelReasonHint')}
                error={fieldErrors.reason}
              >
                <Textarea
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  rows={2}
                />
              </Field>
              <Button
                variant="secondary"
                className="mt-3"
                busy={busy === 'cancel'}
                disabled={busy !== null || reason.trim() === ''}
                onClick={() => void act('cancel', { reason })}
              >
                {t('nx.ord.cancel')}
              </Button>
            </Panel>
          ) : null}
        </div>
      ) : null}

      {data.state !== 'quotation' && data.state !== 'cancelled' ? (
        <div className="mt-6">
          {/* Three documents, three jobs: what to fetch, what to check, what
              goes in the box. The route names them picking, packing and
              delivery, and refuses anything else by name. */}
          <p className="mb-2 text-caption text-muted">{t('nx.doc.deliveryHint')}</p>
          <div className="flex flex-wrap gap-2">
            {(['picking', 'packing', 'delivery'] as const).map((kind) => (
              <Link
                key={kind}
                href={`/orders/${orderID}/documents/${kind}`}
                className="inline-flex h-10 items-center gap-2 rounded-sm border border-line-strong bg-surface px-3 text-body font-medium hover:border-primary"
              >
                <FileText aria-hidden="true" />
                {t(kind === 'picking' ? 'nx.doc.picking' : kind === 'packing' ? 'nx.doc.packing' : 'nx.doc.delivery')}
              </Link>
            ))}
          </div>
        </div>
      ) : null}
    </>
  );
}

export default function OrderPage({
  params,
}: {
  params: Promise<{ orderID: string }>;
}) {
  const { orderID } = use(params);
  return (
    <RequirePermission anyOf={['order.view']}>
      <OrderScreen orderID={orderID} />
    </RequirePermission>
  );
}
