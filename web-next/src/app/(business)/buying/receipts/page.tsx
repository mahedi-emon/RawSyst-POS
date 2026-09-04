'use client';

// Booking in a delivery.
//
// This is the only screen in the product that increases stock through a
// purchase. B5 is explicit that an order alone never does, and the route note
// says the same -- so the counting happens here, against what was ordered, and
// nowhere else.
//
// # The uuid is made in the browser, once per delivery
//
// `POST /purchasing/receipts` takes a client-assigned `uuid` and answers
// `already_received: true` with the ORIGINAL receipt if it has seen it before.
// A clerk on a bad connection who presses the button twice books one delivery,
// not two -- and the screen says so rather than pretending it was the first
// time. The id is minted when the order is chosen and thrown away only when the
// form is cleared for the next delivery.
//
// # Accepted and sent back are two columns, not one
//
// `qty_received` and `qty_rejected` are separate on the wire and separate here.
// A case that arrived broken did arrive: the supplier delivered it and will
// invoice it, and a screen that let a clerk net it off would lose the argument
// about who pays for the breakage.
//
// # A lot number is asked for only where it is needed
//
// `tracks_batches` per line, added by migration 0126. Before that a receiving
// screen could only find out which lines needed one by submitting the delivery
// and reading the error -- which means typing a whole pallet and being told.
//
// # Duty and import VAT are not the same money
//
// Freight, duty and handling go into what the stock cost. Import VAT is
// reclaimed and must never be added to it (E2.5). Two fields, because one would
// invite adding them together.

import { PackageCheck, Truck } from 'lucide-react';
import Link from 'next/link';
import { Suspense, useMemo, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { ORDER_STATUS, type Order, type OrderLine } from '@/lib/purchasing/orders';
import { useUrlState } from '@/lib/url-state';

interface Counted {
  received: string;
  rejected: string;
  reason: string;
  batch: string;
  made: string;
  expires: string;
}

const BLANK: Counted = {
  received: '',
  rejected: '',
  reason: '',
  batch: '',
  made: '',
  expires: '',
};

interface Booked {
  id: string;
  grn_number: string;
  po_id: string;
  order_status: string;
  already_received: boolean;
  /** Signed. Positive means those goods cost more than the till estimated. */
  cost_correction: string;
  units_recosted: string;
}

function ReceivingScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const [poID, setPoID] = useUrlState('po');

  // Only what can still be received against. Two requests rather than one
  // because the route filters on a single status, and "sent" and "part
  // arrived" are both open deliveries.
  const issued = useApiList<Order>(
    scope ? '/purchasing/orders' : null,
    scope ? { ...scope, status: 'issued' } : undefined,
  );
  const receiving = useApiList<Order>(
    scope ? '/purchasing/orders' : null,
    scope ? { ...scope, status: 'receiving' } : undefined,
  );

  const open = useMemo(
    () => [...(issued.data?.data ?? []), ...(receiving.data?.data ?? [])],
    [issued.data, receiving.data],
  );

  const {
    data: order,
    isLoading,
    error,
  } = useApi<Order>(
    scope && poID ? `/purchasing/orders/${poID}` : null,
    scope ?? undefined,
  );

  const [counts, setCounts] = useState<Record<string, Counted>>({});
  const [deliveryNote, setDeliveryNote] = useState('');
  const [notes, setNotes] = useState('');
  const [landed, setLanded] = useState('');
  const [importVat, setImportVat] = useState('');
  const [basis, setBasis] = useState('value');
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [booked, setBooked] = useState<Booked | null>(null);

  // One id per delivery, minted when the order is chosen. Regenerating it on
  // every render would defeat the idempotency the route offers; keeping it
  // across a cleared form would collide with the previous delivery.
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());

  const lines = order?.lines ?? [];
  const at = (id: string) => counts[id] ?? BLANK;

  function set(id: string, patch: Partial<Counted>) {
    setCounts((c) => ({ ...c, [id]: { ...at(id), ...patch } }));
    setFieldErrors({});
  }

  function fillEverything() {
    const next: Record<string, Counted> = {};
    for (const l of lines) {
      next[l.id] = { ...at(l.id), received: l.qty_outstanding };
    }
    setCounts(next);
  }

  function reset() {
    setCounts({});
    setDeliveryNote('');
    setNotes('');
    setLanded('');
    setImportVat('');
    setBasis('value');
    setBooked(null);
    setSaveError(null);
    setDocUUID(crypto.randomUUID());
  }

  // A line is on the delivery only if something was counted against it.
  const counted = lines.filter(
    (l) => !isZero(at(l.id).received) || !isZero(at(l.id).rejected),
  );

  async function book() {
    if (!scope || !order || counted.length === 0) return;
    setBusy(true);
    setSaveError(null);
    setFieldErrors({});
    try {
      const out = await api.post<Booked>(
        `/purchasing/receipts?company_id=${scope.company_id}`,
        {
          uuid: docUUID,
          po_id: order.id,
          delivery_note_ref: deliveryNote,
          notes,
          landed_cost: landed,
          import_vat: importVat,
          landed_cost_basis: basis,
          lines: counted.map((l) => {
            const c = at(l.id);
            return {
              po_line_id: l.id,
              qty_received: c.received || '0',
              qty_rejected: c.rejected || '0',
              reject_reason: c.reason,
              batch_no: c.batch,
              manufactured_on: c.made,
              expires_on: c.expires,
            };
          }),
        },
      );
      setBooked(out);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setSaveError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (booked) {
    return (
      <BookedIn
        booked={booked}
        currency={order?.currency || currency}
        market={market}
        onAnother={reset}
      />
    );
  }

  return (
    <>
      <PageHeader title={t('nx.grn.title')} description={t('nx.grn.subtitle')} />

      <Panel className="mb-6">
        <Field label={t('nx.grn.pickOrder')} hint={t('nx.grn.pickOrderHint')}>
          <Select
            value={poID}
            onChange={(e) => {
              setPoID(e.target.value);
              reset();
            }}
          >
            <option value="">{t('nx.grn.chooseOne')}</option>
            {open.map((o) => (
              <option key={o.id} value={o.id}>
                {`${o.po_number} · ${o.supplier}`}
              </option>
            ))}
          </Select>
        </Field>
      </Panel>

      {poID === '' && !issued.isLoading && !receiving.isLoading && open.length === 0 ? (
        <EmptyState
          icon={Truck}
          title={t('nx.grn.noneOpen')}
          description={t('nx.grn.noneOpenDesc')}
        />
      ) : null}

      {error ? <ErrorState error={error} /> : null}
      {poID !== '' && isLoading ? <Skeleton className="h-64 w-full" /> : null}

      {order ? (
        <>
          <FormError message={saveError} className="mb-4" />

          <Panel
            title={t('nx.grn.what')}
            actions={
              <div className="flex gap-2">
                <Button variant="secondary" size="sm" onClick={fillEverything}>
                  {t('nx.grn.receiveAll')}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setCounts({})}
                  disabled={counted.length === 0}
                >
                  {t('nx.grn.clearAll')}
                </Button>
              </div>
            }
          >
            <ul className="flex flex-col divide-y divide-line">
              {lines.map((l) => (
                <LineRow
                  key={l.id}
                  line={l}
                  value={at(l.id)}
                  onChange={(patch) => set(l.id, patch)}
                />
              ))}
            </ul>
          </Panel>

          <div className="mt-6 grid gap-6 lg:grid-cols-2">
            <Panel title={t('nx.grn.costs')}>
              <div className="flex flex-col gap-4">
                <Field
                  label={t('nx.grn.landed')}
                  hint={t('nx.grn.landedHint')}
                  error={fieldErrors.landed_cost}
                >
                  <Input
                    value={landed}
                    onChange={(e) => setLanded(e.target.value)}
                    inputMode="decimal"
                    autoComplete="off"
                  />
                </Field>
                <Field
                  label={t('nx.grn.importVat')}
                  hint={t('nx.grn.importVatHint')}
                  error={fieldErrors.import_vat}
                >
                  <Input
                    value={importVat}
                    onChange={(e) => setImportVat(e.target.value)}
                    inputMode="decimal"
                    autoComplete="off"
                  />
                </Field>
                <Field label={t('nx.grn.basis')} hint={t('nx.grn.basisHint')}>
                  <Select value={basis} onChange={(e) => setBasis(e.target.value)}>
                    <option value="value">{t('nx.grn.basisValue')}</option>
                    <option value="quantity">{t('nx.grn.basisQty')}</option>
                  </Select>
                </Field>
              </div>
            </Panel>

            <Panel>
              <div className="flex flex-col gap-4">
                <Field
                  label={t('nx.grn.deliveryNote')}
                  hint={t('nx.grn.deliveryNoteHint')}
                  error={fieldErrors.delivery_note_ref}
                >
                  <Input
                    value={deliveryNote}
                    onChange={(e) => setDeliveryNote(e.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </Field>
                <Field label={t('nx.grn.notes')}>
                  <Textarea
                    value={notes}
                    onChange={(e) => setNotes(e.target.value)}
                    rows={3}
                  />
                </Field>
              </div>
            </Panel>
          </div>

          <div className="mt-6 flex flex-wrap items-center gap-3">
            <Button
              variant="primary"
              busy={busy}
              disabled={counted.length === 0}
              onClick={() => void book()}
            >
              {t('nx.grn.book')}
            </Button>
            {counted.length === 0 ? (
              <span className="text-body text-muted">
                {t('nx.grn.nothingCounted')}
              </span>
            ) : null}
          </div>
        </>
      ) : null}
    </>
  );
}

/** One order line, with what turned up against it. */
function LineRow({
  line,
  value,
  onChange,
}: {
  line: OrderLine;
  value: Counted;
  onChange: (patch: Partial<Counted>) => void;
}) {
  const t = useT();

  // Over-receiving is allowed and the order simply shows nothing still due, so
  // this is a note rather than an error.
  const over =
    Number(value.received || '0') > Number(line.qty_outstanding || '0') &&
    Number(line.qty_outstanding || '0') > 0;
  const rejectedWithoutReason =
    !isZero(value.rejected) && value.reason.trim() === '';

  return (
    <li className="py-4 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="text-body font-medium text-fg">{line.description || '—'}</p>
        <p className="text-caption text-muted">
          {t('nx.grn.colDue')}{' '}
          <span className="num text-fg">{formatQuantity(line.qty_outstanding)}</span>
        </p>
      </div>

      <div className="mt-3 grid gap-3 sm:grid-cols-3">
        <Field label={t('nx.grn.colGood')}>
          <Input
            value={value.received}
            onChange={(e) => onChange({ received: e.target.value })}
            inputMode="decimal"
            autoComplete="off"
          />
        </Field>
        <Field label={t('nx.grn.colRejected')}>
          <Input
            value={value.rejected}
            onChange={(e) => onChange({ rejected: e.target.value })}
            inputMode="decimal"
            autoComplete="off"
          />
        </Field>
        <Field
          label={t('nx.grn.colWhyRejected')}
          error={rejectedWithoutReason ? t('nx.grn.rejectNeedsReason') : undefined}
        >
          <Input
            value={value.reason}
            onChange={(e) => onChange({ reason: e.target.value })}
            disabled={isZero(value.rejected)}
          />
        </Field>
      </div>

      {over ? (
        <p className="mt-2 text-caption text-muted">{t('nx.grn.overHint')}</p>
      ) : null}

      {/* Asked for only where the product is tracked by batch. Everywhere else
          the route refuses a lot number, so a field would be a trap. */}
      {line.tracks_batches ? (
        <div className="mt-3 rounded-sm border border-line bg-surface-sunken p-3">
          <Badge tone="info">{t('nx.grn.lot')}</Badge>
          <p className="mt-1 text-caption text-muted">{t('nx.grn.lotHint')}</p>
          <div className="mt-3 grid gap-3 sm:grid-cols-3">
            <Field label={t('nx.grn.lot')}>
              <Input
                value={value.batch}
                onChange={(e) => onChange({ batch: e.target.value })}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label={t('nx.grn.madeOn')}>
              <Input
                type="date"
                value={value.made}
                onChange={(e) => onChange({ made: e.target.value })}
              />
            </Field>
            <Field label={t('nx.grn.expires')}>
              <Input
                type="date"
                value={value.expires}
                onChange={(e) => onChange({ expires: e.target.value })}
              />
            </Field>
          </div>
        </div>
      ) : null}
    </li>
  );
}

/** What happened, including the thing nobody asked about but needs to know. */
function BookedIn({
  booked,
  currency,
  market,
  onAnother,
}: {
  booked: Booked;
  currency: string;
  market: ReturnType<typeof useCompany>['market'];
  onAnother: () => void;
}) {
  const t = useT();
  const status = ORDER_STATUS[booked.order_status];
  const recosted = !isZero(booked.cost_correction);

  return (
    <>
      <PageHeader
        title={t('nx.grn.doneTitle', { grn: booked.grn_number })}
        description={
          status ? t('nx.grn.orderNow', { status: t(status.key) }) : undefined
        }
      />

      {booked.already_received ? (
        <Panel className="mb-6">
          <p className="text-body text-fg">{t('nx.grn.doneAgain')}</p>
        </Panel>
      ) : null}

      {/* C13. Nobody asked, and somebody reading last week's margin needs to
          know it moved: these goods were sold before they arrived, so the
          till priced them on an estimate and this delivery settled it. */}
      {recosted ? (
        <Panel className="mb-6" title={t('nx.grn.recostTitle')}>
          <p className="text-body text-muted">
            {t('nx.grn.recostBody', {
              units: formatQuantity(booked.units_recosted),
              amount: formatMoney(booked.cost_correction, { currency, market }),
            })}
          </p>
        </Panel>
      ) : null}

      <div className="flex flex-wrap gap-3">
        <Button variant="primary" onClick={onAnother}>
          <PackageCheck aria-hidden="true" />
          {t('nx.grn.bookAnother')}
        </Button>
        <Link
          href={`/buying/orders/${booked.po_id}`}
          className="inline-flex h-10 items-center rounded-sm border border-line-strong bg-surface px-3 text-body font-medium hover:border-primary"
        >
          {t('nx.grn.viewOrder')}
        </Link>
      </div>
    </>
  );
}

export default function ReceiptsPage() {
  return (
    <RequirePermission anyOf={['purchasing.receive_goods']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ReceivingScreen />
      </Suspense>
    </RequirePermission>
  );
}
