'use client';

// The platform's own sub-processor register, E4.1.
//
// # Why this list is the platform's and not a tenant's
//
// Under PDPL the platform operator is a processor for every client, and the
// third parties it uses are sub-processors for all of them at once. So there is
// one register, kept here, and every tenant reads it to complete their own
// processing record. A tenant cannot add a row: they would be declaring
// something about the platform's suppliers on the platform's behalf.
//
// # Retired rows are shown here and nowhere else
//
// `GET /privacy/subprocessors` is what a tenant reads, and it returns only the
// active rows, because a supplier the platform stopped using does not belong in
// a client's current processing record. The operator maintaining the list needs
// the opposite: without the retired rows, retiring one is indistinguishable
// from deleting it and it can never be brought back.
//
// That is why this screen reads `GET /platform/subprocessors` instead. Until
// this session that route did not exist and the tenant-scoped one was the only
// read there was — so the one person allowed to write this register was the one
// person who could not see it, because they have no tenant to scope the read to.
//
// # There is no delete
//
// `is_active` is the whole vocabulary. A sub-processor that handled client data
// last year is a fact about last year, and a register that can be emptied is
// not evidence of anything.

import { Network } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface Subprocessor {
  id: string;
  name: string;
  purpose: string;
  country: string;
  data_categories: string;
  safeguard?: string;
  dpa_signed_on?: string;
  is_active: boolean;
}

/** A blank row to start from, so "add" and "edit" are one form. */
function blank(): Subprocessor {
  return {
    id: '',
    name: '',
    purpose: '',
    country: '',
    data_categories: '',
    safeguard: '',
    dpa_signed_on: '',
    is_active: true,
  };
}

function SubprocessorsScreen() {
  const t = useT();
  const { data, isLoading, error, refetch } = useApiList<Subprocessor>(
    '/platform/subprocessors',
  );

  const [draft, setDraft] = useState<Subprocessor | null>(null);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const rows = data?.data ?? [];

  async function save() {
    if (!draft) return;
    setBusy(true);
    setSaveError(null);
    setFieldErrors(null);
    try {
      // The route is a single PUT that inserts or updates depending on whether
      // an id came with it, so a blank id is how "new" is expressed.
      await api.put('/platform/subprocessors', {
        ...(draft.id ? { id: draft.id } : {}),
        name: draft.name,
        purpose: draft.purpose,
        country: draft.country,
        data_categories: draft.data_categories,
        safeguard: draft.safeguard || undefined,
        dpa_signed_on: draft.dpa_signed_on || undefined,
        is_active: draft.is_active,
      });
      setDraft(null);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setSaveError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Subprocessor>[] = [
    {
      key: 'name',
      header: t('nx.plat.spName'),
      primary: true,
      cell: (x) => (
        <span className="flex items-center gap-2">
          {x.name}
          {/* A word, not only a colour: status has to survive a colour vision
              deficiency and a monochrome print of this register. */}
          {!x.is_active ? <Badge tone="neutral">{t('nx.plat.spRetired')}</Badge> : null}
        </span>
      ),
    },
    { key: 'purpose', header: t('nx.plat.spPurpose'), cell: (x) => x.purpose },
    {
      key: 'country',
      header: t('nx.plat.spCountry'),
      width: 'w-24',
      cell: (x) => <span className="num uppercase text-muted">{x.country || '—'}</span>,
    },
    {
      key: 'categories',
      header: t('nx.plat.spCategories'),
      secondary: true,
      cell: (x) => x.data_categories || '—',
    },
    {
      key: 'safeguard',
      header: t('nx.plat.spSafeguard'),
      secondary: true,
      cell: (x) =>
        x.safeguard ? (
          x.safeguard
        ) : (
          // An absent safeguard for a supplier outside the country is a real
          // gap in the register, not a cosmetic blank.
          <span className="text-muted">{t('nx.plat.spNoSafeguard')}</span>
        ),
    },
    {
      key: 'dpa',
      header: t('nx.plat.spDpa'),
      width: 'w-32',
      cell: (x) =>
        x.dpa_signed_on ? (
          <time dateTime={x.dpa_signed_on}>{x.dpa_signed_on}</time>
        ) : (
          <Badge tone="caution">{t('nx.plat.spNoDpa')}</Badge>
        ),
    },
    {
      key: 'edit',
      header: t('nx.plat.spEditHeader'),
      width: 'w-24',
      cell: (x) => (
        <Button size="sm" variant="ghost" onClick={() => setDraft({ ...x })}>
          {t('nx.plat.spEdit')}
        </Button>
      ),
    },
  ];

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.plat.spTitle')}
        description={t('nx.plat.spSubtitle')}
        actions={
          !draft ? (
            <Button onClick={() => setDraft(blank())}>{t('nx.plat.spAdd')}</Button>
          ) : null
        }
      />

      {draft ? (
        <Panel
          title={draft.id ? t('nx.plat.spEditTitle') : t('nx.plat.spAddTitle')}
          className="mb-4"
        >
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <FormError message={saveError} fields={fieldErrors} className="mb-4" />
            <div className="grid gap-4 sm:grid-cols-2">
              <Field name="name" label={t('nx.plat.spName')}>
                <Input
                  value={draft.name}
                  onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                  required
                />
              </Field>
              <Field name="purpose" label={t('nx.plat.spPurpose')}>
                <Input
                  value={draft.purpose}
                  onChange={(e) => setDraft({ ...draft, purpose: e.target.value })}
                  required
                />
              </Field>
              <Field
                name="country"
                label={t('nx.plat.spCountry')}
                hint={t('nx.plat.spCountryHint')}
              >
                <Input
                  dir="ltr"
                  value={draft.country}
                  onChange={(e) => setDraft({ ...draft, country: e.target.value })}
                  maxLength={2}
                  required
                />
              </Field>
              <Field
                name="data_categories"
                label={t('nx.plat.spCategories')}
                hint={t('nx.plat.spCategoriesHint')}
              >
                <Input
                  value={draft.data_categories}
                  onChange={(e) =>
                    setDraft({ ...draft, data_categories: e.target.value })
                  }
                  required
                />
              </Field>
              <Field
                name="safeguard"
                label={t('nx.plat.spSafeguard')}
                hint={t('nx.plat.spSafeguardHint')}
              >
                <Input
                  value={draft.safeguard ?? ''}
                  onChange={(e) => setDraft({ ...draft, safeguard: e.target.value })}
                />
              </Field>
              <Field name="dpa_signed_on" label={t('nx.plat.spDpa')}>
                <Input
                  type="date"
                  value={draft.dpa_signed_on ?? ''}
                  onChange={(e) => setDraft({ ...draft, dpa_signed_on: e.target.value })}
                />
              </Field>
            </div>

            <div className="mt-4">
              <Checkbox
                checked={draft.is_active}
                onChange={(e) => setDraft({ ...draft, is_active: e.target.checked })}
                label={t('nx.plat.spActive')}
                hint={t('nx.plat.spActiveHint')}
              />
            </div>

            <div className="mt-6 flex flex-wrap gap-2">
              <Button type="submit" busy={busy} busyLabel={t('nx.plat.spSaving')}>
                {t('nx.plat.spSave')}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setDraft(null)}>
                {t('nx.plat.spCancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={7} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Network}
          title={t('nx.plat.spEmptyTitle')}
          description={t('nx.plat.spEmptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<Subprocessor>
          rows={rows}
          columns={columns}
          rowKey={(x) => x.id}
          caption={t('nx.plat.spCaption')}
        />
      ) : null}
    </>
  );
}

export default function SubprocessorsPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SubprocessorsScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
