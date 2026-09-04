'use client';

// Raising a quotation.
//
// # It is always a quotation, and the title says so
//
// There is no route that creates a confirmed order, deliberately: confirming is
// the customer's decision and putting it behind one button here would put "the
// customer agreed" in the hands of whoever typed it. So the button says
// quotation, and confirming happens on the order itself, once.
//
// # The price is the shelf price until somebody changes it
//
// Prefilled from the catalogue rather than left blank, because a quotation
// usually is the shelf price and typing every one again is a chance to get one
// wrong. Editable, because the whole reason to raise a quotation rather than
// ring it up is that something about it is negotiated.

import { Plus, Trash2 } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useMemo, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { orderTotals, type OrderDraftLine } from '@/lib/orders/draft';
import type { Order } from '@/lib/orders/orders';

interface CustomerRow {
  id: string;
  code: string;
  name: string;
}

interface CatalogueItem {
  id: string;
  sku: string;
  name: string;
  price: string;
}

const CHANNELS: ReadonlyArray<{ value: string; key: Key }> = [
  { value: 'walk_in', key: 'nx.ord.chWalkIn' },
  { value: 'phone', key: 'nx.ord.chPhone' },
  { value: 'online', key: 'nx.ord.chOnline' },
  { value: 'wholesale', key: 'nx.ord.chWholesale' },
];

function NewOrderScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const customers = useApiList<CustomerRow>(
    scope ? '/customers' : null,
    scope ?? undefined,
  );

  const [customerId, setCustomerId] = useState('');
  const [channel, setChannel] = useState('walk_in');
  const [validUntil, setValidUntil] = useState('');
  const [deliverTo, setDeliverTo] = useState('');
  const [deliverPhone, setDeliverPhone] = useState('');
  const [notes, setNotes] = useState('');
  const [lines, setLines] = useState<OrderDraftLine[]>([]);
  const [term, setTerm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  // The sellable catalogue, which is what the till scans off too.
  const catalogue = useApiList<CatalogueItem>(
    scope ? '/catalog/snapshot' : null,
    scope ? { ...scope, search: term || undefined, limit: 25 } : undefined,
  );

  const chosen = useMemo(() => new Set(lines.map((l) => l.variant_id)), [lines]);
  const items =
    (catalogue.data as unknown as { items?: CatalogueItem[] } | undefined)?.items ??
    catalogue.data?.data ??
    [];

  const totals = orderTotals(lines);
  const money = (v: string) => formatMoney(v, { currency, market });

  async function raise() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (lines.length === 0) return setError(t('nx.ord.needLines'));

    setBusy(true);
    try {
      const out = await api.post<Order>(`/orders?company_id=${scope.company_id}`, {
        customer_id: customerId || undefined,
        channel,
        valid_until: validUntil,
        deliver_to: deliverTo,
        deliver_phone: deliverPhone,
        notes,
        lines: lines.map((l) => ({
          variant_id: l.variant_id,
          description: l.description,
          qty: l.qty || '0',
          unit_price: l.unit_price || '0',
          discount: l.discount || '0',
        })),
      });
      router.push(`/orders/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title={t('nx.ord.newTitle')} description={t('nx.ord.newSubtitle')} />

      <FormError message={error} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <Panel title={t('nx.ord.what')}>
            <Field label={t('nx.npo.pick')} hint={t('nx.npo.pickHint')}>
              <Input
                value={term}
                onChange={(e) => setTerm(e.target.value)}
                type="search"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <div className="mt-3">
              {catalogue.isLoading ? <Skeleton className="h-24 w-full" /> : null}
              <ul className="flex flex-col divide-y divide-line">
                {items.map((item) => (
                  <li
                    key={item.id}
                    className="flex flex-wrap items-center justify-between gap-3 py-2"
                  >
                    <div className="min-w-0">
                      <p className="truncate text-body text-fg">{item.name}</p>
                      <p className="text-caption text-muted">
                        <span className="num">{item.sku}</span>
                        {' · '}
                        <span className="num">{money(item.price)}</span>
                      </p>
                    </div>
                    {chosen.has(item.id) ? (
                      <span className="text-caption text-subtle">{t('nx.npo.added')}</span>
                    ) : (
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() =>
                          setLines((c) => [
                            ...c,
                            {
                              variant_id: item.id,
                              description: `${item.name} · ${item.sku}`,
                              qty: '1',
                              // The shelf price, editable: the reason to raise
                              // a quotation rather than ring it up is usually
                              // that something about it is negotiated.
                              unit_price: item.price,
                              discount: '',
                            },
                          ])
                        }
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

          <Panel>
            {lines.length === 0 ? (
              <p className="text-body text-muted">{t('nx.req.noneYet')}</p>
            ) : (
              <ul className="flex flex-col divide-y divide-line">
                {lines.map((line, i) => (
                  <li key={line.variant_id} className="py-3 first:pt-0">
                    <div className="flex items-baseline justify-between gap-3">
                      <p className="min-w-0 truncate text-body text-fg">
                        {line.description}
                      </p>
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={t('nx.npo.remove')}
                        onClick={() => setLines((c) => c.filter((_, j) => j !== i))}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    </div>
                    <div className="mt-2 grid gap-3 sm:grid-cols-4">
                      <Field label={t('nx.ord.colQty')}>
                        <Input
                          value={line.qty}
                          onChange={(e) =>
                            setLines((c) =>
                              c.map((l, j) => (j === i ? { ...l, qty: e.target.value } : l)),
                            )
                          }
                          inputMode="decimal"
                          autoComplete="off"
                        />
                      </Field>
                      <Field label={t('nx.ord.colPrice')}>
                        <Input
                          value={line.unit_price}
                          onChange={(e) =>
                            setLines((c) =>
                              c.map((l, j) =>
                                j === i ? { ...l, unit_price: e.target.value } : l,
                              ),
                            )
                          }
                          inputMode="decimal"
                          autoComplete="off"
                        />
                      </Field>
                      <Field label={t('nx.ord.colDiscount')}>
                        <Input
                          value={line.discount}
                          onChange={(e) =>
                            setLines((c) =>
                              c.map((l, j) =>
                                j === i ? { ...l, discount: e.target.value } : l,
                              ),
                            )
                          }
                          inputMode="decimal"
                          autoComplete="off"
                        />
                      </Field>
                      <div className="flex flex-col justify-end pb-2">
                        <p className="text-caption text-muted">{t('nx.ord.colLine')}</p>
                        <p className="num text-body font-medium text-fg">
                          {money(orderTotals([line]).total)}
                        </p>
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            )}

            {lines.length > 0 ? (
              <>
                <dl className="mt-4 flex flex-col gap-1 border-t border-line pt-3 text-body">
                  <div className="flex justify-between gap-4">
                    <dt className="text-muted">{t('nx.ord.subtotal')}</dt>
                    <dd className="num">{money(totals.subtotal)}</dd>
                  </div>
                  <div className="flex justify-between gap-4">
                    <dt className="text-muted">{t('nx.ord.discount')}</dt>
                    <dd className="num">{money(totals.discount)}</dd>
                  </div>
                  <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
                    <dt>{t('nx.ord.total')}</dt>
                    <dd className="num">{money(totals.total)}</dd>
                  </div>
                </dl>
                <p className="mt-2 text-caption text-muted">{t('nx.ord.taxLater')}</p>
              </>
            ) : null}
          </Panel>
        </div>

        <div className="flex flex-col gap-6">
          <Panel>
            <div className="flex flex-col gap-4">
              <Field label={t('nx.ord.customer')} error={fieldErrors.customer_id}>
                <Select
                  value={customerId}
                  onChange={(e) => setCustomerId(e.target.value)}
                >
                  <option value="">{t('nx.ord.walkIn')}</option>
                  {(customers.data?.data ?? []).map((c) => (
                    <option key={c.id} value={c.id}>
                      {`${c.code} · ${c.name}`}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label={t('nx.ord.channel')}>
                <Select value={channel} onChange={(e) => setChannel(e.target.value)}>
                  {CHANNELS.map((c) => (
                    <option key={c.value} value={c.value}>
                      {t(c.key)}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field
                label={t('nx.ord.validUntil')}
                hint={t('nx.ord.validHint')}
                error={fieldErrors.valid_until}
              >
                <Input
                  type="date"
                  value={validUntil}
                  onChange={(e) => setValidUntil(e.target.value)}
                />
              </Field>
              <Field label={t('nx.ord.deliverTo')}>
                <Textarea
                  value={deliverTo}
                  onChange={(e) => setDeliverTo(e.target.value)}
                  rows={2}
                />
              </Field>
              <Field label={t('nx.ord.deliverPhone')}>
                <Input
                  type="tel"
                  value={deliverPhone}
                  onChange={(e) => setDeliverPhone(e.target.value)}
                  autoComplete="off"
                />
              </Field>
              <Field label={t('nx.ord.notes')}>
                <Textarea value={notes} onChange={(e) => setNotes(e.target.value)} rows={2} />
              </Field>
            </div>
          </Panel>

          <Button variant="primary" busy={busy} className="w-full" onClick={() => void raise()}>
            {t('nx.ord.send')}
          </Button>
        </div>
      </div>
    </>
  );
}

export default function NewOrderPage() {
  return (
    <RequirePermission anyOf={['order.manage']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewOrderScreen />
      </Suspense>
    </RequirePermission>
  );
}
