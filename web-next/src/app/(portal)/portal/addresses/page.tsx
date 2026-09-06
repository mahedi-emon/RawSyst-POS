'use client';

// Where to deliver — F2's saved addresses.
//
// `PUT /portal/addresses` both creates and updates: it takes the whole address
// and an id when there is one, so the form is the same either way and the
// screen does not have two.
//
// The delete route names the address in the path, and the query names the
// caller — so another customer's id finds nothing rather than deleting
// something. Nothing here relies on that; it is stated because the button
// looks identical to one that would be dangerous in a product built the other
// way.

import { MapPin } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';
import { portalApi } from '@/lib/portal/client';
import { usePortalGuard, usePortalList } from '@/lib/portal/hooks';
import { usePortalSession } from '@/lib/portal/session';
import type { PortalAddress } from '@/lib/portal/types';

const BLANK = {
  id: '',
  label: '',
  line1: '',
  line2: '',
  city: '',
  district: '',
  postcode: '',
  country: '',
  phone: '',
  is_default: false,
};

export default function PortalAddressesPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal');
  const { shop, token } = usePortalSession();

  const { data, isLoading, error, refetch } = usePortalList<PortalAddress>(
    waiting ? null : '/portal/addresses',
  );

  const [form, setForm] = useState<typeof BLANK | null>(null);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const rows = data?.data ?? [];

  async function save() {
    if (!shop || !form) return;
    setBusy(true);
    setSaveError(null);
    setFieldErrors({});
    try {
      await portalApi.put('/portal/addresses', { shop, token }, {
        ...form,
        // An id is what makes this an amendment rather than a new address, and
        // an empty string is not an id.
        ...(form.id === '' ? { id: undefined } : {}),
      });
      setForm(null);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setSaveError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string) {
    if (!shop) return;
    setBusy(true);
    setSaveError(null);
    try {
      await portalApi.del(`/portal/addresses/${id}`, { shop, token });
      void refetch();
    } catch (e) {
      setSaveError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (waiting) return <Skeleton className="h-48" />;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold">{t('nx.sp.addressesTitle')}</h1>
        <Button variant="primary" size="sm" onClick={() => setForm({ ...BLANK })}>
          {t('nx.sp.addAddress')}
        </Button>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <Skeleton className="h-32" /> : null}

      {!isLoading && !error && rows.length === 0 && form === null ? (
        <EmptyState
          icon={MapPin}
          title={t('nx.sp.noAddressesTitle')}
          description={t('nx.sp.noAddressesBody')}
        />
      ) : null}

      {rows.map((a) => (
        <Panel
          key={a.id}
          title={
            <span className="flex items-center gap-2">
              {a.label}
              {a.is_default ? (
                <Badge tone="info">{t('nx.sp.defaultAddress')}</Badge>
              ) : null}
            </span>
          }
          actions={
            <span className="flex gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  setForm({
                    id: a.id,
                    label: a.label,
                    line1: a.line1,
                    line2: a.line2 ?? '',
                    city: a.city ?? '',
                    district: a.district ?? '',
                    postcode: a.postcode ?? '',
                    country: a.country ?? '',
                    phone: a.phone ?? '',
                    is_default: a.is_default,
                  })
                }
              >
                {t('nx.sp.edit')}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() => void remove(a.id)}
              >
                {t('nx.sp.remove')}
              </Button>
            </span>
          }
        >
          <address className="not-italic text-body text-muted">
            {[a.line1, a.line2, a.district, a.city, a.postcode, a.country]
              .filter((part) => part !== undefined && part !== '')
              .join(', ')}
            {a.phone ? (
              <span dir="ltr" className="num block">
                {a.phone}
              </span>
            ) : null}
          </address>
        </Panel>
      ))}

      {form ? (
        <Panel title={form.id === '' ? t('nx.sp.addAddress') : t('nx.sp.editAddress')}>
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <FormError message={saveError} fields={fieldErrors} />

            <Field
              name="label"
              label={t('nx.sp.fLabel')}
              hint={t('nx.sp.fLabelHint')}
              error={fieldErrors.label}
              required
            >
              <Input
                value={form.label}
                onChange={(e) => setForm({ ...form, label: e.target.value })}
                autoFocus
              />
            </Field>

            <Field name="line1" label={t('nx.sp.fLine1')} error={fieldErrors.line1} required>
              <Input
                value={form.line1}
                onChange={(e) => setForm({ ...form, line1: e.target.value })}
              />
            </Field>

            <Field name="line2" label={t('nx.sp.fLine2')}>
              <Input
                value={form.line2}
                onChange={(e) => setForm({ ...form, line2: e.target.value })}
              />
            </Field>

            <div className="grid gap-3 sm:grid-cols-2">
              <Field name="district" label={t('nx.sp.fDistrict')}>
                <Input
                  value={form.district}
                  onChange={(e) => setForm({ ...form, district: e.target.value })}
                />
              </Field>
              <Field name="city" label={t('nx.sp.fCity')}>
                <Input
                  value={form.city}
                  onChange={(e) => setForm({ ...form, city: e.target.value })}
                />
              </Field>
              <Field name="postcode" label={t('nx.sp.fPostcode')}>
                <Input
                  className="num"
                  dir="ltr"
                  value={form.postcode}
                  onChange={(e) => setForm({ ...form, postcode: e.target.value })}
                />
              </Field>
              <Field name="phone" label={t('nx.sp.fPhone')}>
                <Input
                  type="tel"
                  dir="ltr"
                  className="num"
                  value={form.phone}
                  onChange={(e) => setForm({ ...form, phone: e.target.value })}
                />
              </Field>
            </div>

            <Checkbox
              label={t('nx.sp.fDefault')}
              checked={form.is_default}
              onChange={(e) => setForm({ ...form, is_default: e.target.checked })}
            />

            <div className="flex flex-wrap gap-2 border-t border-line pt-4">
              <Button type="submit" variant="primary" busy={busy}>
                {t('nx.sp.save')}
              </Button>
              <Button variant="ghost" onClick={() => setForm(null)} disabled={busy}>
                {t('nx.sp.cancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}
    </div>
  );
}
