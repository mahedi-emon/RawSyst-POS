'use client';

// E-invoicing setup, per signing unit.
//
// # The one thing in this product that genuinely needs something from outside
//
// ZATCA issues a compliance certificate only against a one-time password the
// taxpayer reads from their own Fatoora portal. Software cannot generate it and
// fabricating one would be forging a credential. So the screen says that
// plainly — "read the one-time password from your Fatoora portal" — rather than
// presenting it as a fault in the product or a step somebody has skipped.
//
// Everything around it is here and works: the unit, its nine certificate-request
// fields, which environment it is being onboarded into, what stage it has
// reached, and renewal.
//
// # The password is typed and never stored
//
// `POST .../onboarding/compliance` takes it in the body and the server never
// keeps it. The field is `type="password"` and is cleared the moment the
// request returns, whichever way it went: a one-time password left sitting in
// an input on a shop counter is the same exposure as writing it down.
//
// # Three environments, and the difference matters
//
// Sandbox and simulation are for proving the setup works. Production binds the
// business's tax identity. They are a deliberate, explicit choice rather than a
// default, because onboarding into the wrong one produces a till that appears
// to work and reports nothing.

import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  ZATCA_ENVIRONMENTS,
  type EGSUnit,
  type OnboardingStatus,
  type ZatcaEnvironment,
} from '@/lib/tax/compliance';
import { useUrlState } from '@/lib/url-state';

const ENVIRONMENT_LABEL: Record<ZatcaEnvironment, Key> = {
  sandbox: 'nx.einv.sandbox',
  simulation: 'nx.einv.simulation',
  production: 'nx.einv.production',
};

/** The nine fields ZATCA wants on a certificate request. */
const CSR_FIELDS = [
  'common_name',
  'egs_serial_number',
  'organization_identifier',
  'organization_unit',
  'organization_name',
  'country',
  'invoice_type',
  'location',
  'industry',
] as const;

const CSR_LABEL: Record<string, Key> = {
  common_name: 'nx.einv.commonName',
  egs_serial_number: 'nx.einv.serial',
  organization_identifier: 'nx.einv.orgId',
  organization_unit: 'nx.einv.orgUnit',
  organization_name: 'nx.einv.orgName',
  country: 'nx.einv.country',
  invoice_type: 'nx.einv.invoiceType',
  location: 'nx.einv.location',
  industry: 'nx.einv.industry',
};

function UnitPanel({
  unit,
  environment,
  mayOnboard,
  mayManage,
  onAmended,
}: {
  unit: EGSUnit;
  environment: ZatcaEnvironment;
  mayOnboard: boolean;
  mayManage: boolean;
  onAmended: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  // Correcting the unit itself. `PUT /einvoicing/units/{id}` was live and
  // reachable from nothing, so a unit registered with a typo in its serial
  // number or filed against the wrong branch could never be put right - and
  // the nine CSR fields are what ZATCA signs a certificate against, so a wrong
  // one is a unit that cannot be onboarded at all.
  //
  // The architecture is deliberately absent: the route fixes it at creation,
  // and offering a control the service refuses would teach the rule by
  // failure.
  const [amending, setAmending] = useState(false);
  const [label, setLabel] = useState(unit.label);
  const [csrDraft, setCsrDraft] = useState<Record<string, string>>({});

  const status = useApi<OnboardingStatus>(
    scope ? `/einvoicing/units/${unit.id}/onboarding` : null,
    scope ? { ...scope, environment } : undefined,
  );

  const [csr, setCSR] = useState('');
  const [otp, setOTP] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  async function act(path: string, body: Record<string, unknown>) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      await api.post(
        `/einvoicing/units/${unit.id}/onboarding/${path}?company_id=${scope.company_id}`,
        { environment, ...body },
      );
      setNote(t('nx.einv.done'));
      void status.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      // Cleared whichever way it went. A one-time password left in an input on
      // a shop counter is the same exposure as writing it on paper.
      setOTP('');
      setBusy(false);
    }
  }

  async function amend() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      await api.put(`/einvoicing/units/${unit.id}?company_id=${scope.company_id}`, {
        label: label.trim(),
        // Only the fields that were touched; the rest keep what the unit
        // already carries.
        csr: { ...unit.csr, ...csrDraft },
      });
      // Read the one unit back rather than trusting the request. The nine CSR
      // fields are what ZATCA signs a certificate against, so a correction
      // that did not land is a unit that cannot be onboarded — and the screen
      // would show the typed value either way.
      const after = await api.get<EGSUnit>(
        `/einvoicing/units/${unit.id}?company_id=${scope.company_id}`,
      );
      setNote(
        t('nx.einv.unitAmendedAs', { name: after.label || label.trim() }),
      );
      setAmending(false);
      onAmended();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const state = status.data;

  return (
    <Panel
      title={unit.label}
      description={unit.store}
      actions={
        <span className="flex items-center gap-2">
          {state?.connected ? (
            <Badge tone="positive">{t('nx.einv.connected')}</Badge>
          ) : (
            <Badge tone="caution">{t('nx.einv.notConnected')}</Badge>
          )}
          {mayManage ? (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setLabel(unit.label);
                setCsrDraft({});
                setAmending((v) => !v);
              }}
            >
              {t('nx.einv.correct')}
            </Button>
          ) : null}
        </span>
      }
    >
      <div className="mb-4 flex flex-wrap gap-2">
        <Badge>{t('nx.einv.terminals', { count: String(unit.terminals) })}</Badge>
        <Badge>{t('nx.einv.invoices', { count: String(unit.invoices) })}</Badge>
        {state?.needs_renewal ? (
          <Badge tone="critical">{t('nx.einv.needsRenewal')}</Badge>
        ) : null}
        {!unit.csr_complete ? (
          <Badge tone="caution">{t('nx.einv.csrIncomplete')}</Badge>
        ) : null}
      </div>

      {/* The server's own sentence about what happens next. Rewording it is how
          an instruction stops matching the portal somebody is looking at. */}
      {state?.next_action ? (
        <p className="mb-4 max-w-prose rounded-sm border border-line bg-surface-sunken p-3 text-body">
          {state.next_action}
        </p>
      ) : null}

      <FormError message={error} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {amending && mayManage ? (
        <div className="mb-4 rounded-sm border border-line p-4">
          <p className="mb-3 max-w-prose text-caption text-muted">
            {t('nx.einv.correctHint')}
          </p>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field name="label" label={t('nx.einv.unitLabel')} required>
              <Input value={label} onChange={(e) => setLabel(e.target.value)} />
            </Field>
            {CSR_FIELDS.map((field) => (
              <Field key={field} name={field} label={t(CSR_LABEL[field] as Key)}>
                <Input
                  dir="ltr"
                  className="num"
                  value={csrDraft[field] ?? unit.csr[field] ?? ''}
                  onChange={(e) =>
                    setCsrDraft((was) => ({ ...was, [field]: e.target.value }))
                  }
                />
              </Field>
            ))}
          </div>
          <div className="mt-4 flex flex-wrap gap-2">
            <Button
              variant="primary"
              busy={busy}
              disabled={label.trim() === ''}
              onClick={() => void amend()}
            >
              {t('nx.einv.saveUnit')}
            </Button>
            <Button variant="ghost" onClick={() => setAmending(false)}>
              {t('nx.einv.cancel')}
            </Button>
          </div>
        </div>
      ) : (
        <dl className="mb-4 grid gap-3 sm:grid-cols-3">
          {CSR_FIELDS.map((field) => (
            <div key={field}>
              <dt className="text-label text-muted">{t(CSR_LABEL[field] as Key)}</dt>
              <dd className="num mt-0.5 text-body break-words">
                {unit.csr[field] || '—'}
              </dd>
            </div>
          ))}
        </dl>
      )}

      <dl className="mb-4 grid gap-3 sm:grid-cols-2">
        <div>
          <dt className="text-label text-muted">{t('nx.einv.complianceCert')}</dt>
          <dd className="mt-0.5 text-body">
            {state?.compliance
              ? t('nx.einv.issued', { when: state.compliance.issued_at ?? '' })
              : t('nx.einv.notIssued')}
          </dd>
        </div>
        <div>
          <dt className="text-label text-muted">{t('nx.einv.productionCert')}</dt>
          <dd className="mt-0.5 text-body">
            {state?.production
              ? t('nx.einv.issued', { when: state.production.issued_at ?? '' })
              : t('nx.einv.notIssued')}
          </dd>
        </div>
      </dl>

      {mayOnboard ? (
        <div className="border-t border-line pt-4">
          {!state?.compliance ? (
            <>
              <Field
                name="csr"
                label={t('nx.einv.csr')}
                hint={t('nx.einv.csrHint')}
                required
              >
                <Textarea
                  value={csr}
                  onChange={(e) => setCSR(e.target.value)}
                  rows={3}
                  className="num text-caption"
                />
              </Field>
              <div className="mt-4">
                <Field
                  name="otp"
                  label={t('nx.einv.otp')}
                  hint={t('nx.einv.otpHint')}
                  required
                >
                  <Input
                    type="password"
                    value={otp}
                    onChange={(e) => setOTP(e.target.value)}
                    autoComplete="off"
                    className="num"
                  />
                </Field>
              </div>
              <div className="mt-4">
                <Button
                  variant="primary"
                  disabled={busy || !csr.trim() || !otp.trim()}
                  onClick={() => void act('compliance', { csr: csr.trim(), otp: otp.trim() })}
                >
                  {t('nx.einv.requestCompliance')}
                </Button>
              </div>
            </>
          ) : (
            <div className="flex flex-wrap gap-3">
              {!state.production ? (
                <Button
                  variant="primary"
                  disabled={busy}
                  onClick={() => void act('production', {})}
                >
                  {t('nx.einv.requestProduction')}
                </Button>
              ) : null}
              <Button disabled={busy} onClick={() => void act('renew', {})}>
                {t('nx.einv.renew')}
              </Button>
            </div>
          )}
        </div>
      ) : null}
    </Panel>
  );
}

function EInvoicingScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayOnboard = grants.can('einvoicing.onboard');
  // Correcting a unit is `einvoicing.manage`; onboarding it with ZATCA is
  // `einvoicing.onboard`. Two permissions, read separately, because they are
  // two jobs: one is filing, the other is talking to the authority.
  const mayManage = grants.can('einvoicing.manage');

  const [rawEnv, setEnv] = useUrlState('environment', 'production');
  const environment: ZatcaEnvironment = (ZATCA_ENVIRONMENTS as readonly string[]).includes(
    rawEnv,
  )
    ? (rawEnv as ZatcaEnvironment)
    : 'production';

  const units = useApiList<EGSUnit>(
    scope ? '/einvoicing/units' : null,
    scope ?? undefined,
  );
  const rows = units.data?.data ?? [];

  return (
    <>
      <PageHeader title={t('nx.einv.title')} description={t('nx.einv.subtitle')} />

      {/* Said once, at the top, and not repeated per unit: this is a property
          of the obligation, not of any one till. */}
      <section
        className="mb-6 rounded-md border border-info/25 bg-info-subtle p-4"
        aria-labelledby="external"
      >
        <h2 id="external" className="text-card-title font-semibold text-info-fg">
          {t('nx.einv.externalTitle')}
        </h2>
        <p className="mt-1 max-w-prose text-body text-info-fg">
          {t('nx.einv.externalBody')}
        </p>
      </section>

      <div className="mb-5 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted">{t('nx.einv.environment')}</span>
          <Select
            value={environment}
            onChange={(e) => setEnv(e.target.value)}
            className="w-auto"
          >
            {ZATCA_ENVIRONMENTS.map((env) => (
              <option key={env} value={env}>
                {t(ENVIRONMENT_LABEL[env])}
              </option>
            ))}
          </Select>
        </label>
        <p className="max-w-prose pb-2 text-caption text-muted">
          {environment === 'production'
            ? t('nx.einv.productionHint')
            : t('nx.einv.testHint')}
        </p>
      </div>

      {units.error ? (
        <ErrorState error={units.error} onRetry={() => void units.refetch()} />
      ) : null}
      {units.isLoading && !units.data ? (
        <div className="h-64" aria-busy="true" />
      ) : null}

      {!units.isLoading && rows.length === 0 ? (
        <EmptyState
          title={t('nx.einv.emptyTitle')}
          description={t('nx.einv.emptyDesc')}
        />
      ) : null}

      <div className="flex flex-col gap-5">
        {rows.map((unit) => (
          <UnitPanel
            mayManage={mayManage}
            onAmended={() => void units.refetch()}
            key={unit.id}
            unit={unit}
            environment={environment}
            mayOnboard={mayOnboard}
          />
        ))}
      </div>
    </>
  );
}

export default function EInvoicingPage() {
  return (
    <RequirePermission anyOf={['einvoicing.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <EInvoicingScreen />
      </Suspense>
    </RequirePermission>
  );
}
