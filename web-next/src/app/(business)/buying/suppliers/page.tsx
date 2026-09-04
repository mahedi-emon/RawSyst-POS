'use client';

// Who the shop buys from.
//
// # Editing happens here, not on a page of its own
//
// There is no `GET /purchasing/suppliers/{id}`. The list carries every field
// the form needs, so a detail route would fetch the whole list to show one row
// of it. The form opens beside the list instead, and which supplier is open
// lives in the URL — so a link still opens the right one, the back button
// closes it, and nothing is fetched twice.
//
// # The code cannot change
//
// `PUT /purchasing/suppliers/{id}` says so plainly: the code is on orders
// already issued. So the field is disabled when editing rather than accepted
// and silently ignored, and it says why.
//
// # What is owed is on the list
//
// Not behind the row. A buyer choosing a supplier needs to know they are
// already forty thousand behind on that account, and a figure they have to go
// and look up is one they will not look up.

import { Truck } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

interface Supplier {
  id: string;
  code: string;
  legal_name: string;
  name_ar?: string;
  contact_name?: string;
  email?: string;
  phone?: string;
  vat_number?: string;
  cr_number?: string;
  country?: string;
  payment_terms_days: number;
  credit_limit?: string;
  notes?: string;
  is_active: boolean;
  /** What is owed right now. Always present, "0.00" when nothing is. */
  outstanding: string;
}

/** The fields the create and update routes both take. */
interface Draft {
  code: string;
  legal_name: string;
  name_ar: string;
  contact_name: string;
  email: string;
  phone: string;
  vat_number: string;
  cr_number: string;
  country: string;
  payment_terms_days: string;
  credit_limit: string;
  notes: string;
}

function draftOf(s: Supplier | null): Draft {
  return {
    code: s?.code ?? '',
    legal_name: s?.legal_name ?? '',
    name_ar: s?.name_ar ?? '',
    contact_name: s?.contact_name ?? '',
    email: s?.email ?? '',
    phone: s?.phone ?? '',
    vat_number: s?.vat_number ?? '',
    cr_number: s?.cr_number ?? '',
    country: s?.country ?? '',
    // A number in a text field, because it is typed and validated as one
    // string and sent as one; parsing it early would only mean formatting it
    // back for the input.
    payment_terms_days: String(s?.payment_terms_days ?? 30),
    credit_limit: s?.credit_limit ?? '',
    notes: s?.notes ?? '',
  };
}

function SuppliersScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const companyId = scope?.company_id ?? null;

  const [inactive, setInactive] = useUrlFlag('inactive');
  const [editing, setEditing] = useUrlState('edit');
  const [creating, setCreating] = useUrlFlag('new');
  // Bumped after a save so the list refetches. The list owns its own data and
  // there is no route that returns one supplier, so this is how a change made
  // in the form reaches the rows beside it.
  const [version, setVersion] = useState(0);
  const [rows, setRows] = useState<Supplier[]>([]);

  const open = creating ? null : (rows.find((s) => s.id === editing) ?? null);
  const showForm = creating || (editing !== '' && open !== null);

  const columns: Column<Supplier>[] = [
    {
      key: 'code',
      header: t('nx.sup.colCode'),
      width: 'w-24',
      // Assigned by the shop and printed on orders; shown as written.
      cell: (s) => <span className="num text-muted">{s.code}</span>,
    },
    {
      key: 'name',
      header: t('nx.sup.colName'),
      primary: true,
      cell: (s) => (
        <span className="flex items-center gap-2">
          {s.legal_name}
          {!s.is_active ? (
            <Badge tone="neutral">{t('nx.sup.inactive')}</Badge>
          ) : null}
        </span>
      ),
    },
    {
      key: 'contact',
      header: t('nx.sup.colContact'),
      secondary: true,
      cell: (s) => <span className="text-muted">{s.contact_name || '—'}</span>,
    },
    {
      key: 'terms',
      header: t('nx.sup.colTerms'),
      secondary: true,
      width: 'w-32',
      cell: (s) =>
        // Zero days is not "0 days"; it is a different arrangement, and saying
        // it in words is how a buyer reads it at a glance.
        s.payment_terms_days === 0 ? (
          t('nx.sup.onDelivery')
        ) : (
          <span className="num">
            {t('nx.sup.days', { count: s.payment_terms_days })}
          </span>
        ),
    },
    {
      key: 'owed',
      header: t('nx.sup.colOwed'),
      numeric: true,
      width: 'w-36',
      cell: (s) =>
        isZero(s.outstanding) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num font-medium">
            {formatMoney(s.outstanding, { currency, market })}
          </span>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.sup.title')}
        description={t('nx.sup.subtitle')}
        actions={
          <Button
            variant="primary"
            onClick={() => {
              setEditing('');
              setCreating(true);
            }}
          >
            {t('nx.sup.add')}
          </Button>
        }
      />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0">
          <ResourceList<Supplier>
            key={version}
            path={companyId ? '/purchasing/suppliers' : null}
            query={{
              company_id: companyId,
              // The route reads the literal string, so it goes only when on.
              include_inactive: inactive ? 'true' : undefined,
            }}
            columns={columns}
            rowKey={(s) => s.id}
            onOpenRow={(s) => {
              setCreating(false);
              setEditing(s.id);
            }}
            onRows={setRows}
            caption={t('nx.sup.caption')}
            searchPlaceholder={t('nx.sup.search')}
            searchLabel={t('nx.sup.searchLabel')}
            noun={t('nx.sup.noun')}
            filters={
              <Checkbox
                label={t('nx.sup.showInactive')}
                checked={inactive}
                onChange={(e) => setInactive(e.target.checked)}
              />
            }
            emptyState={
              <EmptyState
                icon={Truck}
                title={t('nx.sup.emptyTitle')}
                description={t('nx.sup.emptyDesc')}
              />
            }
          />
        </div>

        {showForm ? (
          <SupplierForm
            key={open?.id ?? 'new'}
            companyId={companyId}
            supplier={open}
            onSaved={() => {
              setVersion((v) => v + 1);
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
    </>
  );
}

function SupplierForm({
  companyId,
  supplier,
  onSaved,
  onCancel,
}: {
  companyId: string | null;
  supplier: Supplier | null;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { currency, market } = useCompany();

  const [draft, setDraft] = useState<Draft>(() => draftOf(supplier));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  function set<K extends keyof Draft>(key: K, value: string) {
    setDraft((d) => ({ ...d, [key]: value }));
    // The field stops complaining as soon as it is touched, rather than after
    // a second failed save.
    setFieldErrors((f) => (key in f ? { ...f, [key]: '' } : f));
  }

  async function save() {
    if (!companyId) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const body = {
        ...draft,
        // The server takes an integer here, and an empty box means the
        // default rather than a parse error.
        payment_terms_days: Number(draft.payment_terms_days || '0'),
      };
      if (supplier) {
        await api.put(
          `/purchasing/suppliers/${supplier.id}?company_id=${companyId}`,
          body,
        );
      } else {
        await api.post(`/purchasing/suppliers?company_id=${companyId}`, body);
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
    if (!supplier || !companyId) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/purchasing/suppliers/${supplier.id}/active?company_id=${companyId}`,
        { is_active: active },
      );
      onSaved();
    } catch (e) {
      // The refusal while money is owed arrives here, and it is the whole
      // reason this is not a silent toggle.
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const owed = supplier && !isZero(supplier.outstanding);

  return (
    <Panel
      title={
        supplier
          ? t('nx.sup.editTitle', { name: supplier.legal_name })
          : t('nx.sup.newTitle')
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
          label={t('nx.sup.fName')}
          hint={t('nx.sup.fNameHint')}
          error={fieldErrors.legal_name}
          required
        >
          <Input
            value={draft.legal_name}
            onChange={(e) => set('legal_name', e.target.value)}
            autoFocus={!supplier}
          />
        </Field>

        <Field label={t('nx.sup.fNameAr')}>
          <Input
            value={draft.name_ar}
            onChange={(e) => set('name_ar', e.target.value)}
            dir="rtl"
            lang="ar"
          />
        </Field>

        <Field
          label={t('nx.sup.fCode')}
          hint={supplier ? t('nx.sup.fCodeLocked') : t('nx.sup.fCodeHint')}
          error={fieldErrors.code}
          required={!supplier}
        >
          <Input
            value={draft.code}
            onChange={(e) => set('code', e.target.value)}
            // On orders already sent. Disabled rather than accepted and
            // ignored, which would look like a save that did not stick.
            disabled={Boolean(supplier)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field label={t('nx.sup.fContact')}>
          <Input
            value={draft.contact_name}
            onChange={(e) => set('contact_name', e.target.value)}
          />
        </Field>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('nx.sup.fEmail')} error={fieldErrors.email}>
            <Input
              type="email"
              value={draft.email}
              onChange={(e) => set('email', e.target.value)}
              autoComplete="off"
            />
          </Field>
          <Field label={t('nx.sup.fPhone')}>
            <Input
              type="tel"
              value={draft.phone}
              onChange={(e) => set('phone', e.target.value)}
              autoComplete="off"
            />
          </Field>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('nx.sup.fVat')} error={fieldErrors.vat_number}>
            <Input
              value={draft.vat_number}
              onChange={(e) => set('vat_number', e.target.value)}
              inputMode="numeric"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <Field label={t('nx.sup.fCr')}>
            <Input
              value={draft.cr_number}
              onChange={(e) => set('cr_number', e.target.value)}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
        </div>

        <Field label={t('nx.sup.fCountry')}>
          <Select
            value={draft.country}
            onChange={(e) => set('country', e.target.value)}
          >
            <option value="">—</option>
            <option value="sa">SA</option>
            <option value="bd">BD</option>
            <option value="us">US</option>
          </Select>
        </Field>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label={t('nx.sup.fTerms')}
            hint={t('nx.sup.fTermsHint')}
            error={fieldErrors.payment_terms_days}
          >
            <Input
              value={draft.payment_terms_days}
              onChange={(e) => set('payment_terms_days', e.target.value)}
              inputMode="numeric"
              min={0}
              max={365}
              type="number"
            />
          </Field>
          <Field
            label={t('nx.sup.fLimit')}
            hint={t('nx.sup.fLimitHint')}
            error={fieldErrors.credit_limit}
          >
            <Input
              value={draft.credit_limit}
              onChange={(e) => set('credit_limit', e.target.value)}
              inputMode="decimal"
              autoComplete="off"
            />
          </Field>
        </div>

        <Field label={t('nx.sup.fNotes')}>
          <Textarea
            value={draft.notes}
            onChange={(e) => set('notes', e.target.value)}
            rows={3}
          />
        </Field>

        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="primary" busy={busy}>
            {supplier ? t('nx.sup.saveChanges') : t('nx.sup.save')}
          </Button>
          <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.sup.cancel')}
          </Button>
        </div>

        {supplier ? (
          <div className="border-t border-line pt-4">
            <p className="text-caption text-muted">{t('nx.sup.stopHint')}</p>
            {owed ? (
              <p className="mt-1 text-caption text-caution-fg">
                {t('nx.sup.owedHint', {
                  amount: formatMoney(supplier.outstanding, { currency, market }),
                })}
              </p>
            ) : null}
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="mt-2"
              disabled={busy}
              onClick={() => void setActive(!supplier.is_active)}
            >
              {supplier.is_active
                ? t('nx.sup.stopUsing')
                : t('nx.sup.startUsing')}
            </Button>
          </div>
        ) : null}
      </form>
    </Panel>
  );
}

export default function SuppliersPage() {
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SuppliersScreen />
      </Suspense>
    </RequirePermission>
  );
}
