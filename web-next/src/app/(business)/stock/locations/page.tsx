'use client';

// Where stock is kept.
//
// # Editing beside the list, because there is no route for one
//
// `/stock/locations` answers with every location and every branch in one call,
// and there is no `GET .../{id}`. A detail page would fetch the whole list to
// render one row of it, so the form opens beside the list and which one is open
// lives in the URL.
//
// # Empty is a fact, not a warning
//
// `holds_stock` is false for a place that exists and has nothing in it. That is
// ordinary for a room that was just added, so it is said quietly rather than
// coloured like a problem.
//
// # Stopping use moves nothing
//
// The route deactivates; it does not empty. Anything still on those shelves
// stays exactly where it is, and saying so is the difference between a settings
// change and somebody thinking they have just written off a store room.

import { Warehouse } from 'lucide-react';
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
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { LOCATION_KIND, type Branch, type StockLocation } from '@/lib/stock/stock';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

interface LocationsPayload {
  data: StockLocation[];
  branches: Branch[];
}

function LocationsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApi<LocationsPayload>(
    scope ? '/stock/locations' : null,
    scope ?? undefined,
  );

  const [editing, setEditing] = useUrlState('edit');
  const [creating, setCreating] = useUrlFlag('new');

  const rows = data?.data ?? [];
  const branches = data?.branches ?? [];
  const open = creating ? null : (rows.find((l) => l.id === editing) ?? null);
  const showForm =
    grants.can('inventory.adjust_stock') &&
    (creating || (editing !== '' && open !== null));

  const columns: Column<StockLocation>[] = [
    {
      key: 'code',
      header: t('nx.loc.colCode'),
      width: 'w-32',
      cell: (l) => <span className="num text-muted">{l.code}</span>,
    },
    {
      key: 'name',
      header: t('nx.loc.colName'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-wrap items-center gap-2">
          {l.name}
          {!l.is_active ? <Badge tone="neutral">{t('nx.loc.inactive')}</Badge> : null}
          {/* Quiet: a room that was just added holds nothing, and that is not
              a problem to solve. */}
          {l.is_active && !l.holds_stock ? (
            <span className="text-caption text-subtle">{t('nx.loc.holdsNothing')}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'kind',
      header: t('nx.loc.colKind'),
      width: 'w-44',
      cell: (l) => {
        const named = LOCATION_KIND[l.kind];
        return named ? t(named) : <span className="num">{l.kind}</span>;
      },
    },
    {
      key: 'branch',
      header: t('nx.loc.colBranch'),
      secondary: true,
      cell: (l) => <span className="text-muted">{l.store || '—'}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.loc.title')}
        description={t('nx.loc.subtitle')}
        actions={
          grants.can('inventory.adjust_stock') ? (
            <Button
              variant="primary"
              onClick={() => {
                setEditing('');
                setCreating(true);
              }}
            >
              {t('nx.loc.add')}
            </Button>
          ) : null
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={4} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Warehouse}
          title={t('nx.loc.emptyTitle')}
          description={t('nx.loc.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem]">
          <div className="min-w-0">
            <DataTable
              caption={t('nx.loc.caption')}
              columns={columns}
              rows={rows}
              rowKey={(l) => l.id}
              onOpenRow={
                grants.can('inventory.adjust_stock')
                  ? (l) => {
                      setCreating(false);
                      setEditing(l.id);
                    }
                  : undefined
              }
            />
          </div>

          {showForm ? (
            <LocationForm
              key={open?.id ?? 'new'}
              location={open}
              branches={branches}
              onSaved={() => {
                void refetch();
                setCreating(false);
                setEditing('');
              }}
              onCancel={() => {
                setCreating(false);
                setEditing('');
              }}
            />
          ) : null}
        </div>
      ) : null}
    </>
  );
}

function LocationForm({
  location,
  branches,
  onSaved,
  onCancel,
}: {
  location: StockLocation | null;
  branches: Branch[];
  onSaved: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [code, setCode] = useState(location?.code ?? '');
  const [name, setName] = useState(location?.name ?? '');
  const [kind, setKind] = useState(location?.kind ?? 'store_room');
  const [branchId, setBranchId] = useState(
    branches.find((b) => b.name === location?.store)?.id ?? branches[0]?.id ?? '',
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function save() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (code.trim() === '') return setError(t('nx.loc.needCode'));
    if (name.trim() === '') return setError(t('nx.loc.needName'));
    if (kind === '') return setError(t('nx.loc.needKind'));

    setBusy(true);
    try {
      const body = { code, name, kind, store_id: branchId };
      if (location) {
        await api.put(
          `/stock/locations/${location.id}?company_id=${scope.company_id}`,
          body,
        );
      } else {
        await api.post(`/stock/locations?company_id=${scope.company_id}`, body);
      }
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function setActive(active: boolean) {
    if (!scope || !location) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/stock/locations/${location.id}/active?company_id=${scope.company_id}`,
        { is_active: active },
      );
      onSaved();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel
      title={
        location ? t('nx.loc.editTitle', { name: location.name }) : t('nx.loc.newTitle')
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

        <Field label={t('nx.loc.fName')} error={fieldErrors.name} required>
          <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus={!location} />
        </Field>

        <Field
          label={t('nx.loc.fCode')}
          hint={t('nx.loc.fCodeHint')}
          error={fieldErrors.code}
          required
        >
          <Input
            value={code}
            onChange={(e) => setCode(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field label={t('nx.loc.fKind')} hint={t('nx.loc.fKindHint')} error={fieldErrors.kind} required>
          <Select value={kind} onChange={(e) => setKind(e.target.value)}>
            {/* `transit` is the system's own and is never chosen by a person:
                it is where stock lives between a dispatch and a receipt. */}
            {(['shop_floor', 'store_room', 'central'] as const).map((k) => (
              <option key={k} value={k}>
                {t(LOCATION_KIND[k]!)}
              </option>
            ))}
          </Select>
        </Field>

        <Field label={t('nx.loc.fBranch')} error={fieldErrors.store_id}>
          <Select value={branchId} onChange={(e) => setBranchId(e.target.value)}>
            <option value="">{t('nx.loc.chooseBranch')}</option>
            {branches.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </Select>
        </Field>

        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="primary" busy={busy}>
            {location ? t('nx.loc.saveChanges') : t('nx.loc.save')}
          </Button>
          <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.loc.cancel')}
          </Button>
        </div>

        {location ? (
          <div className="border-t border-line pt-4">
            <p className="text-caption text-muted">{t('nx.loc.stopHint')}</p>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="mt-2"
              disabled={busy}
              onClick={() => void setActive(!location.is_active)}
            >
              {location.is_active ? t('nx.loc.stopUsing') : t('nx.loc.startUsing')}
            </Button>
          </div>
        ) : null}
      </form>
    </Panel>
  );
}

export default function LocationsPage() {
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <LocationsScreen />
      </Suspense>
    </RequirePermission>
  );
}
