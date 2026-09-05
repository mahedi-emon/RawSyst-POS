'use client';

// Discounts, and whether any of them are working.
//
// # Four states, not on and off
//
// "Inactive" covers a campaign somebody switched off, one that has not started
// and one that finished last month. An owner asking why a discount is not
// applying at the till needs to know which of those it is, and a single toggle
// cannot tell them.
//
// # What it cost is on the row
//
// `times_used`, `discount_given` and `sales_generated` come back with the
// promotion, because "is this working" is a question asked while looking at the
// list — not one worth a second screen. A campaign that gave away four thousand
// and generated four thousand one hundred is a campaign to stop.
//
// # The scope is the server's sentence
//
// `applies_to` is words, not four ids to resolve. A list that made the reader
// join a category, a brand and a variant in their head to find out what a
// discount touches is a list nobody checks.

import { Percent } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { exhausted, promotionState, type Promotion } from '@/lib/pos/counter-ops';

const KIND_LABEL: Record<string, Key> = {
  percentage: 'nx.pro.kPercentage',
  amount: 'nx.pro.kAmount',
  buy_x_get_y: 'nx.pro.kBuyGet',
  bundle_price: 'nx.pro.kBundle',
};

const STATE_LABEL: Record<string, Key> = {
  off: 'nx.pro.off',
  waiting: 'nx.pro.waiting',
  running: 'nx.pro.running',
  finished: 'nx.pro.finished',
};

const STATE_TONE: Record<string, 'positive' | 'caution' | 'neutral'> = {
  off: 'neutral',
  waiting: 'caution',
  running: 'positive',
  finished: 'neutral',
};

function PromotionsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('promotion.manage');

  const { data, isLoading, error, refetch } = useApiList<Promotion>(
    scope ? '/promotions' : null,
    scope ?? undefined,
  );
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const rows = data?.data ?? [];
  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || currency, market });

  const running = rows.filter((p) => promotionState(p) === 'running').length;
  const givenAway = rows.reduce((sum, p) => sum + Number(p.discount_given || 0), 0);

  async function setActive(p: Promotion, active: boolean) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(
        `/promotions/${p.id}/active?company_id=${scope.company_id}`,
        { active },
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Promotion>[] = [
    {
      key: 'name',
      header: t('nx.pro.colName'),
      primary: true,
      cell: (p) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{p.name}</span>
          <span className="num text-caption text-muted">
            {p.code}
            {p.coupon_code ? ` · ${p.coupon_code}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'what',
      header: t('nx.pro.colWhat'),
      cell: (p) => (
        <span className="flex flex-col gap-0.5">
          <span>
            {t(KIND_LABEL[p.kind] ?? 'nx.pro.kPercentage')}
            {p.kind === 'percentage' && p.value ? ` — ${p.value}%` : ''}
            {p.kind === 'amount' && p.value ? ` — ${money(p.value, p.currency)}` : ''}
            {p.kind === 'buy_x_get_y' && p.buy_qty
              ? ` — ${p.buy_qty}/${p.get_qty ?? ''}`
              : ''}
          </span>
          {/* The server's own sentence, so a reader does not join three
              tables in their head to learn what it touches. */}
          <span className="text-caption text-muted">{p.applies_to}</span>
        </span>
      ),
    },
    {
      key: 'when',
      header: t('nx.pro.colWhen'),
      secondary: true,
      width: 'w-44',
      cell: (p) =>
        p.starts_on || p.ends_on ? (
          <span className="num text-caption text-muted">
            {p.starts_on ?? '…'} — {p.ends_on ?? '…'}
          </span>
        ) : (
          <span className="text-muted">{t('nx.pro.always')}</span>
        ),
    },
    {
      key: 'used',
      header: t('nx.pro.colUsed'),
      numeric: true,
      width: 'w-32',
      cell: (p) => (
        <span className="flex flex-col items-end gap-1">
          <span className="num">
            {p.max_uses !== undefined
              ? `${p.times_used} / ${p.max_uses}`
              : p.times_used}
          </span>
          {exhausted(p) ? (
            <Badge tone="caution">{t('nx.pro.usedUp')}</Badge>
          ) : null}
        </span>
      ),
    },
    {
      key: 'cost',
      header: t('nx.pro.colGiven'),
      numeric: true,
      width: 'w-36',
      cell: (p) => (
        <span className="num">{money(p.discount_given, p.currency)}</span>
      ),
    },
    {
      key: 'generated',
      header: t('nx.pro.colGenerated'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (p) => (
        <span className="num text-muted">
          {money(p.sales_generated, p.currency)}
        </span>
      ),
    },
    {
      key: 'state',
      header: t('nx.pro.colState'),
      width: 'w-44',
      cell: (p) => {
        const state = promotionState(p);
        return (
          <span className="flex flex-wrap items-center gap-2">
            <Badge tone={STATE_TONE[state]}>{t(STATE_LABEL[state] as Key)}</Badge>
            {mayManage ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => void setActive(p, !p.is_active)}
              >
                {p.is_active ? t('nx.pro.switchOff') : t('nx.pro.switchOn')}
              </Button>
            ) : null}
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.pro.title')} description={t('nx.pro.subtitle')} />

      <FormError message={actionError} className="mb-4" />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={7} /> : null}

      {rows.length > 0 ? (
        <div className="mb-5 grid gap-4 sm:grid-cols-3">
          <Panel>
            <Figure label={t('nx.pro.runningNow')} value={String(running)} />
          </Panel>
          <Panel>
            <Figure
              label={t('nx.pro.givenAway')}
              value={money(givenAway.toFixed(2))}
              caption={t('nx.pro.givenAwayHint')}
            />
          </Panel>
          <Panel>
            <Figure label={t('nx.pro.campaigns')} value={String(rows.length)} />
          </Panel>
        </div>
      ) : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Percent}
          title={t('nx.pro.emptyTitle')}
          description={t('nx.pro.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.pro.title')}
          columns={columns}
          rows={rows}
          rowKey={(p) => p.id}
        />
      ) : null}
    </>
  );
}

export default function PromotionsPage() {
  return (
    <RequirePermission anyOf={['promotion.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <PromotionsScreen />
      </Suspense>
    </RequirePermission>
  );
}
