'use client';

// D2's analytics.
//
// # Thirteen figures, one request
//
// They arrive together because an average order value that does not divide
// THIS revenue by THESE orders is worse than none. A screen firing a request
// per widget takes its figures from thirteen different instants and can appear
// not to add up when nothing is wrong.
//
// # A blank is not a zero
//
// Four of them come back empty on a young business: inventory turnover needs a
// period of purchase history, repeat-customer share and lifetime value need
// customers who have come back. They render as a dash and a sentence, never as
// 0% — a shop told nobody returns will believe it.
//
// # The forecast says what it is, next to the number
//
// "Sales over the last 90 days, repeated." An owner ordering stock against a
// forecast has to know it is arithmetic on the past. A forecast that hides its
// method gets trusted more than it deserves, and this one is deliberately
// modest.

import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Input } from '@/components/ui/field';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { Tabs, TabPanel } from '@/components/ui/tabs';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  isShort,
  moversFor,
  stated,
  type ForecastLine,
  type KPIs,
  type Mover,
  type MoverView,
  type ProfitLine,
} from '@/lib/reports/analytics';
import { yearToDate } from '@/lib/reports/statements';
import { useUrlState } from '@/lib/url-state';

/** The thirteen, in the order an owner reads them. */
const FIGURES: { key: keyof KPIs; label: Key; money?: boolean; suffix?: string }[] = [
  { key: 'revenue', label: 'nx.an.revenue', money: true },
  { key: 'gross_profit', label: 'nx.an.grossProfit', money: true },
  { key: 'gross_margin_pct', label: 'nx.an.grossMargin', suffix: '%' },
  { key: 'average_order_value', label: 'nx.an.aov', money: true },
  { key: 'units_per_transaction', label: 'nx.an.upt' },
  { key: 'discount_ratio_pct', label: 'nx.an.discountRatio', suffix: '%' },
  { key: 'return_rate_pct', label: 'nx.an.returnRate', suffix: '%' },
  { key: 'inventory_turnover', label: 'nx.an.turnover' },
  { key: 'repeat_customer_pct', label: 'nx.an.repeat', suffix: '%' },
  { key: 'customer_lifetime_value', label: 'nx.an.clv', money: true },
  { key: 'sales_per_store', label: 'nx.an.perStore', money: true },
  { key: 'sales_per_employee', label: 'nx.an.perEmployee', money: true },
];

function AnalyticsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const period = yearToDate();
  const [from, setFrom] = useUrlState('from', period.from);
  const [to, setTo] = useUrlState('to', period.to);
  const [rawView, setView] = useUrlState('view', 'fast');
  const view: MoverView = rawView === 'dead' ? 'dead' : 'fast';

  const query = scope ? { ...scope, from, to } : undefined;
  const kpis = useApi<KPIs>(scope ? '/analytics/kpis' : null, query);
  const movers = useApiList<Mover>(scope ? '/analytics/movers' : null, query);
  const forecast = useApiList<ForecastLine>(
    scope ? '/analytics/forecast' : null,
    query,
  );
  const profit = useApiList<ProfitLine>(
    scope ? '/analytics/profitability' : null,
    query,
  );

  const k = kpis.data;
  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || k?.currency || currency, market });

  const rows = moversFor(movers.data?.data ?? [], view);
  const short = (forecast.data?.data ?? []).filter(isShort);

  const moverColumns: Column<Mover>[] = [
    {
      key: 'product',
      header: t('nx.an.colProduct'),
      primary: true,
      cell: (m) => (
        <span className="flex flex-col gap-0.5">
          <span>{m.product}</span>
          <span className="num text-caption text-muted">{m.sku}</span>
        </span>
      ),
    },
    {
      key: 'sold',
      header: t('nx.an.colSold'),
      numeric: true,
      width: 'w-24',
      cell: (m) => <span className="num">{m.sold_qty}</span>,
    },
    {
      key: 'revenue',
      header: t('nx.an.colRevenue'),
      numeric: true,
      width: 'w-36',
      cell: (m) => <span className="num">{money(m.revenue, m.currency)}</span>,
    },
    {
      key: 'profit',
      header: t('nx.an.colProfit'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (m) => (
        <span className="num text-muted">{money(m.profit, m.currency)}</span>
      ),
    },
    {
      key: 'onhand',
      header: t('nx.an.colOnHand'),
      numeric: true,
      width: 'w-24',
      cell: (m) => <span className="num">{m.on_hand}</span>,
    },
    {
      key: 'cover',
      header: t('nx.an.colCover'),
      numeric: true,
      secondary: true,
      width: 'w-40',
      cell: (m) =>
        m.days_cover !== undefined ? (
          <span className="flex flex-col items-end gap-0.5">
            <span className="num">{t('nx.an.days', { days: String(m.days_cover) })}</span>
            {m.reorder_on ? (
              <span className="num text-caption text-muted">{m.reorder_on}</span>
            ) : null}
          </span>
        ) : (
          // Never sold, so there is no rate to divide the shelf by. A large
          // number here would be arithmetic on nothing.
          <span className="text-muted">{t('nx.an.noCover')}</span>
        ),
    },
  ];

  const forecastColumns: Column<ForecastLine>[] = [
    {
      key: 'product',
      header: t('nx.an.colProduct'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>{l.product}</span>
          <span className="num text-caption text-muted">{l.sku}</span>
        </span>
      ),
    },
    {
      key: 'expected',
      header: t('nx.an.colExpected'),
      numeric: true,
      width: 'w-32',
      cell: (l) => <span className="num">{l.expected_demand}</span>,
    },
    {
      key: 'onhand',
      header: t('nx.an.colOnHand'),
      numeric: true,
      width: 'w-24',
      cell: (l) => <span className="num">{l.on_hand}</span>,
    },
    {
      key: 'shortfall',
      header: t('nx.an.colShortfall'),
      numeric: true,
      width: 'w-28',
      cell: (l) =>
        isShort(l) ? (
          <Badge tone="caution">
            <span className="num">{l.shortfall}</span>
          </Badge>
        ) : (
          <span className="num text-muted">0</span>
        ),
    },
  ];

  const profitColumns: Column<ProfitLine>[] = [
    { key: 'label', header: t('nx.an.colCategory'), primary: true, cell: (p) => p.label },
    {
      key: 'units',
      header: t('nx.an.colUnits'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      cell: (p) => <span className="num text-muted">{p.units}</span>,
    },
    {
      key: 'revenue',
      header: t('nx.an.colRevenue'),
      numeric: true,
      width: 'w-36',
      cell: (p) => <span className="num">{money(p.revenue, p.currency)}</span>,
    },
    {
      key: 'cost',
      header: t('nx.an.colCost'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (p) => <span className="num text-muted">{money(p.cost, p.currency)}</span>,
    },
    {
      key: 'profit',
      header: t('nx.an.colProfit'),
      numeric: true,
      width: 'w-36',
      cell: (p) => (
        <span className="num font-medium">{money(p.profit, p.currency)}</span>
      ),
    },
    {
      key: 'margin',
      header: t('nx.an.colMargin'),
      numeric: true,
      width: 'w-24',
      cell: (p) => <span className="num">{p.margin_pct}%</span>,
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.an.title')} description={t('nx.an.subtitle')} />

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
      </div>

      {kpis.error ? (
        <ErrorState error={kpis.error} onRetry={() => void kpis.refetch()} />
      ) : null}

      {k ? (
        <>
          <div className="mb-3 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Panel>
              <Figure
                label={t('nx.an.orders')}
                value={String(k.orders)}
                caption={t('nx.an.inPeriod')}
              />
            </Panel>
            {FIGURES.map((f) => {
              const raw = k[f.key] as string;
              return (
                <Panel key={f.key}>
                  {stated(raw) ? (
                    <Figure
                      label={t(f.label)}
                      value={f.money ? money(raw) : `${raw}${f.suffix ?? ''}`}
                    />
                  ) : (
                    // A dash and a reason. Rendering 0% here would tell a shop
                    // something false about itself.
                    <Figure
                      label={t(f.label)}
                      value="—"
                      caption={t('nx.an.notYet')}
                    />
                  )}
                </Panel>
              );
            })}
          </div>
          <p className="mb-6 max-w-prose text-caption text-muted">
            {t('nx.an.oneRequest')}
          </p>
        </>
      ) : (
        <div className="mb-6 h-40" aria-busy="true" />
      )}

      <h2 className="mb-3 text-card-title font-semibold text-fg">
        {t('nx.an.moversTitle')}
      </h2>
      <Tabs
        label={t('nx.an.moversTitle')}
        value={view}
        onChange={setView}
        items={[
          { id: 'fast', label: t('nx.an.fast') },
          { id: 'dead', label: t('nx.an.dead') },
        ]}
      />
      <TabPanel id={view}>
        <p className="mb-3 max-w-prose text-caption text-muted">
          {view === 'fast' ? t('nx.an.fastHint') : t('nx.an.deadHint')}
        </p>
        {movers.isLoading && !movers.data ? <TableSkeleton columns={6} /> : null}
        {!movers.isLoading && rows.length === 0 ? (
          <EmptyState
            title={t('nx.an.noMoversTitle')}
            description={t('nx.an.noMoversDesc')}
          />
        ) : null}
        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.an.moversTitle')}
            columns={moverColumns}
            rows={rows}
            rowKey={(m) => m.variant_id}
          />
        ) : null}
      </TabPanel>

      <div className="mt-8 grid gap-6 lg:grid-cols-2">
        <div className="min-w-0">
          <h2 className="mb-1 text-card-title font-semibold text-fg">
            {t('nx.an.forecastTitle')}
          </h2>
          {/* The server's own sentence about what this is, next to it. */}
          <p className="mb-3 max-w-prose text-caption text-muted">
            {forecast.data?.data?.[0]?.basis ?? t('nx.an.forecastHint')}
          </p>
          {short.length === 0 ? (
            <EmptyState
              title={t('nx.an.nothingShortTitle')}
              description={t('nx.an.nothingShortDesc')}
            />
          ) : (
            <DataTable
              caption={t('nx.an.forecastTitle')}
              columns={forecastColumns}
              rows={short}
              rowKey={(l) => l.variant_id}
            />
          )}
        </div>

        <div className="min-w-0">
          <h2 className="mb-1 text-card-title font-semibold text-fg">
            {t('nx.an.profitTitle')}
          </h2>
          <p className="mb-3 max-w-prose text-caption text-muted">
            {t('nx.an.profitHint')}
          </p>
          {(profit.data?.data ?? []).length === 0 ? (
            <EmptyState
              title={t('nx.an.noProfitTitle')}
              description={t('nx.an.noProfitDesc')}
            />
          ) : (
            <DataTable
              caption={t('nx.an.profitTitle')}
              columns={profitColumns}
              rows={profit.data?.data ?? []}
              rowKey={(p) => p.id || p.label}
            />
          )}
        </div>
      </div>
    </>
  );
}

export default function AnalyticsPage() {
  return (
    <RequirePermission anyOf={['report.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <AnalyticsScreen />
      </Suspense>
    </RequirePermission>
  );
}
