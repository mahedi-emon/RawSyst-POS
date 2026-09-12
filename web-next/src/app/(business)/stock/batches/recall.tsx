'use client';

// Withdrawing a lot from sale, and answering who bought from it.
//
// # Why a recall is not a stock adjustment
//
// An adjustment changes what a shop believes it has. A recall changes what a
// shop is allowed to sell, everywhere at once, and then produces a list of
// people to telephone. `POST /stock/batches/{id}/recall` has been live since
// batch tracking landed and nothing has ever called it, so a shop that learned
// its supplier had a contamination problem had no way to act on it inside
// Biz1core at all.
//
// # The trace is the point, not the confirmation
//
// The route answers with every sale that took stock from the lot — invoice,
// date, quantity, customer and telephone number — plus what never left the
// shelf. That answer is the reason to do this here rather than by hand: it is
// the difference between "we have stopped selling it" and "we know who has it".
//
// # Withdrawn, not deleted
//
// The batch stays, with a reason and a time. A recall that erased the lot would
// erase the evidence of what was sold from it, which is the one thing anybody
// investigating afterwards needs.

import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Panel } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';

/** One sale that took stock from the lot. */
interface RecalledSale {
  invoice_id: string;
  invoice_no?: string;
  issued_at: string;
  qty: string;
  customer_id?: string;
  customer?: string;
  phone?: string;
}

interface RecallTrace {
  sales: RecalledSale[];
  still_on_hand: string;
}

export function RecallPanel({
  batchId,
  batchNo,
  companyId,
  onDone,
  onCancel,
}: {
  batchId: string;
  batchNo: string;
  companyId: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();

  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string> | null>(null);
  const [trace, setTrace] = useState<RecallTrace | null>(null);

  async function recall() {
    setBusy(true);
    setError(null);
    setFields(null);
    try {
      const out = await api.post<RecallTrace>(
        `/stock/batches/${batchId}/recall?company_id=${companyId}`,
        { reason },
      );
      // The list stays on screen after the recall rather than closing. Somebody
      // has telephone calls to make and this is the list.
      setTrace(out);
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<RecalledSale>[] = [
    {
      key: 'invoice',
      header: t('nx.bat.colInvoice'),
      primary: true,
      cell: (r) => <span className="num">{r.invoice_no ?? r.invoice_id.slice(0, 8)}</span>,
    },
    {
      key: 'when',
      header: t('nx.bat.colWhen'),
      secondary: true,
      cell: (r) => <time dateTime={r.issued_at}>{r.issued_at.slice(0, 10)}</time>,
    },
    {
      key: 'qty',
      header: t('nx.bat.colQty'),
      numeric: true,
      width: 'w-28',
      cell: (r) => <span className="num">{r.qty}</span>,
    },
    {
      key: 'customer',
      header: t('nx.bat.colCustomer'),
      // A counter sale has nobody to telephone, and saying so is the useful
      // answer. An em dash would read as missing data.
      cell: (r) =>
        r.customer ?? <span className="text-subtle">{t('nx.bat.walkIn')}</span>,
    },
    {
      key: 'phone',
      header: t('nx.bat.colPhone'),
      cell: (r) =>
        r.phone ? (
          <a href={`tel:${r.phone}`} dir="ltr" className="num text-primary underline">
            {r.phone}
          </a>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
  ];

  if (trace) {
    return (
      <Panel
        title={t('nx.bat.traceTitle', { no: batchNo })}
        description={t('nx.bat.stillOnHand') + ': ' + trace.still_on_hand}
        className="mb-4"
        flush={trace.sales.length > 0}
      >
        {trace.sales.length === 0 ? (
          <p className="text-body text-muted">{t('nx.bat.traceEmpty')}</p>
        ) : (
          <DataTable
            caption={t('nx.bat.traceTitle', { no: batchNo })}
            columns={columns}
            rows={trace.sales}
            rowKey={(r) => r.invoice_id}
            className="rounded-none border-0"
          />
        )}
        <div className="p-4">
          <Button variant="ghost" onClick={onCancel}>
            {t('nx.bat.recallCancel')}
          </Button>
        </div>
      </Panel>
    );
  }

  return (
    <Panel title={t('nx.bat.recallTitle', { no: batchNo })} className="mb-4">
      <p className="mb-4 text-body text-muted">{t('nx.bat.recallBody')}</p>

      <Field
        name="reason"
        label={t('nx.bat.recallReason')}
        hint={t('nx.bat.recallReasonHint')}
        error={fields?.reason}
      >
        <Textarea
          rows={3}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          required
        />
      </Field>

      {error && <FormError message={error} />}

      <div className="mt-4 flex flex-wrap gap-2">
        <Button variant="destructive" onClick={() => void recall()} disabled={busy}>
          {t('nx.bat.recallConfirm')}
        </Button>
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          {t('nx.bat.recallCancel')}
        </Button>
      </div>
    </Panel>
  );
}
