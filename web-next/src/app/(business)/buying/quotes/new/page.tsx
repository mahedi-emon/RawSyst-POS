'use client';

// Asking suppliers to quote.
//
// # It usually starts from an approved request
//
// `?requisition=` arrives from the request screen, and the lines come with it —
// somebody has already said what is needed and somebody else has already
// approved it. Typing that list again would be a second chance to get it wrong.
// Raising one from nothing is supported too, for the purchase nobody requested.
//
// # Asking one supplier is not a comparison
//
// The whole module exists to answer "who chose this supplier, and why", and one
// quote cannot answer it. The screen says so rather than refusing, because a
// sole supplier is a real situation and the reason for it belongs in the award.

import { Suspense, useEffect, useMemo, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { RFQ, Requisition } from '@/lib/purchasing/sourcing';

interface Supplier {
  id: string;
  code: string;
  legal_name: string;
  is_active: boolean;
}

interface Warehouse {
  id: string;
  code: string;
  name: string;
}

interface Wanted {
  variant_id: string;
  description: string;
  qty: string;
}

function NewRFQScreen() {
  const t = useT();
  const router = useRouter();
  const params = useSearchParams();
  const scope = useCompanyScope();
  const requisitionID = params.get('requisition') ?? '';

  const suppliers = useApiList<Supplier>(
    scope ? '/purchasing/suppliers' : null,
    scope ?? undefined,
  );
  const warehouses = useApiList<Warehouse>(
    scope ? '/purchasing/warehouses' : null,
    scope ?? undefined,
  );
  const source = useApi<Requisition>(
    scope && requisitionID ? `/purchasing/requisitions/${requisitionID}` : null,
    scope ?? undefined,
  );

  const [warehouseId, setWarehouseId] = useState('');
  const [closesOn, setClosesOn] = useState('');
  const [notes, setNotes] = useState('');
  const [invited, setInvited] = useState<Set<string>>(new Set());
  const [lines, setLines] = useState<Wanted[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  // Carried across from the request that was approved, once it arrives.
  const requisition = source.data;
  useEffect(() => {
    if (!requisition) return;
    setWarehouseId((w) => w || requisition.warehouse_id || '');
    setLines((current) =>
      current.length > 0
        ? current
        : (requisition.lines ?? []).map((l) => ({
            variant_id: l.variant_id ?? '',
            description: l.description,
            qty: l.qty_requested,
          })),
    );
  }, [requisition]);

  const active = useMemo(
    () => (suppliers.data?.data ?? []).filter((s) => s.is_active),
    [suppliers.data],
  );

  function toggle(id: string) {
    setInvited((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function send() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (!warehouseId) return setError(t('nx.rfq.needWarehouse'));
    // The server refuses one: 'a quotation from a single supplier is not a
    // comparison. Raise a purchase order directly instead.' Said here so the
    // buyer learns it while choosing rather than after pressing send.
    if (invited.size < 2) return setError(t('nx.rfq.needTwo'));
    if (lines.length === 0) return setError(t('nx.rfq.needLines'));

    setBusy(true);
    try {
      const out = await api.post<RFQ>(
        `/purchasing/rfqs?company_id=${scope.company_id}`,
        {
          requisition_id: requisitionID || undefined,
          warehouse_id: warehouseId,
          closes_on: closesOn,
          notes,
          supplier_ids: [...invited],
          lines: lines.map((l) => ({
            variant_id: l.variant_id,
            description: l.description,
            qty: l.qty || '0',
          })),
        },
      );
      router.push(`/buying/quotes/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.rfq.newTitle')}
        description={
          requisition
            ? t('nx.rfq.fromRequisition', { number: requisition.requisition_no })
            : t('nx.rfq.newSubtitle')
        }
      />

      <FormError message={error} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <Panel
            title={t('nx.rfq.whoToAsk')}
            actions={
              <span className="text-caption text-muted">
                {t('nx.rfq.chosenCount', { count: invited.size })}
              </span>
            }
          >
            <p className="mb-3 text-caption text-muted">{t('nx.rfq.needTwo')}</p>
            {suppliers.isLoading ? <Skeleton className="h-24 w-full" /> : null}
            <ul className="flex flex-col gap-2">
              {active.map((s) => (
                <li key={s.id}>
                  <Checkbox
                    label={`${s.code} · ${s.legal_name}`}
                    checked={invited.has(s.id)}
                    onChange={() => toggle(s.id)}
                  />
                </li>
              ))}
            </ul>
          </Panel>

          <Panel title={t('nx.rfq.wanted')}>
            {source.isLoading ? <Skeleton className="h-24 w-full" /> : null}
            {lines.length === 0 && !source.isLoading ? (
              <p className="text-body text-muted">{t('nx.req.noneYet')}</p>
            ) : null}
            <ul className="flex flex-col divide-y divide-line">
              {lines.map((l, i) => (
                <li
                  key={l.variant_id || i}
                  className="flex flex-wrap items-center justify-between gap-3 py-2"
                >
                  <span className="min-w-0 truncate text-body text-fg">
                    {l.description}
                  </span>
                  <div className="w-32">
                    <Field label={t('nx.req.colQty')}>
                      <Input
                        value={l.qty}
                        onChange={(e) =>
                          setLines((c) =>
                            c.map((x, j) => (j === i ? { ...x, qty: e.target.value } : x)),
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
            {lines.length > 0 ? (
              <p className="mt-3 text-caption text-muted">
                {/* Quantities only. What it costs is the whole question being
                    put to the suppliers, so nothing here pre-empts it. */}
                {formatQuantity(
                  String(lines.reduce((n, l) => n + Number(l.qty || 0), 0)),
                )}
              </p>
            ) : null}
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
              <Field label={t('nx.rfq.closesOn')} error={fieldErrors.closes_on}>
                <Input
                  type="date"
                  value={closesOn}
                  onChange={(e) => setClosesOn(e.target.value)}
                />
              </Field>
              <Field label={t('nx.rfq.notes')}>
                <Textarea
                  value={notes}
                  onChange={(e) => setNotes(e.target.value)}
                  placeholder={t('nx.rfq.notesPlaceholder')}
                  rows={3}
                />
              </Field>
            </div>
          </Panel>

          <Button variant="primary" busy={busy} className="w-full" onClick={() => void send()}>
            {t('nx.rfq.send')}
          </Button>
        </div>
      </div>
    </>
  );
}

export default function NewRFQPage() {
  return (
    <RequirePermission anyOf={['purchasing.manage_rfq']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewRFQScreen />
      </Suspense>
    </RequirePermission>
  );
}
