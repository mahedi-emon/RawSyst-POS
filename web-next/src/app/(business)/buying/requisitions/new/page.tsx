'use client';

// Asking for stock.
//
// # No prices anywhere on this screen
//
// A requisition carries quantities and a reason, and nothing else. B5 puts it
// in reach of any authorised staff precisely because the person who notices an
// empty shelf should not need to know what the thing costs or who sells it —
// and `purchasing.request` deliberately does not carry `catalog.view_cost_price`.
// A cost field here would be a permission leak wearing a form label.
//
// # It opens on what is running out
//
// Same reason as the order screen: the request usually starts with a shelf that
// is nearly empty, and `/stock/on-hand?low=true` already answers that.
//
// # Sending is the only action
//
// `RaiseRequisition` creates it `submitted` — there is no draft state to save
// into — so the button says what happens.

import { Plus, Trash2 } from 'lucide-react';
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
import { useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { Requisition } from '@/lib/purchasing/sourcing';

interface Warehouse {
  id: string;
  code: string;
  name: string;
}

interface StockRow {
  variant_id: string;
  sku: string;
  product: string;
  on_hand: string;
  reorder_level?: string;
}

interface Wanted {
  variant_id: string;
  description: string;
  qty: string;
  note: string;
}

function NewRequisitionScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const warehouses = useApiList<Warehouse>(
    scope ? '/purchasing/warehouses' : null,
    scope ?? undefined,
  );

  const [warehouseId, setWarehouseId] = useState('');
  const [neededBy, setNeededBy] = useState('');
  const [why, setWhy] = useState('');
  const [lines, setLines] = useState<Wanted[]>([]);
  const [term, setTerm] = useState('');
  const [lowOnly, setLowOnly] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const stock = useApiList<StockRow>(
    scope ? '/stock/on-hand' : null,
    scope
      ? { ...scope, search: term || undefined, low: lowOnly ? 'true' : undefined, limit: 25 }
      : undefined,
  );

  const chosen = useMemo(() => new Set(lines.map((l) => l.variant_id)), [lines]);
  const rows = stock.data?.data ?? [];

  function add(row: StockRow) {
    setLines((c) => [
      ...c,
      {
        variant_id: row.variant_id,
        description: `${row.product} · ${row.sku}`,
        qty: '',
        note: '',
      },
    ]);
  }

  async function send() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (!warehouseId) return setError(t('nx.req.needWarehouse'));
    if (lines.length === 0) return setError(t('nx.req.needLines'));

    setBusy(true);
    try {
      const out = await api.post<Requisition>(
        `/purchasing/requisitions?company_id=${scope.company_id}`,
        {
          warehouse_id: warehouseId,
          needed_by: neededBy,
          justification: why,
          lines: lines.map((l) => ({
            variant_id: l.variant_id,
            description: l.description,
            qty: l.qty || '0',
            note: l.note,
          })),
        },
      );
      router.push(`/buying/requisitions/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.req.newTitle')}
        description={t('nx.req.newSubtitle')}
      />

      <FormError message={error} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <Panel title={t('nx.req.what')}>
            <div className="flex flex-wrap items-end gap-3">
              <Field label={t('nx.npo.pick')} hint={t('nx.npo.pickHint')} className="min-w-56 flex-1">
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
                <p className="py-4 text-body text-muted">{t('nx.npo.searchEmpty')}</p>
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
                      </p>
                    </div>
                    {chosen.has(row.variant_id) ? (
                      <span className="text-caption text-subtle">{t('nx.npo.added')}</span>
                    ) : (
                      <Button variant="secondary" size="sm" onClick={() => add(row)}>
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
                      <p className="text-body text-fg">{line.description}</p>
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={t('nx.npo.remove')}
                        onClick={() => setLines((c) => c.filter((_, j) => j !== i))}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    </div>
                    <div className="mt-2 grid gap-3 sm:grid-cols-2">
                      <Field label={t('nx.req.colQty')}>
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
                      <Field label={t('nx.req.colNote')}>
                        <Input
                          value={line.note}
                          onChange={(e) =>
                            setLines((c) =>
                              c.map((l, j) => (j === i ? { ...l, note: e.target.value } : l)),
                            )
                          }
                        />
                      </Field>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
        </div>

        <div className="flex flex-col gap-6">
          <Panel>
            <div className="flex flex-col gap-4">
              <Field label={t('nx.req.deliverTo')} error={fieldErrors.warehouse_id} required>
                <Select value={warehouseId} onChange={(e) => setWarehouseId(e.target.value)}>
                  <option value="">{t('nx.req.chooseWarehouse')}</option>
                  {(warehouses.data?.data ?? []).map((w) => (
                    <option key={w.id} value={w.id}>
                      {`${w.code} · ${w.name}`}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label={t('nx.req.neededBy')} error={fieldErrors.needed_by}>
                <Input
                  type="date"
                  value={neededBy}
                  onChange={(e) => setNeededBy(e.target.value)}
                />
              </Field>
              <Field label={t('nx.req.why')} hint={t('nx.req.whyHint')}>
                <Textarea
                  value={why}
                  onChange={(e) => setWhy(e.target.value)}
                  placeholder={t('nx.req.whyPlaceholder')}
                  rows={3}
                />
              </Field>
            </div>
          </Panel>

          <div>
            <Button variant="primary" busy={busy} className="w-full" onClick={() => void send()}>
              {t('nx.req.send')}
            </Button>
            <p className="mt-2 text-caption text-muted">{t('nx.req.sendHint')}</p>
          </div>
        </div>
      </div>
    </>
  );
}

export default function NewRequisitionPage() {
  return (
    <RequirePermission anyOf={['purchasing.request']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewRequisitionScreen />
      </Suspense>
    </RequirePermission>
  );
}
