'use client';

// Setting what a variant costs.
//
// # Why this exists at all
//
// `PUT /catalog/variants/{id}/prices` has been live since it was written and
// nothing in the product ever called it. The product screen showed a price and
// offered no way to change one, so a shop could not put a new price on
// anything it sells — through this product. The route's own comment says the
// point of it was that "a shop could not price its trade customers through the
// product screens"; the route landed and the screen did not.
//
// # Four numbers, not one
//
// Retail is what the public pays. Wholesale and dealer are what the trade and
// a reseller pay, and B12 refuses a wholesale order priced below the minimum —
// so a shop with no wholesale price cannot sell to the trade at all. The floor
// is not a price anybody is charged: it is the lowest a discount at the till
// may take the line, and the POS enforces it.
//
// # Empty is not zero, and only two fields may be empty
//
// Clearing wholesale means "not sold to the trade"; clearing dealer means the
// same. Retail and floor cannot be cleared — the server refuses with the field
// named, and the form does not offer it — because every variant needs a price
// and a floor of nothing would let a discount take a line to zero.

import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';

export interface PriceableVariant {
  id: string;
  sku: string;
  price: string;
  price_wholesale?: string;
  price_dealer?: string;
  price_floor: string;
}

export function PricingPanel({
  variant,
  companyId,
  onSaved,
  onCancel,
}: {
  variant: PriceableVariant;
  companyId: string;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const t = useT();

  const [retail, setRetail] = useState(variant.price);
  const [wholesale, setWholesale] = useState(variant.price_wholesale ?? '');
  const [dealer, setDealer] = useState(variant.price_dealer ?? '');
  const [floor, setFloor] = useState(variant.price_floor);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  async function save() {
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    try {
      // Every field is sent every time, including the empty ones: the server
      // reads an absent field as "leave it alone" and an empty one as "clear
      // it", and a form that omitted a field the user had just emptied would
      // silently keep the old price.
      await api.put(`/catalog/variants/${variant.id}/prices?company_id=${companyId}`, {
        price_retail: retail,
        price_wholesale: wholesale,
        price_dealer: dealer,
        price_floor: floor,
      });
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel title={t('nx.prod.pricingTitle', { sku: variant.sku })} className="mb-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          name="price_retail"
          label={t('nx.prod.priceRetail')}
          hint={t('nx.prod.priceRetailHint')}
          error={fieldErrors?.price_retail}
        >
          <Input
            dir="ltr"
            inputMode="decimal"
            className="num"
            value={retail}
            onChange={(e) => setRetail(e.target.value)}
            required
          />
        </Field>

        <Field
          name="price_floor"
          label={t('nx.prod.priceFloor')}
          hint={t('nx.prod.priceFloorHint')}
          error={fieldErrors?.price_floor}
        >
          <Input
            dir="ltr"
            inputMode="decimal"
            className="num"
            value={floor}
            onChange={(e) => setFloor(e.target.value)}
            required
          />
        </Field>

        <Field
          name="price_wholesale"
          label={t('nx.prod.priceWholesale')}
          hint={t('nx.prod.priceWholesaleHint')}
          error={fieldErrors?.price_wholesale}
        >
          <Input
            dir="ltr"
            inputMode="decimal"
            className="num"
            value={wholesale}
            onChange={(e) => setWholesale(e.target.value)}
          />
        </Field>

        <Field
          name="price_dealer"
          label={t('nx.prod.priceDealer')}
          hint={t('nx.prod.priceDealerHint')}
          error={fieldErrors?.price_dealer}
        >
          <Input
            dir="ltr"
            inputMode="decimal"
            className="num"
            value={dealer}
            onChange={(e) => setDealer(e.target.value)}
          />
        </Field>
      </div>

      {error && <FormError message={error} />}

      <div className="mt-4 flex flex-wrap gap-2">
        <Button variant="primary" onClick={() => void save()} disabled={busy}>
          {t('nx.prod.savePrices')}
        </Button>
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          {t('nx.prod.cancel')}
        </Button>
      </div>
    </Panel>
  );
}
