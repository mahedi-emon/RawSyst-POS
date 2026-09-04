'use client';

// What the business did today, and what needs somebody.
//
// # Attention comes first, figures come second
//
// An owner opening this screen has one real question: is anything wrong. The
// API already answers it -- `attention` is a list the server computed, each
// entry with a severity, a count and a link to the records behind it -- so that
// list is at the top, above the money. A dashboard that leads with a row of
// large numbers makes somebody read six figures to discover a problem the
// server already knew about.
//
// # Every figure opens
//
// A KPI you cannot open is trivia. An owner who sees an unexpected number has
// exactly one useful next question -- which transactions made it -- and a
// dashboard that cannot answer it sends them to a spreadsheet. The drill-through
// routes exist for this, and each tile here uses one.
//
// # Nothing is invented
//
// There are no placeholder charts, no sample sparklines and no figures made up
// to fill a grid. Where the server reports a capability as not yet built it is
// listed as such rather than mocked, because a fake number on a financial
// screen is worse than an absent one.

import { AlertTriangle, ArrowRight, Info, TriangleAlert } from 'lucide-react';
import Link from 'next/link';

import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { formatMoneyParts, formatQuantity, isNegative } from '@/lib/format/money';
import { cn } from '@/lib/utils';

interface TrendPoint {
  date: string;
  total: string;
}

interface AttentionItem {
  severity: 'critical' | 'warning' | 'notice';
  kind: string;
  title: string;
  detail: string;
  count: number;
  link: string;
}

interface Overview {
  date: string;
  base_currency: string;
  sales: {
    total: string;
    yesterday: string;
    change_pct: string | null;
    invoice_count: number;
    trend: TrendPoint[];
  };
  profit: {
    revenue: string;
    cost: string;
    gross: string;
    margin_pct: string | null;
  };
  expenses: { total: string };
  money: {
    cash: string;
    bank: string;
    unsettled: string;
    receivable: string;
    store_credit: string;
    accrued_purchases: string;
    total: string;
  };
  inventory: {
    value: string;
    low_stock: number;
    out_of_stock: number;
    variant_count: number;
  };
  tenders: { method: string; total: string; count: number }[];
  attention: AttentionItem[];
  unbuilt: string[];
}

/**
 * Where an attention row actually goes.
 *
 * The server returns links written for the OLD back office -- `/inventory`,
 * `/compliance` -- because it was built against it. Rendering them verbatim
 * sends somebody to a 404, which live validation showed on the very first
 * dashboard load.
 *
 * Mapped here rather than changed in Go on purpose: the API is shared with the
 * Tauri till and the old web app, both of which still resolve those paths. A
 * translation on this side is the change that breaks nothing. Anything the map
 * does not recognise falls through to the dashboard rather than to a dead URL.
 */
function routeFor(link: string): string {
  const [path = '', query] = link.split('?');
  const suffix = query ? `?${query}` : '';
  const map: Record<string, string> = {
    '/inventory': '/stock',
    '/stock': '/stock',
    '/compliance': '/oversight/compliance',
    '/customers': '/customers',
    '/sales': '/sales',
    '/purchasing': '/buying/orders',
    '/expenses': '/money/expenses',
    '/approvals': '/approvals',
  };
  const mapped = map[path];
  return mapped ? `${mapped}${suffix}` : '/dashboard';
}

export default function DashboardPage() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market, company } = useCompany();

  const { data, isLoading, error, refetch } = useApi<Overview>(
    scope ? '/dashboard/overview' : null,
    scope ?? undefined,
  );

  const money = (value: string | null | undefined, bare = true) =>
    formatMoneyParts(value, { currency, market, bare }).figure;

  return (
    <>
      <PageHeader
        title={t('nx.dash.title')}
        description={
          company
            ? t('nx.dash.subtitleNamed', {
                name: company.trade_name || company.legal_name,
              })
            : t('nx.dash.subtitle')
        }
      />

      {error && <ErrorState error={error} onRetry={() => void refetch()} />}

      {isLoading && !data && (
        <div className="flex flex-col gap-5">
          <Skeleton className="h-24 w-full" />
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-28 w-full" />
            ))}
          </div>
        </div>
      )}

      {data && (
        <div className="flex flex-col gap-6">
          <AttentionList items={data.attention} />

          {/* --- the four figures an owner actually asks for --------------- */}
          <section
            aria-label={t('nx.dash.today')}
            className="grid gap-px overflow-hidden rounded-md border border-line bg-line sm:grid-cols-2 xl:grid-cols-4"
          >
            {/* Gap-px on a line-coloured background draws the dividers, so the
                four figures read as one ruled block rather than four floating
                cards. */}
            <div className="bg-surface p-4">
              <Figure
                label={t('nx.dash.soldToday')}
                currency={currency}
                value={money(data.sales.total)}
                caption={
                  data.sales.change_pct
                    ? t('nx.dash.vsYesterday', {
                        pct: `${isNegative(data.sales.change_pct) ? '' : '+'}${data.sales.change_pct}`,
                        count: data.sales.invoice_count,
                      })
                    : t('nx.dash.invoices', { count: data.sales.invoice_count })
                }
                href="/sales"
              />
              {data.sales.trend.length > 1 && (
                <Sparkline points={data.sales.trend} />
              )}
            </div>

            <div className="bg-surface p-4">
              <Figure
                label={t('nx.dash.grossProfit')}
                currency={currency}
                value={money(data.profit.gross)}
                caption={
                  data.profit.margin_pct
                    ? t('nx.dash.marginOn', {
                        pct: data.profit.margin_pct,
                        revenue: money(data.profit.revenue),
                      })
                    : t('nx.dash.onRevenue', {
                        revenue: money(data.profit.revenue),
                      })
                }
                tone={isNegative(data.profit.gross) ? 'critical' : undefined}
                href="/reports/financials"
              />
            </div>

            <div className="bg-surface p-4">
              <Figure
                label={t('nx.dash.cashAndBank')}
                currency={currency}
                value={money(data.money.total)}
                caption={t('nx.dash.cashSplit', {
                  cash: money(data.money.cash),
                  bank: money(data.money.bank),
                })}
                href="/money/accounts"
              />
            </div>

            <div className="bg-surface p-4">
              <Figure
                label={t('nx.dash.owedToYou')}
                currency={currency}
                value={money(data.money.receivable)}
                caption={
                  data.money.unsettled !== '0'
                    ? t('nx.dash.unsettled', {
                        amount: money(data.money.unsettled),
                      })
                    : t('nx.dash.balancesOutstanding')
                }
                href="/customers/ageing"
              />
            </div>
          </section>

          <div className="grid gap-5 lg:grid-cols-2">
            <Panel
              title={t('nx.dash.howPaid')}
              description={t('nx.dash.howPaidDesc')}
            >
              {data.tenders.length === 0 ? (
                <p className="py-4 text-body text-muted">
                  {t('nx.dash.nothingTakenYet')}
                </p>
              ) : (
                <ul className="flex flex-col">
                  {data.tenders.map((tender) => (
                    <li
                      key={tender.method}
                      className="flex items-baseline justify-between gap-4 border-b border-line py-2.5 last:border-b-0"
                    >
                      <span className="text-body text-fg capitalize">
                        {tender.method.replace(/_/g, ' ')}
                        <span className="ms-2 text-caption text-subtle">
                          {tender.count === 1
                            ? t('nx.dash.saleOne')
                            : t('nx.dash.saleMany', { count: tender.count })}
                        </span>
                      </span>
                      <span className="num text-body font-medium tabular-nums">
                        {money(tender.total)}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </Panel>

            <Panel
              title={t('nx.dash.stock')}
              description={t('nx.dash.stockDesc')}
            >
              <dl className="grid grid-cols-2 gap-4">
                <div>
                  <dt className="text-label text-muted">
                    {t('nx.dash.valueAtCost')}
                  </dt>
                  <dd className="num mt-0.5 text-section font-semibold tabular-nums">
                    <span className="text-label font-medium text-muted">
                      {currency}{' '}
                    </span>
                    {money(data.inventory.value)}
                  </dd>
                </div>
                <div>
                  <dt className="text-label text-muted">
                    {t('nx.dash.linesHeld')}
                  </dt>
                  <dd className="num mt-0.5 text-section font-semibold tabular-nums">
                    {formatQuantity(String(data.inventory.variant_count), market)}
                  </dd>
                </div>
                <div>
                  <dt className="text-label text-muted">
                    {t('nx.dash.runningLow')}
                  </dt>
                  <dd className="mt-0.5">
                    <Link
                      href="/stock?filter=low"
                      className="num text-section font-semibold tabular-nums hover:underline"
                    >
                      {data.inventory.low_stock}
                    </Link>
                  </dd>
                </div>
                <div>
                  <dt className="text-label text-muted">
                    {t('stock.outOfStock')}
                  </dt>
                  <dd className="mt-0.5">
                    <Link
                      href="/stock?filter=out"
                      className={cn(
                        'num text-section font-semibold tabular-nums hover:underline',
                        data.inventory.out_of_stock > 0 && 'text-critical-fg',
                      )}
                    >
                      {data.inventory.out_of_stock}
                    </Link>
                  </dd>
                </div>
              </dl>
            </Panel>
          </div>

          {/* The server says which capabilities it cannot report on yet.
              Saying so is better than a tile of zeros, which reads as a
              business with no expenses rather than a figure not yet wired. */}
          {data.unbuilt.length > 0 && (
            <p className="text-caption text-subtle">
              {t('nx.dash.notReported', { items: data.unbuilt.join(', ') })}
            </p>
          )}
        </div>
      )}
    </>
  );
}

function AttentionList({ items }: { items: AttentionItem[] }) {
  const t = useT();
  if (items.length === 0) {
    return (
      <EmptyState
        title={t('nx.dash.nothingNeedsYou')}
        description={t('nx.dash.nothingNeedsYouDesc')}
      />
    );
  }

  const icon = {
    critical: TriangleAlert,
    warning: AlertTriangle,
    notice: Info,
  } as const;

  const tone = {
    critical: 'critical',
    warning: 'caution',
    notice: 'info',
  } as const;

  return (
    <section aria-label={t('dash.needsAttention')}>
      <h2 className="pb-2 text-label font-semibold text-muted">
        {t('dash.needsAttention')}
      </h2>
      <ul className="overflow-hidden rounded-md border border-line bg-surface">
        {items.map((item) => {
          const Icon = icon[item.severity] ?? Info;
          return (
            <li key={`${item.kind}-${item.title}`}>
              <Link
                href={routeFor(item.link)}
                className={cn(
                  'flex items-start gap-3 border-b border-line px-4 py-3 last:border-b-0',
                  'hover:bg-surface-hover',
                )}
              >
                <Icon
                  className={cn(
                    'mt-0.5 size-4 shrink-0',
                    item.severity === 'critical' && 'text-critical',
                    item.severity === 'warning' && 'text-caution',
                    item.severity === 'notice' && 'text-info',
                  )}
                  aria-hidden="true"
                />
                <div className="min-w-0 flex-1">
                  <p className="text-body font-medium text-fg">
                    {item.title}
                    {item.count > 1 && (
                      <Badge tone={tone[item.severity]} className="ms-2">
                        {item.count}
                      </Badge>
                    )}
                  </p>
                  <p className="mt-0.5 text-label text-muted">{item.detail}</p>
                </div>
                <ArrowRight
                  className="mt-0.5 size-4 shrink-0 text-disabled rtl:rotate-180"
                  aria-hidden="true"
                />
              </Link>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/**
 * Fourteen days of takings.
 *
 * Deliberately unlabelled and unaxed: it is there to show the SHAPE of the last
 * fortnight beside today's figure, which is a question an owner answers by
 * glancing rather than by reading. Anybody who wants the numbers opens Sales,
 * which the tile above already links to. A chart with a legend and a tooltip
 * here would be a second, worse version of that screen.
 */
function Sparkline({ points }: { points: TrendPoint[] }) {
  // Compared as decimal strings by length-then-lexical order, which is correct
  // for non-negative fixed-point values and avoids parsing money to a float
  // just to draw a line.
  const values = points.map((p) => p.total);
  const numeric = values.map((v) => {
    const [whole = '0', frac = ''] = v.replace('-', '').split('.');
    // Only for the drawing geometry -- never shown, never compared to an
    // amount, and never sent back to the server.
    return Number(`${whole}.${frac.slice(0, 4)}`) * (v.startsWith('-') ? -1 : 1);
  });
  const max = Math.max(...numeric, 0);
  const min = Math.min(...numeric, 0);
  const span = max - min || 1;

  const w = 100;
  const h = 24;
  const step = points.length > 1 ? w / (points.length - 1) : w;
  const d = numeric
    .map((v, i) => {
      const x = (i * step).toFixed(2);
      const y = (h - ((v - min) / span) * h).toFixed(2);
      return `${i === 0 ? 'M' : 'L'}${x},${y}`;
    })
    .join(' ');

  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      className="mt-3 h-6 w-full"
      // Decorative: the figure above it carries the information, and the
      // caption states the comparison. Announcing fourteen unlabelled numbers
      // would be noise.
      aria-hidden="true"
      focusable="false"
    >
      <path
        d={d}
        fill="none"
        stroke="var(--ry-primary)"
        strokeWidth="1.5"
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}
