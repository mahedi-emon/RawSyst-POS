'use client';

// Taking on a client.
//
// # This is the only screen in the product that shows a password
//
// `POST /platform/tenants` answers with a temporary password for the owner it
// just created, once. It is not stored anywhere in readable form and cannot be
// fetched again — the route says so in its own response — so the operator has
// one chance to pass it on, and a screen that treated it as an ordinary field
// would lose somebody their account on a refresh.
//
// So the handover is a step of its own rather than a line in a toast: the form
// is replaced by it, the sentence says plainly that it will not be shown again,
// and there is a copy button because reading sixteen random characters aloud
// down a phone line is how a character gets transposed. It is never logged and
// never put in the URL.
//
// # The market is a commitment, not a preference
//
// It decides which regulatory register the client's tax comes from, and
// `CommitBusinessInfo` later refuses a company whose country disagrees with it.
// So it is chosen once here by the operator selling the account, and the client
// confirms rather than picks.
//
// A market whose release-blocking rules have never been verified refuses the
// whole request with `unverified_regulatory_rule`, naming them. That is a
// production gate — `registry.New(pool, cfg.Env.IsProduction())` — so it cannot
// fire in development, which is exactly why the refusal is handled here by
// pointing at the registry rather than by being discovered when it first fires
// in production.
//
// # Data region is not the market
//
// Where the rows live, versus which country's law the figures follow. They
// default together because Saudi Arabia is the launch market, and they are
// separate boxes because a Bangladeshi client hosted in Asia is a sentence that
// has to be expressible.

import { ArrowLeft, Check, Copy, KeyRound } from 'lucide-react';
import Link from 'next/link';
import { Suspense, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';

interface Provisioned {
  tenant_id: string;
  owner_user_id: string;
  owner_email: string;
  temporary_password: string;
}

/** The one-time credential handover. */
function Handover({ result }: { result: Provisioned }) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(result.temporary_password);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // A denied clipboard is not a failure worth an error panel: the password
      // is on screen and can be selected. Saying nothing is better than
      // claiming a copy that did not happen.
      setCopied(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.plat.newDoneTitle')}
        description={t('nx.plat.newDoneSubtitle')}
      />

      <Panel>
        <div className="flex items-start gap-3 border-b border-line pb-4">
          <KeyRound className="mt-0.5 size-5 shrink-0 text-caution" aria-hidden />
          <p className="text-body text-fg">{t('nx.plat.newShownOnce')}</p>
        </div>

        <dl className="mt-4 space-y-4">
          <div>
            <dt className="text-caption font-medium uppercase tracking-wide text-muted">
              {t('nx.plat.newOwnerEmail')}
            </dt>
            <dd className="mt-1 text-body text-fg">{result.owner_email}</dd>
          </div>
          <div>
            <dt className="text-caption font-medium uppercase tracking-wide text-muted">
              {t('nx.plat.newTempPassword')}
            </dt>
            <dd className="mt-1 flex flex-wrap items-center gap-2">
              {/* Latin, fixed width and never mirrored: a generated credential
                  is a sequence of characters, not prose. */}
              <code
                dir="ltr"
                className="rounded border border-line bg-surface-sunken px-2 py-1 font-mono text-body text-fg"
              >
                {result.temporary_password}
              </code>
              <Button type="button" size="sm" variant="ghost" onClick={() => void copy()}>
                {copied ? (
                  <Check className="size-4" aria-hidden />
                ) : (
                  <Copy className="size-4" aria-hidden />
                )}
                {copied ? t('nx.plat.newCopied') : t('nx.plat.newCopy')}
              </Button>
            </dd>
          </div>
        </dl>

        <p className="mt-4 text-body text-muted">{t('nx.plat.newMustChange')}</p>

        <div className="mt-6 flex flex-wrap gap-2">
          <Button asChild variant="secondary">
            <Link href="/platform/businesses">{t('nx.plat.newBackToList')}</Link>
          </Button>
        </div>
      </Panel>
    </>
  );
}

function NewBusinessScreen() {
  const t = useT();

  const [name, setName] = useState('');
  const [ownerName, setOwnerName] = useState('');
  const [ownerEmail, setOwnerEmail] = useState('');
  const [market, setMarket] = useState('');
  const [dataRegion, setDataRegion] = useState('sa');
  const [planTier, setPlanTier] = useState('starter');

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);
  // The refusal that has its remedy on another screen.
  const [registryBlocked, setRegistryBlocked] = useState(false);
  const [result, setResult] = useState<Provisioned | null>(null);

  async function create() {
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    setRegistryBlocked(false);
    try {
      const out = await api.post<Provisioned>('/platform/tenants', {
        name,
        owner_name: ownerName,
        owner_email: ownerEmail,
        market,
        data_region: dataRegion,
        plan_tier: planTier,
      });
      setResult(out);
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.fields) setFieldErrors(e.fields);
        // The market's legal values have never been checked against their
        // source. Nothing on this form fixes that, so the message carries a way
        // to the screen that does.
        if (e.code === 'unverified_regulatory_rule') setRegistryBlocked(true);
      }
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (result) return <Handover result={result} />;

  return (
    <>
      <PageHeader
        title={t('nx.plat.newTitle')}
        description={t('nx.plat.newSubtitle')}
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          void create();
        }}
        className="max-w-2xl"
      >
        <FormError
          message={error}
          fields={fieldErrors}
          className="mb-4"
          action={
            registryBlocked ? (
              <Link
                href="/platform/rules"
                className="font-medium underline underline-offset-2"
              >
                {t('nx.plat.newOpenRegistry')}
              </Link>
            ) : undefined
          }
        />

        <Panel title={t('nx.plat.newTheBusiness')}>
          <div className="space-y-4">
            <Field name="name" label={t('nx.plat.newName')}>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="organization"
                required
              />
            </Field>

            <Field
              name="market"
              label={t('nx.plat.newMarket')}
              hint={t('nx.plat.newMarketHint')}
            >
              <Select value={market} onChange={(e) => setMarket(e.target.value)} required>
                <option value="">{t('nx.plat.newChooseMarket')}</option>
                <option value="sa">{t('plat.marketSa')}</option>
                <option value="bd">{t('plat.marketBd')}</option>
                <option value="us">{t('plat.marketUs')}</option>
              </Select>
            </Field>

            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                name="plan_tier"
                label={t('nx.plat.newPlan')}
                hint={t('nx.plat.newPlanHint')}
              >
                <Select value={planTier} onChange={(e) => setPlanTier(e.target.value)}>
                  <option value="starter">{t('nx.plat.tierStarter')}</option>
                  <option value="professional">{t('nx.plat.tierProfessional')}</option>
                  <option value="business">{t('nx.plat.tierBusiness')}</option>
                  <option value="enterprise">{t('nx.plat.tierEnterprise')}</option>
                </Select>
              </Field>

              <Field
                name="data_region"
                label={t('nx.plat.newRegion')}
                hint={t('nx.plat.newRegionHint')}
              >
                <Select
                  value={dataRegion}
                  onChange={(e) => setDataRegion(e.target.value)}
                >
                  <option value="sa">{t('nx.plat.regionSa')}</option>
                  <option value="eu">{t('nx.plat.regionEu')}</option>
                  <option value="asia">{t('nx.plat.regionAsia')}</option>
                  <option value="other">{t('nx.plat.regionOther')}</option>
                </Select>
              </Field>
            </div>
          </div>
        </Panel>

        <Panel title={t('nx.plat.newTheOwner')} className="mt-4">
          <p className="mb-4 text-body text-muted">{t('nx.plat.newOwnerIntro')}</p>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field name="owner_name" label={t('nx.plat.newOwnerName')}>
              <Input
                value={ownerName}
                onChange={(e) => setOwnerName(e.target.value)}
                autoComplete="name"
                required
              />
            </Field>
            <Field name="owner_email" label={t('nx.plat.newOwnerEmailLabel')}>
              <Input
                type="email"
                dir="ltr"
                value={ownerEmail}
                onChange={(e) => setOwnerEmail(e.target.value)}
                autoComplete="email"
                required
              />
            </Field>
          </div>
        </Panel>

        <div className="mt-6 flex flex-wrap items-center gap-2">
          {/* `busy` keeps the button's width and blocks a second press, which
              matters here more than anywhere: pressing create twice would
              provision two clients and two owner accounts. */}
          <Button type="submit" busy={busy} busyLabel={t('nx.plat.newCreating')}>
            {t('nx.plat.newCreate')}
          </Button>
          <Button asChild variant="ghost">
            <Link href="/platform/businesses">
              <ArrowLeft className="size-4 rtl:rotate-180" aria-hidden />
              {t('nx.plat.newCancel')}
            </Link>
          </Button>
        </div>
      </form>
    </>
  );
}

export default function NewBusinessPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewBusinessScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
