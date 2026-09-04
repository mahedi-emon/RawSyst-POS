'use client';

// Asking for stock to be moved.
//
// # Asking is not moving
//
// The button says "ask", because that is what pressing it does. Nothing leaves
// a shelf until somebody else approves it — and it has to be somebody else:
// the server refuses your own, and it says why. The hint says so before the
// press rather than after.
//
// # The list is what is actually in the place it is coming FROM
//
// Filtered on the source location, so somebody cannot ask for stock to be moved
// out of a room it was never in. Changing the source clears the lines, because
// what was chosen no longer necessarily exists there.

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
import { useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { StockLocation, Transfer } from '@/lib/stock/stock';

interface StockRow {
  variant_id: string;
  sku: string;
  product: string;
  on_hand: string;
}

interface Line {
  variant_id: string;
  description: string;
  available: string;
  qty: string;
}

function NewTransferScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const locations = useApiList<StockLocation>(
    scope ? '/stock/locations' : null,
    scope ?? undefined,
  );

  const [fromId, setFromId] = useState('');
  const [toId, setToId] = useState('');
  const [note, setNote] = useState('');
  const [lines, setLines] = useState<Line[]>([]);
  const [term, setTerm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  // What is actually in the place it is coming from.
  const stock = useApiList<StockRow>(
    scope && fromId ? '/stock/on-hand' : null,
    scope
      ? { ...scope, warehouse_id: fromId, search: term || undefined, limit: 25 }
      : undefined,
  );

  const chosen = useMemo(() => new Set(lines.map((l) => l.variant_id)), [lines]);
  const rows = stock.data?.data ?? [];
  const sameBothEnds = fromId !== '' && fromId === toId;
  const places = (locations.data?.data ?? []).filter((l) => l.is_active);

  async function request() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (!fromId || !toId) return setError(t('nx.adj.needWhere'));
    if (sameBothEnds) return setError(t('nx.trf.sameWarning'));
    if (lines.length === 0) return setError(t('nx.adj.needLines'));

    setBusy(true);
    try {
      const out = await api.post<Transfer>(
        `/stock/transfers?company_id=${scope.company_id}`,
        {
          from_location_id: fromId,
          to_location_id: toId,
          note,
          lines: lines
            .filter((l) => l.qty.trim() !== '' && l.qty.trim() !== '0')
            .map((l) => ({ variant_id: l.variant_id, qty: l.qty })),
        },
      );
      router.push(`/stock/transfers/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title={t('nx.trf.newTitle')} description={t('nx.trf.newSubtitle')} />

      <FormError message={error} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <Panel title={t('nx.npo.pick')}>
            {fromId === '' ? (
              <p className="text-body text-muted">{t('nx.adj.chooseWhere')}</p>
            ) : (
              <>
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
                  {stock.isLoading ? <Skeleton className="h-24 w-full" /> : null}
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
                            {t('nx.adj.onHandNow', {
                              qty: formatQuantity(row.on_hand),
                            })}
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
                            onClick={() =>
                              setLines((c) => [
                                ...c,
                                {
                                  variant_id: row.variant_id,
                                  description: `${row.product} · ${row.sku}`,
                                  available: row.on_hand,
                                  qty: '',
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
              </>
            )}
          </Panel>

          <Panel>
            {lines.length === 0 ? (
              <p className="text-body text-muted">{t('nx.req.noneYet')}</p>
            ) : (
              <ul className="flex flex-col divide-y divide-line">
                {lines.map((line, i) => (
                  <li key={line.variant_id} className="py-3 first:pt-0">
                    <div className="flex items-baseline justify-between gap-3">
                      <span className="min-w-0">
                        <span className="block truncate text-body text-fg">
                          {line.description}
                        </span>
                        <span className="text-caption text-muted">
                          {t('nx.adj.onHandNow', {
                            qty: formatQuantity(line.available),
                          })}
                        </span>
                      </span>
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={t('nx.npo.remove')}
                        onClick={() => setLines((c) => c.filter((_, j) => j !== i))}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    </div>
                    <div className="mt-2 max-w-48">
                      <Field label={t('nx.req.colQty')}>
                        <Input
                          value={line.qty}
                          onChange={(e) =>
                            setLines((c) =>
                              c.map((l, j) =>
                                j === i ? { ...l, qty: e.target.value } : l,
                              ),
                            )
                          }
                          inputMode="decimal"
                          autoComplete="off"
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
              <Field label={t('nx.trf.from')} error={fieldErrors.from_location_id} required>
                <Select
                  value={fromId}
                  onChange={(e) => {
                    setFromId(e.target.value);
                    // What was chosen may not be in the new place at all.
                    setLines([]);
                  }}
                >
                  <option value="">{t('nx.adj.chooseWhere')}</option>
                  {places.map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field
                label={t('nx.trf.to')}
                error={
                  sameBothEnds ? t('nx.trf.sameWarning') : fieldErrors.to_location_id
                }
                required
              >
                <Select value={toId} onChange={(e) => setToId(e.target.value)}>
                  <option value="">{t('nx.adj.chooseWhere')}</option>
                  {places.map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label={t('nx.adj.note')}>
                <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={3} />
              </Field>
            </div>
          </Panel>

          <div>
            <Button
              variant="primary"
              busy={busy}
              className="w-full"
              disabled={sameBothEnds}
              onClick={() => void request()}
            >
              {t('nx.trf.request')}
            </Button>
            <p className="mt-2 text-caption text-muted">{t('nx.trf.requestHint')}</p>
          </div>
        </div>
      </div>
    </>
  );
}

export default function NewTransferPage() {
  return (
    <RequirePermission anyOf={['inventory.transfer_stock']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewTransferScreen />
      </Suspense>
    </RequirePermission>
  );
}
