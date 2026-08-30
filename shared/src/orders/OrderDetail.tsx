// One order, and everything anybody does to it.
//
// # The lifecycle is drawn, not described
//
// Six steps across the top with the current one marked. A person who picks up
// somebody else's order needs to know where it got to before they need to know
// anything else, and a word in a status column does not tell them what comes
// next.
//
// # Picking is typed against what is outstanding, not against what was ordered
//
// The quantity boxes start empty and the row says how many are still to pick.
// A box pre-filled with the ordered quantity means a picker who found four of
// six on the shelf has to correct a number the screen made up, and the common
// failure is that they do not.

import { useCallback, useState } from 'react';

import {
  advanceOrder,
  cancelOrder,
  orderDocument,
  readOrder,
  recordDelivered,
  recordPicked,
  type Order,
  type OrderDocument,
} from '../api/orders';
import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError, TextInput } from '../ui/Form';
import { money, shortDate } from '../ui/format';
import { DocumentSheet } from './DocumentSheet';
import {
  LIFECYCLE,
  cancellable,
  documentsFor,
  isZero,
  nextState,
  outstanding,
  stepOf,
} from './orders';

export function OrderDetail({
  companyId,
  orderId,
  onBack,
}: {
  companyId: string;
  orderId: string;
  onBack: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('order.manage');

  const load = useCallback(
    () => readOrder(client, companyId, orderId),
    [client, companyId, orderId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const [reason, setReason] = useState('');
  const [sheet, setSheet] = useState<OrderDocument | null>(null);
  const [recording, setRecording] = useState<'pick' | 'deliver' | null>(null);
  const [typed, setTyped] = useState<Record<string, string>>({});

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function show(kind: 'picking' | 'packing' | 'delivery') {
    setFailure(null);
    try {
      setSheet(await orderDocument(client, companyId, orderId, kind));
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    }
  }

  function submitQuantities(order: Order) {
    const lines = Object.entries(typed)
      .filter(([, qty]) => qty.trim() !== '' && !isZero(qty))
      .map(([line_id, qty]) => ({ line_id, qty: qty.trim() }));
    if (lines.length === 0) return;

    const record = recording === 'pick' ? recordPicked : recordDelivered;
    void run(async () => {
      await record(client, companyId, order.id, lines);
      setRecording(null);
      setTyped({});
    });
  }

  return (
    <main className="detail">
      <RemoteBody remote={remote} onRetry={reload}>
        {(order: Order) => {
          const at = stepOf(order.state);
          const next = nextState(order.state);
          const documents = documentsFor(order);

          return (
            <>
              <header className="detail__head">
                <div className="detail__titles">
                  <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                    {t('action.back')}
                  </button>
                  <h1 className="ds-h1">{order.order_no}</h1>
                  <p className="ds-caption">
                    {order.customer ?? t('orders.walkIn')} ·{' '}
                    {t(`orders.channel.${order.channel}` as Key)} ·{' '}
                    {shortDate(order.created_at, locale)}
                  </p>
                </div>

                <div className="detail__actions">
                  {documents.map((kind) => (
                    <button
                      key={kind}
                      className="ds-btn ds-btn--quiet"
                      onClick={() => void show(kind)}
                    >
                      {t(`orders.document.${kind}` as Key)}
                    </button>
                  ))}

                  {mayManage && order.state === 'confirmed' && (
                    <button
                      className="ds-btn ds-btn--quiet"
                      onClick={() => setRecording('pick')}
                    >
                      {t('orders.recordPicked')}
                    </button>
                  )}
                  {mayManage && order.state === 'packed' && (
                    <button
                      className="ds-btn ds-btn--quiet"
                      onClick={() => setRecording('deliver')}
                    >
                      {t('orders.recordDelivered')}
                    </button>
                  )}

                  {mayManage && cancellable(order.state) && !cancelling && (
                    <button
                      className="ds-btn ds-btn--quiet"
                      onClick={() => setCancelling(true)}
                    >
                      {t('orders.cancel')}
                    </button>
                  )}

                  {mayManage && next && (
                    <button
                      className="ds-btn ds-btn--primary"
                      disabled={busy}
                      onClick={() =>
                        void run(() => advanceOrder(client, companyId, order.id))
                      }
                    >
                      {t('orders.moveTo', {
                        state: t(`orders.state.${next}` as Key),
                      })}
                    </button>
                  )}
                </div>
              </header>

              <FormError message={failure} />

              {/* A cancelled order did not stop somewhere along the line, it
                  left. Drawing it on the track would say the opposite. */}
              {order.state === 'cancelled' ? (
                <p className="orders__cancelled" role="status">
                  {t('orders.wasCancelled', {
                    reason: order.cancel_reason ?? '',
                  })}
                </p>
              ) : (
                <ol className="orders__track" aria-label={t('common.status')}>
                  {LIFECYCLE.map((state, i) => (
                    <li
                      key={state}
                      className={`orders__step${
                        i < at ? ' orders__step--done' : ''
                      }${i === at ? ' orders__step--now' : ''}`}
                      aria-current={i === at ? 'step' : undefined}
                    >
                      {t(`orders.state.${state}` as Key)}
                    </li>
                  ))}
                </ol>
              )}

              {order.state === 'delivered' && (
                // The one thing this screen cannot do. Saying so beats a button
                // that comes back refused.
                <p className="ds-caption orders__hint">
                  {t('orders.invoiceToComplete')}
                </p>
              )}

              {cancelling && (
                <div className="ds-panel orders__form">
                  <div className="ds-panel__body">
                    <label className="field__label" htmlFor="cancel-reason">
                      {t('orders.cancelReason')}
                    </label>
                    <TextInput
                      id="cancel-reason"
                      value={reason}
                      onChange={setReason}
                    />
                    <div className="form__actions">
                      <button
                        className="ds-btn ds-btn--primary"
                        disabled={busy || reason.trim() === ''}
                        onClick={() =>
                          void run(async () => {
                            await cancelOrder(
                              client,
                              companyId,
                              order.id,
                              reason.trim(),
                            );
                            setCancelling(false);
                            setReason('');
                          })
                        }
                      >
                        {t('orders.cancel')}
                      </button>
                      <button
                        className="ds-btn ds-btn--quiet"
                        onClick={() => setCancelling(false)}
                      >
                        {t('action.cancel')}
                      </button>
                    </div>
                  </div>
                </div>
              )}

              <section className="ds-panel" aria-label={t('orders.lines')}>
                <div className="ds-panel__head">
                  <h2 className="ds-h3">{t('orders.lines')}</h2>
                  {recording && (
                    <div className="orders__actions">
                      <button
                        className="ds-btn ds-btn--primary"
                        disabled={busy}
                        onClick={() => submitQuantities(order)}
                      >
                        {t(
                          recording === 'pick'
                            ? 'orders.savePicked'
                            : 'orders.saveDelivered',
                        )}
                      </button>
                      <button
                        className="ds-btn ds-btn--quiet"
                        onClick={() => {
                          setRecording(null);
                          setTyped({});
                        }}
                      >
                        {t('action.cancel')}
                      </button>
                    </div>
                  )}
                </div>

                <div className="ds-panel__body ds-scroll-x">
                  <table className="ds-table">
                    <thead>
                      <tr>
                        <th scope="col">{t('orders.item')}</th>
                        <th scope="col" className="num">
                          {t('orders.qty')}
                        </th>
                        <th scope="col" className="num">
                          {t('orders.picked')}
                        </th>
                        <th scope="col" className="num">
                          {t('orders.delivered')}
                        </th>
                        <th scope="col" className="num">
                          {t('orders.unitPrice')}
                        </th>
                        <th scope="col" className="num">
                          {t('orders.lineTotal')}
                        </th>
                        {recording && (
                          <th scope="col" className="num">
                            {t(
                              recording === 'pick'
                                ? 'orders.pickingNow'
                                : 'orders.deliveringNow',
                            )}
                          </th>
                        )}
                      </tr>
                    </thead>
                    <tbody>
                      {(order.lines ?? []).map((l) => (
                        <tr key={l.id}>
                          <td>
                            <span className="detail__strong">{l.product}</span>
                            <span className="ds-caption">{l.sku}</span>
                            {l.description && (
                              <span className="ds-caption">{l.description}</span>
                            )}
                          </td>
                          <td className="num">{l.qty}</td>
                          <td className="num">
                            {l.qty_picked}
                            {/* What the picker actually works from. */}
                            {!isZero(outstanding(l)) && (
                              <span className="ds-caption">
                                {t('orders.stillToPick', {
                                  qty: outstanding(l),
                                })}
                              </span>
                            )}
                          </td>
                          <td className="num">{l.qty_delivered}</td>
                          <td className="num">
                            {money(l.unit_price, { currency: order.currency })}
                          </td>
                          <td className="num">
                            {money(l.line_total, { currency: order.currency })}
                          </td>
                          {recording && (
                            <td className="num">
                              <TextInput
                                id={`record-${l.id}`}
                                value={typed[l.id] ?? ''}
                                onChange={(v) =>
                                  setTyped((prev) => ({ ...prev, [l.id]: v }))
                                }
                                inputMode="decimal"
                              />
                            </td>
                          )}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className="ds-panel__body">
                  <dl className="orders__totals">
                    <div>
                      <dt>{t('orders.subtotal')}</dt>
                      <dd className="num">
                        {money(order.subtotal, { currency: order.currency })}
                      </dd>
                    </div>
                    <div>
                      <dt>{t('orders.discount')}</dt>
                      <dd className="num">
                        {money(order.discount, { currency: order.currency })}
                      </dd>
                    </div>
                    <div className="orders__totals-final">
                      <dt>{t('orders.total')}</dt>
                      <dd className="num">
                        {money(order.total, { currency: order.currency })}
                      </dd>
                    </div>
                  </dl>
                </div>
              </section>

              {sheet && (
                <DocumentSheet sheet={sheet} onClose={() => setSheet(null)} />
              )}
            </>
          );
        }}
      </RemoteBody>
    </main>
  );
}
