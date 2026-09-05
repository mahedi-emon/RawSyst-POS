'use client';

// The tills, the morning after.
//
// # The variance is the whole reason this screen exists
//
// A blind close asks a cashier to count the drawer without being told what the
// system expects. The difference between the two is the only signal the
// practice produces, and until this screen there was nowhere to read it: the
// till routes need a terminal-bound token or a session id only the till holds.
//
// Which is also why it sits behind `report.view` rather than the till's own
// permission. A cashier who can see the expected figure can make tonight's
// drawer agree with it, and then every variance reads zero for ever.
//
// # Over and short are different problems
//
// A shortfall is money missing. A surplus is money that should not be there —
// a sale rung up wrong, change not given. A manager acts differently on each,
// and a minus sign in a column of numbers is easy to miss, so they are named.
//
// # An open drawer shows nothing rather than zero
//
// The counted, expected and variance figures are absent on a session still
// running. A zero there would send somebody to every till in the shop.

import { Coins } from 'lucide-react';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Input, Select } from '@/components/ui/field';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  counted,
  localDay,
  shiftTotals,
  varianceState,
  type Shift,
} from '@/lib/pos/counter-ops';
import { useUrlState } from '@/lib/url-state';

interface Store {
  id: string;
  name: string;
}

const VARIANCE_LABEL: Record<string, Key> = {
  over: 'nx.sft.over',
  short: 'nx.sft.short',
  exact: 'nx.sft.exact',
  uncounted: 'nx.sft.uncounted',
};

const VARIANCE_TONE: Record<string, 'positive' | 'caution' | 'critical' | 'neutral'> =
  {
    over: 'caution',
    short: 'critical',
    exact: 'positive',
    uncounted: 'neutral',
  };

function daysAgo(n: number): string {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return localDay(d);
}

function ShiftsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const [from, setFrom] = useUrlState('from', daysAgo(14));
  const [to, setTo] = useUrlState('to', localDay());
  const [storeID, setStoreID] = useUrlState('store_id', '');

  const stores = useApiList<Store>(scope ? '/stores' : null, scope ?? undefined);
  const { data, isLoading, error, refetch } = useApiList<Shift>(
    scope ? '/shifts' : null,
    scope ? { ...scope, from, to, ...(storeID ? { store_id: storeID } : {}) } : undefined,
  );

  const rows = data?.data ?? [];
  const totals = shiftTotals(rows);
  const money = (v: string) => formatMoney(v, { currency, market });

  const columns: Column<Shift>[] = [
    {
      key: 'session',
      header: t('nx.sft.colSession'),
      primary: true,
      width: 'w-44',
      cell: (s) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">
            {s.device || t('nx.sft.unnamedTill')} · {s.session_no}
          </span>
          <span className="text-caption text-muted">{s.store}</span>
        </span>
      ),
    },
    {
      key: 'who',
      header: t('nx.sft.colWho'),
      cell: (s) => (
        <span className="flex flex-col gap-0.5">
          <span>{s.opened_by || '—'}</span>
          {s.closed_by && s.closed_by !== s.opened_by ? (
            <span className="text-caption text-muted">
              {t('nx.sft.closedBy', { who: s.closed_by })}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'when',
      header: t('nx.sft.colWhen'),
      secondary: true,
      width: 'w-44',
      cell: (s) => (
        <span className="flex flex-col gap-0.5">
          <time dateTime={s.opened_at} className="num text-muted">
            {s.opened_at.slice(0, 16).replace('T', ' ')}
          </time>
          {s.closed_at ? (
            <time dateTime={s.closed_at} className="num text-caption text-muted">
              {s.closed_at.slice(11, 16)}
            </time>
          ) : null}
        </span>
      ),
    },
    {
      key: 'float',
      header: t('nx.sft.colFloat'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      cell: (s) => <span className="num text-muted">{money(s.opening_float)}</span>,
    },
    {
      key: 'counted',
      header: t('nx.sft.colCounted'),
      numeric: true,
      width: 'w-32',
      cell: (s) =>
        counted(s) ? (
          <span className="num">{money(s.counted_cash as string)}</span>
        ) : (
          // Absent, not zero.
          <span className="text-muted">—</span>
        ),
    },
    {
      key: 'expected',
      header: t('nx.sft.colExpected'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      cell: (s) =>
        s.expected_cash !== undefined ? (
          <span className="num text-muted">{money(s.expected_cash)}</span>
        ) : (
          <span className="text-muted">—</span>
        ),
    },
    {
      key: 'variance',
      header: t('nx.sft.colVariance'),
      numeric: true,
      width: 'w-40',
      cell: (s) => {
        const state = varianceState(s);
        if (state === 'uncounted') {
          return (
            <Badge tone="neutral">{t('nx.sft.stillOpen')}</Badge>
          );
        }
        return (
          <span className="flex flex-col items-end gap-1">
            <span className="num font-medium">{money(s.variance as string)}</span>
            {/* Named, not signed. A minus in a column of numbers is missed. */}
            <Badge tone={VARIANCE_TONE[state]}>
              {t(VARIANCE_LABEL[state] as Key)}
            </Badge>
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.sft.title')} description={t('nx.sft.subtitle')} />

      <div className="mb-5 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted">{t('nx.fin.from')}</span>
          <Input
            type="date"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
            className="w-auto"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted">{t('nx.fin.to')}</span>
          <Input
            type="date"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            className="w-auto"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted">{t('nx.sft.branch')}</span>
          <Select
            value={storeID}
            onChange={(e) => setStoreID(e.target.value)}
            className="w-auto"
          >
            <option value="">{t('nx.sft.allBranches')}</option>
            {(stores.data?.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </label>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={7} /> : null}

      {rows.length > 0 ? (
        <div className="mb-5 grid gap-4 sm:grid-cols-3">
          <Panel>
            <Figure label={t('nx.sft.countedShifts')} value={String(totals.counted)} />
          </Panel>
          <Panel>
            <Figure label={t('nx.sft.stillOpenCount')} value={String(totals.open)} />
          </Panel>
          <Panel>
            <Figure
              label={t('nx.sft.outBy')}
              value={money(totals.outBy)}
              caption={t('nx.sft.outByHint')}
              tone={Number(totals.outBy) === 0 ? 'positive' : undefined}
            />
          </Panel>
        </div>
      ) : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Coins}
          title={t('nx.sft.emptyTitle')}
          description={t('nx.sft.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.sft.title')}
          columns={columns}
          rows={rows}
          rowKey={(s) => s.id}
        />
      ) : null}
    </>
  );
}

export default function ShiftsPage() {
  return (
    <RequirePermission anyOf={['report.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ShiftsScreen />
      </Suspense>
    </RequirePermission>
  );
}
