'use client';

// Adding a product.
//
// # The button existed and did nothing
//
// `/products` carried a "New product" button with no handler and an empty
// state offering "Add product" with no handler either. `POST /catalog/products`
// was live and uncalled, so a business owner could not add a single item to
// their own catalogue and the product told them twice that they could.
//
// # Only the treatments this country uses
//
// `GET /catalog/tax-treatments` answers what the company's country allows and
// which of those oblige an exemption reason. A form that offered every
// treatment Biz1core has heard of would let somebody choose `zero_rated` in a
// US catalogue and be refused on save with a list it should have shown first.
// The exemption-reason field appears exactly when the chosen treatment needs
// one, which is the server's own rule read from the same answer.
//
// # A product is a heading; variants are what a shop sells
//
// Creating one here does not create anything sellable. The matrix screen on
// the product generates the variants, and this form ends by going there —
// stopping on a list would leave somebody wondering why their new product has
// no barcode and no price.

import { ArrowLeft } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import {
  indented,
  type Brand,
  type Category,
  type TreatmentOptions,
  type Unit,
} from '@/lib/catalog/taxonomy';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';

interface CreatedProduct {
  id: string;
}

function NewProductScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const categories = useApiList<Category>(
    scope ? '/catalog/categories' : null,
    scope ?? undefined,
  );
  const brands = useApiList<Brand>(
    scope ? '/catalog/brands' : null,
    scope ?? undefined,
  );
  const units = useApiList<Unit>(
    scope ? '/catalog/units' : null,
    scope ?? undefined,
  );
  const tax = useApi<TreatmentOptions>(
    scope ? '/catalog/tax-treatments' : null,
    scope ?? undefined,
  );

  const [sku, setSku] = useState('');
  const [name, setName] = useState('');
  const [nameAr, setNameAr] = useState('');
  const [description, setDescription] = useState('');
  const [categoryID, setCategoryID] = useState('');
  const [brandID, setBrandID] = useState('');
  const [unitID, setUnitID] = useState('');
  const [treatment, setTreatment] = useState('');
  const [reason, setReason] = useState('');
  const [trackSerial, setTrackSerial] = useState(false);
  const [trackBatch, setTrackBatch] = useState(false);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const treatments = tax.data?.treatments ?? [];

  // Defaulted once the country's list arrives, rather than to a hard-coded
  // "standard" that is meaningless in a US catalogue.
  useEffect(() => {
    if (treatment === '' && treatments.length > 0) {
      const preferred =
        treatments.find((x) => x.code === 'standard') ?? treatments[0];
      if (preferred) setTreatment(preferred.code);
    }
  }, [treatment, treatments]);

  const chosen = treatments.find((x) => x.code === treatment);
  const needsReason = chosen?.needs_reason ?? false;

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const made = await api.post<CreatedProduct>('/catalog/products', {
        company_id: scope.company_id,
        sku,
        name,
        name_ar: nameAr,
        description,
        category_id: categoryID,
        brand_id: brandID,
        unit_id: unitID,
        tax_treatment: treatment,
        // Sent only where the treatment asks for it: a reason on a standard
        // product is a field the invoice has nowhere to print.
        tax_exemption_reason_code: needsReason ? reason : '',
        track_serial: trackSerial,
        track_batch: trackBatch,
      });
      // Straight to the product, where the variants are generated. A product
      // with no variants is a heading, not something a till can sell.
      router.push(`/products/${made.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.newprod.title')}
        description={t('nx.newprod.subtitle')}
        actions={
          <Button variant="ghost" onClick={() => router.push('/products')}>
            <ArrowLeft aria-hidden className="size-4" />
            {t('nx.newprod.back')}
          </Button>
        }
      />

      <form
        className="grid gap-6 lg:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Panel title={t('nx.newprod.identity')}>
          <div className="flex flex-col gap-4">
            <FormError message={error} fields={fieldErrors} />

            <Field
              name="sku"
              label={t('nx.newprod.fSku')}
              hint={t('nx.newprod.fSkuHint')}
              error={fieldErrors.sku}
              required
            >
              <Input
                className="num"
                value={sku}
                onChange={(e) => setSku(e.target.value)}
                autoFocus
                autoComplete="off"
                spellCheck={false}
              />
            </Field>

            <Field
              name="name"
              label={t('nx.newprod.fName')}
              error={fieldErrors.name}
              required
            >
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </Field>

            <Field
              name="name_ar"
              label={t('nx.newprod.fNameAr')}
              hint={t('nx.newprod.fNameArHint')}
            >
              <Input
                value={nameAr}
                onChange={(e) => setNameAr(e.target.value)}
                dir="rtl"
                lang="ar"
              />
            </Field>

            <Field name="description" label={t('nx.newprod.fDescription')}>
              <Textarea
                rows={3}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </Field>
          </div>
        </Panel>

        <div className="flex flex-col gap-6">
          <Panel
            title={t('nx.newprod.arrangement')}
            description={t('nx.newprod.arrangementHint')}
          >
            <div className="flex flex-col gap-4">
              <Field
                name="category_id"
                label={t('nx.newprod.fCategory')}
                error={fieldErrors.category_id}
              >
                <Select
                  value={categoryID}
                  onChange={(e) => setCategoryID(e.target.value)}
                >
                  <option value="">{t('nx.newprod.none')}</option>
                  {(categories.data?.data ?? [])
                    .filter((c) => c.is_active)
                    .map((c) => (
                      <option key={c.id} value={c.id}>
                        {indented(c)}
                      </option>
                    ))}
                </Select>
              </Field>

              <Field
                name="brand_id"
                label={t('nx.newprod.fBrand')}
                error={fieldErrors.brand_id}
              >
                <Select value={brandID} onChange={(e) => setBrandID(e.target.value)}>
                  <option value="">{t('nx.newprod.none')}</option>
                  {(brands.data?.data ?? [])
                    .filter((b) => b.is_active)
                    .map((b) => (
                      <option key={b.id} value={b.id}>
                        {b.name}
                      </option>
                    ))}
                </Select>
              </Field>

              <Field
                name="unit_id"
                label={t('nx.newprod.fUnit')}
                hint={t('nx.newprod.fUnitHint')}
                error={fieldErrors.unit_id}
              >
                <Select value={unitID} onChange={(e) => setUnitID(e.target.value)}>
                  <option value="">{t('nx.newprod.none')}</option>
                  {(units.data?.data ?? [])
                    .filter((u) => u.is_active)
                    .map((u) => (
                      <option key={u.id} value={u.id}>
                        {u.code} · {u.name}
                      </option>
                    ))}
                </Select>
              </Field>

              <p className="text-caption text-muted">
                {t('nx.newprod.arrangeElsewhere')}{' '}
                <a className="underline" href="/products/arrangement">
                  {t('nx.arr.title')}
                </a>
              </p>
            </div>
          </Panel>

          <Panel
            title={t('nx.newprod.tax')}
            description={
              tax.data
                ? t('nx.newprod.taxFor', { country: tax.data.country })
                : undefined
            }
          >
            <div className="flex flex-col gap-4">
              {tax.error ? (
                // The country's rules could not be read. Saying so is the only
                // honest option: guessing a treatment here would price stock
                // wrongly for as long as nobody noticed.
                <p className="text-caption text-critical-fg" role="alert">
                  {messageFor(tax.error, t)}
                </p>
              ) : null}

              <Field
                name="tax_treatment"
                label={t('nx.newprod.fTreatment')}
                error={fieldErrors.tax_treatment}
                required
              >
                <Select
                  value={treatment}
                  onChange={(e) => setTreatment(e.target.value)}
                  disabled={treatments.length === 0}
                >
                  {treatments.map((x) => (
                    <option key={x.code} value={x.code}>
                      {x.code}
                    </option>
                  ))}
                </Select>
              </Field>

              {needsReason ? (
                <Field
                  name="tax_exemption_reason_code"
                  label={t('nx.newprod.fReason')}
                  hint={t('nx.newprod.fReasonHint')}
                  error={fieldErrors.tax_exemption_reason_code}
                  required
                >
                  <Input
                    className="num"
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </Field>
              ) : null}
            </div>
          </Panel>

          <Panel
            title={t('nx.newprod.tracking')}
            description={t('nx.newprod.trackingHint')}
          >
            <div className="flex flex-col gap-3">
              <Checkbox
                label={t('nx.newprod.fSerial')}
                hint={t('nx.newprod.fSerialHint')}
                checked={trackSerial}
                onChange={(e) => setTrackSerial(e.target.checked)}
              />
              <Checkbox
                label={t('nx.newprod.fBatch')}
                hint={t('nx.newprod.fBatchHint')}
                checked={trackBatch}
                onChange={(e) => setTrackBatch(e.target.checked)}
              />
            </div>
          </Panel>

          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" variant="primary" busy={busy}>
              {t('nx.newprod.save')}
            </Button>
            <Button
              variant="ghost"
              onClick={() => router.push('/products')}
              disabled={busy}
            >
              {t('nx.newprod.cancel')}
            </Button>
          </div>
        </div>
      </form>
    </>
  );
}

export default function NewProductPage() {
  return (
    <RequirePermission anyOf={['catalog.create']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewProductScreen />
      </Suspense>
    </RequirePermission>
  );
}
