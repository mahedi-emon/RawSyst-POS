'use client';

// What the business says about itself.
//
// # A field settles; it is not simply editable or not
//
// The server sends `settled` — a map from field name to the sentence saying why
// that field can no longer change — and absence from the map means still
// editable. The distinction is a fact about what the business has already done
// rather than a property of the field: `base_currency` is perfectly changeable
// on the day a company is created and settles the moment a journal entry
// exists, because changing it then would restate the books rather than convert
// them.
//
// So a settled field is rendered fixed WITH the server's own reason beside it.
// Not a disabled box: a greyed input with no explanation reads as a fault, and
// the explanation is the useful half. `amendment()` also drops settled fields
// before sending, so the screen never collects a 409 it has already explained.
//
// # Saving one section cannot blank another
//
// The amendment is partial — an absent field is left alone, an empty string
// clears it — which is what makes it safe for this screen to save the identity
// fields without mentioning the tax ones.
//
// # The missing disclosures are the server's list
//
// `GET /privacy/disclosure` says which E5 disclosures are absent, in its own
// field names, and the compliance dashboard reads the same list. Two lists would
// let this screen and that one disagree about whether the business can trade
// online. The ones that live on the company record rather than here are named
// separately — telling somebody to fill in a box that does not exist is worse
// than saying where the number actually lives.
//
// # Four documents, one at a time
//
// The stationery differs per document type: a credit note says something a
// standard invoice does not. Tabs rather than four stacked forms, because
// nobody edits all four at once and a page of thirty-two boxes is a page
// nobody finishes.

import { Suspense, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { Tabs, TabPanel } from '@/components/ui/tabs';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';

import { LogoPanel } from './logo';
import {
  addressLine,
  amendment,
  DOC_TYPES,
  invoiceBlock,
  isSettled,
  settledReason,
  splitMissing,
  type Branch,
  type Business,
  type Disclosure,
  type DocType,
  type DocumentTemplate,
} from '@/lib/settings/business';
import { useUrlState } from '@/lib/url-state';
import { cn } from '@/lib/utils';

const DOC_LABEL: Record<string, Key> = {
  standard: 'nx.biz.docStandard',
  simplified: 'nx.biz.docSimplified',
  credit_note: 'nx.biz.docCreditNote',
  debit_note: 'nx.biz.docDebitNote',
};

/** A read-only pair. Label above, so the eye reads what before which. */
function Fact({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <dt className="text-label text-muted">{label}</dt>
      <dd className="mt-0.5 text-body text-fg">{value || '—'}</dd>
    </div>
  );
}

/**
 * One business field: a box while it can still change, a fact once it cannot.
 *
 * A settled field is NOT rendered as a disabled input. A greyed box with no
 * explanation reads as a fault in the product, and the explanation is the
 * useful half — so the value is shown plainly with the server's own sentence
 * under it, in the server's words, because a reworded account of why the books
 * cannot be restated is one that has stopped matching the rule it describes.
 */
function Amendable({
  business,
  field,
  label,
  value,
  error,
  disabled,
  numeric,
  rtl,
  onChange,
}: {
  business: Business;
  field: string;
  label: string;
  value: string;
  error?: string;
  disabled?: boolean;
  numeric?: boolean;
  rtl?: boolean;
  onChange: (next: string) => void;
}) {
  const reason = settledReason(business, field);
  if (reason !== null || isSettled(business, field)) {
    return (
      <div>
        <p className="text-label text-muted">{label}</p>
        <p className={cn('mt-0.5 text-body text-fg', numeric && 'num')}>
          {value || '—'}
        </p>
        <p className="mt-1 max-w-prose text-caption text-muted">{reason}</p>
      </div>
    );
  }
  return (
    <Field name={field} label={label} error={error}>
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        dir={rtl ? 'rtl' : undefined}
        className={numeric ? 'num' : undefined}
      />
    </Field>
  );
}

function BusinessScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayEdit = grants.can('identity.edit');
  const mayDisclose = grants.can('privacy.manage');

  // One read for the business AND its branches, so the two cannot be drawn
  // from different instants and disagree about which branch exists.
  const record = useApi<{ business: Business; branches: Branch[] }>(
    scope ? `/companies/${scope.company_id}` : null,
    scope ?? undefined,
  );
  const templates = useApiList<DocumentTemplate>(
    scope ? `/companies/${scope.company_id}/templates` : null,
    scope ?? undefined,
  );
  const privacy = useApi<{ disclosure: Disclosure }>(
    scope && grants.can('privacy.view') ? '/privacy/disclosure' : null,
    scope ?? undefined,
  );

  const [rawDoc, setDoc] = useUrlState('doc', 'standard');
  const doc: DocType = (DOC_TYPES as readonly string[]).includes(rawDoc)
    ? (rawDoc as DocType)
    : 'standard';

  const [draft, setDraft] = useState<DocumentTemplate | null>(null);
  const [identity, setIdentity] = useState<Business | null>(null);
  const [disclosure, setDisclosure] = useState<Disclosure | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState<string | null>(null);

  const business = record.data?.business;
  const branchRows = record.data?.branches ?? [];
  const template = (templates.data?.data ?? []).find((tpl) => tpl.doc_type === doc);

  // The draft follows the tab, so switching document type does not carry one
  // document's footer onto another.
  useEffect(() => {
    if (template) setDraft(template);
  }, [template]);

  useEffect(() => {
    if (privacy.data?.disclosure) setDisclosure(privacy.data.disclosure);
  }, [privacy.data]);

  useEffect(() => {
    if (business) setIdentity(business);
  }, [business]);

  async function saveIdentity() {
    if (!scope || !business || !identity) return;
    // Only what changed, and never a settled field: the amendment is partial,
    // so sending the whole record back would claim every other field is exactly
    // as it was found.
    const changes = amendment(business, identity);
    if (Object.keys(changes).length === 0) {
      setSaved(t('nx.biz.nothingChanged'));
      return;
    }
    setBusy(true);
    setError(null);
    setFieldErrors({});
    setSaved(null);
    try {
      await api.put(`/companies/${scope.company_id}?company_id=${scope.company_id}`, changes);
      setSaved(t('nx.biz.savedIdentity'));
      void record.refetch();
    } catch (e) {
      // A 409 arrives with the reason per field, in the server's words.
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function saveTemplate() {
    if (!scope || !draft) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    setSaved(null);
    try {
      await api.put(
        `/companies/${scope.company_id}/templates/${draft.doc_type}?company_id=${scope.company_id}`,
        {
          header_text: draft.header_text,
          header_text_ar: draft.header_text_ar,
          footer_text: draft.footer_text,
          footer_text_ar: draft.footer_text_ar,
          return_policy: draft.return_policy,
          return_policy_ar: draft.return_policy_ar,
          payment_terms: draft.payment_terms,
          payment_terms_ar: draft.payment_terms_ar,
          show_logo: draft.show_logo,
          show_tax_number: draft.show_tax_number,
        },
      );
      setSaved(t('nx.biz.savedStationery'));
      void templates.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function saveDisclosure() {
    if (!scope || !disclosure) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    setSaved(null);
    try {
      await api.put(`/privacy/disclosure?company_id=${scope.company_id}`, {
        registration_ref: disclosure.registration_ref ?? '',
        registration_channel: disclosure.registration_channel ?? '',
        verification_badge_url: disclosure.verification_badge_url ?? '',
        return_policy: disclosure.return_policy ?? '',
        return_policy_ar: disclosure.return_policy_ar ?? '',
        delivery_terms: disclosure.delivery_terms ?? '',
        delivery_terms_ar: disclosure.delivery_terms_ar ?? '',
        contact_email: disclosure.contact_email ?? '',
        contact_phone: disclosure.contact_phone ?? '',
        support_hours: disclosure.support_hours ?? '',
        ...(disclosure.cooling_off_days !== undefined
          ? { cooling_off_days: disclosure.cooling_off_days }
          : {}),
      });
      setSaved(t('nx.biz.savedDisclosure'));
      void privacy.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const branchColumns: Column<Branch>[] = [
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
      width: 'w-56',
      cell: (b) => {
        const blocked = invoiceBlock(b);
        if (blocked.length === 0) {
          return <Badge tone="positive">{t('nx.biz.canInvoice')}</Badge>;
        }
        return (
          <span className="flex flex-col gap-1">
            <Badge tone="critical">{t('nx.biz.cannotInvoice')}</Badge>
            <span className="num text-caption text-critical-fg">
              {blocked.join(', ')}
            </span>
          </span>
        );
      },
    },
    {
      key: 'phone',
      header: t('nx.biz.colPhone'),
      secondary: true,
      width: 'w-40',
      cell: (b) => <span className="num text-muted">{b.phone || '—'}</span>,
    },
    {
      key: 'state',
      header: t('nx.biz.colState'),
      width: 'w-32',
      cell: (b) =>
        b.is_active ? (
          <Badge tone="positive">{t('nx.biz.trading')}</Badge>
        ) : (
          <Badge>{t('nx.biz.closed')}</Badge>
        ),
    },
  ];

  const split = disclosure
    ? splitMissing(disclosure, business?.settled ?? {})
    : { here: [], identity: [], fixed: [] };

  return (
    <>
      <PageHeader title={t('nx.biz.title')} description={t('nx.biz.subtitle')} />

      <FormError message={error} fields={fieldErrors} className="mb-4" />
      {saved ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {saved}
        </p>
      ) : null}

      {record.error ? (
        <ErrorState error={record.error} onRetry={() => void record.refetch()} />
      ) : null}

      {business && identity ? (
        <Panel
          className="mb-5"
          title={t('nx.biz.identity')}
          description={t('nx.biz.identityHint')}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Amendable
              business={business}
              field="legal_name"
              label={t('nx.biz.legalName')}
              value={identity.legal_name}
              error={fieldErrors.legal_name}
              disabled={!mayEdit}
              onChange={(v) => setIdentity({ ...identity, legal_name: v })}
            />
            <Amendable
              business={business}
              field="legal_name_ar"
              label={t('nx.biz.legalNameAr')}
              value={identity.legal_name_ar}
              error={fieldErrors.legal_name_ar}
              disabled={!mayEdit}
              rtl
              onChange={(v) => setIdentity({ ...identity, legal_name_ar: v })}
            />
            <Amendable
              business={business}
              field="trade_name"
              label={t('nx.biz.tradeName')}
              value={identity.trade_name}
              error={fieldErrors.trade_name}
              disabled={!mayEdit}
              onChange={(v) => setIdentity({ ...identity, trade_name: v })}
            />
            <Amendable
              business={business}
              field="country"
              label={t('nx.biz.market')}
              value={business.market_name || identity.country.toUpperCase()}
              error={fieldErrors.country}
              disabled={!mayEdit}
              onChange={(v) => setIdentity({ ...identity, country: v })}
            />
            <Amendable
              business={business}
              field="base_currency"
              label={t('nx.biz.baseCurrency')}
              value={identity.base_currency}
              error={fieldErrors.base_currency}
              disabled={!mayEdit}
              numeric
              onChange={(v) => setIdentity({ ...identity, base_currency: v })}
            />
            <Amendable
              business={business}
              field="timezone"
              label={t('nx.biz.timezone')}
              value={identity.timezone}
              error={fieldErrors.timezone}
              disabled={!mayEdit}
              onChange={(v) => setIdentity({ ...identity, timezone: v })}
            />
            <Amendable
              business={business}
              field="cr_number"
              label={t('nx.biz.crNumber')}
              value={identity.cr_number}
              error={fieldErrors.cr_number}
              disabled={!mayEdit}
              numeric
              onChange={(v) => setIdentity({ ...identity, cr_number: v })}
            />
            <Amendable
              business={business}
              field="vat_number"
              label={t('nx.biz.taxNumber')}
              value={identity.vat_number}
              error={fieldErrors.vat_number}
              disabled={!mayEdit}
              numeric
              onChange={(v) => setIdentity({ ...identity, vat_number: v })}
            />
            <Amendable
              business={business}
              field="costing_method"
              label={t('nx.biz.costingMethod')}
              value={identity.costing_method}
              error={fieldErrors.costing_method}
              disabled={!mayEdit}
              onChange={(v) => setIdentity({ ...identity, costing_method: v })}
            />
          </div>

          {mayEdit ? (
            <div className="mt-4">
              <Button
                variant="primary"
                disabled={busy}
                onClick={() => void saveIdentity()}
              >
                {t('nx.biz.saveIdentity')}
              </Button>
            </div>
          ) : null}
        </Panel>
      ) : null}

      <Panel
        flush
        className="mb-5"
        title={t('nx.biz.branches')}
        description={t('nx.biz.branchesHint')}
      >
        {branchRows.length === 0 ? (
          <div className="p-4">
            <EmptyState
              title={t('nx.biz.noBranchesTitle')}
              description={t('nx.biz.noBranchesDesc')}
            />
          </div>
        ) : (
          <DataTable
            caption={t('nx.biz.branches')}
            columns={branchColumns}
            rows={branchRows}
            rowKey={(b) => b.id}
            className="rounded-none border-0"
          />
        )}
      </Panel>

      {/* --- the stationery ------------------------------------------- */}
      <h2 className="mt-8 mb-1 text-card-title font-semibold text-fg">
        {t('nx.biz.stationery')}
      </h2>
      {/* The logo above the templates, because the template below carries a
          "print the logo" switch and this is where the logo comes from.
          Ticking that switch with nothing on file promised something the shop
          had no way to supply. */}
      {scope ? <LogoPanel companyId={scope.company_id} /> : null}
      <p className="mb-3 max-w-prose text-caption text-muted">
        {t('nx.biz.stationeryHint')}
      </p>
      <Tabs
        label={t('nx.biz.stationery')}
        value={doc}
        onChange={setDoc}
        items={DOC_TYPES.map((d) => ({
          id: d,
          label: t(DOC_LABEL[d] as Key),
          badge:
            (templates.data?.data ?? []).find((tpl) => tpl.doc_type === d)
              ?.configured
              ? undefined
              : t('nx.biz.defaults'),
        }))}
      />
      <TabPanel id={doc}>
        {draft ? (
          <Panel>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field name="header_text" label={t('nx.biz.header')}>
                <Textarea
                  value={draft.header_text}
                  onChange={(e) => setDraft({ ...draft, header_text: e.target.value })}
                  rows={2}
                  disabled={!mayEdit}
                />
              </Field>
              <Field name="header_text_ar" label={t('nx.biz.headerAr')}>
                <Textarea
                  value={draft.header_text_ar}
                  onChange={(e) =>
                    setDraft({ ...draft, header_text_ar: e.target.value })
                  }
                  rows={2}
                  dir="rtl"
                  disabled={!mayEdit}
                />
              </Field>
              <Field name="footer_text" label={t('nx.biz.footer')}>
                <Textarea
                  value={draft.footer_text}
                  onChange={(e) => setDraft({ ...draft, footer_text: e.target.value })}
                  rows={2}
                  disabled={!mayEdit}
                />
              </Field>
              <Field name="footer_text_ar" label={t('nx.biz.footerAr')}>
                <Textarea
                  value={draft.footer_text_ar}
                  onChange={(e) =>
                    setDraft({ ...draft, footer_text_ar: e.target.value })
                  }
                  rows={2}
                  dir="rtl"
                  disabled={!mayEdit}
                />
              </Field>
              <Field name="return_policy" label={t('nx.biz.returnPolicy')}>
                <Textarea
                  value={draft.return_policy}
                  onChange={(e) =>
                    setDraft({ ...draft, return_policy: e.target.value })
                  }
                  rows={2}
                  disabled={!mayEdit}
                />
              </Field>
              <Field name="return_policy_ar" label={t('nx.biz.returnPolicyAr')}>
                <Textarea
                  value={draft.return_policy_ar}
                  onChange={(e) =>
                    setDraft({ ...draft, return_policy_ar: e.target.value })
                  }
                  rows={2}
                  dir="rtl"
                  disabled={!mayEdit}
                />
              </Field>
              <Field name="payment_terms" label={t('nx.biz.paymentTerms')}>
                <Textarea
                  value={draft.payment_terms}
                  onChange={(e) =>
                    setDraft({ ...draft, payment_terms: e.target.value })
                  }
                  rows={2}
                  disabled={!mayEdit}
                />
              </Field>
              <Field name="payment_terms_ar" label={t('nx.biz.paymentTermsAr')}>
                <Textarea
                  value={draft.payment_terms_ar}
                  onChange={(e) =>
                    setDraft({ ...draft, payment_terms_ar: e.target.value })
                  }
                  rows={2}
                  dir="rtl"
                  disabled={!mayEdit}
                />
              </Field>
              <Checkbox
                label={t('nx.biz.showLogo')}
                checked={draft.show_logo}
                disabled={!mayEdit}
                onChange={(e) => setDraft({ ...draft, show_logo: e.target.checked })}
              />
              <Checkbox
                label={t('nx.biz.showTaxNumber')}
                hint={t('nx.biz.showTaxNumberHint')}
                checked={draft.show_tax_number}
                disabled={!mayEdit}
                onChange={(e) =>
                  setDraft({ ...draft, show_tax_number: e.target.checked })
                }
              />
            </div>
            {mayEdit ? (
              <div className="mt-4">
                <Button
                  variant="primary"
                  disabled={busy}
                  onClick={() => void saveTemplate()}
                >
                  {t('nx.biz.saveStationery')}
                </Button>
              </div>
            ) : null}
          </Panel>
        ) : (
          <div className="h-40" aria-busy="true" />
        )}
      </TabPanel>

      {/* --- what the storefront must say ------------------------------ */}
      {disclosure ? (
        <>
          <h2 className="mt-8 mb-1 text-card-title font-semibold text-fg">
            {t('nx.biz.disclosures')}
          </h2>
          <p className="mb-3 max-w-prose text-caption text-muted">
            {t('nx.biz.disclosuresHint')}
          </p>

          {split.here.length + split.identity.length + split.fixed.length > 0 ? (
            <section
              className="mb-4 rounded-md border border-caution/25 bg-caution-subtle p-4"
              aria-labelledby="missing"
            >
              <h3
                id="missing"
                className="text-card-title font-semibold text-caution-fg"
              >
                {t('nx.biz.missingTitle', {
                  count: String(
                    split.here.length + split.identity.length + split.fixed.length,
                  ),
                })}
              </h3>
              {split.here.length > 0 ? (
                <p className="mt-1 max-w-prose text-body text-caution-fg">
                  {t('nx.biz.missingHere')}{' '}
                  <span className="num">{split.here.join(', ')}</span>
                </p>
              ) : null}
              {split.identity.length > 0 ? (
                // On this same screen, in the business form above — not
                // "somewhere else". Sending somebody hunting for a screen that
                // has no such field is worse than saying nothing.
                <p className="mt-2 max-w-prose text-body text-caution-fg">
                  {t('nx.biz.missingIdentity')}{' '}
                  <span className="num">{split.identity.join(', ')}</span>
                </p>
              ) : null}
              {split.fixed.length > 0 ? (
                // Settled, so no form owns it. The server's reason is already
                // rendered beside the field itself, above.
                <p className="mt-2 max-w-prose text-body text-caution-fg">
                  {t('nx.biz.missingFixed')}{' '}
                  <span className="num">{split.fixed.join(', ')}</span>
                </p>
              ) : null}
            </section>
          ) : (
            <p className="mb-4">
              <Badge tone="positive">{t('nx.biz.nothingMissing')}</Badge>
            </p>
          )}

          <Panel>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field name="registration_ref" label={t('nx.biz.registrationRef')}>
                <Input
                  value={disclosure.registration_ref ?? ''}
                  onChange={(e) =>
                    setDisclosure({ ...disclosure, registration_ref: e.target.value })
                  }
                  className="num"
                  disabled={!mayDisclose}
                />
              </Field>
              <Field name="registration_channel" label={t('nx.biz.registrationChannel')}>
                <Input
                  value={disclosure.registration_channel ?? ''}
                  onChange={(e) =>
                    setDisclosure({
                      ...disclosure,
                      registration_channel: e.target.value,
                    })
                  }
                  disabled={!mayDisclose}
                />
              </Field>
              <Field name="contact_email" label={t('nx.biz.contactEmail')}>
                <Input
                  type="email"
                  value={disclosure.contact_email ?? ''}
                  onChange={(e) =>
                    setDisclosure({ ...disclosure, contact_email: e.target.value })
                  }
                  disabled={!mayDisclose}
                />
              </Field>
              <Field name="contact_phone" label={t('nx.biz.contactPhone')}>
                <Input
                  type="tel"
                  value={disclosure.contact_phone ?? ''}
                  onChange={(e) =>
                    setDisclosure({ ...disclosure, contact_phone: e.target.value })
                  }
                  disabled={!mayDisclose}
                />
              </Field>
              <Field name="support_hours" label={t('nx.biz.supportHours')}>
                <Input
                  value={disclosure.support_hours ?? ''}
                  onChange={(e) =>
                    setDisclosure({ ...disclosure, support_hours: e.target.value })
                  }
                  disabled={!mayDisclose}
                />
              </Field>
              <Field
                name="cooling_off_days"
                label={t('nx.biz.coolingOff')}
                hint={t('nx.biz.coolingOffHint')}
              >
                <Input
                  value={
                    disclosure.cooling_off_days === undefined
                      ? ''
                      : String(disclosure.cooling_off_days)
                  }
                  onChange={(e) => {
                    // Blank means "not stated", which is different from a
                    // cooling-off period of zero days.
                    const raw = e.target.value.trim();
                    setDisclosure({
                      ...disclosure,
                      cooling_off_days: raw === '' ? undefined : Number(raw),
                    });
                  }}
                  inputMode="numeric"
                  className="num text-end"
                  disabled={!mayDisclose}
                />
              </Field>
              <Field
                name="return_policy"
                label={t('nx.biz.storeReturnPolicy')}
                className="sm:col-span-2"
              >
                <Textarea
                  value={disclosure.return_policy ?? ''}
                  onChange={(e) =>
                    setDisclosure({ ...disclosure, return_policy: e.target.value })
                  }
                  rows={2}
                  disabled={!mayDisclose}
                />
              </Field>
              <Field
                name="return_policy_ar"
                label={t('nx.biz.storeReturnPolicyAr')}
                className="sm:col-span-2"
              >
                <Textarea
                  value={disclosure.return_policy_ar ?? ''}
                  onChange={(e) =>
                    setDisclosure({ ...disclosure, return_policy_ar: e.target.value })
                  }
                  rows={2}
                  dir="rtl"
                  disabled={!mayDisclose}
                />
              </Field>
              <Field
                name="delivery_terms"
                label={t('nx.biz.deliveryTerms')}
                className="sm:col-span-2"
              >
                <Textarea
                  value={disclosure.delivery_terms ?? ''}
                  onChange={(e) =>
                    setDisclosure({ ...disclosure, delivery_terms: e.target.value })
                  }
                  rows={2}
                  disabled={!mayDisclose}
                />
              </Field>
              <Field
                name="delivery_terms_ar"
                label={t('nx.biz.deliveryTermsAr')}
                className="sm:col-span-2"
              >
                <Textarea
                  value={disclosure.delivery_terms_ar ?? ''}
                  onChange={(e) =>
                    setDisclosure({ ...disclosure, delivery_terms_ar: e.target.value })
                  }
                  rows={2}
                  dir="rtl"
                  disabled={!mayDisclose}
                />
              </Field>
            </div>
            {mayDisclose ? (
              <div className="mt-4">
                <Button
                  variant="primary"
                  disabled={busy}
                  onClick={() => void saveDisclosure()}
                >
                  {t('nx.biz.saveDisclosure')}
                </Button>
              </div>
            ) : null}
          </Panel>
        </>
      ) : null}

      <p className="mt-8 max-w-prose text-caption text-muted">
        {t('nx.biz.marketNote', { market })}
      </p>
    </>
  );
}

export default function BusinessSettingsPage() {
  return (
    <RequirePermission anyOf={['identity.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <BusinessScreen />
      </Suspense>
    </RequirePermission>
  );
}
