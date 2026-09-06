'use client';

// Units of measure — how a shop counts what it sells.
//
// # The code does not move
//
// It is what appears on an invoice line. Changing it would rewrite the meaning
// of every document already issued, so the field is fixed once the unit exists
// and the form says why rather than silently disabling it.
//
// # Fractions are the whole point of having units at all
//
// A metre of cloth may be cut in half; a shirt may not. `allows_fraction` is
// what a till reads before letting somebody key 0.5, so it is a first-class
// question on the form rather than an advanced option.

import { Ruler } from 'lucide-react';
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
import type { Unit } from '@/lib/catalog/taxonomy';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

export function Units() {
  const t = useT();
  const scope = useCompanyScope();
  const mayEdit = useGrants().can('catalog.edit');

  const [retired, setRetired] = useUrlFlag('retiredUnits');
  const [editing, setEditing] = useUrlState('unit');
  const [creating, setCreating] = useUrlFlag('newUnit');

  const { data, isLoading, error, refetch } = useApiList<Unit>(
    scope ? '/catalog/units' : null,
    scope
      ? { ...scope, include_retired: retired ? 'true' : undefined }
      : undefined,
  );

  const rows = data?.data ?? [];
  const open = creating ? null : (rows.find((u) => u.id === editing) ?? null);
  const showForm = mayEdit && (creating || open !== null);

  function close() {
    setCreating(false);
    setEditing('');
  }

  const columns: Column<Unit>[] = [
    {
      key: 'code',
      header: t('nx.arr.colCode'),
      width: 'w-24',
      cell: (u) => <span className="num text-muted">{u.code}</span>,
    },
    {
      key: 'name',
      header: t('nx.arr.colUnit'),
      primary: true,
      cell: (u) => (
        <span className="flex items-center gap-2">
          {u.name}
          {u.allows_fraction ? (
            <Badge tone="info">{t('nx.arr.divisible')}</Badge>
          ) : null}
          {!u.is_active ? <Badge>{t('nx.arr.retired')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'products',
      header: t('nx.arr.colProducts'),
      numeric: true,
      width: 'w-24',
      headerHint: t('nx.arr.productsHint'),
      cell: (u) => u.product_count,
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
              {t('nx.arr.newUnit')}
            </Button>
          ) : null}
        </div>

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={3} /> : null}

        {!isLoading && !error && rows.length === 0 ? (
          <EmptyState
            icon={Ruler}
            title={t('nx.arr.unitEmptyTitle')}
            description={t('nx.arr.unitEmptyDesc')}
          />
        ) : null}

        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.arr.unitCaption')}
            columns={columns}
            rows={rows}
            rowKey={(u) => u.id}
            isSelected={(u) => u.id === open?.id}
            onOpenRow={
              mayEdit
                ? (u) => {
                    setCreating(false);
                    setEditing(u.id);
                  }
                : undefined
            }
          />
        ) : null}
      </div>

      {showForm ? (
        <UnitForm
          key={open?.id ?? 'new'}
          unit={open}
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

function UnitForm({
  unit,
  onDone,
  onCancel,
}: {
  unit: Unit | null;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [code, setCode] = useState(unit?.code ?? '');
  const [name, setName] = useState(unit?.name ?? '');
  const [nameAr, setNameAr] = useState(unit?.name_ar ?? '');
  const [fraction, setFraction] = useState(unit?.allows_fraction ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    const body = {
      code,
      name,
      name_ar: nameAr,
      allows_fraction: fraction,
    };
    try {
      if (unit) {
        await api.put(
          `/catalog/units/${unit.id}?company_id=${scope.company_id}`,
          body,
        );
      } else {
        await api.post(`/catalog/units?company_id=${scope.company_id}`, body);
      }
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  async function setActive(active: boolean) {
    if (!scope || !unit) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/catalog/units/${unit.id}/active?company_id=${scope.company_id}`,
        { is_active: active },
      );
      onDone();
    } catch (e) {
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel title={unit ? t('nx.arr.editUnit') : t('nx.arr.newUnit')}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} fields={fieldErrors} />

        <Field
          name="code"
          label={t('nx.arr.colCode')}
          hint={unit ? t('nx.arr.fCodeFixed') : t('nx.arr.fCodeHint')}
          error={fieldErrors.code}
          required={!unit}
        >
          <Input
            className="num"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            disabled={Boolean(unit)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field name="name" label={t('nx.arr.fName')} error={fieldErrors.name} required>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus={!unit}
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

        <Checkbox
          label={t('nx.arr.fFraction')}
          hint={t('nx.arr.fFractionHint')}
          checked={fraction}
          onChange={(e) => setFraction(e.target.checked)}
        />

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button type="submit" variant="primary" busy={busy}>
            {t('nx.arr.save')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.arr.cancel')}
          </Button>
          {unit ? (
            <Button
              variant="ghost"
              className="ms-auto"
              disabled={busy}
              onClick={() => void setActive(!unit.is_active)}
            >
              {unit.is_active ? t('nx.arr.retire') : t('nx.arr.restore')}
            </Button>
          ) : null}
        </div>
      </form>
    </Panel>
  );
}
