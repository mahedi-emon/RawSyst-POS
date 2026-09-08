'use client';

// Things brought back to be repaired, and where each one has got to.
//
// # Warranty or paid is decided when the item is booked in, not here
//
// `BookIn` checks the serial's warranty and sets the job's kind from it. A
// counter cannot talk somebody into a warranty repair by picking it from a
// dropdown, and this screen shows the kind rather than offering it — which is
// also why "goodwill" reads as a decision somebody made rather than a state the
// item is in.
//
// # The three costs are separate because they answer different questions
//
// Parts cost is what the components cost the shop; labour is what the work
// cost; charged is what the customer pays. On a warranty job the first two are
// real and the third is zero, and a screen that showed only what was charged
// would make warranty work look free to run.
//
// # Replaced is not repaired
//
// Swapping a unit rather than fixing it changes which serial number the
// customer now owns, so the new serial is asked for when the status becomes
// `replaced` and not otherwise. B15 wants every replacement logged with the old
// and new serial; the old one is already on the job.

import { Wrench } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

interface ServicePart {
  id: string;
  variant_id: string;
  sku?: string;
  qty: string;
  unit_cost: string;
  issued_at: string;
}

interface ServiceOrder {
  id: string;
  job_no: string;
  kind: string;
  status: string;
  customer_id?: string;
  customer?: string;
  serial_id?: string;
  serial_no?: string;
  variant_id?: string;
  product?: string;
  fault_reported: string;
  diagnosis?: string;
  work_done?: string;
  parts_cost: string;
  labour_cost: string;
  charged: string;
  promised_on?: string;
  received_at: string;
  closed_at?: string;
  currency: string;
  parts?: ServicePart[];
}

const STATUSES = [
  'received',
  'inspecting',
  'awaiting_parts',
  'repaired',
  'irreparable',
  'replaced',
  'delivered',
  'cancelled',
] as const;

const STATUS_TONE: Record<string, 'neutral' | 'info' | 'caution' | 'positive' | 'critical'> =
  {
    received: 'neutral',
    inspecting: 'info',
    awaiting_parts: 'caution',
    repaired: 'positive',
    irreparable: 'critical',
    replaced: 'info',
    delivered: 'positive',
    cancelled: 'neutral',
  };

const KIND_TONE: Record<string, 'positive' | 'neutral' | 'info'> = {
  warranty: 'positive',
  paid: 'neutral',
  goodwill: 'info',
};

function ServiceScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('service.manage');

  const { data, isLoading, error, refetch } = useApiList<ServiceOrder>(
    scope ? '/service-jobs' : null,
    { company_id: scope?.company_id },
  );

  const [editing, setEditing] = useState<ServiceOrder | null>(null);
  const [status, setStatus] = useState('');
  const [diagnosis, setDiagnosis] = useState('');
  const [workDone, setWorkDone] = useState('');
  const [labour, setLabour] = useState('');
  const [charged, setCharged] = useState('');
  const [replacement, setReplacement] = useState('');
  const [busy, setBusy] = useState(false);
  const [partSearch, setPartSearch] = useState('');
  const [partVariant, setPartVariant] = useState('');
  const [partLocation, setPartLocation] = useState('');
  const [partQty, setPartQty] = useState('1');
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const rows = data?.data ?? [];

  function begin(job: ServiceOrder) {
    setEditing(job);
    setStatus(job.status);
    setDiagnosis(job.diagnosis ?? '');
    setWorkDone(job.work_done ?? '');
    setLabour(job.labour_cost);
    setCharged(job.charged);
    setReplacement('');
    setActionError(null);
    setFieldErrors(null);
  }

  // Fitting a part. The catalogue and the stock locations are only fetched
  // while a job is open for editing, because most of the time nobody is
  // fitting anything and two more lists on every visit is two more queries.
  const catalogue = useApiList<{ variant_id: string; sku: string; name?: string }>(
    scope && editing ? '/catalog/snapshot' : null,
    scope ? { ...scope, search: partSearch || undefined, limit: 25 } : undefined,
  );
  const locations = useApiList<{ id: string; name: string }>(
    scope && editing ? '/stock/locations' : null,
    scope ?? undefined,
  );
  const catalogueItems =
    (catalogue.data as unknown as { items?: { variant_id: string; sku: string; name?: string }[] } | undefined)
      ?.items ?? catalogue.data?.data ?? [];

  async function fitPart() {
    if (!scope || !editing || partVariant === '' || partLocation === '') return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    try {
      await api.post(
        `/service-jobs/${editing.id}/parts?company_id=${scope.company_id}`,
        { variant_id: partVariant, warehouse_id: partLocation, qty: partQty },
      );
      setPartVariant('');
      setPartQty('1');
      // The job is re-read rather than patched locally: fitting a part changes
      // the parts cost, and a screen that added the row itself would show a
      // total the server does not agree with.
      // The list is re-read rather than patched locally: fitting a part
      // changes the parts cost, and a screen that added the row itself would
      // show a total the server does not agree with. `live` below reads the
      // fitted parts out of the refreshed list rather than out of the snapshot
      // this editor was opened with.
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    if (!scope || !editing) return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    try {
      await api.post(`/service-jobs/${editing.id}?company_id=${scope.company_id}`, {
        status,
        diagnosis,
        work_done: workDone,
        labour_cost: labour,
        charged,
        replacement_serial: replacement,
      });
      setEditing(null);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  // The job as the server last described it, rather than as it was when the
  // editor opened. Only the parts list is read from here; the form fields are
  // deliberately the snapshot, so a refresh does not overwrite what somebody
  // is halfway through typing.
  const live = editing ? (rows.find((j) => j.id === editing.id) ?? editing) : null;

  const columns: Column<ServiceOrder>[] = [
    {
      key: 'job',
      header: t('nx.svc.job'),
      primary: true,
      cell: (j) => (
        <span className="flex flex-col">
          <span className="flex items-center gap-2">
            <span className="num">{j.job_no}</span>
            <Badge tone={KIND_TONE[j.kind] ?? 'neutral'}>
              {t(`nx.svc.kind.${j.kind}` as 'nx.svc.kind.paid')}
            </Badge>
          </span>
          <span className="text-caption text-muted">
            {j.product ?? '—'}
            {j.serial_no ? ` · ${j.serial_no}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'customer',
      header: t('nx.svc.customer'),
      secondary: true,
      cell: (j) => j.customer ?? <span className="text-muted">—</span>,
    },
    {
      key: 'fault',
      header: t('nx.svc.fault'),
      cell: (j) => <span className="text-muted">{j.fault_reported}</span>,
    },
    {
      key: 'cost',
      header: t('nx.svc.cost'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      // What the job cost the shop, which on a warranty repair is real money
      // even though nothing was charged.
      cell: (j) => (
        <span className="num text-muted">
          {formatMoney(j.parts_cost, {
            currency: j.currency || currency,
            market,
            bare: true,
          })}
          {' + '}
          {formatMoney(j.labour_cost, {
            currency: j.currency || currency,
            market,
            bare: true,
          })}
        </span>
      ),
    },
    {
      key: 'charged',
      header: t('nx.svc.charged'),
      numeric: true,
      width: 'w-36',
      cell: (j) => (
        <span className="num font-medium">
          {formatMoney(j.charged, { currency: j.currency || currency, market })}
        </span>
      ),
    },
    {
      key: 'promised',
      header: t('nx.svc.promised'),
      width: 'w-28',
      cell: (j) =>
        j.promised_on ? (
          <time dateTime={j.promised_on}>{j.promised_on}</time>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'status',
      header: t('nx.svc.status'),
      width: 'w-36',
      cell: (j) => (
        <Badge tone={STATUS_TONE[j.status] ?? 'neutral'}>
          {t(`nx.svc.state.${j.status}` as 'nx.svc.state.received')}
        </Badge>
      ),
    },
  ];

  if (mayManage) {
    columns.push({
      key: 'update',
      header: t('nx.svc.updateHeader'),
      width: 'w-28',
      cell: (j) => (
        <Button size="sm" variant="ghost" onClick={() => begin(j)}>
          {t('nx.svc.update')}
        </Button>
      ),
    });
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader title={t('nx.svc.title')} description={t('nx.svc.subtitle')} />

      <FormError message={actionError} fields={fieldErrors} className="mb-4" />

      {editing ? (
        <Panel title={t('nx.svc.editing', { no: editing.job_no })} className="mb-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field name="status" label={t('nx.svc.status')}>
              <Select value={status} onChange={(e) => setStatus(e.target.value)}>
                {STATUSES.map((s) => (
                  <option key={s} value={s}>
                    {t(`nx.svc.state.${s}` as 'nx.svc.state.received')}
                  </option>
                ))}
              </Select>
            </Field>

            {/* Only when the unit was swapped. Asking for it otherwise invites
                somebody to type the old serial back in. */}
            {status === 'replaced' ? (
              <Field
                name="replacement_serial"
                label={t('nx.svc.replacementSerial')}
                hint={t('nx.svc.replacementHint')}
              >
                <Input
                  dir="ltr"
                  value={replacement}
                  onChange={(e) => setReplacement(e.target.value)}
                  required
                />
              </Field>
            ) : null}

            <Field name="labour_cost" label={t('nx.svc.labour')}>
              <Input
                inputMode="decimal"
                dir="ltr"
                value={labour}
                onChange={(e) => setLabour(e.target.value)}
              />
            </Field>
            <Field
              name="charged"
              label={t('nx.svc.charged')}
              hint={
                editing.kind === 'warranty' ? t('nx.svc.warrantyHint') : undefined
              }
            >
              <Input
                inputMode="decimal"
                dir="ltr"
                value={charged}
                onChange={(e) => setCharged(e.target.value)}
              />
            </Field>
          </div>

          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            <Field name="diagnosis" label={t('nx.svc.diagnosis')}>
              <Textarea
                rows={2}
                value={diagnosis}
                onChange={(e) => setDiagnosis(e.target.value)}
              />
            </Field>
            <Field name="work_done" label={t('nx.svc.workDone')}>
              <Textarea
                rows={2}
                value={workDone}
                onChange={(e) => setWorkDone(e.target.value)}
              />
            </Field>
          </div>

          {/* What has been fitted, and how to fit another. Read before the
              save buttons because it is a different act: a part leaves stock
              the moment it is fitted, and does not wait for Save. */}
          <div className="mt-6 border-t border-line pt-4">
            <h3 className="text-label font-medium text-fg">{t('nx.svc.partsTitle')}</h3>

            {live?.parts && live.parts.length > 0 ? (
              <ul className="mt-2 flex flex-col gap-1">
                {live.parts.map((p) => (
                  <li key={p.id} className="flex flex-wrap gap-x-3 text-body text-muted">
                    <span className="num text-fg">{p.sku ?? p.variant_id.slice(0, 8)}</span>
                    <span className="num">
                      {t('nx.svc.partQty')}: {p.qty}
                    </span>
                    <span className="num">
                      {formatMoney(p.unit_cost, { currency: live.currency, market })}
                    </span>
                    <time dateTime={p.issued_at}>{p.issued_at.slice(0, 10)}</time>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mt-2 text-body text-muted">{t('nx.svc.partsNone')}</p>
            )}

            <p className="mt-3 text-caption text-muted">{t('nx.svc.fitPartHint')}</p>

            <div className="mt-2 grid gap-3 sm:grid-cols-4">
              <Field name="part_search" label={t('nx.svc.fitPartSearch')}>
                <Input
                  value={partSearch}
                  onChange={(e) => setPartSearch(e.target.value)}
                />
              </Field>
              <Field name="variant_id" label={t('nx.svc.partSku')}>
                <Select
                  value={partVariant}
                  onChange={(e) => setPartVariant(e.target.value)}
                >
                  <option value="">{t('nx.svc.fitPartChoose')}</option>
                  {catalogueItems.map((i) => (
                    <option key={i.variant_id} value={i.variant_id}>
                      {i.sku}
                      {i.name ? ` — ${i.name}` : ''}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field name="warehouse_id" label={t('nx.svc.partFrom')}>
                <Select
                  value={partLocation}
                  onChange={(e) => setPartLocation(e.target.value)}
                >
                  <option value="">{t('nx.svc.fitPartChoose')}</option>
                  {(locations.data?.data ?? []).map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field name="qty" label={t('nx.svc.partQty')}>
                <Input
                  dir="ltr"
                  inputMode="decimal"
                  className="num"
                  value={partQty}
                  onChange={(e) => setPartQty(e.target.value)}
                />
              </Field>
            </div>

            <div className="mt-3">
              <Button
                disabled={busy || partVariant === '' || partLocation === ''}
                onClick={() => void fitPart()}
              >
                {t('nx.svc.fitPartConfirm')}
              </Button>
            </div>
          </div>

          <div className="mt-6 flex flex-wrap gap-2">
            <Button
              busy={busy}
              busyLabel={t('nx.svc.saving')}
              disabled={status === 'replaced' && replacement.trim() === ''}
              onClick={() => void save()}
            >
              {t('nx.svc.save')}
            </Button>
            <Button variant="ghost" onClick={() => setEditing(null)}>
              {t('nx.svc.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={7} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Wrench}
          title={t('nx.svc.emptyTitle')}
          description={t('nx.svc.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<ServiceOrder>
          rows={rows}
          columns={columns}
          rowKey={(j) => j.id}
          caption={t('nx.svc.caption')}
        />
      ) : null}
    </>
  );
}

export default function ServicePage() {
  return (
    <RequirePermission anyOf={['service.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ServiceScreen />
      </Suspense>
    </RequirePermission>
  );
}
