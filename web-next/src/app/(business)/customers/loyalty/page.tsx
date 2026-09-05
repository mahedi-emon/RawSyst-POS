'use client';

// Points, what they are worth, and what the shop owes in them.
//
// # A scheme that does not exist is not a scheme set to zero
//
// `exists: false` comes back with empty rates rather than zeros. A form full of
// defaults reads as a scheme somebody has configured, and a shop would start
// handing out points nobody can spend. So the screen says there is no scheme
// and offers to set one up, rather than showing a rate of nothing.
//
// # What is owed is the figure an owner asks about
//
// Points are a liability: every one outstanding is a discount the shop has
// already promised. `owed` is that in money, and it leads, because the number
// of points on its own means nothing to anybody.
//
// # Expiring points is done on demand, never on a timer
//
// The route is explicit about it: "a background job that quietly changed a
// liability on a Saturday would be a change nobody could attribute". So it is a
// button somebody presses, and the screen says what it will do first.

import { Sparkles } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  schemeRunning,
  type LoyaltyMember,
  type LoyaltyProgram,
} from '@/lib/pos/counter-ops';

function LoyaltyScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('loyalty.manage');

  const program = useApi<LoyaltyProgram>(
    scope ? '/loyalty/program' : null,
    scope ?? undefined,
  );
  const members = useApiList<LoyaltyMember>(
    scope ? '/loyalty/members' : null,
    scope ?? undefined,
  );

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const p = program.data;
  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || p?.currency || currency, market });

  async function expire() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const out = await api.post<{ expired?: number; points?: number }>(
        `/loyalty/expire?company_id=${scope.company_id}`,
        {},
      );
      setNote(
        t('nx.loy.expiredNote', {
          count: String(out.expired ?? out.points ?? 0),
        }),
      );
      void program.refetch();
      void members.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<LoyaltyMember>[] = [
    {
      key: 'customer',
      header: t('nx.loy.colCustomer'),
      primary: true,
      cell: (m) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{m.customer}</span>
          <span className="text-caption text-muted">{m.segment}</span>
        </span>
      ),
    },
    {
      key: 'tier',
      header: t('nx.loy.colTier'),
      width: 'w-44',
      cell: (m) =>
        m.tier ? (
          <span className="flex flex-col gap-0.5">
            <Badge tone="info">{m.tier}</Badge>
            {m.next_tier && m.to_next_tier ? (
              <span className="text-caption text-muted">
                {t('nx.loy.toNext', {
                  amount: money(m.to_next_tier, m.currency),
                  tier: m.next_tier,
                })}
              </span>
            ) : null}
          </span>
        ) : (
          <span className="text-muted">—</span>
        ),
    },
    {
      key: 'points',
      header: t('nx.loy.colPoints'),
      numeric: true,
      width: 'w-28',
      cell: (m) => <span className="num">{m.points}</span>,
    },
    {
      key: 'worth',
      header: t('nx.loy.colWorth'),
      numeric: true,
      width: 'w-32',
      cell: (m) => <span className="num font-medium">{money(m.worth, m.currency)}</span>,
    },
    {
      key: 'spend',
      header: t('nx.loy.colSpend'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (m) => (
        <span className="num text-muted">{money(m.lifetime_spend, m.currency)}</span>
      ),
    },
    {
      key: 'visits',
      header: t('nx.loy.colVisits'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      cell: (m) => (
        <span className="flex flex-col items-end gap-0.5">
          <span className="num text-muted">{m.visits}</span>
          {m.last_purchase ? (
            <time dateTime={m.last_purchase} className="num text-caption text-muted">
              {m.last_purchase}
            </time>
          ) : null}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.loy.title')}
        description={t('nx.loy.subtitle')}
        actions={
          mayManage && p?.exists ? (
            <Button disabled={busy} onClick={() => void expire()}>
              {t('nx.loy.expire')}
            </Button>
          ) : null
        }
      />

      <FormError message={error} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {program.error ? (
        <ErrorState error={program.error} onRetry={() => void program.refetch()} />
      ) : null}

      {p && !p.exists ? (
        // Not a form of zeros. A shop with no scheme is not a shop whose
        // scheme earns nothing.
        <EmptyState
          icon={Sparkles}
          title={t('nx.loy.noSchemeTitle')}
          description={t('nx.loy.noSchemeDesc')}
        />
      ) : null}

      {p?.exists ? (
        <>
          <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Panel>
              {/* The figure an owner actually asks about: every point
                  outstanding is a discount already promised. */}
              <Figure
                label={t('nx.loy.owed')}
                value={money(p.owed)}
                caption={t('nx.loy.owedHint')}
              />
            </Panel>
            <Panel>
              <Figure
                label={t('nx.loy.outstanding')}
                value={String(p.points_outstanding)}
              />
            </Panel>
            <Panel>
              <Figure
                label={t('nx.loy.earnRate')}
                value={
                  p.spend_per_point
                    ? t('nx.loy.perPoint', { amount: money(p.spend_per_point) })
                    : '—'
                }
              />
            </Panel>
            <Panel>
              <Figure
                label={t('nx.loy.pointWorth')}
                value={p.point_value ? money(p.point_value) : '—'}
                caption={
                  p.expiry_months
                    ? t('nx.loy.expiresAfter', { months: String(p.expiry_months) })
                    : t('nx.loy.neverExpires')
                }
              />
            </Panel>
          </div>

          <div className="mb-6 flex flex-wrap items-center gap-2">
            {schemeRunning(p) ? (
              <Badge tone="positive">{t('nx.loy.running')}</Badge>
            ) : (
              // Paused, which is different from never having had one.
              <Badge tone="caution">{t('nx.loy.paused')}</Badge>
            )}
            {(p.tiers ?? []).map((tier) => (
              <Badge key={tier.key}>
                {tier.name} · {money(tier.min_spend)}
                {tier.discount_percent ? ` · ${tier.discount_percent}%` : ''}
              </Badge>
            ))}
          </div>
        </>
      ) : null}

      {members.isLoading && !members.data ? <TableSkeleton columns={6} /> : null}
      {(members.data?.data ?? []).length > 0 ? (
        <DataTable
          caption={t('nx.loy.membersCaption')}
          columns={columns}
          rows={members.data?.data ?? []}
          rowKey={(m) => m.customer_id}
        />
      ) : null}
    </>
  );
}

export default function LoyaltyPage() {
  return (
    <RequirePermission anyOf={['loyalty.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <LoyaltyScreen />
      </Suspense>
    </RequirePermission>
  );
}
