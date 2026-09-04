'use client';

// Sales, one trading day at a time.
//
// # Why a day and not an infinite list
//
// Because that is the capability the backend has, and it is the right one.
// There is no "list every invoice ever" route; `GET /dashboard/sales` takes a
// `date` and answers with that day's invoices AND that day's totals. A shop
// reconciles by the day -- the drawer is counted by the day, the Z report is by
// the day, and "what did we take on Tuesday" is the question actually asked.
//
// Building a scrolling all-time list would have meant inventing an endpoint, or
// worse, paging one day at a time behind a UI that pretended otherwise.
//
// # The totals are the day's, not the page's
//
// The server computes them over the whole day and says so, so a truncated list
// still tells an owner what the day came to. `has_more` is surfaced rather than
// hidden, because a day with more invoices than the page holds is a fact.

import { ArrowLeft, ArrowRight, ReceiptText } from 'lucide-react';
import { useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

interface SaleRow {
  id: string;
  human_number?: string;
  doc_type: string;
  /** The ZATCA lifecycle position. An identifier, shown through `stateOf`. */
  state: string;
  /** Time of day only -- the date is the page's subject. */
  issued_at: string;
  total_inclusive: string;
  tax_total: string;
  /** A readable summary: "Cash", or "Cash + Mada" for a split. */
  tenders: string;
  line_count: number;
  store_name?: string;
  is_credit_note: boolean;
}

interface SalesDay {
  date: string;
  rows: SaleRow[];
  sales_total: string;
  refund_total: string;
  net_total: string;
  tax_total: string;
  invoice_count: number;
  refund_count: number;
  retail_total: string;
  wholesale_total: string;
  retail_count: number;
  wholesale_count: number;
  has_more: boolean;
  base_currency: string;
}

/**
 * The ZATCA lifecycle, in words.
 *
 * The raw value is an identifier the API defines and a shopkeeper has never
 * seen. What they need to know is whether the authority has it: anything
 * unrecognised falls through to the identifier rather than to a guess, because
 * a wrong reassuring label on a compliance state is worse than an unfamiliar
 * one.
 */
function stateOf(state: string): { key: Key | null; tone: Tone } {
  switch (state) {
    case 'reported':
      return { key: 'nx.sales.stateReported', tone: 'positive' };
    case 'cleared':
      return { key: 'nx.sales.stateCleared', tone: 'positive' };
    case 'signed_pending_report':
    case 'pending':
      return { key: 'nx.sales.statePending', tone: 'caution' };
    case 'failed':
    case 'rejected':
      return { key: 'nx.sales.stateFailed', tone: 'critical' };
    default:
      return { key: null, tone: 'neutral' };
  }
}

/** Today, as the shop's own date string. */
function today(): string {
  return new Date().toISOString().slice(0, 10);
}

/** Steps a YYYY-MM-DD string by whole days without a timezone getting involved. */
function shiftDay(date: string, days: number): string {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

function SalesScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const [date, setDate] = useState(today());

  const { data, isLoading, error, refetch } = useApi<SalesDay>(
    scope ? '/dashboard/sales' : null,
    scope ? { ...scope, date, limit: 200 } : undefined,
  );

  const money = (v: string | null | undefined) =>
    formatMoney(v ?? null, {
      currency: data?.base_currency ?? currency,
      market,
      bare: true,
    });

  const columns: Column<SaleRow>[] = [
    {
      key: 'number',
      header: t('nx.sales.colNumber'),
      primary: true,
      cell: (r) => (
        <span className="flex items-center gap-2">
          <span className="num">{r.human_number || r.id.slice(0, 8)}</span>
          {r.is_credit_note && (
            <Badge tone="caution">{t('nx.sales.creditNote')}</Badge>
          )}
        </span>
      ),
    },
    {
      key: 'time',
      header: t('nx.sales.colTime'),
      width: 'w-20',
      cell: (r) => <span className="num text-muted">{r.issued_at}</span>,
    },
    {
      key: 'tenders',
      header: t('nx.sales.colTenders'),
      secondary: true,
      cell: (r) => <span className="text-muted">{r.tenders || '—'}</span>,
    },
    {
      key: 'lines',
      header: t('nx.sales.colLines'),
      numeric: true,
      secondary: true,
      width: 'w-20',
      cell: (r) => r.line_count,
    },
    {
      key: 'state',
      header: t('nx.cat.colStatus'),
      width: 'w-40',
      cell: (r) => {
        const s = stateOf(r.state);
        return (
          <Badge tone={s.tone}>
            {s.key ? t(s.key) : r.state.replace(/_/g, ' ')}
          </Badge>
        );
      },
    },
    {
      key: 'tax',
      header: t('nx.sales.colTax'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (r) => money(r.tax_total),
    },
    {
      key: 'total',
      header: t('nx.sales.colTotal'),
      numeric: true,
      width: 'w-32',
      cell: (r) => <span className="font-medium">{money(r.total_inclusive)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.sales.dayTitle')}
        description={t('nx.sales.daySubtitle')}
        actions={
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon"
              aria-label={t('nx.sales.previousDay')}
              onClick={() => setDate((d) => shiftDay(d, -1))}
            >
              {/* Directional: previous is toward the inline start, which is the
                  right-hand side in Arabic. */}
              <ArrowLeft className="rtl:rotate-180" aria-hidden="true" />
            </Button>
            <input
              type="date"
              value={date}
              onChange={(e) => setDate(e.target.value || today())}
              aria-label={t('nx.sales.day')}
              className="num h-9 rounded-sm border border-input bg-input-bg px-2 text-body"
            />
            <Button
              variant="ghost"
              size="icon"
              aria-label={t('nx.sales.nextDay')}
              disabled={date >= today()}
              onClick={() => setDate((d) => shiftDay(d, 1))}
            >
              <ArrowRight className="rtl:rotate-180" aria-hidden="true" />
            </Button>
            {date !== today() && (
              <Button variant="secondary" onClick={() => setDate(today())}>
                {t('nx.sales.today')}
              </Button>
            )}
          </div>
        }
      />

      {error && <ErrorState error={error} onRetry={() => void refetch()} />}

      {isLoading && !data && <TableSkeleton columns={7} />}

      {data && (
        <div className="flex flex-col gap-6">
          <section
            aria-label={t('nx.sales.dayTitle')}
            className="grid gap-px overflow-hidden rounded-md border border-line bg-line sm:grid-cols-2 xl:grid-cols-4"
          >
            <Total
              label={t('nx.sales.sold')}
              value={money(data.sales_total)}
              currency={data.base_currency}
              caption={t('nx.sales.invoiceCount', { count: data.invoice_count })}
            />
            <Total
              label={t('nx.sales.refunded')}
              value={money(data.refund_total)}
              currency={data.base_currency}
              caption={t('nx.sales.refundCount', { count: data.refund_count })}
              tone={isZero(data.refund_total) ? undefined : 'critical'}
            />
            <Total
              label={t('nx.sales.net')}
              value={money(data.net_total)}
              currency={data.base_currency}
              // B12 asks for wholesale to be kept apart from retail so retail
              // reporting is not distorted by bulk orders. The server splits
              // them; showing only the sum would put the distortion back.
              caption={
                data.wholesale_count > 0
                  ? t('nx.sales.splitWholesale', {
                      amount: money(data.wholesale_total),
                      count: data.wholesale_count,
                    })
                  : t('nx.sales.splitRetail', {
                      amount: money(data.retail_total),
                      count: data.retail_count,
                    })
              }
            />
            <Total
              label={t('nx.sales.taxCollected')}
              value={money(data.tax_total)}
              currency={data.base_currency}
            />
          </section>

          <Panel flush>
            {data.rows.length === 0 ? (
              <div className="p-4">
                <EmptyState
                  icon={ReceiptText}
                  title={t('nx.sales.emptyDayTitle')}
                  description={t('nx.sales.emptyDayDesc')}
                  action={
                    <Button asChild variant="primary">
                      <a href="/pos">{t('nx.sales.openPos')}</a>
                    </Button>
                  }
                />
              </div>
            ) : (
              <>
                <DataTable
                  caption={t('nx.sales.caption')}
                  columns={columns}
                  rows={data.rows}
                  rowKey={(r) => r.id}
                  className="rounded-none border-0"
                />
                {data.has_more && (
                  <p className="border-t border-line px-4 py-2.5 text-caption text-muted">
                    {t('nx.sales.moreThanShown')}
                  </p>
                )}
              </>
            )}
          </Panel>
        </div>
      )}
    </>
  );
}

function Total({
  label,
  value,
  currency,
  caption,
  tone,
}: {
  label: string;
  value: string;
  currency: string;
  caption?: string;
  tone?: 'critical';
}) {
  return (
    <div className="bg-surface p-4">
      <p className="text-label text-muted">{label}</p>
      <p
        className={cn(
          'num mt-1 flex items-baseline gap-1.5 text-display font-semibold tabular-nums tracking-tight',
          tone === 'critical' && 'text-critical-fg',
        )}
      >
        <span className="text-lede font-medium text-muted">{currency}</span>
        {value}
      </p>
      {caption && <p className="mt-1 text-caption text-subtle">{caption}</p>}
    </div>
  );
}

export default function SalesPage() {
  return (
    <RequirePermission anyOf={['sales.view']}>
      <SalesScreen />
    </RequirePermission>
  );
}
