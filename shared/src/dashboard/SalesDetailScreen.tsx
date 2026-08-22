// The invoices behind the sales figure.
//
// What an owner opens when a day's takings are not what they expected. The
// question is almost always one of three: which sale was that, was anything
// refunded, and did everything report — so those are the three things the table
// answers without further clicking.

import { useCallback } from 'react';

import { fetchSales, type SaleRow } from '../api/drilldown';
import { useAuth } from '../auth/session';
import { money, shortDate } from '../ui/format';
import { DetailScreen, EmptyState, RemoteBody } from './DetailScreen';
import { InvoiceState } from './InvoiceState';
import { useRemote } from './useRemote';
import { useT } from '../i18n/locale';

export function SalesDetailScreen({
  companyId,
  date,
  onBack,
  onOpenInvoice,
}: {
  companyId: string;
  date: string;
  onBack: () => void;
  /** Opens one invoice (UI spec §5). Absent leaves the numbers as plain text,
   *  which is what a surface with nowhere to navigate to should show. */
  onOpenInvoice?: (invoiceId: string) => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const load = useCallback(
    () => fetchSales(client, companyId, date),
    [client, companyId, date],
  );
  const { remote, reload, refreshing } = useRemote(load);

  return (
    <DetailScreen
      title={t('common.sales')}
      subtitle={shortDate(date)}
      onBack={onBack}
      onRefresh={reload}
      refreshing={refreshing}
    >
      <RemoteBody remote={remote} onRetry={reload}>
        {(d) => (
          <>
            {/* The day's shape before the detail. Sales and refunds stay
                apart: netting them into one figure hides a day where a lot was
                sold and a lot came back, which is the day worth seeing. */}
            <section className="detail__summary" aria-label={t('dash.dayTotals')}>
              <Figure label={t('dash.sold')} value={money(d.sales_total, { currency: d.base_currency })} note={`${d.invoice_count} sale${d.invoice_count === 1 ? '' : 's'}`} />
              <Figure
                label={t('dash.refunded')}
                value={money(d.refund_total, { currency: d.base_currency })}
                note={`${d.refund_count} credit note${d.refund_count === 1 ? '' : 's'}`}
                muted={d.refund_count === 0}
              />
              <Figure label={t('common.net')} value={money(d.net_total, { currency: d.base_currency })} note="after refunds" strong />
              <Figure label={t('common.vat')} value={money(d.tax_total, { currency: d.base_currency })} note="on sales" />
            </section>

            <div className="ds-panel">
              <div className="ds-panel__body ds-scroll-x">
                {d.rows.length === 0 ? (
                  <EmptyState
                    title={`Nothing was rung up on ${shortDate(date)}`}
                    body="Sales appear here as the till records them. Nothing is wrong."
                  />
                ) : (
                  <table className="ds-table">
                    <thead>
                      <tr>
                        <th scope="col">{t('common.time')}</th>
                        <th scope="col">{t('common.invoice')}</th>
                        <th scope="col">{t('dash.paidBy')}</th>
                        <th scope="col">{t('dash.reporting')}</th>
                        <th scope="col" className="num">{t('common.amount')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {d.rows.map((row) => (
                        <Row
                          key={row.id}
                          row={row}
                          currency={d.base_currency}
                          onOpen={onOpenInvoice}
                        />
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            </div>

            {d.has_more && (
              <p className="ds-caption detail__more">
                Showing the most recent {d.rows.length}. The totals above cover
                the whole day.
              </p>
            )}
          </>
        )}
      </RemoteBody>
    </DetailScreen>
  );
}

function Row({
  row,
  currency,
  onOpen,
}: {
  row: SaleRow;
  currency: string;
  onOpen?: (invoiceId: string) => void;
}) {
  return (
    <tr className={row.is_credit_note ? 'detail__row--credit' : undefined}>
      <td className="num">{row.issued_at}</td>
      <td>
        {/* The number is the handle people quote, so it is the thing that
            opens the document. A whole clickable row would swallow selecting
            text, which is what somebody reading a figure out loud does. */}
        {onOpen ? (
          <button className="detail__open" onClick={() => onOpen(row.id)}>
            {row.human_number || row.id.slice(0, 8)}
          </button>
        ) : (
          <span className="detail__strong">
            {row.human_number || row.id.slice(0, 8)}
          </span>
        )}
        <span className="ds-caption">
          {row.is_credit_note ? 'Credit note' : `${row.line_count} item${row.line_count === 1 ? '' : 's'}`}
          {row.store_name ? ` · ${row.store_name}` : ''}
        </span>
      </td>
      <td>{row.tenders || <span className="ds-subtle">—</span>}</td>
      <td>
        <InvoiceState state={row.state} />
      </td>
      {/* A refund is money going the other way, so it reads as a negative.
          Brackets rather than a hyphen, per the design system. */}
      <td className="num">
        {money(row.is_credit_note ? `-${row.total_inclusive}` : row.total_inclusive, {
          currency,
        })}
      </td>
    </tr>
  );
}

function Figure({
  label,
  value,
  note,
  strong,
  muted,
}: {
  label: string;
  value: string;
  note: string;
  strong?: boolean;
  muted?: boolean;
}) {
  return (
    <div className={`figure${strong ? ' figure--strong' : ''}`}>
      <span className="ds-caption">{label}</span>
      <span className={`figure__value num${muted ? ' ds-subtle' : ''}`}>{value}</span>
      <span className="ds-caption">{note}</span>
    </div>
  );
}
