'use client';

// Getting an order to the customer, and what is owed when it arrives.
//
// # The status ladder is the screen
//
// pending → assigned → picked up → out for delivery → delivered, with failed
// and returned as the two ways out. A driver or a dispatcher does not choose
// freely from seven options; they move a delivery one rung, so the screen
// offers the next rung and the two exits rather than a dropdown of everything.
//
// # Three rules the database enforces, asked for here instead of refused
//
// `delivery_assigned_has_a_driver` — anything past pending needs a driver, so
// the driver is asked for when assigning rather than collected as a constraint
// violation. `delivery_failure_says_why` — a failed delivery must carry a
// reason, so the reason box is required before the button works. And COD is
// collected on arrival, so "money taken" is asked at the point of delivery and
// only for a delivery that carries a cash amount.
//
// # Cash on delivery is the driver holding the shop's money
//
// A COD delivery marked delivered without the money collected is a real state —
// the goods went and the cash did not — and it is the state a shop most needs
// to see. So it is a column, and it says which of the two happened rather than
// showing a blank.

import { Truck } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useUrlState } from '@/lib/url-state';

import { ConsignmentPanel } from './consignment';
import { api } from '@/lib/api/client';
import { blockedBy, collectsCash, needsDriver, nextStates } from '@/lib/aftersales/delivery';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';

interface Delivery {
  id: string;
  delivery_no: string;
  order_id: string;
  order_no?: string;
  status: string;
  customer?: string;
  driver_id?: string;
  driver_name?: string;
  address: string;
  phone?: string;
  fee: string;
  is_cod: boolean;
  cod_amount: string;
  cod_collected_at?: string;
  assigned_at?: string;
  picked_up_at?: string;
  delivered_at?: string;
  failure_reason?: string;
}

interface Person {
  id: string;
  full_name?: string;
  name?: string;
}

const STATUS_TONE: Record<string, 'neutral' | 'info' | 'caution' | 'positive' | 'critical'> =
  {
    pending: 'neutral',
    assigned: 'info',
    picked_up: 'info',
    out_for_delivery: 'caution',
    delivered: 'positive',
    failed: 'critical',
    returned: 'neutral',
  };

function DeliveriesScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayDeliver = grants.can('delivery.deliver');

  const { data, isLoading, error, refetch } = useApiList<Delivery>(
    scope ? '/deliveries' : null,
    { company_id: scope?.company_id },
  );

  // Drivers come from the people list. A delivery past pending must name one,
  // so the picker is filled from the same place the constraint points at.
  const people = useApiList<Person>(scope ? '/people' : null, {
    company_id: scope?.company_id,
  });

  const [moving, setMoving] = useState<string | null>(null);
  const [target, setTarget] = useState('');
  const [driverId, setDriverId] = useState('');
  const [note, setNote] = useState('');
  const [collectedCOD, setCollectedCOD] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  // Which consignment is being read. In the URL, so "where is my order" is a
  // link somebody can send to the person asking.
  const [reading, setReading] = useUrlState('delivery');

  const rows = data?.data ?? [];
  const open = rows.find((d) => d.id === moving);

  function begin(d: Delivery) {
    setMoving(d.id);
    setTarget(nextStates(d.status)[0] ?? '');
    setDriverId(d.driver_id ?? '');
    setNote('');
    setCollectedCOD(false);
    setActionError(null);
  }

  async function advance() {
    if (!scope || !open || target === '') return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(`/deliveries/${open.id}/advance?company_id=${scope.company_id}`, {
        status: target,
        note,
        driver_id: driverId,
        collected_cod: collectedCOD,
      });
      setMoving(null);
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  // The three rules delivery's CHECK constraints enforce, asserted in
  // lib/aftersales/delivery.test.ts rather than restated here.
  const driverRequired = needsDriver(target);
  const blocked = blockedBy({ target, driverId, note }) !== null;

  const columns: Column<Delivery>[] = [
    {
      key: 'no',
      header: t('nx.del.delivery'),
      primary: true,
      cell: (d) => (
        <span className="flex flex-col">
          <span className="num">{d.delivery_no}</span>
          <span className="text-caption text-muted">
            {d.order_no ? <span className="num">{d.order_no}</span> : null}
            {d.customer ? ` · ${d.customer}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'address',
      header: t('nx.del.to'),
      cell: (d) => (
        <span className="flex flex-col">
          <span>{d.address}</span>
          {d.phone ? (
            <span className="num text-caption text-muted" dir="ltr">
              {d.phone}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'driver',
      header: t('nx.del.driver'),
      secondary: true,
      cell: (d) =>
        d.driver_name ?? <span className="text-muted">{t('nx.del.noDriver')}</span>,
    },
    {
      key: 'cod',
      header: t('nx.del.cod'),
      numeric: true,
      width: 'w-40',
      cell: (d) =>
        !d.is_cod ? (
          <span className="text-muted">—</span>
        ) : (
          <span className="flex flex-col items-end">
            <span className="num" dir="ltr">
              {d.cod_amount}
            </span>
            {/* Goods gone and cash not collected is the state a shop most
                needs to see, so it is said rather than left blank. */}
            {d.cod_collected_at ? (
              <Badge tone="positive">{t('nx.del.codTaken')}</Badge>
            ) : (
              <Badge tone="caution">{t('nx.del.codOutstanding')}</Badge>
            )}
          </span>
        ),
    },
    {
      key: 'status',
      header: t('nx.del.status'),
      width: 'w-40',
      cell: (d) => (
        <span className="flex flex-col gap-1">
          <Badge tone={STATUS_TONE[d.status] ?? 'neutral'}>
            {t(`nx.del.state.${d.status}` as 'nx.del.state.pending')}
          </Badge>
          {d.failure_reason ? (
            <span className="text-caption text-muted">{d.failure_reason}</span>
          ) : null}
        </span>
      ),
    },
  ];

  if (mayDeliver) {
    columns.push({
      key: 'move',
      header: t('nx.del.move'),
      width: 'w-32',
      cell: (d) =>
        nextStates(d.status).length === 0 ? (
          <span className="text-muted">—</span>
        ) : (
          <Button
            size="sm"
            variant="ghost"
            // The row opens the consignment, so a control inside it has to
            // stop the click reaching the row.
            onClick={(e) => {
              e.stopPropagation();
              begin(d);
            }}
          >
            {t('nx.del.update')}
          </Button>
        ),
    });
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader title={t('nx.del.title')} description={t('nx.del.subtitle')} />

      <FormError message={actionError} className="mb-4" />

      {open ? (
        <Panel title={t('nx.del.moving', { no: open.delivery_no })} className="mb-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field name="status" label={t('nx.del.newStatus')}>
              <Select value={target} onChange={(e) => setTarget(e.target.value)}>
                {nextStates(open.status).map((s) => (
                  <option key={s} value={s}>
                    {t(`nx.del.state.${s}` as 'nx.del.state.pending')}
                  </option>
                ))}
              </Select>
            </Field>

            {driverRequired ? (
              <Field
                name="driver_id"
                label={t('nx.del.driver')}
                hint={t('nx.del.driverHint')}
              >
                <Select
                  value={driverId}
                  onChange={(e) => setDriverId(e.target.value)}
                  required
                >
                  <option value="">{t('nx.del.chooseDriver')}</option>
                  {(people.data?.data ?? []).map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.full_name ?? p.name ?? p.id}
                    </option>
                  ))}
                </Select>
              </Field>
            ) : null}
          </div>

          <div className="mt-4">
            <Field
              name="note"
              label={
                target === 'failed' ? t('nx.del.whyFailed') : t('nx.del.noteLabel')
              }
              hint={target === 'failed' ? t('nx.del.whyFailedHint') : undefined}
            >
              <Textarea rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
            </Field>
          </div>

          {/* Only for a delivery that actually carries cash, and only at the
              point it arrives. */}
          {collectsCash(open.is_cod, target) ? (
            <div className="mt-4">
              <Checkbox
                checked={collectedCOD}
                onChange={(e) => setCollectedCOD(e.target.checked)}
                label={t('nx.del.codCollected', { amount: open.cod_amount })}
                hint={t('nx.del.codCollectedHint')}
              />
            </div>
          ) : null}

          <div className="mt-6 flex flex-wrap gap-2">
            <Button
              busy={busy}
              busyLabel={t('nx.del.saving')}
              disabled={blocked}
              onClick={() => void advance()}
            >
              {t('nx.del.save')}
            </Button>
            <Button variant="ghost" onClick={() => setMoving(null)}>
              {t('nx.del.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={6} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Truck}
          title={t('nx.del.emptyTitle')}
          description={t('nx.del.emptyDesc')}
        />
      ) : null}

      {/* One consignment and every step it has been through. Four timestamps
          are four columns nobody has room for; as a sequence they answer
          "where is my order", which is what this module is for. */}
      {reading && scope ? (
        <ConsignmentPanel
          companyId={scope.company_id}
          deliveryId={reading}
          currency={currency}
          market={market}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<Delivery>
          rows={rows}
          columns={columns}
          rowKey={(d) => d.id}
          caption={t('nx.del.caption')}
          isSelected={(d) => d.id === reading}
          onOpenRow={(d) => setReading(d.id === reading ? '' : d.id)}
        />
      ) : null}
    </>
  );
}

export default function DeliveriesPage() {
  return (
    <RequirePermission anyOf={['delivery.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <DeliveriesScreen />
      </Suspense>
    </RequirePermission>
  );
}
