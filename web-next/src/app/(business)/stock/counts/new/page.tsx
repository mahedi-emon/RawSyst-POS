'use client';

// Starting a stock count.
//
// # Opening a count is choosing a scope, not entering numbers
//
// `POST /stock/counts` takes a location and optionally a category or a list of
// variants, and answers with every line in that scope already listed and its
// system quantity filled in. Nobody types the product names; they walk the
// shelf with the list the server made.
//
// So this screen is small on purpose. The counting happens on the next one.

import { useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import type { Adjustment, StockLocation } from '@/lib/stock/stock';

function NewCountScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const locations = useApiList<StockLocation>(
    scope ? '/stock/locations' : null,
    scope ?? undefined,
  );

  const [locationId, setLocationId] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function start() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (!locationId) return setError(t('nx.adj.needWhere'));

    setBusy(true);
    try {
      const out = await api.post<Adjustment>(
        `/stock/counts?company_id=${scope.company_id}`,
        { location_id: locationId, note },
      );
      router.push(`/stock/counts/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title={t('nx.cnt.newTitle')} description={t('nx.cnt.subtitle')} />

      <FormError message={error} className="mb-4" />

      <Panel className="max-w-xl">
        <div className="flex flex-col gap-4">
          <Field label={t('nx.adj.where')} error={fieldErrors.location_id} required>
            <Select value={locationId} onChange={(e) => setLocationId(e.target.value)}>
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
          <Field label={t('nx.adj.note')} hint={t('nx.adj.noteHint')}>
            <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={3} />
          </Field>
          <Button variant="primary" busy={busy} onClick={() => void start()}>
            {t('nx.cnt.start')}
          </Button>
        </div>
      </Panel>
    </>
  );
}

export default function NewCountPage() {
  return (
    <RequirePermission anyOf={['inventory.adjust_stock']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewCountScreen />
      </Suspense>
    </RequirePermission>
  );
}
