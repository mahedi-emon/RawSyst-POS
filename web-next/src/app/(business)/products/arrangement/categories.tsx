'use client';

// Departments, and the tree they make.
//
// The list arrives parents-first with a depth on each row, so the indentation
// here is reading a number rather than rebuilding an ancestry. That is
// deliberate on both sides: the server stores `path` and `depth` because a
// category tree is read far more often than it is written.
//
// # Retiring, not deleting
//
// `product_category_id_fkey` is ON DELETE RESTRICT. A department anything has
// ever been filed under cannot be removed, so the row shows how many products
// are in it and the action is "retire" — which the server applies to the whole
// branch, because a subcategory of a closed department is not one anybody may
// file under either. The screen says so before it happens rather than after.

import { FolderTree } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { indented, type Category } from '@/lib/catalog/taxonomy';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

export function Categories() {
  const t = useT();
  const scope = useCompanyScope();
  const mayEdit = useGrants().can('catalog.edit');

  const [retired, setRetired] = useUrlFlag('retired');
  const [editing, setEditing] = useUrlState('cat');
  const [creating, setCreating] = useUrlFlag('newCat');

  const { data, isLoading, error, refetch } = useApiList<Category>(
    scope ? '/catalog/categories' : null,
    scope
      ? { ...scope, include_retired: retired ? 'true' : undefined }
      : undefined,
  );

  const rows = data?.data ?? [];
  const open = creating ? null : (rows.find((c) => c.id === editing) ?? null);
  const showForm = mayEdit && (creating || open !== null);

  function close() {
    setCreating(false);
    setEditing('');
  }

  const columns: Column<Category>[] = [
    {
      key: 'name',
      header: t('nx.arr.colDepartment'),
      primary: true,
      cell: (c) => (
        <span className="flex items-center gap-2">
          {/* The rule characters carry the nesting a native table cannot.
              They are direction-neutral, so Arabic reads correctly. */}
          <span>{indented(c)}</span>
          {!c.is_active ? <Badge>{t('nx.arr.retired')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'nameAr',
      header: t('nx.arr.colArabic'),
      secondary: true,
      cell: (c) =>
        c.name_ar !== undefined && c.name_ar !== '' ? (
          <span dir="rtl" lang="ar" className="text-muted">
            {c.name_ar}
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
      cell: (c) => c.product_count,
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
              {t('nx.arr.newCategory')}
            </Button>
          ) : null}
        </div>

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={3} /> : null}

        {!isLoading && !error && rows.length === 0 ? (
          <EmptyState
            icon={FolderTree}
            title={t('nx.arr.catEmptyTitle')}
            description={t('nx.arr.catEmptyDesc')}
          />
        ) : null}

        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.arr.catCaption')}
            columns={columns}
            rows={rows}
            rowKey={(c) => c.id}
            isSelected={(c) => c.id === open?.id}
            onOpenRow={
              mayEdit
                ? (c) => {
                    setCreating(false);
                    setEditing(c.id);
                  }
                : undefined
            }
          />
        ) : null}
      </div>

      {showForm ? (
        <CategoryForm
          key={open?.id ?? 'new'}
          category={open}
          all={rows}
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

function CategoryForm({
  category,
  all,
  onDone,
  onCancel,
}: {
  category: Category | null;
  all: Category[];
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [name, setName] = useState(category?.name ?? '');
  const [nameAr, setNameAr] = useState(category?.name_ar ?? '');
  const [parent, setParent] = useState(category?.parent_id ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  // A category cannot be filed under itself or under its own subtree, so the
  // branch it heads is removed from the parent picker. The server refuses it
  // too — this is so the option is never offered, not so the rule is enforced
  // here.
  const inOwnBranch = (c: Category): boolean => {
    if (category === null) return false;
    if (c.id === category.id) return true;
    let walk: Category | undefined = c;
    while (walk?.parent_id !== undefined) {
      if (walk.parent_id === category.id) return true;
      const next: Category | undefined = all.find((p) => p.id === walk?.parent_id);
      walk = next;
    }
    return false;
  };

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    const body = { name, name_ar: nameAr, parent_id: parent };
    try {
      if (category) {
        await api.put(
          `/catalog/categories/${category.id}?company_id=${scope.company_id}`,
          body,
        );
      } else {
        await api.post(`/catalog/categories?company_id=${scope.company_id}`, body);
      }
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  async function setActive(active: boolean) {
    if (!scope || !category) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/catalog/categories/${category.id}/active?company_id=${scope.company_id}`,
        { is_active: active },
      );
      onDone();
    } catch (e) {
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  const hasChildren =
    category !== null && all.some((c) => c.parent_id === category.id);

  return (
    <Panel
      title={category ? t('nx.arr.editCategory') : t('nx.arr.newCategory')}
    >
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
            autoFocus={!category}
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

        <Field
          name="parent_id"
          label={t('nx.arr.fParent')}
          hint={t('nx.arr.fParentHint')}
          error={fieldErrors.parent_id}
        >
          <Select value={parent} onChange={(e) => setParent(e.target.value)}>
            <option value="">{t('nx.arr.noParent')}</option>
            {all
              .filter((c) => c.is_active && !inOwnBranch(c))
              .map((c) => (
                <option key={c.id} value={c.id}>
                  {indented(c)}
                </option>
              ))}
          </Select>
        </Field>

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button type="submit" variant="primary" busy={busy}>
            {t('nx.arr.save')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.arr.cancel')}
          </Button>
          {category ? (
            <Button
              variant="ghost"
              className="ms-auto"
              disabled={busy}
              onClick={() => void setActive(!category.is_active)}
            >
              {category.is_active ? t('nx.arr.retire') : t('nx.arr.restore')}
            </Button>
          ) : null}
        </div>

        {category?.is_active && hasChildren ? (
          <p className="text-caption text-muted">{t('nx.arr.retireBranchWarn')}</p>
        ) : null}
      </form>
    </Panel>
  );
}
