'use client';

// Brands — the makers a shop stocks.
//
// Flat, unlike categories: a brand has no parent, and a hierarchy of makers is
// not a thing any shop keeps. Retired rather than deleted, for the same reason
// as a department — the foreign key from `product` is ON DELETE RESTRICT, so a
// brand anything has ever carried cannot be removed, and the row shows how
// many products carry it.
//
// Two brands cannot share a name: `brand_name_uq` is on lower(name), so
// "Adidas" and "adidas" are the same maker. The server refuses the second with
// a sentence rather than a constraint name.

import { Tag } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import type { Brand } from '@/lib/catalog/taxonomy';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

export function Brands() {
  const t = useT();
  const scope = useCompanyScope();
  const mayEdit = useGrants().can('catalog.edit');

  const [retired, setRetired] = useUrlFlag('retiredBrands');
  const [editing, setEditing] = useUrlState('brand');
  const [creating, setCreating] = useUrlFlag('newBrand');

  const { data, isLoading, error, refetch } = useApiList<Brand>(
    scope ? '/catalog/brands' : null,
    scope
      ? { ...scope, include_retired: retired ? 'true' : undefined }
      : undefined,
  );

  const rows = data?.data ?? [];
  const open = creating ? null : (rows.find((b) => b.id === editing) ?? null);
  const showForm = mayEdit && (creating || open !== null);

  function close() {
    setCreating(false);
    setEditing('');
  }

  const columns: Column<Brand>[] = [
    {
      key: 'name',
      header: t('nx.arr.colBrand'),
      primary: true,
      cell: (b) => (
        <span className="flex items-center gap-2">
          {b.name}
          {!b.is_active ? <Badge>{t('nx.arr.retired')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'nameAr',
      header: t('nx.arr.colArabic'),
      secondary: true,
      cell: (b) =>
        b.name_ar !== undefined && b.name_ar !== '' ? (
          <span dir="rtl" lang="ar" className="text-muted">
            {b.name_ar}
          </span>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'products',
      header: t('nx.arr.colProducts'),
      numeric: true,
      width: 'w-24',
      headerHint: t('nx.arr.productsHint'),
      cell: (b) => b.product_count,
    },
  ];

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_23rem]">
      <div className="min-w-0">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <Checkbox
            label={t('nx.arr.showRetired')}
            checked={retired}
            onChange={(e) => setRetired(e.target.checked)}
          />
          {mayEdit ? (
            <Button
              variant="primary"
              size="sm"
              onClick={() => {
                setEditing('');
                setCreating(true);
              }}
            >
              {t('nx.arr.newBrand')}
            </Button>
          ) : null}
        </div>

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={3} /> : null}

        {!isLoading && !error && rows.length === 0 ? (
          <EmptyState
            icon={Tag}
            title={t('nx.arr.brandEmptyTitle')}
            description={t('nx.arr.brandEmptyDesc')}
          />
        ) : null}

        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.arr.brandCaption')}
            columns={columns}
            rows={rows}
            rowKey={(b) => b.id}
            isSelected={(b) => b.id === open?.id}
            onOpenRow={
              mayEdit
                ? (b) => {
                    setCreating(false);
                    setEditing(b.id);
                  }
                : undefined
            }
          />
        ) : null}
      </div>

      {showForm ? (
        <BrandForm
          key={open?.id ?? 'new'}
          brand={open}
          onDone={() => {
            void refetch();
            close();
          }}
          onCancel={close}
        />
      ) : null}
    </div>
  );
}

function BrandForm({
  brand,
  onDone,
  onCancel,
}: {
  brand: Brand | null;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [name, setName] = useState(brand?.name ?? '');
  const [nameAr, setNameAr] = useState(brand?.name_ar ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    const body = { name, name_ar: nameAr };
    try {
      if (brand) {
        await api.put(
          `/catalog/brands/${brand.id}?company_id=${scope.company_id}`,
          body,
        );
      } else {
        await api.post(`/catalog/brands?company_id=${scope.company_id}`, body);
      }
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  async function setActive(active: boolean) {
    if (!scope || !brand) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/catalog/brands/${brand.id}/active?company_id=${scope.company_id}`,
        { is_active: active },
      );
      onDone();
    } catch (e) {
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel title={brand ? t('nx.arr.editBrand') : t('nx.arr.newBrand')}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} fields={fieldErrors} />

        <Field name="name" label={t('nx.arr.fName')} error={fieldErrors.name} required>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus={!brand}
          />
        </Field>

        <Field name="name_ar" label={t('nx.arr.colArabic')} hint={t('nx.arr.fArabicHint')}>
          <Input
            value={nameAr}
            onChange={(e) => setNameAr(e.target.value)}
            dir="rtl"
            lang="ar"
          />
        </Field>

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button type="submit" variant="primary" busy={busy}>
            {t('nx.arr.save')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.arr.cancel')}
          </Button>
          {brand ? (
            <Button
              variant="ghost"
              className="ms-auto"
              disabled={busy}
              onClick={() => void setActive(!brand.is_active)}
            >
              {brand.is_active ? t('nx.arr.retire') : t('nx.arr.restore')}
            </Button>
          ) : null}
        </div>
      </form>
    </Panel>
  );
}
