'use client';

// What this business pays RawSyst, and what its plan lets it do.
//
// # Read-only, and that is the feature
//
// There is no `PUT /subscription`. A tenant who could edit their own plan would
// be a tenant on the Enterprise plan — the platform's own route says exactly
// that. So this screen shows the plan and never offers to change it, and where
// a change is wanted it says who to ask rather than presenting a control that
// would refuse.
//
// # Allowances read against usage
//
// The route answers ceilings and live counts together. A ceiling on its own
// does not answer the question somebody opens this screen with, which is nearly
// always "why can I not add another till". A business at its ceiling is marked,
// because that is the moment the answer is "your plan".
//
// # Entitlements are not the same as allowances
//
// An allowance is how many; an entitlement is whether at all. They come from
// different routes and mean different things — `in_plan` says the tier includes
// a module, `allowed` says this business may use it, and the two differ when
// the platform has granted something outside the tier. Where they differ, the
// screen says so, because "included in your plan" and "switched on for you" are
// different promises.

import { Receipt } from 'lucide-react';
import Link from 'next/link';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompany } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
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
  overdue: boolean;
}

interface Entitlement {
  feature: string;
  allowed: boolean;
  in_plan: boolean;
}

/** The four allowances with a live count behind them. */
const COUNTED = [
  ['companies', 'max_companies', 'nx.sub.companies'],
  ['stores', 'max_stores', 'nx.sub.stores'],
  ['users', 'max_users', 'nx.sub.users'],
  ['terminals', 'max_terminals', 'nx.sub.terminals'],
] as const;

const UNCOUNTED = [
  ['max_skus', 'nx.sub.skus'],
  ['max_custom_roles', 'nx.sub.roles'],
  ['max_storage_mb', 'nx.sub.storage'],
  ['sms_credits', 'nx.sub.sms'],
] as const;

function SubscriptionScreen() {
  const t = useT();
  const { market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<{ subscription: Subscription }>(
    '/subscription',
  );
  const invoices = useApiList<SubInvoice>('/subscription/invoices');
  const entitlements = useApiList<Entitlement>('/subscription/entitlements');

  const sub = data?.subscription;
  const limits = sub?.limits;

  const invoiceColumns: Column<SubInvoice>[] = [
    {
      key: 'no',
      header: t('nx.sub.invoiceNo'),
      primary: true,
      cell: (x) => <span className="num">{x.invoice_no}</span>,
    },
    {
      key: 'period',
      header: t('nx.sub.period'),
      cell: (x) => (
        <span className="num">
          {x.period_start} → {x.period_end}
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.sub.amount'),
      numeric: true,
      width: 'w-36',
      cell: (x) => (
        <span className="num font-medium">
          {formatMoney(x.amount, { currency: x.currency, market })}
        </span>
      ),
    },
    {
      key: 'due',
      header: t('nx.sub.due'),
      width: 'w-28',
      cell: (x) => <time dateTime={x.due_on}>{x.due_on}</time>,
    },
    {
      key: 'status',
      header: t('nx.sub.status'),
      width: 'w-36',
      cell: (x) => (
        <span className="flex flex-wrap items-center gap-1.5">
          <Badge tone={x.status === 'paid' ? 'positive' : 'neutral'}>
            {x.status === 'paid' ? t('nx.sub.paid') : t('nx.sub.unpaid')}
          </Badge>
          {x.overdue ? <Badge tone="critical">{t('nx.sub.overdue')}</Badge> : null}
        </span>
      ),
    },
  ];

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;
  if (isLoading && !data) return <div className="h-64" aria-busy="true" />;
  if (!sub || !limits) return null;

  const granted = (entitlements.data?.data ?? []).filter((e) => e.allowed);
  const outsideTier = granted.filter((e) => !e.in_plan);

  return (
    <>
      <PageHeader title={t('nx.sub.title')} description={t('nx.sub.subtitle')} />

      <Panel title={t('nx.sub.planTitle')} className="mb-4">
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Figure label={t('nx.sub.tier')} value={sub.tier} />
          <Figure
            label={t('nx.sub.price')}
            value={sub.price}
            currency={sub.currency}
            caption={t(`nx.sub.cycle.${sub.cycle}` as 'nx.sub.cycle.monthly')}
          />
          <Figure
            label={t('nx.sub.outstanding')}
            value={sub.outstanding}
            currency={sub.currency}
            tone={sub.outstanding !== '0.00' ? 'critical' : undefined}
          />
          <Figure
            label={t('nx.sub.since')}
            value={sub.started_on}
            caption={
              sub.trial_ends_on
                ? t('nx.sub.trialEnds', { date: sub.trial_ends_on })
                : undefined
            }
          />
        </div>
        {/* No control, because there is no route. Saying who changes it is
            more use than a button that would refuse. */}
        <p className="mt-4 border-t border-line pt-4 text-body text-muted">
          {t('nx.sub.changeHow')}{' '}
          <Link
            href="/settings/support"
            className="font-medium underline underline-offset-2"
          >
            {t('nx.sub.raiseTicket')}
          </Link>
        </p>
      </Panel>

      <Panel title={t('nx.sub.limitsTitle')} className="mb-4">
        <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {COUNTED.map(([used, max, labelKey]) => {
            const inUse = limits[used];
            const ceiling = limits[max];
            const atCeiling = inUse >= ceiling;
            return (
              <div key={max}>
                <dt className="text-caption text-muted">{t(labelKey)}</dt>
                <dd className="mt-1 flex items-center gap-2">
                  <span className="num text-fg">
                    {inUse} / {ceiling}
                  </span>
                  {/* A word, not only a colour. */}
                  {atCeiling ? (
                    <Badge tone="caution">{t('nx.sub.atCeiling')}</Badge>
                  ) : null}
                </dd>
              </div>
            );
          })}
          {UNCOUNTED.map(([max, labelKey]) => (
            <div key={max}>
              <dt className="text-caption text-muted">{t(labelKey)}</dt>
              <dd className="num mt-1 text-fg">{limits[max]}</dd>
            </div>
          ))}
        </dl>
      </Panel>

      <Panel title={t('nx.sub.featuresTitle')} className="mb-4">
        <p className="mb-3 text-body text-muted">{t('nx.sub.featuresHint')}</p>
        <ul className="flex flex-wrap gap-2">
          {granted.map((e) => (
            <li key={e.feature}>
              <Badge tone={e.in_plan ? 'neutral' : 'primary'}>
                {t(`nx.sub.feature.${e.feature}` as 'nx.sub.feature.analytics')}
              </Badge>
            </li>
          ))}
        </ul>
        {outsideTier.length > 0 ? (
          <p className="mt-3 text-body text-muted">
            {t('nx.sub.outsideTier', { count: String(outsideTier.length) })}
          </p>
        ) : null}
      </Panel>

      <Panel title={t('nx.sub.invoicesTitle')} flush>
        {(invoices.data?.data ?? []).length === 0 ? (
          <div className="p-6">
            <EmptyState
              icon={Receipt}
              title={t('nx.sub.noInvoicesTitle')}
              description={t('nx.sub.noInvoicesDesc')}
            />
          </div>
        ) : (
          <DataTable<SubInvoice>
            rows={invoices.data?.data ?? []}
            columns={invoiceColumns}
            rowKey={(x) => x.id}
            caption={t('nx.sub.invoicesCaption')}
          />
        )}
      </Panel>
    </>
  );
}

export default function SubscriptionPage() {
  return (
    <RequirePermission anyOf={['subscription.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SubscriptionScreen />
      </Suspense>
    </RequirePermission>
  );
}
