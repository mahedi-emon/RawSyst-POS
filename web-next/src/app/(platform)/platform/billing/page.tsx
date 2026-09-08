'use client';

// What a client pays the platform, and what their plan lets them do.
//
// # This is the platform's ledger, never the client's
//
// A subscription invoice is RawSyst billing a shop for the software. It is not
// the shop's own sales ledger, it does not touch their books, and it never
// appears in their accounts. Two things called "invoice" in one product is a
// real hazard, so this screen says whose invoice it is in its own heading and
// keeps the word "subscription" on every one of them.
//
// # Allowances are shown against usage, because that is the operational question
//
// `GET .../limits` answers the ceilings and the current counts together. An
// operator opening this screen is almost always answering "why can this client
// not add another till" — so a ceiling on its own is the wrong rendering, and
// the number in use beside it is the answer. A client at their ceiling is
// marked, because that is the state that produces a support ticket.
//
// # A blank box is not a zero
//
// The limits route takes pointers: an absent field is left alone, and a zero is
// a real ceiling of zero. So an untouched box sends nothing rather than sending
// the value it happens to be displaying, and clearing a box is not the same as
// typing 0 into it.
//
// # Dunning is not on this screen
//
// `POST /platform/dunning` suspends every client past their grace period across
// the whole platform. It is idempotent and it is not per-tenant, so putting it
// on a screen scoped to one client would misrepresent what pressing it does.

import { Receipt } from 'lucide-react';
import { Suspense, useEffect, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

import { ModulesPanel } from './modules';
import { useUrlState } from '@/lib/url-state';

interface Tenant {
  id: string;
  name: string;
  plan_tier?: string;
  market?: string;
}

interface Limits {
  max_companies: number;
  max_stores: number;
  max_users: number;
  max_terminals: number;
  max_skus: number;
  max_custom_roles: number;
  max_storage_mb: number;
  sms_credits: number;
  companies: number;
  stores: number;
  users: number;
  terminals: number;
}

interface Subscription {
  tier: string;
  cycle: string;
  price: string;
  currency: string;
  status: string;
  started_on: string;
  grace_days: number;
  outstanding: string;
  trial_ends_on?: string;
  limits: Limits;
}

interface SubInvoice {
  id: string;
  invoice_no: string;
  period_start: string;
  period_end: string;
  amount: string;
  currency: string;
  status: string;
  issued_on: string;
  due_on: string;
  paid_at?: string;
  payment_ref?: string;
  overdue: boolean;
}

/** The four allowances that have a live count to compare against. */
const COUNTED = [
  ['companies', 'max_companies', 'nx.plat.biCompanies'],
  ['stores', 'max_stores', 'nx.plat.biStores'],
  ['users', 'max_users', 'nx.plat.biUsers'],
  ['terminals', 'max_terminals', 'nx.plat.biTerminals'],
] as const;

/** The rest are ceilings with no counter behind them. */
const UNCOUNTED = [
  ['max_skus', 'nx.plat.biSkus'],
  ['max_custom_roles', 'nx.plat.biRoles'],
  ['max_storage_mb', 'nx.plat.biStorage'],
  ['sms_credits', 'nx.plat.biSms'],
] as const;

function BillingScreen() {
  const t = useT();
  const [tenantId, setTenantId] = useUrlState('tenant');

  // Only to fill the picker. The operator arrives here from a client, so the
  // newest accounts are the useful default and the search finds the rest.
  const [query, setQuery] = useState('');
  const tenants = useApiList<Tenant>('/platform/tenants', {
    limit: 20,
    search: query,
  });

  const path = tenantId ? `/platform/tenants/${tenantId}/subscription` : null;
  const { data, isLoading, error, refetch } = useApi<{
    subscription: Subscription;
    invoices: SubInvoice[];
  }>(path);

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const sub = data?.subscription;
  const limits = sub?.limits;

  // Blank means "leave it alone". Seeded empty and reset when the client
  // changes, so a value typed for one client cannot be sent for another.
  const [limitDraft, setLimitDraft] = useState<Record<string, string>>({});
  useEffect(() => {
    setLimitDraft({});
    setNote(null);
    setActionError(null);
  }, [tenantId]);

  // The real price list, rather than four options typed into this file.
  //
  // `GET /plans` answers {tier: [features]} and is the same for every client --
  // the route says so, "a public one". It had no caller at all, and the tier
  // dropdown here was four hard-coded strings, so adding or renaming a tier
  // would leave an operator moving a client onto a tier the product does not
  // have. It also lets the operator see what a tier INCLUDES before moving
  // somebody onto it, which is the question they are actually asking.
  const plans = useApi<{ plans: Record<string, string[]> }>('/plans');
  const tiers = Object.keys(plans.data?.plans ?? {});

  const [tier, setTier] = useState('');
  useEffect(() => {
    if (sub?.tier) setTier(sub.tier);
  }, [sub?.tier]);

  async function saveLimits() {
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    setNote(null);
    try {
      // Only the boxes somebody actually typed in. An absent field is left
      // alone by the route, which is what makes a partial save safe.
      const body: Record<string, number> = {};
      for (const [key, raw] of Object.entries(limitDraft)) {
        const trimmed = raw.trim();
        if (trimmed === '') continue;
        const n = Number(trimmed);
        if (!Number.isInteger(n) || n < 0) {
          setFieldErrors({ [key]: t('nx.plat.biNotAWholeNumber') });
          setActionError(t('nx.plat.biNotSaved'));
          setBusy(false);
          return;
        }
        body[key] = n;
      }
      if (Object.keys(body).length === 0) {
        setBusy(false);
        return;
      }
      await api.put(`/platform/tenants/${tenantId}/limits`, body);
      setLimitDraft({});
      setNote(t('nx.plat.biLimitsSaved'));
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function savePlan() {
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    setNote(null);
    try {
      await api.put(`/platform/tenants/${tenantId}/subscription`, { tier });
      setNote(t('nx.plat.biPlanSaved'));
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function settle(invoice: SubInvoice) {
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await api.post(`/platform/invoices/${invoice.id}/settle`, {
        payment_ref: '',
      });
      setNote(t('nx.plat.biMarkedPaid', { no: invoice.invoice_no }));
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const invoiceColumns: Column<SubInvoice>[] = [
    {
      key: 'no',
      header: t('nx.plat.biInvoiceNo'),
      primary: true,
      cell: (x) => <span className="num">{x.invoice_no}</span>,
    },
    {
      key: 'period',
      header: t('nx.plat.biPeriod'),
      cell: (x) => (
        <span className="num">
          {x.period_start} → {x.period_end}
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.plat.biAmount'),
      numeric: true,
      width: 'w-32',
      // Tabular and never mirrored: a column of money is scanned, not read.
      cell: (x) => (
        <span className="num" dir="ltr">
          {x.amount} {x.currency}
        </span>
      ),
    },
    {
      key: 'status',
      header: t('nx.plat.biStatus'),
      width: 'w-36',
      cell: (x) => (
        <span className="flex flex-wrap items-center gap-1.5">
          <Badge tone={x.status === 'paid' ? 'positive' : 'neutral'}>{x.status}</Badge>
          {x.overdue ? <Badge tone="critical">{t('nx.plat.biOverdue')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'due',
      header: t('nx.plat.biDue'),
      width: 'w-28',
      cell: (x) => <time dateTime={x.due_on}>{x.due_on}</time>,
    },
    {
      key: 'action',
      header: t('nx.plat.biAction'),
      width: 'w-32',
      cell: (x) =>
        x.status === 'paid' ? (
          <span className="text-muted">—</span>
        ) : (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void settle(x)}>
            {t('nx.plat.biMarkPaid')}
          </Button>
        ),
    },
  ];

  const picker = (
    <div className="flex flex-wrap items-end gap-2">
      <Field name="search" label={t('nx.plat.biFindClient')}>
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('nx.plat.biFindPlaceholder')}
        />
      </Field>
      <Field name="tenant" label={t('nx.plat.biClient')}>
        <Select value={tenantId} onChange={(e) => setTenantId(e.target.value)}>
          <option value="">{t('nx.plat.biChooseClient')}</option>
          {(tenants.data?.data ?? []).map((x) => (
            <option key={x.id} value={x.id}>
              {x.name}
            </option>
          ))}
        </Select>
      </Field>
    </div>
  );

  return (
    <>
      <PageHeader
        title={t('nx.plat.biTitle')}
        description={t('nx.plat.biSubtitle')}
      />

      <Panel className="mb-4">{picker}</Panel>

      {tenantId === '' ? (
        <EmptyState
          icon={Receipt}
          title={t('nx.plat.biPickTitle')}
          description={t('nx.plat.biPickDesc')}
        />
      ) : error ? (
        <ErrorState error={error} onRetry={() => void refetch()} />
      ) : isLoading && !data ? (
        <div className="h-64" aria-busy="true" />
      ) : sub && limits ? (
        <>
          <FormError message={actionError} fields={fieldErrors} className="mb-4" />
          {note ? (
            <p className="mb-4 text-body text-positive-fg" role="status">
              {note}
            </p>
          ) : null}

          <Panel title={t('nx.plat.biPlanTitle')} className="mb-4">
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <Figure label={t('nx.plat.biTier')} value={sub.tier} />
              {/* The currency is a separate prop so it is set quieter than the
                  figure: the amount is what is being read. */}
              <Figure
                label={t('nx.plat.biPrice')}
                value={sub.price}
                currency={sub.currency}
              />
              <Figure label={t('nx.plat.biCycle')} value={sub.cycle} />
              <Figure
                label={t('nx.plat.biOutstanding')}
                value={sub.outstanding}
                currency={sub.currency}
                // Anything owed is the reason somebody opened this screen.
                tone={sub.outstanding !== '0.00' ? 'critical' : undefined}
              />
            </div>

            <div className="mt-4 flex flex-wrap items-end gap-2 border-t border-line pt-4">
              <Field name="tier" label={t('nx.plat.biChangeTier')}>
                <Select value={tier} onChange={(e) => setTier(e.target.value)}>
                  {/* Falls back to the client's own tier while the list is in
                      flight, so the control is never empty and never silently
                      changes what it is showing. */}
                  {(tiers.length > 0 ? tiers : [sub.tier]).map((name) => (
                    <option key={name} value={name}>
                      {t(
                        `nx.plat.tier${name.charAt(0).toUpperCase()}${name.slice(1)}` as
                          'nx.plat.tierStarter',
                      )}
                    </option>
                  ))}
                </Select>
              </Field>

              {/* What the tier being chosen actually grants. An operator moving
                  a client between tiers is deciding what that client may do,
                  and the answer was on no screen at all. */}
              {tier ? (
                <p className="max-w-prose basis-full pb-1 text-caption text-muted">
                  <span className="text-fg">{t('nx.plat.biTierIncludes')}: </span>
                  {(plans.data?.plans?.[tier] ?? []).length > 0
                    ? (plans.data?.plans?.[tier] ?? []).join(', ')
                    : t('nx.plat.biTierNothing')}
                </p>
              ) : null}
              <Button
                busy={busy}
                busyLabel={t('nx.plat.biSaving')}
                disabled={tier === sub.tier}
                onClick={() => void savePlan()}
              >
                {t('nx.plat.biSavePlan')}
              </Button>
            </div>
          </Panel>

          <Panel title={t('nx.plat.biLimitsTitle')} className="mb-4">
            <p className="mb-4 text-body text-muted">{t('nx.plat.biLimitsHint')}</p>

            <div className="grid gap-4 sm:grid-cols-2">
              {COUNTED.map(([used, max, labelKey]) => {
                const inUse = limits[used];
                const ceiling = limits[max];
                const atCeiling = inUse >= ceiling;
                return (
                  <Field
                    key={max}
                    name={max}
                    label={t(labelKey)}
                    hint={
                      atCeiling
                        ? t('nx.plat.biAtCeiling', {
                            used: String(inUse),
                            max: String(ceiling),
                          })
                        : t('nx.plat.biInUse', {
                            used: String(inUse),
                            max: String(ceiling),
                          })
                    }
                  >
                    <Input
                      inputMode="numeric"
                      placeholder={String(ceiling)}
                      value={limitDraft[max] ?? ''}
                      onChange={(e) =>
                        setLimitDraft({ ...limitDraft, [max]: e.target.value })
                      }
                    />
                  </Field>
                );
              })}
              {UNCOUNTED.map(([max, labelKey]) => (
                <Field
                  key={max}
                  name={max}
                  label={t(labelKey)}
                  hint={t('nx.plat.biCurrent', { max: String(limits[max]) })}
                >
                  <Input
                    inputMode="numeric"
                    placeholder={String(limits[max])}
                    value={limitDraft[max] ?? ''}
                    onChange={(e) =>
                      setLimitDraft({ ...limitDraft, [max]: e.target.value })
                    }
                  />
                </Field>
              ))}
            </div>

            <div className="mt-6">
              <Button
                busy={busy}
                busyLabel={t('nx.plat.biSaving')}
                disabled={Object.values(limitDraft).every((v) => v.trim() === '')}
                onClick={() => void saveLimits()}
              >
                {t('nx.plat.biSaveLimits')}
              </Button>
            </div>
          </Panel>

          {/* Modules before invoices: an operator opening a client's billing is
              usually answering "why can they not use X", and that is this
              panel rather than the invoice list. */}
          <ModulesPanel tenantId={tenantId} />

          <Panel title={t('nx.plat.biInvoicesTitle')} flush>
            {(data?.invoices ?? []).length === 0 ? (
              <div className="p-6">
                <EmptyState
                  icon={Receipt}
                  title={t('nx.plat.biNoInvoicesTitle')}
                  description={t('nx.plat.biNoInvoicesDesc')}
                />
              </div>
            ) : (
              <DataTable<SubInvoice>
                rows={data?.invoices ?? []}
                columns={invoiceColumns}
                rowKey={(x) => x.id}
                caption={t('nx.plat.biInvoicesCaption')}
              />
            )}
          </Panel>
        </>
      ) : null}
    </>
  );
}

export default function BillingPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <BillingScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
