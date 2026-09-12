'use client';

// What a client pays the platform, and what their plan lets them do.
//
// # This is the platform's ledger, never the client's
//
// A subscription invoice is Biz1core billing a shop for the software. It is not
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
import { expiryTone } from '@/lib/subscription';

import { MembersPanel } from './members';
import { ModulesPanel } from './modules';
import { StandingPanel } from './standing';
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
  /** The last day paid for. Absent on a lifetime plan, and on one whose end
      was never recorded — which are not the same thing. */
  current_period_end?: string;
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

  // Raising an invoice for the selected client, and the periodic suspension
  // run. Two different acts on two different scopes, kept apart.
  const [raising, setRaising] = useState(false);
  const [periodStart, setPeriodStart] = useState('');
  const [periodEnd, setPeriodEnd] = useState('');
  const [amount, setAmount] = useState('');
  const [invoiceNote, setInvoiceNote] = useState('');
  const [dunning, setDunning] = useState(false);

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

  // The whole plan, not just the tier.
  //
  // This form used to send `{ tier }` alone, and the route refuses a
  // subscription with no cycle and no price -- so the Change plan button
  // returned 400 every single time it was pressed, for as long as it has
  // existed. Nothing here was ever editable.
  //
  // Seeded from the subscription and re-seeded when the operator picks a
  // different client, so a price typed for one business can never be sent for
  // another.
  const [tier, setTier] = useState('');
  const [cycle, setCycle] = useState('');
  const [price, setPrice] = useState('');
  const [status, setStatus] = useState('');
  const [startedOn, setStartedOn] = useState('');
  const [expiresOn, setExpiresOn] = useState('');
  useEffect(() => {
    if (!sub) return;
    setTier(sub.tier);
    setCycle(sub.cycle);
    setPrice(sub.price);
    setStatus(sub.status);
    setStartedOn(sub.started_on ?? '');
    setExpiresOn(sub.current_period_end ?? '');
  }, [sub]);

  /** Whether anything in the plan form differs from what is on file. */
  const planChanged =
    !!sub &&
    (tier !== sub.tier ||
      cycle !== sub.cycle ||
      price !== sub.price ||
      status !== sub.status ||
      startedOn !== (sub.started_on ?? '') ||
      expiresOn !== (sub.current_period_end ?? ''));

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
      // Every field the route needs. The dates are sent as typed and
      // validated on the server -- a period that ends before it starts, or a
      // lifetime plan given a renewal date, is refused there rather than here,
      // because a rule only the browser enforces is one curl skips.
      await api.put(`/platform/tenants/${tenantId}/subscription`, {
        tier,
        cycle,
        price,
        currency: sub?.currency,
        status,
        grace_days: sub?.grace_days,
        started_on: startedOn,
        // A cleared box on a cycled plan means "work it out from the cycle"
        // rather than "no expiry": only a lifetime subscription has none.
        expires_on: cycle === 'lifetime' ? '' : expiresOn,
      });
      setNote(t('nx.plat.biPlanSaved'));
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  /**
   * Billing a client for the software.
   *
   * `POST /platform/tenants/{id}/invoices` was live and reachable from
   * nothing: the screen could mark an invoice paid and could not raise one, so
   * every subscription invoice this product has ever issued was inserted by
   * hand. This is the platform's own ledger and it never touches the client's
   * sales books.
   */
  async function raiseInvoice() {
    if (!tenantId) return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    setNote(null);
    try {
      const out = await api.post<{ invoice: SubInvoice }>(
        `/platform/tenants/${tenantId}/invoices`,
        {
          period_start: periodStart,
          period_end: periodEnd,
          amount: amount.trim(),
          note: invoiceNote.trim(),
        },
      );
      setNote(t('nx.plat.biRaised', { no: out.invoice.invoice_no }));
      setAmount('');
      setInvoiceNote('');
      setRaising(false);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  /**
   * Suspending every client past its grace period.
   *
   * Idempotent, so pressing it twice is safe — the route says so. It is the
   * one control on this screen that acts on clients other than the one
   * selected, so it says how many it moved rather than reporting success.
   */
  async function runDunning() {
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      const out = await api.post<{ suspended: number }>('/platform/dunning', {});
      setNote(t('nx.plat.biDunningDone', { n: String(out.suspended) }));
      setDunning(false);
      void refetch();
    } catch (e) {
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

      {/* Suspending clients past their grace period. Acts on every client
          rather than the one selected, so it sits outside the picker and says
          how many it moved. */}
      <Panel
        className="mb-4"
        title={t('nx.plat.biDunningTitle')}
        description={t('nx.plat.biDunningHint')}
      >
        {dunning ? (
          <div className="flex flex-wrap gap-2">
            <Button variant="destructive" busy={busy} onClick={() => void runDunning()}>
              {t('nx.plat.biRunDunning')}
            </Button>
            <Button variant="ghost" onClick={() => setDunning(false)}>
              {t('nx.plat.biCancel')}
            </Button>
          </div>
        ) : (
          <Button onClick={() => setDunning(true)}>{t('nx.plat.biDunning')}</Button>
        )}
      </Panel>

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

            {/* How long the client has, which was on no screen at all: the
                route has answered `current_period_end` since the table was
                built and nothing rendered it. An operator could not see when
                a subscription ran out, let alone change it. */}
            <div className="mt-4 grid gap-4 border-t border-line pt-4 sm:grid-cols-2 lg:grid-cols-4">
              <Figure label={t('nx.plat.biStarted')} value={sub.started_on} />
              <Figure
                label={t('nx.plat.biExpires')}
                // A lifetime subscription genuinely has no end; a cycled one
                // with no end recorded is a gap somebody should close, and the
                // two must not render as the same empty cell.
                value={
                  sub.current_period_end ??
                  (sub.cycle === 'lifetime'
                    ? t('nx.plat.biNoExpiry')
                    : t('nx.plat.biExpiryUnknown'))
                }
                tone={expiryTone({ expiresOn: sub.current_period_end, cycle: sub.cycle })}
              />
              <Figure label={t('nx.plat.biStatus')} value={sub.status} />
              <Figure
                label={t('nx.plat.biGrace')}
                value={String(sub.grace_days)}
              />
            </div>

            <div className="mt-4 grid gap-4 border-t border-line pt-4 sm:grid-cols-2 lg:grid-cols-3">
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

              <Field name="cycle" label={t('nx.plat.biChangeCycle')}>
                <Select value={cycle} onChange={(e) => setCycle(e.target.value)}>
                  <option value="monthly">{t('nx.plat.biCycleMonthly')}</option>
                  <option value="yearly">{t('nx.plat.biCycleYearly')}</option>
                  <option value="lifetime">{t('nx.plat.biCycleLifetime')}</option>
                </Select>
              </Field>

              <Field name="price" label={t('nx.plat.biChangePrice')}>
                {/* Latin and left-to-right: an amount is digits, not prose. */}
                <Input
                  dir="ltr"
                  inputMode="decimal"
                  value={price}
                  onChange={(e) => setPrice(e.target.value)}
                />
              </Field>

              <Field
                name="started_on"
                label={t('nx.plat.biChangeStarted')}
                hint={t('nx.plat.biStartedHint')}
              >
                <Input
                  type="date"
                  value={startedOn}
                  onChange={(e) => setStartedOn(e.target.value)}
                />
              </Field>

              {/* Hidden for a lifetime plan rather than disabled, because a
                  greyed box still invites the question. The server refuses an
                  expiry on a lifetime subscription either way. */}
              {cycle !== 'lifetime' ? (
                <Field
                  name="expires_on"
                  label={t('nx.plat.biChangeExpires')}
                  hint={t('nx.plat.biExpiresHint')}
                >
                  <Input
                    type="date"
                    value={expiresOn}
                    onChange={(e) => setExpiresOn(e.target.value)}
                  />
                </Field>
              ) : null}

              <Field
                name="status"
                label={t('nx.plat.biChangeStatus')}
                hint={t('nx.plat.biStatusHint')}
              >
                <Select value={status} onChange={(e) => setStatus(e.target.value)}>
                  <option value="trialing">{t('nx.plat.biStatusTrialing')}</option>
                  <option value="active">{t('nx.plat.biStatusActive')}</option>
                  <option value="past_due">{t('nx.plat.biStatusPastDue')}</option>
                  <option value="suspended">{t('nx.plat.biStatusSuspended')}</option>
                  <option value="cancelled">{t('nx.plat.biStatusCancelled')}</option>
                </Select>
              </Field>
            </div>

            {/* What the tier being chosen actually grants. An operator moving
                a client between tiers is deciding what that client may do,
                and the answer was on no screen at all. */}
            {tier ? (
              <p className="mt-4 max-w-prose text-caption text-muted">
                <span className="text-fg">{t('nx.plat.biTierIncludes')}: </span>
                {(plans.data?.plans?.[tier] ?? []).length > 0
                  ? (plans.data?.plans?.[tier] ?? []).join(', ')
                  : t('nx.plat.biTierNothing')}
              </p>
            ) : null}

            {/* Said plainly, because it is the question an operator will ask
                the moment they change a status to suspended and nothing
                happens. Enforcement is the next phase's work. */}
            <p className="mt-2 max-w-prose text-caption text-subtle">
              {t('nx.plat.biNotEnforcedYet')}
            </p>

            <div className="mt-4 flex flex-wrap items-center gap-2">
              <Button
                busy={busy}
                busyLabel={t('nx.plat.biSaving')}
                disabled={!planChanged}
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

          {/* Suspending, reactivating and switching off. Phase 3 left these
              out deliberately, because tenant.status was written by dunning
              and read by nothing — a button that suspended nobody would have
              been worse than no button. The enforcement exists now. */}
          <StandingPanel tenantId={tenantId} />

          {/* Who is actually inside the business. An operator answering "how
              many of your five seats are in use" or "who am I resetting a
              password for" was asking the client to read it out. */}
          <MembersPanel tenantId={tenantId} />

          {/* Raising one. The screen could mark an invoice paid and could not
              issue one, so every subscription invoice this product has ever
              billed was inserted by hand. */}
          <Panel
            title={t('nx.plat.biRaiseTitle')}
            description={t('nx.plat.biRaiseHint')}
            actions={
              raising ? (
                <Button variant="ghost" onClick={() => setRaising(false)}>
                  {t('nx.plat.biCancel')}
                </Button>
              ) : (
                <Button onClick={() => setRaising(true)}>{t('nx.plat.biRaise')}</Button>
              )
            }
          >
            {raising ? (
              <>
                <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                  <Field name="period_start" label={t('nx.plat.biPeriodStart')} required>
                    <Input
                      type="date"
                      className="num"
                      value={periodStart}
                      onChange={(e) => setPeriodStart(e.target.value)}
                    />
                  </Field>
                  <Field name="period_end" label={t('nx.plat.biPeriodEnd')} required>
                    <Input
                      type="date"
                      className="num"
                      value={periodEnd}
                      onChange={(e) => setPeriodEnd(e.target.value)}
                    />
                  </Field>
                  <Field
                    name="amount"
                    label={t('nx.plat.biAmount')}
                    hint={t('nx.plat.biAmountHint')}
                    required
                  >
                    <Input
                      numeric
                      inputMode="decimal"
                      value={amount}
                      onChange={(e) => setAmount(e.target.value)}
                    />
                  </Field>
                  <Field name="note" label={t('nx.plat.biNote')}>
                    <Input
                      value={invoiceNote}
                      onChange={(e) => setInvoiceNote(e.target.value)}
                    />
                  </Field>
                </div>
                <div className="mt-4">
                  <Button
                    variant="primary"
                    busy={busy}
                    disabled={
                      periodStart === '' || periodEnd === '' || amount.trim() === ''
                    }
                    onClick={() => void raiseInvoice()}
                  >
                    {t('nx.plat.biRaiseIt')}
                  </Button>
                </div>
              </>
            ) : (
              <p className="max-w-prose text-body text-muted">
                {t('nx.plat.biRaiseHint')}
              </p>
            )}
          </Panel>

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
