'use client';

// Changing stock by hand.
//
// # The reason is a list, and the note is a box
//
// B4 requires a mandatory reason on a write-off. A free text box would give
// "damaged", "Damaged" and "brokn" in the same report, and a report nobody can
// group is a control nobody can run. So the reason is chosen and the note says
// what the list cannot.
//
// # Change by, not count to
//
// The route takes a `delta`. A screen that asked for the new total would have
// to subtract, and the subtraction would be wrong the moment somebody sold one
// while the form was open. Asking for the change is asking for the thing that
// is actually known: two were dropped.
//
// # The uuid is minted here
//
// "So a retry after a lost response returns the original rather than writing
// the stock off twice." Minted when the form opens and replaced when it clears.

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
import {
  ADJUSTMENT_REASONS,
  type Adjustment,
  type StockLocation,
} from '@/lib/stock/stock';

interface StockRow {
  variant_id: string;
  sku: string;
  product: string;
  on_hand: string;
}

interface Line {
  variant_id: string;
  description: string;
  on_hand: string;
  delta: string;
}

function NewAdjustmentScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const locations = useApiList<StockLocation>(
    scope ? '/stock/locations' : null,
    scope ?? undefined,
  );

  const [locationId, setLocationId] = useState('');
  const [reason, setReason] = useState('');
  const [note, setNote] = useState('');
  const [lines, setLines] = useState<Line[]>([]);
  const [term, setTerm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());

  const stock = useApiList<StockRow>(
    scope ? '/stock/on-hand' : null,
    scope
      ? {
          ...scope,
          search: term || undefined,
          warehouse_id: locationId || undefined,
          limit: 25,
        }
      : undefined,
  );

  const chosen = useMemo(() => new Set(lines.map((l) => l.variant_id)), [lines]);
  const rows = stock.data?.data ?? [];

  // A write-off is a loss and a correction is not, so the wording follows the
  // reason rather than sitting on one label for both.
  const isWriteOff = reason !== '' && reason !== 'found' && reason !== 'correction';

  async function post() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (!locationId) return setError(t('nx.adj.needWhere'));
    if (!reason) return setError(t('nx.adj.needReason'));
    if (lines.length === 0) return setError(t('nx.adj.needLines'));

    setBusy(true);
    try {
      const out = await api.post<Adjustment>(
        `/stock/adjustments?company_id=${scope.company_id}`,
        {
          uuid: docUUID,
          location_id: locationId,
          // Wastage and adjustment are the same document with different
          // meanings, and the books treat them differently.
          kind: isWriteOff ? 'wastage' : 'adjustment',
          reason,
          note,
          lines: lines
            .filter((l) => l.delta.trim() !== '' && l.delta.trim() !== '0')
            .map((l) => ({ variant_id: l.variant_id, delta: l.delta })),
        },
      );
      setDocUUID(crypto.randomUUID());
      router.push(`/stock/adjustments/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.adj.newTitle')}
        description={t('nx.adj.newSubtitle')}
      />

      <FormError message={error} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <Panel title={t('nx.npo.pick')}>
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
                              on_hand: row.on_hand,
                              delta: '',
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
                      <span className="min-w-0">
                        <span className="block truncate text-body text-fg">
                          {line.description}
                        </span>
                        <span className="text-caption text-muted">
                          {t('nx.adj.onHandNow', {
                            qty: formatQuantity(line.on_hand),
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
                      <Field
                        label={t('nx.adj.colChange')}
                        hint={i === 0 ? t('nx.adj.changeHint') : undefined}
                      >
                        <Input
                          value={line.delta}
                          onChange={(e) =>
                            setLines((c) =>
                              c.map((l, j) =>
                                j === i ? { ...l, delta: e.target.value } : l,
                              ),
                            )
                          }
                          inputMode="text"
                          autoComplete="off"
                          placeholder="-2"
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
              <Field label={t('nx.adj.where')} error={fieldErrors.location_id} required>
                <Select
                  value={locationId}
                  onChange={(e) => {
                    setLocationId(e.target.value);
                    setLines([]);
                  }}
                >
                  <option value="">{t('nx.adj.chooseWhere')}</option>
                  {(locations.data?.data ?? [])
                    .filter((l) => l.is_active)
                    .map((l) => (
                      <option key={l.id} value={l.id}>
                        {l.name}
                      </option>
                    ))}
                </Select>
              </Field>

              <Field
                label={t('nx.adj.why')}
                hint={t('nx.adj.whyHint')}
                error={fieldErrors.reason}
                required
              >
                <Select value={reason} onChange={(e) => setReason(e.target.value)}>
                  <option value="">{t('nx.adj.chooseWhere')}</option>
                  {ADJUSTMENT_REASONS.map((r) => (
                    <option key={r.value} value={r.value}>
                      {t(r.key)}
                    </option>
                  ))}
                </Select>
              </Field>

              <Field label={t('nx.adj.note')} hint={t('nx.adj.noteHint')}>
                <Textarea
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  rows={3}
                />
              </Field>
            </div>
          </Panel>

          <div>
            <Button
              variant="primary"
              busy={busy}
              className="w-full"
              onClick={() => void post()}
            >
              {t('nx.adj.post')}
            </Button>
            <p className="mt-2 text-caption text-muted">{t('nx.adj.postHint')}</p>
          </div>
        </div>
      </div>
    </>
  );
}

export default function NewAdjustmentPage() {
  return (
    <RequirePermission anyOf={['inventory.adjust_stock']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewAdjustmentScreen />
      </Suspense>
    </RequirePermission>
  );
}
