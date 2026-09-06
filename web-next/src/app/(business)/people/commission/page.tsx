'use client';

// Commission schemes — C6.
//
// `GET /commission-rules` and `POST /commission-rules` were live and uncalled.
// A payslip could carry a commission line and nothing in the product could say
// what scheme paid it, or set one up, or stop one.
//
// # A scheme is not edited, it is replaced
//
// There is no PUT, and that is right: a payslip already issued names the
// scheme that paid it, and rewriting a rate in place would change the
// explanation of money already in somebody's bank account. Changing a rate
// means ending the old scheme and starting a new one, which the form does in
// one action and says so.
//
// # Most specific wins, and the screen has to say which that is
//
// `commissionFor` picks one scheme per employee per month, ordered by whether
// it names an employee, then whether it names a store, then by start date. Two
// schemes that look identical in a list can therefore pay different money, so
// every part of a scheme's scope is a column — a list that hid them would be
// read at exactly the moment somebody is trying to work out why a salesperson
// was paid what they were.
//
// # Tiers pay on the whole amount
//
// C6's example is 2% once sales exceed 50,000 in a month. The highest band
// reached applies to everything, not marginally to the excess — which is how
// commission schemes are written and understood, and the opposite would
// surprise the salesperson. The form says it in words above the bands.

import { Percent, Plus, X } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { indented, type Brand, type Category } from '@/lib/catalog/taxonomy';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

interface Store {
  id: string;
  name: string;
}

interface Employee {
  id: string;
  full_name: string;
}

/** One band of a tiered scheme. Both figures are decimal strings, because the
 *  server reads them through a decimal parser and a float would round. */
interface Tier {
  from: string;
  rate: string;
}

interface Scheme {
  id: string;
  name: string;
  is_active: boolean;
  /** "revenue" or "profit". */
  basis: string;
  employee_id?: string;
  store_id?: string;
  category_id?: string;
  brand_id?: string;
  variant_id?: string;
  /** A fraction: "0.02" is two per cent. */
  rate: string;
  /** A JSON array of bands. */
  tiers: string;
  effective_from: string;
  effective_to?: string;
}

/** Decodes a scheme's bands, dropping anything unreadable rather than crashing
 *  the list. The column is jsonb and may hold a shape written by hand. */
function decodeTiers(raw: string): Tier[] {
  try {
    const parsed: unknown = JSON.parse(raw || '[]');
    if (!Array.isArray(parsed)) return [];
    const out: Tier[] = [];
    for (const entry of parsed) {
      if (typeof entry !== 'object' || entry === null) continue;
      const t = entry as Record<string, unknown>;
      const from = typeof t.from === 'string' ? t.from : '';
      const rate = typeof t.rate === 'string' ? t.rate : '';
      if (from === '' || rate === '') continue;
      out.push({ from, rate });
    }
    return out;
  } catch {
    return [];
  }
}

/** A fraction as a percentage, for reading. `Number('')` is 0, so emptiness is
 *  checked rather than relied on falling out of the parse. */
function asPercent(fraction: string): string {
  if (fraction.trim() === '') return '';
  const value = Number(fraction);
  if (!Number.isFinite(value)) return fraction;
  return `${String(Math.round(value * 10000) / 100)}%`;
}

function CommissionScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const mayChange = useGrants().can('payroll.run');

  const [creating, setCreating] = useUrlFlag('newScheme');
  const [replacing, setReplacing] = useUrlState('replace');

  const { data, isLoading, error, refetch } = useApiList<Scheme>(
    scope ? '/commission-rules' : null,
    scope ?? undefined,
  );
  const stores = useApiList<Store>(scope ? '/stores' : null, scope ?? undefined);
  const staff = useApiList<Employee>(
    scope ? '/employees' : null,
    scope ?? undefined,
  );
  const categories = useApiList<Category>(
    scope ? '/catalog/categories' : null,
    scope ?? undefined,
  );
  const brands = useApiList<Brand>(
    scope ? '/catalog/brands' : null,
    scope ?? undefined,
  );

  const rows = data?.data ?? [];
  const from = rows.find((s) => s.id === replacing) ?? null;
  const showForm = mayChange && (creating || from !== null);

  function close() {
    setCreating(false);
    setReplacing('');
  }

  async function setActive(scheme: Scheme, active: boolean) {
    if (!scope) return;
    await api.post(
      `/commission-rules/${scheme.id}/active?company_id=${scope.company_id}`,
      { is_active: active },
    );
    void refetch();
  }

  /** Everything narrowing a scheme, as chips. Two schemes with the same name
   *  and different scopes pay different money. */
  function scopeOf(s: Scheme) {
    const chips: string[] = [];
    if (s.employee_id !== undefined) {
      chips.push(
        (staff.data?.data ?? []).find((e) => e.id === s.employee_id)?.full_name ??
          t('nx.com.oneEmployee'),
      );
    }
    if (s.store_id !== undefined) {
      chips.push(
        (stores.data?.data ?? []).find((x) => x.id === s.store_id)?.name ??
          t('nx.com.oneStore'),
      );
    }
    if (s.category_id !== undefined) {
      chips.push(
        (categories.data?.data ?? []).find((c) => c.id === s.category_id)?.name ??
          t('nx.com.oneCategory'),
      );
    }
    if (s.brand_id !== undefined) {
      chips.push(
        (brands.data?.data ?? []).find((b) => b.id === s.brand_id)?.name ??
          t('nx.com.oneBrand'),
      );
    }
    if (s.variant_id !== undefined) chips.push(t('nx.com.oneProduct'));
    return chips;
  }

  const columns: Column<Scheme>[] = [
    {
      key: 'name',
      header: t('nx.com.colName'),
      primary: true,
      cell: (s) => (
        <span className="flex items-center gap-2">
          {s.name}
          {!s.is_active ? <Badge>{t('nx.com.off')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'rate',
      header: t('nx.com.colRate'),
      numeric: true,
      cell: (s) => {
        const tiers = decodeTiers(s.tiers);
        return (
          <span className="num">
            {tiers.length === 0
              ? asPercent(s.rate)
              : t('nx.com.tieredCount', { count: String(tiers.length) })}
          </span>
        );
      },
    },
    {
      key: 'basis',
      header: t('nx.com.colBasis'),
      cell: (s) => (
        <span className="text-muted">
          {s.basis === 'profit' ? t('nx.com.onProfit') : t('nx.com.onRevenue')}
        </span>
      ),
    },
    {
      key: 'scope',
      header: t('nx.com.colScope'),
      cell: (s) => {
        const chips = scopeOf(s);
        return chips.length === 0 ? (
          <span className="text-muted">{t('nx.com.everything')}</span>
        ) : (
          <span className="flex flex-wrap gap-1">
            {chips.map((c) => (
              <Badge key={c}>{c}</Badge>
            ))}
          </span>
        );
      },
    },
    {
      key: 'when',
      header: t('nx.com.colWhen'),
      secondary: true,
      cell: (s) => (
        <span className="num text-muted">
          {s.effective_to === undefined || s.effective_to === ''
            ? t('nx.com.fromOnly', { from: s.effective_from })
            : t('nx.com.dateRange', { from: s.effective_from, to: s.effective_to })}
        </span>
      ),
    },
  ];

  // Appended rather than spread inline: a `satisfies` expression inside the
  // array literal reads to the i18n coverage checker as untranslated words
  // beside a JSX expression, and the checker is right to be suspicious of that
  // shape even when this one instance is harmless.
  if (mayChange) {
    columns.push({
      key: 'act',
      header: '',
      cell: (s) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => void setActive(s, !s.is_active)}
        >
          {s.is_active ? t('nx.com.switchOff') : t('nx.com.switchOn')}
        </Button>
      ),
    });
  }

  return (
    <>
      <PageHeader title={t('nx.com.title')} description={t('nx.com.subtitle')} />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_26rem]">
        <div className="min-w-0">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
            <p className="text-caption text-muted">{t('nx.com.mostSpecific')}</p>
            {mayChange ? (
              <Button
                variant="primary"
                size="sm"
                onClick={() => {
                  setReplacing('');
                  setCreating(true);
                }}
              >
                {t('nx.com.newScheme')}
              </Button>
            ) : null}
          </div>

          {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
          {isLoading && !data ? <TableSkeleton columns={6} /> : null}

          {!isLoading && !error && rows.length === 0 ? (
            <EmptyState
              icon={Percent}
              title={t('nx.com.emptyTitle')}
              description={t('nx.com.emptyDesc')}
            />
          ) : null}

          {rows.length > 0 ? (
            <DataTable
              caption={t('nx.com.caption')}
              columns={columns}
              rows={rows}
              rowKey={(s) => s.id}
              isSelected={(s) => s.id === from?.id}
              onOpenRow={
                mayChange
                  ? (s) => {
                      setCreating(false);
                      setReplacing(s.id);
                    }
                  : undefined
              }
            />
          ) : null}
        </div>

        {showForm ? (
          <SchemeForm
            key={from?.id ?? 'new'}
            replacing={from}
            stores={stores.data?.data ?? []}
            staff={staff.data?.data ?? []}
            categories={categories.data?.data ?? []}
            brands={brands.data?.data ?? []}
            onDone={() => {
              void refetch();
              close();
            }}
            onCancel={close}
          />
        ) : null}
      </div>
    </>
  );
}

function SchemeForm({
  replacing,
  stores,
  staff,
  categories,
  brands,
  onDone,
  onCancel,
}: {
  replacing: Scheme | null;
  stores: Store[];
  staff: Employee[];
  categories: Category[];
  brands: Brand[];
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [name, setName] = useState(replacing?.name ?? '');
  const [basis, setBasis] = useState(replacing?.basis ?? 'revenue');
  const [rate, setRate] = useState(replacing?.rate ?? '');
  const [employeeID, setEmployeeID] = useState(replacing?.employee_id ?? '');
  const [storeID, setStoreID] = useState(replacing?.store_id ?? '');
  const [categoryID, setCategoryID] = useState(replacing?.category_id ?? '');
  const [brandID, setBrandID] = useState(replacing?.brand_id ?? '');
  const [from, setFrom] = useState(replacing?.effective_from ?? '');
  const [to, setTo] = useState(replacing?.effective_to ?? '');
  const [tiers, setTiers] = useState<Tier[]>(
    replacing ? decodeTiers(replacing.tiers) : [],
  );

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    const banded = tiers.filter((x) => x.from.trim() !== '' && x.rate.trim() !== '');
    try {
      await api.post(`/commission-rules?company_id=${scope.company_id}`, {
        name,
        basis,
        rate,
        employee_id: employeeID,
        store_id: storeID,
        category_id: categoryID,
        brand_id: brandID,
        // Sent as text, because the server reads it as a JSON document.
        tiers: JSON.stringify(banded),
        effective_from: from,
        effective_to: to,
      });

      // Ending the old scheme happens only once the new one is safely saved,
      // so a failure leaves the shop paying the old rate rather than nothing.
      if (replacing && replacing.is_active) {
        await api.post(
          `/commission-rules/${replacing.id}/active?company_id=${scope.company_id}`,
          { is_active: false },
        );
      }
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel title={replacing ? t('nx.com.replaceScheme') : t('nx.com.newScheme')}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} fields={fieldErrors} />

        {replacing ? (
          <p className="rounded-xs border border-line bg-ground px-3 py-2 text-caption text-muted">
            {t('nx.com.replaceExplain')}
          </p>
        ) : null}

        <Field name="name" label={t('nx.com.fName')} error={fieldErrors.name} required>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus
            placeholder={t('nx.com.fNamePlaceholder')}
          />
        </Field>

        <Field name="basis" label={t('nx.com.fBasis')} hint={t('nx.com.fBasisHint')}>
          <Select value={basis} onChange={(e) => setBasis(e.target.value)}>
            <option value="revenue">{t('nx.com.onRevenue')}</option>
            <option value="profit">{t('nx.com.onProfit')}</option>
          </Select>
        </Field>

        <Field
          name="rate"
          label={t('nx.com.fRate')}
          hint={t('nx.com.fRateHint')}
          error={fieldErrors.rate}
          required={tiers.length === 0}
        >
          <Input
            inputMode="decimal"
            className="num"
            value={rate}
            onChange={(e) => setRate(e.target.value)}
            placeholder="0.02"
          />
        </Field>
        {rate.trim() !== '' ? (
          <p className="text-caption text-muted">
            {t('nx.com.rateReads', { percent: asPercent(rate) })}
          </p>
        ) : null}

        <TierBuilder tiers={tiers} onChange={setTiers} />

        <fieldset className="flex flex-col gap-3 border-t border-line pt-4">
          <legend className="sr-only">{t('nx.com.scopeLegend')}</legend>
          <p className="text-caption font-medium">{t('nx.com.scopeLegend')}</p>
          <p className="text-caption text-muted">{t('nx.com.scopeHint')}</p>

          <Field name="employee_id" label={t('nx.com.fEmployee')}>
            <Select
              value={employeeID}
              onChange={(e) => setEmployeeID(e.target.value)}
            >
              <option value="">{t('nx.com.anyEmployee')}</option>
              {staff.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.full_name}
                </option>
              ))}
            </Select>
          </Field>

          <Field name="store_id" label={t('nx.com.fStore')}>
            <Select value={storeID} onChange={(e) => setStoreID(e.target.value)}>
              <option value="">{t('nx.com.anyStore')}</option>
              {stores.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </Select>
          </Field>

          <Field name="category_id" label={t('nx.com.fCategory')}>
            <Select
              value={categoryID}
              onChange={(e) => setCategoryID(e.target.value)}
            >
              <option value="">{t('nx.com.anyCategory')}</option>
              {categories
                .filter((c) => c.is_active)
                .map((c) => (
                  <option key={c.id} value={c.id}>
                    {indented(c)}
                  </option>
                ))}
            </Select>
          </Field>

          <Field name="brand_id" label={t('nx.com.fBrand')}>
            <Select value={brandID} onChange={(e) => setBrandID(e.target.value)}>
              <option value="">{t('nx.com.anyBrand')}</option>
              {brands
                .filter((b) => b.is_active)
                .map((b) => (
                  <option key={b.id} value={b.id}>
                    {b.name}
                  </option>
                ))}
            </Select>
          </Field>
        </fieldset>

        <div className="grid gap-3 border-t border-line pt-4 sm:grid-cols-2">
          <Field
            name="effective_from"
            label={t('nx.com.fFrom')}
            error={fieldErrors.effective_from}
            required
          >
            <Input
              type="date"
              className="num"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            />
          </Field>
          <Field
            name="effective_to"
            label={t('nx.com.fTo')}
            hint={t('nx.com.fToHint')}
            error={fieldErrors.effective_to}
          >
            <Input
              type="date"
              className="num"
              value={to}
              onChange={(e) => setTo(e.target.value)}
            />
          </Field>
        </div>

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button type="submit" variant="primary" busy={busy} disabled={from === ''}>
            {replacing ? t('nx.com.saveReplacement') : t('nx.com.save')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.com.cancel')}
          </Button>
        </div>
      </form>
    </Panel>
  );
}

/**
 * The bands of a tiered scheme.
 *
 * The highest band a month's takings reach applies to the WHOLE amount. Said
 * in words above the rows, because a marginal reading is the natural one and
 * it is not what the engine does.
 */
function TierBuilder({
  tiers,
  onChange,
}: {
  tiers: Tier[];
  onChange: (next: Tier[]) => void;
}) {
  const t = useT();

  function update(index: number, next: Tier) {
    onChange(tiers.map((x, i) => (i === index ? next : x)));
  }

  return (
    <fieldset className="flex flex-col gap-2 border-t border-line pt-4">
      <legend className="sr-only">{t('nx.com.tiersLegend')}</legend>
      <p className="text-caption font-medium">{t('nx.com.tiersLegend')}</p>
      <p className="text-caption text-muted">{t('nx.com.tiersHint')}</p>

      {tiers.map((tier, index) => (
        <div key={index} className="flex flex-wrap items-end gap-2">
          <label className="min-w-0 flex-1">
            <span className="text-caption text-muted">{t('nx.com.tierFrom')}</span>
            <Input
              inputMode="decimal"
              className="num w-full"
              value={tier.from}
              onChange={(e) => update(index, { ...tier, from: e.target.value })}
            />
          </label>
          <label className="min-w-0 flex-1">
            <span className="text-caption text-muted">{t('nx.com.tierRate')}</span>
            <Input
              inputMode="decimal"
              className="num w-full"
              value={tier.rate}
              onChange={(e) => update(index, { ...tier, rate: e.target.value })}
            />
          </label>
          <Button
            variant="ghost"
            size="sm"
            aria-label={t('nx.com.tierRemove')}
            onClick={() => onChange(tiers.filter((_, i) => i !== index))}
          >
            <X aria-hidden className="size-4" />
          </Button>
        </div>
      ))}

      <Button
        variant="ghost"
        size="sm"
        className="self-start"
        onClick={() => onChange([...tiers, { from: '', rate: '' }])}
      >
        <Plus aria-hidden className="size-4" />
        {t('nx.com.tierAdd')}
      </Button>
    </fieldset>
  );
}

export default function CommissionPage() {
  return (
    <RequirePermission anyOf={['payroll.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <CommissionScreen />
      </Suspense>
    </RequirePermission>
  );
}
