'use client';

// Purchase orders, and answering them — F3's "accept or reject with comments".
//
// # The answer is the point of the screen
//
// Everything else here is context for one decision. So the order opens in
// place with its lines and the response form beneath them, rather than on a
// second page: a supplier deciding whether they can fulfil an order is looking
// at the lines while they decide.
//
// # Three answers, not two
//
// `accepted`, `accepted_with_changes`, `rejected`. The middle one is what
// makes the portal usable by a real supplier — "yes, but not until Thursday,
// and only eighty of the hundred" is the commonest answer to a purchase order
// and a two-button screen has nowhere to put it. A rejection must carry a
// comment, which the server enforces and the form asks for before it will
// submit.

import { ClipboardList } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';
import { portalApi } from '@/lib/portal/client';
import { usePortal, usePortalGuard, usePortalList } from '@/lib/portal/hooks';
import { usePortalSession } from '@/lib/portal/session';
import {
  ORDER_RESPONSES,
  type OrderResponse,
  type SupplierOrder,
  type SupplierOrderLine,
} from '@/lib/portal/types';
import { useUrlState } from '@/lib/url-state';

export default function SupplierOrdersPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal/supplier');
  const [openID, setOpenID] = useUrlState('po');

  const list = usePortalList<SupplierOrder>(
    waiting ? null : '/portal/supplier/orders',
  );
  const detail = usePortal<{ order: SupplierOrder }>(
    waiting || openID === '' ? null : `/portal/supplier/orders/${openID}`,
  );

  const rows = list.data?.data ?? [];

  const columns: Column<SupplierOrder>[] = [
    {
      key: 'number',
      header: t('nx.sp.poNumber'),
      primary: true,
      cell: (o) => <span className="num">{o.po_number}</span>,
    },
    {
      key: 'ordered',
      header: t('nx.sp.orderedOn'),
      cell: (o) => <span className="num text-muted">{o.ordered_on ?? '—'}</span>,
    },
    {
      key: 'total',
      header: t('nx.sp.total'),
      numeric: true,
      cell: (o) => (
        <span className="num">
          {o.total} {o.currency}
        </span>
      ),
    },
    {
      key: 'answer',
      header: t('nx.sp.yourAnswer'),
      // The word, not a colour: this column is the difference between an order
      // somebody has to act on and one they have already answered.
      cell: (o) =>
        o.response ? (
          <Badge tone={o.response === 'rejected' ? 'critical' : 'positive'}>
            {responseLabel(t, o.response)}
          </Badge>
        ) : (
          <Badge tone="caution">{t('nx.sp.awaitingYou')}</Badge>
        ),
    },
  ];

  if (waiting) return <Skeleton className="h-48" />;

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-lg font-semibold">{t('nx.sp.poTitle')}</h1>

      {list.error ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : null}
      {list.isLoading && !list.data ? <Skeleton className="h-32" /> : null}

      {!list.isLoading && !list.error && rows.length === 0 ? (
        <EmptyState
          icon={ClipboardList}
          title={t('nx.sp.noPoTitle')}
          description={t('nx.sp.noPoBody')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.sp.poTitle')}
          columns={columns}
          rows={rows}
          rowKey={(o) => o.id}
          isSelected={(o) => o.id === openID}
          onOpenRow={(o) => setOpenID(o.id)}
        />
      ) : null}

      {openID !== '' ? (
        detail.error ? (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        ) : detail.data ? (
          <OrderDetail
            order={detail.data.order}
            onAnswered={() => {
              void detail.refetch();
              void list.refetch();
            }}
            onClose={() => setOpenID('')}
          />
        ) : (
          <Skeleton className="h-48" />
        )
      ) : null}
    </div>
  );
}

function OrderDetail({
  order,
  onAnswered,
  onClose,
}: {
  order: SupplierOrder;
  onAnswered: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const { shop, token } = usePortalSession();

  const [response, setResponse] = useState<OrderResponse>(
    (order.response as OrderResponse | undefined) ?? 'accepted',
  );
  const [comment, setComment] = useState(order.comment ?? '');
  const [promised, setPromised] = useState(order.promised_on ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  // The server refuses a rejection with no comment, and it is right to: a
  // buyer told only "no" has to telephone. Caught here so the message names
  // the field.
  const needsComment = response === 'rejected' && comment.trim() === '';

  const lineColumns: Column<SupplierOrderLine>[] = [
    {
      key: 'description',
      header: t('nx.sp.item'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col">
          <span>{l.description}</span>
          {l.sku ? <span className="num text-caption text-muted">{l.sku}</span> : null}
        </span>
      ),
    },
    {
      key: 'ordered',
      header: t('nx.sp.qtyOrdered'),
      numeric: true,
      cell: (l) => <span className="num">{l.qty_ordered}</span>,
    },
    {
      key: 'received',
      header: t('nx.sp.qtyReceived'),
      numeric: true,
      cell: (l) => <span className="num text-muted">{l.qty_received}</span>,
    },
    {
      key: 'cost',
      header: t('nx.sp.unitCost'),
      numeric: true,
      cell: (l) => <span className="num">{l.unit_cost}</span>,
    },
    {
      key: 'gross',
      header: t('nx.sp.lineTotal'),
      numeric: true,
      cell: (l) => <span className="num">{l.gross_amount}</span>,
    },
  ];

  async function answer() {
    if (!shop) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      await portalApi.post(
        `/portal/supplier/orders/${order.id}/respond`,
        { shop, token },
        { response, comment, promised_on: promised },
      );
      onAnswered();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel
      title={order.po_number}
      description={
        order.expected_on
          ? t('nx.sp.expectedBy', { date: order.expected_on })
          : undefined
      }
      actions={
        <Button variant="ghost" size="sm" onClick={onClose}>
          {t('nx.sp.close')}
        </Button>
      }
    >
      <div className="flex flex-col gap-4">
        {order.lines && order.lines.length > 0 ? (
          <DataTable
            caption={t('nx.sp.poLines')}
            columns={lineColumns}
            rows={order.lines}
            rowKey={(l) => String(l.line_no)}
          />
        ) : (
          <p className="text-caption text-muted">{t('nx.sp.noLines')}</p>
        )}

        <p className="num text-body font-medium">
          {t('nx.sp.orderTotal', { amount: order.total, currency: order.currency })}
        </p>

        {order.responded_at ? (
          <p className="rounded-xs border border-line bg-ground px-3 py-2 text-caption text-muted">
            {t('nx.sp.alreadyAnswered', {
              answer: responseLabel(t, order.response ?? ''),
              when: order.responded_at,
            })}
          </p>
        ) : null}

        <form
          className="flex flex-col gap-4 border-t border-line pt-4"
          onSubmit={(e) => {
            e.preventDefault();
            void answer();
          }}
        >
          <FormError message={error} fields={fieldErrors} />

          <Field name="response" label={t('nx.sp.yourAnswer')} required>
            <Select
              value={response}
              onChange={(e) => setResponse(e.target.value as OrderResponse)}
            >
              {ORDER_RESPONSES.map((r) => (
                <option key={r} value={r}>
                  {responseLabel(t, r)}
                </option>
              ))}
            </Select>
          </Field>

          <Field
            name="promised_on"
            label={t('nx.sp.promisedOn')}
            hint={t('nx.sp.promisedHint')}
            error={fieldErrors.promised_on}
          >
            <Input
              type="date"
              className="num"
              value={promised}
              onChange={(e) => setPromised(e.target.value)}
            />
          </Field>

          <Field
            name="comment"
            label={t('nx.sp.comment')}
            hint={
              response === 'rejected'
                ? t('nx.sp.commentRequired')
                : t('nx.sp.commentHint')
            }
            error={needsComment ? t('nx.sp.commentRequired') : fieldErrors.comment}
            required={response === 'rejected'}
          >
            <Textarea
              rows={3}
              value={comment}
              onChange={(e) => setComment(e.target.value)}
            />
          </Field>

          <Button type="submit" variant="primary" busy={busy} disabled={needsComment}>
            {t('nx.sp.sendAnswer')}
          </Button>
        </form>
      </div>
    </Panel>
  );
}

function responseLabel(t: ReturnType<typeof useT>, response: string): string {
  switch (response) {
    case 'accepted':
      return t('nx.sp.accepted');
    case 'accepted_with_changes':
      return t('nx.sp.acceptedWithChanges');
    case 'rejected':
      return t('nx.sp.rejected');
    default:
      return response;
  }
}
