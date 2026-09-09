'use client';

// Opening a branch, and correcting one.
//
// # Why this had to be built
//
// The branches table was read-only. `POST /companies/{id}/branches` and
// `PUT .../branches/{branchID}` had existed since I1 and nothing called either,
// so a shop could look at its branches and could not open one or change one —
// and that is not a missing convenience. A Saudi branch cannot issue an invoice
// until its National Address is complete, the till refuses the sale citing
// BR-KSA-09/37/66, and the screen that showed the refusal offered no way to
// answer it. A shop could be blocked from trading by four empty boxes it had no
// form for.
//
// # There is no delete
//
// A branch's code is inside every document number it ever issued, so "we do not
// trade here any more" is `is_active`, not a removed row. The server says the
// same thing and has no DELETE route.
//
// # The address is required to INVOICE, not to exist
//
// A shop opening a branch next month knows its name before it knows its postal
// code. So the form asks for a code and a name and takes the address when there
// is one; what an incomplete address blocks is invoicing, which the row already
// says in the server's own words. Refusing to record the branch at all would
// leave somebody unable to start.
//
// # `code` is fixed once the branch exists
//
// Not because the API refuses it, but because a branch's code is the middle of
// its document numbers. Renaming it silently would leave two ranges of invoices
// claiming to come from different branches. The field is shown as a fact when
// amending, with the reason beside it, the same way the business record shows a
// settled field.

import { Building2, Pencil } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';
import { addressLine, invoiceBlock, type Branch } from '@/lib/settings/business';

/** The editable shape, as the form holds it. Strings throughout. */
interface Draft {
  code: string;
  name: string;
  name_ar: string;
  phone: string;
  is_active: boolean;
  street: string;
  building_number: string;
  additional_number: string;
  district: string;
  city: string;
  postal_code: string;
  country_code: string;
}

function blank(): Draft {
  return {
    code: '',
    name: '',
    name_ar: '',
    phone: '',
    is_active: true,
    street: '',
    building_number: '',
    additional_number: '',
    district: '',
    city: '',
    postal_code: '',
    country_code: '',
  };
}

function draftOf(b: Branch): Draft {
  return {
    code: b.code,
    name: b.name,
    name_ar: b.name_ar ?? '',
    phone: b.phone ?? '',
    is_active: b.is_active,
    street: b.street ?? '',
    building_number: b.building_number ?? '',
    additional_number: b.additional_number ?? '',
    district: b.district ?? '',
    city: b.city ?? '',
    postal_code: b.postal_code ?? '',
    // As STORED, never the effective one. Putting the company's inherited
    // country into this box would save it onto the branch on the next
    // amendment, quietly turning a fallback into a fact.
    country_code: b.country_code ?? '',
  };
}

/**
 * Only what changed.
 *
 * The amendment is partial on the server — an absent field is left alone, an
 * empty string clears it — so sending the whole draft back would claim every
 * untouched field was deliberately re-entered. It also means clearing a box is
 * a real instruction rather than an accident of the form being wide.
 */
function changes(before: Draft, after: Draft): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const key of Object.keys(after) as (keyof Draft)[]) {
    if (before[key] !== after[key]) out[key] = after[key];
  }
  // The code never travels on an amendment: it is fixed once documents carry
  // it, and the form shows it as a fact rather than a box.
  delete out.code;
  return out;
}

export function BranchPanel({
  companyId,
  branches,
  mayEdit,
  onSaved,
}: {
  companyId: string;
  branches: readonly Branch[];
  mayEdit: boolean;
  onSaved: () => void;
}) {
  const t = useT();

  // `null` is closed, `'new'` is opening one, anything else is that branch's
  // id. One state rather than two booleans, because "adding and editing at the
  // same time" is not a state this screen has.
  const [open, setOpen] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft>(blank);
  const [original, setOriginal] = useState<Draft>(blank);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  function begin(b: Branch | null) {
    const d = b ? draftOf(b) : blank();
    setDraft(d);
    setOriginal(d);
    setOpen(b ? b.id : 'new');
    setError(null);
    setFieldErrors({});
  }

  function close() {
    setOpen(null);
    setError(null);
    setFieldErrors({});
  }

  function set<K extends keyof Draft>(key: K, value: Draft[K]) {
    setDraft((d) => ({ ...d, [key]: value }));
  }

  async function save() {
    if (!open) return;
    const creating = open === 'new';
    const body = creating ? { ...draft } : changes(original, draft);
    if (!creating && Object.keys(body).length === 0) {
      close();
      return;
    }

    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const q = `?company_id=${companyId}`;
      if (creating) {
        await api.post(`/companies/${companyId}/branches${q}`, body);
      } else {
        await api.put(`/companies/${companyId}/branches/${open}${q}`, body);
      }
      close();
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Branch>[] = [
    {
      key: 'name',
      header: t('nx.biz.colBranch'),
      primary: true,
      cell: (b) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{b.name}</span>
          <span className="num text-caption text-muted">{b.code}</span>
        </span>
      ),
    },
    {
      key: 'address',
      header: t('nx.biz.colAddress'),
      cell: (b) => (
        <span className="flex flex-col gap-0.5">
          <span className="text-muted">{addressLine(b) || '—'}</span>
          {/* What is PRINTED, which is not always what is stored: the document
              layer falls back to the company's country when a branch has none
              of its own. */}
          {b.effective_country_code ? (
            <span className="num text-caption text-muted">
              {b.effective_country_code}
              {b.country_code ? '' : ` ${t('nx.biz.inherited')}`}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'invoicing',
      header: t('nx.biz.colInvoicing'),
      width: 'w-64',
      cell: (b) => {
        const blocked = invoiceBlock(b);
        if (blocked.length === 0) {
          return <Badge tone="positive">{t('nx.biz.canInvoice')}</Badge>;
        }
        return (
          <span className="flex flex-col gap-1">
            <Badge tone="critical">{t('nx.biz.cannotInvoice')}</Badge>
            {/* The server's own field names. A branch that cannot trade should
                say which four boxes to fill, not merely that something is
                wrong. */}
            <span className="text-caption text-muted">{blocked.join(', ')}</span>
          </span>
        );
      },
    },
    {
      key: 'state',
      header: t('nx.biz.colState'),
      width: 'w-28',
      cell: (b) =>
        b.is_active ? (
          <Badge tone="positive">{t('nx.biz.trading')}</Badge>
        ) : (
          <Badge>{t('nx.biz.closed')}</Badge>
        ),
    },
    {
      key: 'actions',
      header: t('nx.biz.colActions'),
      width: 'w-32',
      cell: (b) =>
        mayEdit ? (
          <Button size="sm" variant="ghost" onClick={() => begin(b)}>
            <Pencil aria-hidden="true" className="size-4" />
            {t('nx.biz.amendBranch')}
          </Button>
        ) : null,
    },
  ];

  const creating = open === 'new';

  return (
    <Panel
      flush
      className="mb-5"
      title={t('nx.biz.branches')}
      description={t('nx.biz.branchesHint')}
      actions={
        mayEdit ? (
          <Button
            variant={open ? 'ghost' : 'primary'}
            onClick={() => (open ? close() : begin(null))}
          >
            {open ? t('nx.biz.cancel') : t('nx.biz.openBranch')}
          </Button>
        ) : null
      }
    >
      {open ? (
        <div className="border-b border-line p-4">
          <FormError message={error} fields={fieldErrors} className="mb-4" />

          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {creating ? (
              <Field
                name="code"
                label={t('nx.biz.branchCode')}
                hint={t('nx.biz.branchCodeHint')}
                error={fieldErrors.code}
                required
              >
                <Input
                  value={draft.code}
                  onChange={(e) => set('code', e.target.value.toUpperCase())}
                  maxLength={16}
                  className="num"
                />
              </Field>
            ) : (
              <div>
                <p className="text-label text-muted">{t('nx.biz.branchCode')}</p>
                <p className="num mt-0.5 text-body text-fg">{draft.code}</p>
                <p className="mt-1 max-w-prose text-caption text-muted">
                  {t('nx.biz.branchCodeSettled')}
                </p>
              </div>
            )}

            <Field
              name="name"
              label={t('nx.biz.branchName')}
              error={fieldErrors.name}
              required
            >
              <Input value={draft.name} onChange={(e) => set('name', e.target.value)} />
            </Field>
            <Field
              name="name_ar"
              label={t('nx.biz.branchNameAr')}
              hint={t('nx.biz.branchNameArHint')}
              error={fieldErrors.name_ar}
            >
              <Input
                value={draft.name_ar}
                onChange={(e) => set('name_ar', e.target.value)}
                dir="rtl"
              />
            </Field>
            <Field name="phone" label={t('nx.biz.branchPhone')} error={fieldErrors.phone}>
              <Input
                type="tel"
                value={draft.phone}
                onChange={(e) => set('phone', e.target.value)}
              />
            </Field>
          </div>

          <h3 className="mt-6 mb-1 text-card-title font-semibold text-fg">
            {t('nx.biz.branchAddress')}
          </h3>
          <p className="mb-3 max-w-prose text-caption text-muted">
            {t('nx.biz.branchAddressHint')}
          </p>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field
              name="building_number"
              label={t('nx.biz.buildingNumber')}
              hint={t('nx.biz.buildingNumberHint')}
              error={fieldErrors.building_number}
            >
              <Input
                value={draft.building_number}
                onChange={(e) => set('building_number', e.target.value)}
                className="num"
              />
            </Field>
            <Field name="street" label={t('nx.biz.street')} error={fieldErrors.street}>
              <Input
                value={draft.street}
                onChange={(e) => set('street', e.target.value)}
              />
            </Field>
            <Field
              name="district"
              label={t('nx.biz.district')}
              error={fieldErrors.district}
            >
              <Input
                value={draft.district}
                onChange={(e) => set('district', e.target.value)}
              />
            </Field>
            <Field name="city" label={t('nx.biz.city')} error={fieldErrors.city}>
              <Input value={draft.city} onChange={(e) => set('city', e.target.value)} />
            </Field>
            <Field
              name="postal_code"
              label={t('nx.biz.postalCode')}
              hint={t('nx.biz.postalCodeHint')}
              error={fieldErrors.postal_code}
            >
              <Input
                value={draft.postal_code}
                onChange={(e) => set('postal_code', e.target.value)}
                className="num"
              />
            </Field>
            <Field
              name="additional_number"
              label={t('nx.biz.additionalNumber')}
              hint={t('nx.biz.additionalNumberHint')}
              error={fieldErrors.additional_number}
            >
              <Input
                value={draft.additional_number}
                onChange={(e) => set('additional_number', e.target.value)}
                className="num"
              />
            </Field>
            <Field
              name="country_code"
              label={t('nx.biz.branchCountry')}
              hint={t('nx.biz.branchCountryHint')}
              error={fieldErrors.country_code}
            >
              <Input
                value={draft.country_code}
                onChange={(e) => set('country_code', e.target.value.toUpperCase())}
                maxLength={2}
                className="num"
              />
            </Field>
          </div>

          {!creating ? (
            <div className="mt-4">
              <Checkbox
                name="is_active"
                label={t('nx.biz.branchTrading')}
                hint={t('nx.biz.branchTradingHint')}
                checked={draft.is_active}
                onChange={(e) => set('is_active', e.target.checked)}
              />
            </div>
          ) : null}

          <div className="mt-5 flex gap-3">
            <Button
              variant="primary"
              disabled={busy || !draft.name.trim() || (creating && !draft.code.trim())}
              onClick={() => void save()}
            >
              {busy
                ? t('nx.biz.saving')
                : creating
                  ? t('nx.biz.openBranch')
                  : t('nx.biz.saveBranch')}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={close}>
              {t('nx.biz.cancel')}
            </Button>
          </div>
        </div>
      ) : null}

      {branches.length === 0 ? (
        <div className="p-4">
          <EmptyState
            icon={Building2}
            title={t('nx.biz.noBranchesTitle')}
            description={t('nx.biz.noBranchesDesc')}
          />
        </div>
      ) : (
        <DataTable
          caption={t('nx.biz.branches')}
          columns={columns}
          rows={branches}
          rowKey={(b) => b.id}
          className="rounded-none border-0"
        />
      )}
    </Panel>
  );
}
