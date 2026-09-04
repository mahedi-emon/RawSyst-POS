'use client';

// Raising a purchase order.
//
// # It is driven off what is running out
//
// The reason somebody raises an order is that stock is low, and `/stock/on-hand`
// already answers that with `low=true`. So the picker opens on what needs
// reordering rather than on an empty search box, and searching the rest of the
// catalogue is the other thing it does. The dashboard has been pointing at low
// stock since it was built; this is where that pointing finally leads.
//
// # The buyer types a tax rate, and that is a gap, not a design
//
// A SALE never asks. `applyTaxProfile` is explicit that it "fills in the values
// the till is not allowed to choose", reading the rate from the regulatory
// register at the invoice date, and refusing the sale if no rate is on file.
// Purchasing takes the rate from the request body and defaults it to zero, and
// no business-facing route exposes what the rate should be -- the register is
// readable only through `/platform/jurisdictions/rates`, which is super-admin.
//
// So the honest options were: send nothing and raise every order at zero per
// cent, silently; hardcode 0.15, which is a Saudi assumption in a product that
// sells into three markets; or ask. This asks, says why, and carries the
// previous line's rate down the order so it is typed once. Recorded as a
// finding: purchasing should resolve the rate the way sales does.
//
// # Percent in, fraction out
//
// The field takes 15 and sends 0.15, because a rate is a fraction everywhere in
// this system and 15 meant 1500% until the boundary started refusing it.

import { Plus, Search, Trash2 } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useMemo, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { Order } from '@/lib/purchasing/orders';
import { lineTotals, orderTotals, type Draft } from '@/lib/purchasing/draft-order';

interface Supplier {
  id: string;
  code: string;
  legal_name: string;
}

interface Warehouse {
  id: string;
  code: string;
  name: string;
}

interface StockRow {
  variant_id: string;
  sku: string;
  product: string;
  barcode?: string;
  on_hand: string;
  reorder_level?: string;
}

function NewOrderScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const suppliers = useApiList<Supplier>(
    scope ? '/purchasing/suppliers' : null,
    scope ?? undefined,
  );
  const warehouses = useApiList<Warehouse>(
    scope ? '/purchasing/warehouses' : null,
    scope ?? undefined,
  );

  const [supplierId, setSupplierId] = useState('');
  const [warehouseId, setWarehouseId] = useState('');
  const [expectedOn, setExpectedOn] = useState('');
  const [notes, setNotes] = useState('');
  const [lines, setLines] = useState<Draft[]>([]);

  const [term, setTerm] = useState('');
  // Opens on what is running out, because that is why anybody is here.
  const [lowOnly, setLowOnly] = useState(true);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const stock = useApiList<StockRow>(
    scope ? '/stock/on-hand' : null,
    scope
      ? {
          ...scope,
          search: term || undefined,
          low: lowOnly ? 'true' : undefined,
          limit: 25,
        }
      : undefined,
  );

  const chosen = useMemo(
    () => new Set(lines.map((l) => l.variant_id)),
    [lines],
  );

  function add(row: StockRow) {
    setLines((current) => [
      ...current,
      {
        variant_id: row.variant_id,
        description: `${row.product} · ${row.sku}`,
        qty: '',
        unit_cost: '',
        // Carried down the order so a rate is typed once, per the redundant
        // entry rule. Blank on the first line: guessing one would be worse
        // than asking, and the register is not readable from here.
        tax_percent: current[current.length - 1]?.tax_percent ?? '',
      },
    ]);
  }

  function patch(index: number, next: Partial<Draft>) {
    setLines((current) =>
      current.map((l, i) => (i === index ? { ...l, ...next } : l)),
    );
  }

  function remove(index: number) {
    setLines((current) => current.filter((_, i) => i !== index));
  }

  const totals = orderTotals(lines);
  const money = (v: string) => formatMoney(v, { currency, market });

  async function save() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});

    // Said here rather than by the server, because these three are what the
    // screen already knows and a round trip to be told would be one.
    if (!supplierId) return setError(t('nx.npo.needSupplier'));
    if (!warehouseId) return setError(t('nx.npo.needWarehouse'));
    if (lines.length === 0) return setError(t('nx.npo.needLines'));

    setBusy(true);
    try {
      const out = await api.post<Order>(
        `/purchasing/orders?company_id=${scope.company_id}`,
        {
          supplier_id: supplierId,
          warehouse_id: warehouseId,
          expected_on: expectedOn,
          notes,
          lines: lines.map((l) => ({
            variant_id: l.variant_id,
            description: l.description,
            qty: l.qty || '0',
            unit_cost: l.unit_cost || '0',
            // Sent as the fraction the API takes, from the percentage a buyer
            // reads off an invoice. 15 here would be 1500% there.
            tax_rate: l.tax_percent
              ? String(Number(l.tax_percent) / 100)
              : '0',
          })),
        },
      );
      router.push(`/buying/orders/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  const rows = stock.data?.data ?? [];

  return (
    <>
      <PageHeader title={t('nx.npo.title')} description={t('nx.npo.subtitle')} />

      <FormError message={error} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <Panel title={t('nx.npo.what')}>
            <div className="flex flex-wrap items-end gap-3">
              <Field label={t('nx.npo.pick')} hint={t('nx.npo.pickHint')} className="flex-1 min-w-56">
                <Input
                  value={term}
                  onChange={(e) => setTerm(e.target.value)}
                  type="search"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <Checkbox
                label={t('nx.npo.lowFirst')}
                checked={lowOnly}
                onChange={(e) => setLowOnly(e.target.checked)}
                className="pb-2.5"
              />
            </div>

            <div className="mt-3">
              {stock.isLoading ? <Skeleton className="h-24 w-full" /> : null}
              {!stock.isLoading && rows.length === 0 ? (
                <p className="py-4 text-body text-muted">
                  {t('nx.npo.searchEmpty')}
                </p>
              ) : null}
              <ul className="flex flex-col divide-y divide-line">
                {rows.map((row) => (
                  <li
                    key={row.variant_id}
                    className="flex flex-wrap items-center justify-between gap-3 py-2"
                  >
                    <div className="min-w-0">
                      <p className="truncate text-body text-fg">{row.product}</p>
                      <p className="text-caption text-muted">
                        <span className="num">{row.sku}</span>
                        {' · '}
                        {t('nx.npo.onHand', { qty: formatQuantity(row.on_hand) })}
                        {row.reorder_level
                          ? ` · ${t('nx.npo.reorderAt', {
                              qty: formatQuantity(row.reorder_level),
                            })}`
                          : ''}
                      </p>
                    </div>
                    {chosen.has(row.variant_id) ? (
                      <span className="text-caption text-subtle">
                        {t('nx.npo.added')}
                      </span>
                    ) : (
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => add(row)}
                      >
                        <Plus aria-hidden="true" />
                        {t('nx.npo.addLine')}
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          </Panel>

          <Panel title={t('nx.npo.summary')}>
            {lines.length === 0 ? (
              <p className="text-body text-muted">{t('nx.npo.noneYet')}</p>
            ) : (
              <ul className="flex flex-col divide-y divide-line">
                {lines.map((line, i) => {
                  const sums = lineTotals(line);
                  return (
                    <li key={line.variant_id} className="py-3 first:pt-0">
                      <div className="flex items-baseline justify-between gap-3">
                        <p className="text-body text-fg">{line.description}</p>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => remove(i)}
                          aria-label={t('nx.npo.remove')}
                        >
                          <Trash2 aria-hidden="true" />
                        </Button>
                      </div>
                      <div className="mt-2 grid gap-3 sm:grid-cols-4">
                        <Field label={t('nx.npo.colQty')}>
                          <Input
                            value={line.qty}
                            onChange={(e) => patch(i, { qty: e.target.value })}
                            inputMode="decimal"
                            autoComplete="off"
                          />
                        </Field>
                        <Field label={t('nx.npo.colCost')}>
                          <Input
                            value={line.unit_cost}
                            onChange={(e) =>
                              patch(i, { unit_cost: e.target.value })
                            }
                            inputMode="decimal"
                            autoComplete="off"
                          />
                        </Field>
                        <Field
                          label={t('nx.npo.colRate')}
                          hint={i === 0 ? t('nx.npo.rateHint') : undefined}
                        >
                          <Input
                            value={line.tax_percent}
                            onChange={(e) =>
                              patch(i, { tax_percent: e.target.value })
                            }
                            inputMode="decimal"
                            autoComplete="off"
                          />
                        </Field>
                        <div className="flex flex-col justify-end pb-2">
                          <p className="text-caption text-muted">
                            {t('nx.npo.colLine')}
                          </p>
                          <p className="num text-body font-medium text-fg">
                            {money(sums.gross)}
                          </p>
                        </div>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}

            {lines.length > 0 ? (
              <dl className="mt-4 flex flex-col gap-1 border-t border-line pt-3 text-body">
                <div className="flex justify-between gap-4">
                  <dt className="text-muted">{t('nx.po.net')}</dt>
                  <dd className="num">{money(totals.net)}</dd>
                </div>
                <div className="flex justify-between gap-4">
                  <dt className="text-muted">{t('nx.po.tax')}</dt>
                  <dd className="num">{money(totals.tax)}</dd>
                </div>
                <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
                  <dt>{t('nx.po.total')}</dt>
                  <dd className="num">{money(totals.gross)}</dd>
                </div>
              </dl>
            ) : null}
          </Panel>

          {/* Said once, under the thing it is about, rather than beside every
              rate field. It explains a gap in the product, and a person needs
              to read it once. */}
          <Panel title={t('nx.npo.rateNoteTitle')}>
            <p className="text-body text-muted">{t('nx.npo.rateNoteBody')}</p>
          </Panel>
        </div>

        <div className="flex flex-col gap-6">
          <Panel title={t('nx.npo.who')}>
            <div className="flex flex-col gap-4">
              <Field label={t('nx.npo.supplier')} error={fieldErrors.supplier_id} required>
                <Select
                  value={supplierId}
                  onChange={(e) => setSupplierId(e.target.value)}
                >
                  <option value="">{t('nx.npo.chooseSupplier')}</option>
                  {(suppliers.data?.data ?? []).map((s) => (
                    <option key={s.id} value={s.id}>
                      {`${s.code} · ${s.legal_name}`}
                    </option>
                  ))}
                </Select>
              </Field>

              <Field
                label={t('nx.npo.warehouse')}
                error={fieldErrors.warehouse_id}
                required
              >
                <Select
                  value={warehouseId}
                  onChange={(e) => setWarehouseId(e.target.value)}
                >
                  <option value="">{t('nx.npo.chooseWarehouse')}</option>
                  {(warehouses.data?.data ?? []).map((w) => (
                    <option key={w.id} value={w.id}>
                      {`${w.code} · ${w.name}`}
                    </option>
                  ))}
                </Select>
              </Field>

              <Field
                label={t('nx.npo.expected')}
                hint={t('nx.npo.expectedHint')}
                error={fieldErrors.expected_on}
              >
                <Input
                  type="date"
                  value={expectedOn}
                  onChange={(e) => setExpectedOn(e.target.value)}
                />
              </Field>

              <Field label={t('nx.npo.notes')}>
                <Textarea
                  value={notes}
                  onChange={(e) => setNotes(e.target.value)}
                  rows={3}
                />
              </Field>
            </div>
          </Panel>

          <div>
            <Button
              variant="primary"
              busy={busy}
              onClick={() => void save()}
              className="w-full"
            >
              {t('nx.npo.save')}
            </Button>
            <p className="mt-2 text-caption text-muted">{t('nx.npo.saveHint')}</p>
          </div>
        </div>
      </div>
    </>
  );
}

export default function NewOrderPage() {
  return (
    <RequirePermission anyOf={['purchasing.create_order']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewOrderScreen />
      </Suspense>
    </RequirePermission>
  );
}
