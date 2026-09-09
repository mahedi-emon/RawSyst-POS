'use client';

// The X report for one till session.
//
// # Why it belongs here and not on the counter
//
// `GET /shifts/{id}/x-report` is gated on `report.view`, which a Cashier
// deliberately does not hold: somebody who can read the expected drawer before
// counting it can make the drawer agree, and the variance — the only signal
// there is — then reads zero on every shift. The till uses `GET /shifts/{id}`,
// which withholds the expected figure on a blind-close session.
//
// So this is the supervisor's reading, and the shift register is where a
// supervisor already is. The route was live and reachable from nothing, which
// meant the one figure a manager checks a drawer against existed and could not
// be looked at.
//
// # Withheld is not zero
//
// On a blind close the three takings figures and the expected cash are OMITTED
// from the response until the count is committed. An absent figure is shown as
// absent, with the reason, rather than as an em dash somebody reads as nil.

import { useT } from '@/lib/i18n/locale';
import { Button } from '@/components/ui/button';
import { Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { useApi } from '@/lib/api/hooks';
import { formatMoney, type MarketCode } from '@/lib/format/money';

/** The report, exactly as `GET /shifts/{id}/x-report` answers it. */
export interface ShiftReport {
  session_no: number;
  state: string;
  opened_at: string;
  closed_at?: string;
  opening_float: string;
  invoice_count: number;
  gross_sales: string;
  net_sales: string;
  tax_total: string;
  refund_total: string;
  /** Omitted on a blind close until the count is committed. */
  cash_takings?: string;
  non_cash_takings?: string;
  cash_movements?: string;
  expected_cash?: string;
  counted_cash?: string;
  variance?: string;
}

export function XReportPanel({
  companyId,
  sessionId,
  sessionNo,
  currency,
  market,
  onClose,
}: {
  companyId: string;
  sessionId: string;
  sessionNo: number;
  currency: string;
  market: MarketCode;
  onClose: () => void;
}) {
  const t = useT();
  const { data, isLoading, error, refetch } = useApi<ShiftReport>(
    `/shifts/${sessionId}/x-report`,
    { company_id: companyId },
  );

  const money = (v: string) => formatMoney(v, { currency, market });

  /** A figure, or the reason there is not one. */
  function figure(label: string, value: string | undefined, withheld: string) {
    return (
      <div key={label}>
        <dt className="text-label text-muted">{label}</dt>
        <dd className="num mt-0.5 text-body text-fg">
          {value === undefined ? (
            <span className="text-muted">{withheld}</span>
          ) : (
            money(value)
          )}
        </dd>
      </div>
    );
  }

  return (
    <Panel
      className="mb-5"
      title={t('nx.sft.xTitle', { n: String(sessionNo) })}
      description={t('nx.sft.xHint')}
      actions={
        <Button variant="ghost" onClick={onClose}>
          {t('nx.sft.xClose')}
        </Button>
      }
    >
      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <Skeleton className="h-32" /> : null}

      {data ? (
        <>
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-label text-muted">{t('nx.sft.xInvoices')}</dt>
              <dd className="num mt-0.5 text-body text-fg">{data.invoice_count}</dd>
            </div>
            {figure(t('nx.sft.xOpeningFloat'), data.opening_float, '—')}
            {figure(t('nx.sft.xGross'), data.gross_sales, '—')}
            {figure(t('nx.sft.xNet'), data.net_sales, '—')}
            {figure(t('nx.sft.xTax'), data.tax_total, '—')}
            {figure(t('nx.sft.xRefunds'), data.refund_total, '—')}
            {figure(t('nx.sft.xCashTakings'), data.cash_takings, t('nx.sft.xWithheld'))}
            {figure(
              t('nx.sft.xNonCashTakings'),
              data.non_cash_takings,
              t('nx.sft.xWithheld'),
            )}
            {figure(t('nx.sft.xMovements'), data.cash_movements, t('nx.sft.xWithheld'))}
            {figure(t('nx.sft.xExpected'), data.expected_cash, t('nx.sft.xWithheld'))}
            {figure(t('nx.sft.xCounted'), data.counted_cash, t('nx.sft.xNotCounted'))}
            {figure(t('nx.sft.xVariance'), data.variance, t('nx.sft.xNotCounted'))}
          </dl>

          {data.expected_cash === undefined ? (
            // Said plainly, because an absent figure on a money screen reads as
            // a fault rather than as a control.
            <p className="mt-4 max-w-prose text-caption text-muted">
              {t('nx.sft.xBlindNote')}
            </p>
          ) : null}
        </>
      ) : null}
    </Panel>
  );
}
