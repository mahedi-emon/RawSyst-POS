// Quotations and sales orders (blueprint B11, B12).
//
// # The list opens on what is unfinished
//
// A shop with four hundred completed orders opening this screen wants the six
// that are waiting for somebody. So the default is the working view and the
// finished ones are a press away, rather than the other way round.
//
// # A quotation that has expired says so
//
// B11 gives a quotation a validity date, and a quote nobody noticed had run out
// is how a shop honours a price it set three months ago. The badge is on the
// row, not in a filter somebody has to remember to apply.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { money, shortDate } from '../ui/format';
import { listOrders, type Order } from '../api/orders';
import { OrderDetail } from './OrderDetail';
import { OrderForm } from './OrderForm';

export function OrdersArea({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('order.manage');

  const [open, setOpen] = useState<string | null>(null);
  const [raising, setRaising] = useState(false);
  const [showAll, setShowAll] = useState(false);

  const load = useCallback(
    () => listOrders(client, companyId, { open: !showAll }),
    [client, companyId, showAll],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <OrderDetail
        companyId={companyId}
        orderId={open}
        onBack={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('orders.title')}</h1>
          <p className="ds-caption">{t('orders.intro')}</p>
        </div>

        <div className="detail__actions">
          <button
            className="ds-btn ds-btn--quiet"
            onClick={() => setShowAll(!showAll)}
          >
            {t(showAll ? 'orders.showOpenOnly' : 'orders.showAll')}
          </button>
          {mayManage && !raising && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setRaising(true)}
            >
              {t('orders.raise')}
            </button>
          )}
        </div>
      </header>

      {raising && (
        <OrderForm
          companyId={companyId}
          onCancel={() => setRaising(false)}
          onRaised={(id) => {
            setRaising(false);
            reload();
            setOpen(id);
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('orders.title')}>
        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Order[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t(showAll ? 'orders.noneTitle' : 'orders.noOpenTitle')}
                  body={t(showAll ? 'orders.noneBody' : 'orders.noOpenBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('orders.order')}</th>
                      <th scope="col">{t('orders.customer')}</th>
                      <th scope="col">{t('orders.channel')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col" className="num">
                        {t('orders.total')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((o) => (
                      <tr key={o.id}>
                        <td>
                          <span className="detail__strong">{o.order_no}</span>
                          <span className="ds-caption">
                            {shortDate(o.created_at, locale)}
                          </span>
                          {o.invoice_no && (
                            <span className="ds-caption">{o.invoice_no}</span>
                          )}
                        </td>
                        <td>{o.customer}</td>
                        <td>{t(`orders.channel.${o.channel}` as Key)}</td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${badgeFor(o)}`}
                          >
                            {t(`orders.state.${o.state}` as Key)}
                          </span>
                          {/* A quote nobody noticed had run out is how a shop
                              honours a price it set three months ago. */}
                          {o.expired && (
                            <span className="ds-badge ds-badge--warning">
                              {t('orders.expired')}
                            </span>
                          )}
                        </td>
                        <td className="num">
                          {money(o.total, { currency: o.currency })}
                        </td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setOpen(o.id)}
                          >
                            {t('action.view')}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          }
        </RemoteBody>
      </section>
    </main>
  );
}

function badgeFor(o: Order): string {
  switch (o.state) {
    case 'completed':
      return 'success';
    case 'cancelled':
      return 'neutral';
    case 'quotation':
      return o.expired ? 'warning' : 'info';
    default:
      return 'info';
  }
}
