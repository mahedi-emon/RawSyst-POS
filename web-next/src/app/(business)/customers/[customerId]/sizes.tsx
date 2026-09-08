'use client';

// What a customer takes (blueprint B16).
//
// # Why this is on the customer, and why it matters daily
//
// The Blueprint calls fitting history "fashion-specific, high-value" and
// describes exactly one workflow: staff instantly know the customer's size on
// the next visit. That is only true if it is on the screen somebody opens when
// the customer walks in, which is this one. The three routes have been live
// since B16 landed and nothing called any of them, so the shop assistant asked
// every time.
//
// # One row per garment, not a history
//
// `RecordSize` upserts on the garment and moves `confirmed_on` to today. A
// customer who has gone up a size gets a corrected row rather than two, because
// a screen showing "large (2024), extra large (2026)" makes staff do the
// reading — and staff with a customer in front of them do not.
//
// # Measurements are beside the size, not instead of it
//
// A customer knows they are a large. A tailor knows what large means on them.
// So the size is the column and the numbers sit under it, and the form takes
// them as plain lines rather than a fixed set of fields: a thobe, a suit and a
// pair of shoes are not measured in the same places.

import { useState } from 'react';

import { Can } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Panel } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface CustomerSize {
  id: string;
  garment: string;
  size: string;
  measurements?: Record<string, string>;
  note?: string;
  confirmed_on: string;
  recorded_by?: string;
}

/**
 * Parses `collar = 16` lines into the map the API takes.
 *
 * Returns null for a line that is not a pair, so the form can refuse rather
 * than silently dropping what somebody typed — a measurement quietly lost is
 * worse than one rejected, because nobody finds out until the garment does not
 * fit.
 */
function parseMeasurements(text: string): Record<string, string> | null {
  const out: Record<string, string> = {};
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (line === '') continue;
    const at = line.indexOf('=');
    if (at <= 0) return null;
    const name = line.slice(0, at).trim();
    const value = line.slice(at + 1).trim();
    if (name === '' || value === '') return null;
    out[name] = value;
  }
  return out;
}

function formatMeasurements(m: Record<string, string> | undefined): string {
  if (!m) return '';
  return Object.entries(m)
    .map(([k, v]) => `${k} = ${v}`)
    .join('\n');
}

export function SizesPanel({
  customerId,
  companyId,
}: {
  customerId: string;
  companyId: string;
}) {
  const t = useT();

  const { data, isLoading, error, refetch } = useApiList<CustomerSize>(
    customerId ? `/customers/${customerId}/sizes` : null,
    { company_id: companyId },
  );

  const [adding, setAdding] = useState(false);
  const [garment, setGarment] = useState('');
  const [size, setSize] = useState('');
  const [measurements, setMeasurements] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string> | null>(null);

  const rows = data?.data ?? [];

  function open(existing?: CustomerSize) {
    setGarment(existing?.garment ?? '');
    setSize(existing?.size ?? '');
    setMeasurements(formatMeasurements(existing?.measurements));
    setNote(existing?.note ?? '');
    setActionError(null);
    setFields(null);
    setAdding(true);
  }

  async function save() {
    const parsed = parseMeasurements(measurements);
    if (parsed === null) {
      setActionError(t('nx.cust.sizeBadMeasurement'));
      return;
    }
    setBusy(true);
    setActionError(null);
    setFields(null);
    try {
      await api.put(`/customers/${customerId}/sizes?company_id=${companyId}`, {
        garment,
        size,
        measurements: parsed,
        note,
      });
      setAdding(false);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function forget(sizeId: string) {
    setBusy(true);
    setActionError(null);
    try {
      await api.delete(
        `/customers/${customerId}/sizes/${sizeId}?company_id=${companyId}`,
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<CustomerSize>[] = [
    {
      key: 'garment',
      header: t('nx.cust.colGarment'),
      primary: true,
      cell: (r) => r.garment,
    },
    {
      key: 'size',
      header: t('nx.cust.colSize'),
      width: 'w-24',
      // Latin-to-the-left even in Arabic: a size is a code, like a SKU, and a
      // mirrored "34" is a different number.
      cell: (r) => (
        <span dir="ltr" className="num font-medium">
          {r.size}
        </span>
      ),
    },
    {
      key: 'measurements',
      header: t('nx.cust.colMeasurements'),
      secondary: true,
      cell: (r) => {
        const entries = Object.entries(r.measurements ?? {});
        if (entries.length === 0) return <span className="text-subtle">—</span>;
        return (
          <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-muted">
            {entries.map(([k, v]) => (
              <span key={k}>
                {k} <span dir="ltr" className="num">{v}</span>
              </span>
            ))}
          </span>
        );
      },
    },
    {
      key: 'confirmed',
      header: t('nx.cust.colConfirmed'),
      secondary: true,
      width: 'w-32',
      // The date is what makes the size worth trusting: a collar confirmed
      // four years ago is a guess.
      cell: (r) => (
        <time dateTime={r.confirmed_on} className="num text-muted">
          {r.confirmed_on}
        </time>
      ),
    },
    {
      key: 'by',
      header: t('nx.cust.colRecordedBy'),
      secondary: true,
      width: 'w-32',
      cell: (r) => r.recorded_by ?? <span className="text-subtle">—</span>,
    },
    {
      key: 'actions',
      header: '',
      width: 'w-32',
      cell: (r) => (
        <Can permission="customers.manage">
          <span className="flex justify-end gap-1">
            <Button size="sm" variant="ghost" onClick={() => open(r)}>
              {t('nx.cust.sizeValue')}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => void forget(r.id)}
            >
              {t('nx.cust.sizeForget')}
            </Button>
          </span>
        </Can>
      ),
    },
  ];

  return (
    <Panel
      title={t('nx.cust.sizesTitle')}
      description={t('nx.cust.sizesHint')}
      flush={rows.length > 0 && !adding}
      actions={
        !adding ? (
          <Can permission="customers.manage">
            <Button size="sm" onClick={() => open()}>
              {t('nx.cust.addSize')}
            </Button>
          </Can>
        ) : null
      }
    >
      {error ? <FormError message={messageFor(error, t)} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={6} rows={3} /> : null}

      {adding ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            name="garment"
            label={t('nx.cust.sizeGarment')}
            hint={t('nx.cust.sizeGarmentHint')}
            error={fields?.garment}
          >
            <Input
              value={garment}
              onChange={(e) => setGarment(e.target.value)}
              required
            />
          </Field>
          <Field
            name="size"
            label={t('nx.cust.sizeValue')}
            hint={t('nx.cust.sizeValueHint')}
            error={fields?.size}
          >
            <Input
              dir="ltr"
              className="num"
              value={size}
              onChange={(e) => setSize(e.target.value)}
              required
            />
          </Field>
          <Field
            name="measurements"
            label={t('nx.cust.sizeMeasurements')}
            hint={t('nx.cust.sizeMeasurementsHint')}
          >
            <Textarea
              rows={3}
              dir="ltr"
              value={measurements}
              onChange={(e) => setMeasurements(e.target.value)}
            />
          </Field>
          <Field name="note" label={t('nx.cust.sizeNote')}>
            <Textarea
              rows={3}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </Field>

          <div className="sm:col-span-2">
            {actionError ? <FormError message={actionError} /> : null}
            <div className="mt-2 flex flex-wrap gap-2">
              <Button
                variant="primary"
                disabled={busy || garment.trim() === '' || size.trim() === ''}
                onClick={() => void save()}
              >
                {t('nx.cust.sizeSave')}
              </Button>
              <Button variant="ghost" disabled={busy} onClick={() => setAdding(false)}>
                {t('nx.cust.sizeCancel')}
              </Button>
            </div>
          </div>
        </div>
      ) : null}

      {!adding && !isLoading && rows.length === 0 ? (
        <p className="text-body text-muted">{t('nx.cust.sizesEmpty')}</p>
      ) : null}

      {!adding && rows.length > 0 ? (
        <>
          {actionError ? (
            <div className="p-4">
              <FormError message={actionError} />
            </div>
          ) : null}
          <DataTable
            caption={t('nx.cust.sizesTitle')}
            columns={columns}
            rows={rows}
            rowKey={(r) => r.id}
            className="rounded-none border-0"
          />
        </>
      ) : null}
    </Panel>
  );
}
