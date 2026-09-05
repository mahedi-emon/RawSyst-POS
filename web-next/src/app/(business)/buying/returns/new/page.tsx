'use client';

// Raising a claim against a supplier.
//
// # What may go back is read, never worked out
//
// `qty_returnable` is cumulative across every earlier return, and those are
// rows this browser may never have seen. A screen that subtracted for itself
// would eventually claim the same pallet twice — the same reason the till reads
// `returnable` rather than counting credit notes.
//
// # The claim is the supplier's own arithmetic
//
// Their price, their tax rate, both copied from the bill line. This screen adds
// them up so somebody typing quantities can see the debit note taking shape;
// what the STOCK was carrying is a different figure, decided by the costing
// method, and the server answers it.
//
// # There is no draft
//
// The stock comes off the shelf and the bill is credited in the same
// transaction. A claim that could sit half-raised would be a shelf that says it
// holds goods which are on a lorry.

import { useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { Bill } from '@/lib/purchasing/bills';
import {
  anythingLeft,
  claimFor,
  claimLines,
  overReturnable,
  readiness,
  type PurchaseReturn,
  type Returnable,
} from '@/lib/purchasing/returns';
import { cn } from '@/lib/utils';
import { useUrlState } from '@/lib/url-state';

interface Warehouse {
  id: string;
  code: string;
  name: string;
  store?: string;
}

function NewReturnScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  // In the URL, so a buyer looking at a bill can be sent straight here.
  const [billID, setBillID] = useUrlState('bill');
  const [warehouseID, setWarehouseID] = useState('');
  const [returnedOn, setReturnedOn] = useState(() =>
    new Date().toISOString().slice(0, 10),
  );
  const [reason, setReason] = useState('');
  const [qty, setQty] = useState<Record<string, string>>({});
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const bills = useApiList<Bill>(
    scope ? '/purchasing/bills' : null,
    scope ? { ...scope, limit: 100 } : undefined,
  );
  const warehouses = useApiList<Warehouse>(
    scope ? '/purchasing/warehouses' : null,
    scope ?? undefined,
  );
  const returnable = useApi<{ data: Returnable[] }>(
    scope && billID ? `/purchasing/bills/${billID}/returnable` : null,
    scope ?? undefined,
  );

  const lines = (returnable.data?.data ?? []).filter(anythingLeft);
  const claim = claimFor(lines, qty);
  const state = readiness(lines, qty, reason);
  const places = warehouses.data?.data ?? [];
  const money = (v: string) => formatMoney(v, { currency, market });

  async function send() {
    if (!scope || !state.ok) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const out = await api.post<PurchaseReturn>(
        `/purchasing/returns?company_id=${scope.company_id}`,
        {
          // Minted here, so a clerk pressing the button twice on a bad
          // connection claims once and takes the stock out once.
          uuid: docUUID,
          bill_id: billID,
          warehouse_id: warehouseID || undefined,
          returned_on: returnedOn,
          reason: reason.trim(),
          lines: claimLines(qty),
        },
      );
      setDocUUID(crypto.randomUUID());
      router.push(`/buying/returns/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.pret.newTitle')}
        description={t('nx.pret.newSubtitle')}
      />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_21rem]">
        <div className="min-w-0">
          <Panel title={t('nx.pret.colItem')} flush>
            {!billID ? (
              <p className="px-4 py-3 text-caption text-subtle">
                {t('nx.pret.chooseBill')}
              </p>
            ) : lines.length === 0 && !returnable.isLoading ? (
              <p className="px-4 py-3 text-caption text-muted">
                {t('nx.pret.nothingLeft')}
              </p>
            ) : (
              <table className="w-full text-body">
                <caption className="sr-only">{t('nx.pret.linesCaption')}</caption>
                <thead>
                  <tr className="border-b border-line-strong bg-surface-sunken">
                    <th scope="col" className="px-3 py-2 text-start text-label text-muted">
                      {t('nx.pret.colItem')}
                    </th>
                    <th scope="col" className="hidden px-3 py-2 text-end text-label text-muted sm:table-cell">
                      {t('nx.pret.colBilled')}
                    </th>
                    <th scope="col" className="hidden px-3 py-2 text-end text-label text-muted sm:table-cell">
                      {t('nx.pret.colAlready')}
                    </th>
                    <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                      {t('nx.pret.colCanReturn')}
                    </th>
                    <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                      {t('nx.pret.colPrice')}
                    </th>
                    <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                      {t('nx.pret.colReturning')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l) => {
                    const entered = qty[l.bill_line_id] ?? '';
                    const over = overReturnable(l, entered);
                    return (
                      <tr
                        key={l.bill_line_id}
                        className="border-b border-line last:border-b-0"
                      >
                        <td className="px-3 py-2.5">{l.description}</td>
                        <td className="num hidden px-3 py-2.5 text-end tabular-nums text-muted sm:table-cell">
                          {formatQuantity(l.qty_billed, market)}
                        </td>
                        <td className="num hidden px-3 py-2.5 text-end tabular-nums text-muted sm:table-cell">
                          {formatQuantity(l.qty_returned, market)}
                        </td>
                        <td className="num px-3 py-2.5 text-end font-medium tabular-nums">
                          {formatQuantity(l.qty_returnable, market)}
                        </td>
                        <td className="num px-3 py-2.5 text-end tabular-nums text-muted">
                          {money(l.unit_cost)}
                        </td>
                        <td className="px-3 py-2.5 text-end">
                          <input
                            value={entered}
                            onChange={(e) =>
                              setQty((q) => ({
                                ...q,
                                [l.bill_line_id]: e.target.value,
                              }))
                            }
                            inputMode="decimal"
                            // Capped by what the SERVER says, cumulative across
                            // every earlier claim.
                            max={l.qty_returnable}
                            aria-invalid={over || undefined}
                            aria-label={`${l.description} — ${t('nx.pret.colReturning')}`}
                            className={cn(
                              'num h-10 w-20 rounded-sm border bg-input-bg',
                              'text-center text-body tabular-nums [direction:ltr]',
                              over ? 'border-critical' : 'border-input',
                            )}
                          />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
            {!state.ok && state.reason === 'too_many' ? (
              <p className="border-t border-line px-3 py-2 text-caption text-critical-fg">
                {t('nx.pret.tooMany')}
              </p>
            ) : null}
          </Panel>
        </div>

        <div className="flex flex-col gap-6">
          <Panel>
            <div className="flex flex-col gap-4">
              <Field
                name="bill_id"
                label={t('nx.pret.bill')}
                hint={t('nx.pret.billHint')}
                error={fieldErrors.bill_id}
                required
              >
                <Select
                  value={billID}
                  onChange={(e) => {
                    setBillID(e.target.value);
                    // A quantity typed against another bill's line means
                    // nothing here, and leaving it would send it.
                    setQty({});
                  }}
                >
                  <option value="">{t('nx.pret.chooseBill')}</option>
                  {(bills.data?.data ?? []).map((b) => (
                    <option key={b.id} value={b.id}>
                      {b.supplier_ref || b.id.slice(0, 8)} · {b.supplier}
                    </option>
                  ))}
                </Select>
              </Field>

              {bills.data && (bills.data.data ?? []).length === 0 ? (
                <p className="text-caption text-caution-fg">{t('nx.pret.noBills')}</p>
              ) : null}

              {/* Only asked where there is something to ask. A business with
                  one stock location has one answer and the server finds it. */}
              {places.length > 1 ? (
                <Field
                  name="warehouse_id"
                  label={t('nx.pret.warehouse')}
                  hint={t('nx.pret.warehouseHint')}
                  error={fieldErrors.warehouse_id}
                  required
                >
                  <Select
                    value={warehouseID}
                    onChange={(e) => setWarehouseID(e.target.value)}
                  >
                    <option value="">{t('nx.pret.chooseWarehouse')}</option>
                    {places.map((w) => (
                      <option key={w.id} value={w.id}>
                        {w.name}
                      </option>
                    ))}
                  </Select>
                </Field>
              ) : null}

              <Field name="returned_on" label={t('nx.pret.when')} error={fieldErrors.returned_on}>
                <Input
                  type="date"
                  value={returnedOn}
                  onChange={(e) => setReturnedOn(e.target.value)}
                />
              </Field>

              <Field
                name="reason"
                label={t('nx.pret.reason')}
                hint={t('nx.pret.reasonHint')}
                error={fieldErrors.reason}
                required
              >
                <Textarea
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  rows={3}
                />
              </Field>
            </div>
          </Panel>

          <Panel title={t('nx.pret.claim')} description={t('nx.pret.claimHint')}>
            <dl className="flex flex-col gap-1 text-body">
              <div className="flex justify-between gap-4">
                <dt className="text-muted">{t('nx.pret.net')}</dt>
                <dd className="num">{money(claim.net)}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">{t('nx.pret.tax')}</dt>
                <dd className="num">{money(claim.tax)}</dd>
              </div>
              <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
                <dt>{t('nx.pret.total')}</dt>
                <dd className="num">{money(claim.total)}</dd>
              </div>
            </dl>
          </Panel>

          <div>
            <Button
              variant="primary"
              busy={busy}
              busyLabel={t('nx.pret.sending')}
              className="w-full"
              disabled={!state.ok}
              onClick={() => void send()}
            >
              {t('nx.pret.send')}
            </Button>
            <p className="mt-2 text-caption text-muted">
              {state.ok
                ? t('nx.pret.sendHint')
                : state.reason === 'nothing_chosen'
                  ? t('nx.pret.needLines')
                  : state.reason === 'no_reason'
                    ? t('nx.pret.needReason')
                    : t('nx.pret.tooMany')}
            </p>
          </div>
        </div>
      </div>
    </>
  );
}

export default function NewPurchaseReturnPage() {
  return (
    <RequirePermission anyOf={['purchasing.return_goods']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewReturnScreen />
      </Suspense>
    </RequirePermission>
  );
}
