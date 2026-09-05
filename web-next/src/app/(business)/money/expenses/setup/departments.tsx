'use client';

// Departments — the cost centre an expense is booked to.
//
// C3.1 lists a department as the last field an expense stores, and it is a
// DIMENSION of the voucher rather than of the line: the route carries one
// `department_id` for the whole expense, so the recording screen takes one too.
//
// A shop with a single counter has no use for this and should be able to ignore
// it entirely, which is why nothing anywhere requires one.
//
// As with categories, there is no delete. The foreign key refuses to remove a
// department that has been spent against, and retiring it is the honest form of
// what somebody pressing delete actually wants: stop offering it.

import { Building2 } from 'lucide-react';
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
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import type { Department } from '@/lib/money/expenses';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

export function Departments() {
  const t = useT();
  const scope = useCompanyScope();

  const [retired, setRetired] = useUrlFlag('retired');
  const [editing, setEditing] = useUrlState('dept');
  const [creating, setCreating] = useUrlFlag('newDept');

  const { data, isLoading, error, refetch } = useApiList<Department>(
    scope ? '/expenses/departments' : null,
    scope ? { ...scope, include_inactive: retired ? 'true' : undefined } : undefined,
  );

  const rows = data?.data ?? [];
  const open = creating ? null : (rows.find((d) => d.id === editing) ?? null);
  const showForm = creating || (editing !== '' && open !== null);

  function close() {
    setCreating(false);
    setEditing('');
  }

  const columns: Column<Department>[] = [
    {
      key: 'code',
      header: t('nx.expcfg.colCode'),
      width: 'w-28',
      cell: (d) => <span className="num text-muted">{d.code}</span>,
    },
    {
      key: 'name',
      header: t('nx.expcfg.colName'),
      primary: true,
      cell: (d) => (
        <span className="flex items-center gap-2">
          {d.name}
          {!d.is_active ? <Badge>{t('nx.expcfg.retired')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'nameAr',
      header: t('nx.expcfg.nameAr'),
      secondary: true,
      cell: (d) =>
        d.name_ar ? (
          <span dir="rtl" lang="ar" className="text-muted">
            {d.name_ar}
          </span>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
  ];

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_23rem]">
      <div className="min-w-0">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <Checkbox
            label={t('nx.expcfg.showRetired')}
            checked={retired}
            onChange={(e) => setRetired(e.target.checked)}
          />
          <Button
            variant="primary"
            size="sm"
            onClick={() => {
              setEditing('');
              setCreating(true);
            }}
          >
            {t('nx.expcfg.newDepartment')}
          </Button>
        </div>

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={3} /> : null}

        {!isLoading && !error && rows.length === 0 ? (
          <EmptyState
            icon={Building2}
            title={t('nx.expcfg.deptEmptyTitle')}
            description={t('nx.expcfg.deptEmptyDesc')}
          />
        ) : null}

        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.expcfg.deptCaption')}
            columns={columns}
            rows={rows}
            rowKey={(d) => d.id}
            isSelected={(d) => d.id === open?.id}
            onOpenRow={(d) => {
              setCreating(false);
              setEditing(d.id);
            }}
          />
        ) : null}
      </div>

      {showForm ? (
        <DepartmentForm
          key={open?.id ?? 'new'}
          department={open}
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

function DepartmentForm({
  department,
  onDone,
  onCancel,
}: {
  department: Department | null;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [code, setCode] = useState(department?.code ?? '');
  const [name, setName] = useState(department?.name ?? '');
  const [nameAr, setNameAr] = useState(department?.name_ar ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      if (department) {
        // The update statement sets name and name_ar only; the code is what
        // reports are grouped by and does not move.
        await api.put(
          `/expenses/departments/${department.id}?company_id=${scope.company_id}`,
          { name, name_ar: nameAr },
        );
      } else {
        await api.post(`/expenses/departments?company_id=${scope.company_id}`, {
          code,
          name,
          name_ar: nameAr,
        });
      }
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  async function setActive(active: boolean) {
    if (!scope || !department) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/expenses/departments/${department.id}/active?company_id=${scope.company_id}`,
        { active },
      );
      onDone();
    } catch (e) {
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel
      title={
        department ? t('nx.expcfg.editDepartment') : t('nx.expcfg.newDepartment')
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} />

        <Field
          label={t('nx.expcfg.colCode')}
          hint={t('nx.expcfg.headCodeHint')}
          error={fieldErrors.code}
          required={!department}
        >
          <Input
            value={code}
            onChange={(e) => setCode(e.target.value)}
            disabled={Boolean(department)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field label={t('nx.expcfg.colName')} error={fieldErrors.name} required>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus={!department}
          />
        </Field>

        <Field label={t('nx.expcfg.nameAr')} hint={t('nx.expcfg.nameArHint')}>
          <Input
            value={nameAr}
            onChange={(e) => setNameAr(e.target.value)}
            dir="rtl"
            lang="ar"
          />
        </Field>

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button type="submit" variant="primary" busy={busy}>
            {t('nx.expcfg.save')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.expcfg.cancel')}
          </Button>
          {department ? (
            <Button
              variant="ghost"
              className="ms-auto"
              disabled={busy}
              onClick={() => void setActive(!department.is_active)}
            >
              {department.is_active
                ? t('nx.expcfg.retire')
                : t('nx.expcfg.restore')}
            </Button>
          ) : null}
        </div>
      </form>
    </Panel>
  );
}
