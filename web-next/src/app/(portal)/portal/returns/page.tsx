'use client';

// Asking the shop to take something back — F2.
//
// # This is a request, not a return
//
// Nothing here refunds anything. `POST /portal/returns` records a request that
// goes into the shop's approval queue, and the customer sees its status change
// when somebody decides. The screen says so, because a customer who thought
// they had returned something and had not would find out at the worst moment.
//
// # The invoice is chosen, not typed
//
// The route accepts an invoice id and checks it is one of the caller's own.
// Offering the caller's own invoices in a picker is the same guarantee said
// earlier, and it saves somebody copying a document number off paper.

import { Undo2 } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';
import { portalApi } from '@/lib/portal/client';
import { usePortalGuard, usePortalList } from '@/lib/portal/hooks';
import { usePortalSession } from '@/lib/portal/session';
import type { PortalInvoice, ReturnRequest } from '@/lib/portal/types';

const KINDS = ['return', 'exchange'] as const;

export default function PortalReturnsPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal');
  const { shop, token } = usePortalSession();

  const requests = usePortalList<ReturnRequest>(
    waiting ? null : '/portal/returns',
  );
  const invoices = usePortalList<PortalInvoice>(
    waiting ? null : '/portal/invoices',
  );

  const [asking, setAsking] = useState(false);
  const [invoiceID, setInvoiceID] = useState('');
  const [kind, setKind] = useState<string>('return');
  const [reason, setReason] = useState('');
  const [items, setItems] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const rows = requests.data?.data ?? [];

  async function ask() {
    if (!shop) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      await portalApi.post('/portal/returns', { shop, token }, {
        invoice_id: invoiceID,
        kind,
        reason,
        items,
      });
      setAsking(false);
      setInvoiceID('');
      setReason('');
      setItems('');
      void requests.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<ReturnRequest>[] = [
    {
      key: 'number',
      header: t('nx.sp.requestNo'),
      primary: true,
      cell: (r) => <span className="num">{r.request_no}</span>,
    },
    {
      key: 'kind',
      header: t('nx.sp.kind'),
      cell: (r) => <Badge>{r.kind}</Badge>,
    },
    {
      key: 'about',
      header: t('nx.sp.aboutInvoice'),
      cell: (r) =>
        r.invoice_no ? (
          <span className="num text-muted">{r.invoice_no}</span>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'status',
      header: t('nx.sp.status'),
      cell: (r) => (
        <span className="flex flex-col gap-1">
          <Badge>{r.status}</Badge>
          {r.decision_note ? (
            <span className="text-caption text-muted">{r.decision_note}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'asked',
      header: t('nx.sp.asked'),
      secondary: true,
      cell: (r) => <span className="num text-muted">{r.created_at}</span>,
    },
  ];

  if (waiting) return <Skeleton className="h-48" />;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold">{t('nx.sp.returnsTitle')}</h1>
        <Button variant="primary" size="sm" onClick={() => setAsking(true)}>
          {t('nx.sp.askToReturn')}
        </Button>
      </div>

      <p className="text-caption text-muted">{t('nx.sp.returnsLead')}</p>

      {requests.error ? (
        <ErrorState error={requests.error} onRetry={() => void requests.refetch()} />
      ) : null}
      {requests.isLoading && !requests.data ? <Skeleton className="h-32" /> : null}

      {!requests.isLoading && !requests.error && rows.length === 0 ? (
        <EmptyState
          icon={Undo2}
          title={t('nx.sp.noRequestsTitle')}
          description={t('nx.sp.noRequestsBody')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.sp.returnsTitle')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
        />
      ) : null}

      {asking ? (
        <Panel title={t('nx.sp.askToReturn')} description={t('nx.sp.askLead')}>
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              void ask();
            }}
          >
            <FormError message={error} fields={fieldErrors} />

            <Field
              name="invoice_id"
              label={t('nx.sp.aboutInvoice')}
              hint={t('nx.sp.aboutInvoiceHint')}
              error={fieldErrors.invoice_id}
            >
              <Select
                value={invoiceID}
                onChange={(e) => setInvoiceID(e.target.value)}
              >
                <option value="">{t('nx.sp.noInvoiceChosen')}</option>
                {(invoices.data?.data ?? []).map((i) => (
                  <option key={i.id} value={i.id}>
                    {i.human_number} · {i.issued_at} · {i.total} {i.currency}
                  </option>
                ))}
              </Select>
            </Field>

            <Field name="kind" label={t('nx.sp.kind')}>
              <Select value={kind} onChange={(e) => setKind(e.target.value)}>
                {KINDS.map((k) => (
                  <option key={k} value={k}>
                    {k === 'return' ? t('nx.sp.kindReturn') : t('nx.sp.kindExchange')}
                  </option>
                ))}
              </Select>
            </Field>

            <Field
              name="items"
              label={t('nx.sp.whatItems')}
              hint={t('nx.sp.whatItemsHint')}
              error={fieldErrors.items}
              required
            >
              <Textarea
                rows={2}
                value={items}
                onChange={(e) => setItems(e.target.value)}
              />
            </Field>

            <Field
              name="reason"
              label={t('nx.sp.whyReason')}
              error={fieldErrors.reason}
              required
            >
              <Textarea
                rows={3}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </Field>

            <div className="flex flex-wrap gap-2 border-t border-line pt-4">
              <Button
                type="submit"
                variant="primary"
                busy={busy}
                disabled={items.trim() === '' || reason.trim() === ''}
              >
                {t('nx.sp.sendRequest')}
              </Button>
              <Button variant="ghost" onClick={() => setAsking(false)} disabled={busy}>
                {t('nx.sp.cancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}
    </div>
  );
}
