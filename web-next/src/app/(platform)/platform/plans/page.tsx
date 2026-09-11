'use client';

// The price list itself.
//
// # Why this screen exists
//
// The billing screen next door changes what ONE client gets: a module granted
// as an exception, a ceiling raised for them alone. That is the exception.
//
// The rule — what a tier includes for everybody on it — was written by a
// migration and by nothing else, so "put analytics in Professional" or "raise
// Starter to ten users" meant a code change, a review, a build and a deploy,
// for a decision that is a sentence. This is where the software owner makes it.
//
// # Why a tier cannot be added or removed here
//
// The four are a database enum. A fifth needs seeded features, a place in the
// pricing and a decision about what it contains, none of which a form supplies;
// removing one would orphan every client on it. So the rows are fixed and their
// values are editable, which is the shape the product actually needs.

import { Suspense, useEffect, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface Limits {
  max_companies: number;
  max_stores: number;
  max_users: number;
  max_terminals: number;
  max_skus: number;
  max_custom_roles: number;
  max_storage_mb: number;
  sms_credits: number;
}

interface Plan {
  tier: string;
  features: string[];
  limits: Limits;
}

/** The ceilings, in the order an operator reads them. */
const CEILINGS: ReadonlyArray<[keyof Limits, string]> = [
  ['max_users', 'nx.plat.biUsers'],
  ['max_stores', 'nx.plat.biStores'],
  ['max_terminals', 'nx.plat.biTerminals'],
  ['max_companies', 'nx.plat.biCompanies'],
  ['max_skus', 'nx.plat.biSkus'],
  ['max_custom_roles', 'nx.plat.biRoles'],
  ['max_storage_mb', 'nx.plat.biStorage'],
  ['sms_credits', 'nx.plat.biSms'],
];

function PlansScreen() {
  const t = useT();
  const { data, isLoading, error, refetch } =
    useApi<{ data: Plan[] }>('/platform/plans');
  const plans = data?.data ?? [];

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  /** Every module any tier sells, so a tier can be given one it lacks. */
  const allFeatures = [
    ...new Set(plans.flatMap((p) => p.features)),
  ].sort();

  async function toggle(tier: string, feature: string, included: boolean) {
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await api.put(`/platform/plans/${tier}/features`, { feature, included });
      setNote(
        included
          ? t('nx.plat.plGranted', { feature, tier })
          : t('nx.plat.plRemoved', { feature, tier }),
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.plat.plTitle')}
        description={t('nx.plat.plSubtitle')}
      />

      {error && <ErrorState error={error} onRetry={() => void refetch()} />}
      <FormError message={actionError} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {isLoading && plans.length === 0 ? (
        <Skeleton className="h-64 w-full" />
      ) : null}

      {/* Said once, at the top, because it is the question an operator asks
          the moment they raise a ceiling and a client's allowance does not
          move. */}
      {plans.length > 0 ? (
        <p className="mb-4 max-w-prose text-body text-muted">
          {t('nx.plat.plNewClientsOnly')}
        </p>
      ) : null}

      <div className="flex flex-col gap-4">
        {plans.map((plan) => (
          <PlanCard
            key={plan.tier}
            plan={plan}
            allFeatures={allFeatures}
            busy={busy}
            onToggle={toggle}
            onSaved={(m) => {
              setNote(m);
              void refetch();
            }}
            onError={setActionError}
          />
        ))}
      </div>
    </>
  );
}

function PlanCard({
  plan,
  allFeatures,
  busy,
  onToggle,
  onSaved,
  onError,
}: {
  plan: Plan;
  allFeatures: string[];
  busy: boolean;
  onToggle: (tier: string, feature: string, included: boolean) => void;
  onSaved: (message: string) => void;
  onError: (message: string) => void;
}) {
  const t = useT();
  const [draft, setDraft] = useState<Limits>(plan.limits);
  const [saving, setSaving] = useState(false);
  useEffect(() => setDraft(plan.limits), [plan.limits]);

  const changed = CEILINGS.some(([k]) => draft[k] !== plan.limits[k]);

  async function saveLimits() {
    setSaving(true);
    try {
      await api.put(`/platform/plans/${plan.tier}/limits`, draft);
      onSaved(t('nx.plat.plLimitsSaved', { tier: plan.tier }));
    } catch (e) {
      onError(messageFor(e, t));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Panel title={plan.tier} description={t('nx.plat.plCardDesc')}>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {CEILINGS.map(([key, labelKey]) => (
          <Field key={key} name={key} label={t(labelKey as 'nx.plat.biUsers')}>
            <Input
              dir="ltr"
              inputMode="numeric"
              value={String(draft[key])}
              onChange={(e) =>
                setDraft({ ...draft, [key]: Number(e.target.value) || 0 })
              }
            />
          </Field>
        ))}
      </div>

      <div className="mt-4">
        <Button
          size="sm"
          busy={saving}
          busyLabel={t('nx.plat.biSaving')}
          disabled={!changed}
          onClick={() => void saveLimits()}
        >
          {t('nx.plat.plSaveLimits')}
        </Button>
      </div>

      <div className="rule mt-4 pt-4">
        <p className="mb-2 text-label text-muted">{t('nx.plat.plModules')}</p>
        <div className="flex flex-wrap gap-2">
          {allFeatures.map((f) => {
            const included = plan.features.includes(f);
            return (
              <button
                key={f}
                type="button"
                disabled={busy}
                onClick={() => onToggle(plan.tier, f, !included)}
                className="rounded-sm disabled:opacity-50"
                // The label says what pressing it does, not only what the
                // state is — a toggle whose name is its current value is one
                // nobody can use with a screen reader.
                aria-label={
                  included
                    ? t('nx.plat.plRemoveFrom', { feature: f, tier: plan.tier })
                    : t('nx.plat.plAddTo', { feature: f, tier: plan.tier })
                }
              >
                <Badge tone={included ? 'positive' : undefined}>{f}</Badge>
              </button>
            );
          })}
        </div>
      </div>
    </Panel>
  );
}

export default function PlansPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <PlansScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
