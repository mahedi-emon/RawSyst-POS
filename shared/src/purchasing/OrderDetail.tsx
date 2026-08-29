// One purchase order, and receiving against it.
//
// The receiving form is on the same screen as the order rather than behind a
// button, because they are read together: a storeman with a delivery in front
// of them is comparing what arrived against what was ordered, line by line, and
// making them hold that comparison in their head across a navigation is how
// receiving errors happen.
//
// Quantities default to what is still outstanding. The common case by a long
// way is a delivery that matches the order, and pre-filling it means the
// storeman confirms rather than transcribes.

import { useCallback, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import { DetailScreen, EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { money, longDate } from '../ui/format';
import { trimQuantity } from '../dashboard/drilldown';
import { receiptNotice, receivingDefaults } from './purchasing';
import { issueOrder, readOrder, receiveGoods, type Order } from '../api/purchasing';
import { OrderStatus } from './PurchasingScreen';
import { OrderForm } from './OrderForm';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';

export function OrderDetail({
  companyId,
  poId,
  onBack,
}: {
  companyId: string;
  poId: string;
  onBack: () => void;
}) {
  const t = useT();
  const { locale } = useLocale();
  const { client, can } = useAuth();
  const load = useCallback(
    () => readOrder(client, companyId, poId),
    [client, companyId, poId],
  );
  const { remote, reload, refreshing } = useRemote(load);

  const [receiving, setReceiving] = useState(false);
  const [editing, setEditing] = useState(false);
  const [qty, setQty] = useState<Record<string, string>>({});
  const [rejected, setRejected] = useState<Record<string, string>>({});
  const [deliveryRef, setDeliveryRef] = useState('');
  const [landedCost, setLandedCost] = useState('');
  const [importVat, setImportVat] = useState('');
  const [basis, setBasis] = useState<'value' | 'quantity'>('value');
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const mayReceive = can('purchasing.receive_goods');
  const mayIssue = can('purchasing.issue_order');
  const mayEdit = can('purchasing.create_order');

  function startReceiving(order: Order) {
    // Pre-filled with what is still outstanding: the common case is a delivery
    // that matches, so the storeman confirms rather than transcribes.
    setQty(receivingDefaults(order.lines ?? []));
    setRejected({});
    setNotice(null);
    setReceiving(true);
  }

  async function submitReceipt(order: Order) {
    const lines = Object.entries(qty)
      .filter(([, q]) => q.trim() !== '' && Number(q) > 0)
      .map(([po_line_id, q]) => ({
        po_line_id,
        qty_received: q,
        qty_rejected: rejected[po_line_id] || undefined,
      }));

    if (lines.length === 0) {
      setNotice(t('order.enterArrived'));
      return;
    }

    setBusy(true);
    setNotice(null);
    try {
      const receipt = await receiveGoods(client, companyId, {
        po_id: order.id,
        delivery_note_ref: deliveryRef || undefined,
        landed_cost: landedCost.trim() || undefined,
        import_vat: importVat.trim() || undefined,
        landed_cost_basis: basis,
        lines,
      });
      setReceiving(false);
      setNotice(receiptNotice(receipt, t));
      reload();
    } catch (err) {
      setNotice(explain(err, t('order.deliveryFailed'), t));
    } finally {
      setBusy(false);
    }
  }

  async function issue(order: Order) {
    setBusy(true);
    setNotice(null);
    try {
      await issueOrder(client, companyId, order.id);
      setNotice(t('order.sent'));
      reload();
    } catch (err) {
      setNotice(explain(err, t('order.issueFailed'), t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(order) =>
        // Correcting a draft reuses the form that raised it. A separate edit
        // screen would be the same fields twice, and the two would drift.
        editing ? (
          <DetailScreen
            title={t('buy.correctingOrder', { order: order.po_number })}
            subtitle={t('buy.draftNotCommitted')}
            backLabel={order.po_number}
            onBack={() => setEditing(false)}
          >
            <OrderForm
              companyId={companyId}
              existing={order}
              onSaved={() => {
                setEditing(false);
                setNotice(t('order.updated'));
                reload();
              }}
              onCancel={() => setEditing(false)}
            />
          </DetailScreen>
        ) : (
        <DetailScreen
          title={order.po_number}
          subtitle={t('buy.orderedOn', {
            supplier: order.supplier,
            date: longDate(order.ordered_on, locale),
          })}
          backLabel="Orders"
          onBack={onBack}
          onRefresh={reload}
          refreshing={refreshing}
          actions={
            <>
              <OrderStatus status={order.status} />
              {/* Only a draft. An issued order is a commitment the supplier
                  can hold the shop to, and the server refuses to change one —
                  so the button is absent rather than offering something that
                  would be turned down. */}
              {order.status === 'draft' && mayEdit && (
                <button
                  className="ds-btn ds-btn--secondary"
                  onClick={() => setEditing(true)}
                >
                  {t('action.edit')}
                </button>
              )}
              {order.status === 'draft' && mayIssue && (
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy}
                  onClick={() => void issue(order)}
                >
                  {t('purch.sendToSupplier')}
                </button>
              )}
              {(order.status === 'issued' || order.status === 'receiving') &&
                mayReceive &&
                !receiving && (
                  <button
                    className="ds-btn ds-btn--primary"
                    onClick={() => startReceiving(order)}
                  >
                    {t('purch.recordDelivery')}
                  </button>
                )}
            </>
          }
        >
          {notice && (
            <p className="ds-panel purchase__notice" role="status" aria-live="polite">
              {notice}
            </p>
          )}

          {order.status === 'draft' && (
            <p className="ds-panel purchase__hint">
              {t('order.draftHint', { supplier: order.supplier })}
            </p>
          )}

          <div className="ds-panel">
            <div className="ds-panel__head">
              <h2 className="ds-h3">{receiving ? 'What arrived' : 'Lines'}</h2>
              {receiving && (
                <label className="purchase__ref">
                  <span className="ds-caption">{t('purch.deliveryNote')}</span>
                  <input
                    value={deliveryRef}
                    onChange={(e) => setDeliveryRef(e.target.value)}
                    placeholder={t('purch.theirReference')}
                  />
                </label>
              )}
            </div>

            <div className="ds-panel__body ds-scroll-x">
              {(order.lines ?? []).length === 0 ? (
                <EmptyState title={t('purch.noLines')} body={t('buy.nothingOrdered')} />
              ) : (
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('common.item')}</th>
                      <th scope="col" className="num">{t('common.ordered')}</th>
                      <th scope="col" className="num">{t('common.received')}</th>
                      <th scope="col" className="num">{t('purch.stillDue')}</th>
                      {receiving ? (
                        <>
                          <th scope="col">{t('common.arrived')}</th>
                          <th scope="col">{t('purch.rejected')}</th>
                        </>
                      ) : (
                        <>
                          <th scope="col" className="num">{t('common.unitCost')}</th>
                          <th scope="col" className="num">{t('common.value')}</th>
                        </>
                      )}
                    </tr>
                  </thead>
                  <tbody>
                    {(order.lines ?? []).map((line) => (
                      <tr key={line.id}>
                        <td>
                          <span className="detail__strong">{line.description}</span>
                        </td>
                        <td className="num">{trimQuantity(line.qty_ordered)}</td>
                        <td className="num">{trimQuantity(line.qty_received)}</td>
                        <td
                          className={`num${
                            Number(line.qty_outstanding) > 0 ? '' : ' ds-subtle'
                          }`}
                        >
                          {trimQuantity(line.qty_outstanding)}
                        </td>

                        {receiving ? (
                          <>
                            <td>
                              <input
                                className="cart__qty"
                                inputMode="decimal"
                                value={qty[line.id] ?? ''}
                                onChange={(e) =>
                                  setQty((p) => ({ ...p, [line.id]: e.target.value }))
                                }
                                aria-label={t('buy.qtyArrived', { item: line.description })}
                              />
                            </td>
                            <td>
                              {/* Damaged or wrong goods, recorded rather than
                                  netted off. What arrived and what was kept
                                  are different facts, and the supplier will
                                  argue about both. */}
                              <input
                                className="cart__qty"
                                inputMode="decimal"
                                placeholder="0"
                                value={rejected[line.id] ?? ''}
                                onChange={(e) =>
                                  setRejected((p) => ({ ...p, [line.id]: e.target.value }))
                                }
                                aria-label={t('buy.qtyRejected', { item: line.description })}
                              />
                            </td>
                          </>
                        ) : (
                          <>
                            <td className="num">
                              {money(line.unit_cost, { currency: order.currency })}
                            </td>
                            <td className="num">
                              {money(line.gross_amount, { currency: order.currency })}
                            </td>
                          </>
                        )}
                      </tr>
                    ))}
                  </tbody>
                  {!receiving && (
                    <tfoot>
                      <tr>
                        <td colSpan={5}>{t('purch.orderTotal')}</td>
                        <td className="num">
                          {money(order.total_inclusive, { currency: order.currency })}
                        </td>
                      </tr>
                    </tfoot>
                  )}
                </table>
              )}
            </div>
          </div>

          {receiving && (
            <section className="ds-panel">
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('purch.costsOnDelivery')}</h2>
                <span className="ds-caption">optional</span>
              </div>
              <div className="ds-panel__body purchase__form">
                <label className="purchase__field">
                  <span className="ds-caption">{t('order.landedCost')}</span>
                  <input
                    className="input input--narrow"
                    inputMode="decimal"
                    value={landedCost}
                    placeholder="0.00"
                    aria-label={t('purch.freightDuty')}
                    onChange={(e) => setLandedCost(e.target.value)}
                  />
                </label>
                <label className="purchase__field">
                  <span className="ds-caption">{t('order.importVat')}</span>
                  <input
                    className="input input--narrow"
                    inputMode="decimal"
                    value={importVat}
                    placeholder="0.00"
                    aria-label={t('purch.importVat')}
                    onChange={(e) => setImportVat(e.target.value)}
                  />
                </label>

                {/* Only worth asking once there is something to spread. By
                    value is right for a mixed delivery; by quantity is right
                    when the items are alike and the freight is per box. */}
                {landedCost.trim() !== '' && (
                  <label className="purchase__field">
                    <span className="ds-caption">{t('purch.spreadIt')}</span>
                    <select
                      className="input"
                      value={basis}
                      aria-label={t('purch.howToSpread')}
                      onChange={(e) =>
                        setBasis(e.target.value === 'quantity' ? 'quantity' : 'value')
                      }
                    >
                      <option value="value">
                        {t('purch.spreadByValue')}
                      </option>
                      <option value="quantity">
                        {t('purch.spreadByQty')}
                      </option>
                    </select>
                  </label>
                )}
              </div>
            </section>
          )}

          {receiving && (
            <div className="purchase__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy}
                onClick={() => void submitReceipt(order)}
              >
                {busy ? t('order.recording') : t('order.recordDelivery')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setReceiving(false)}
              >
                {t('action.cancel')}
              </button>
              <p className="ds-caption purchase__aside">{t('order.deliveryExplain')}</p>
            </div>
          )}
        </DetailScreen>
        )
      }
    </RemoteBody>
  );
}

function explain(err: unknown, fallback: string, t: (key: Key) => string): string {
  if (err instanceof Offline) {
    // Purchasing is a back-office activity against live records: what is
    // already received and billed cannot be known offline, and guessing would
    // put stock in twice.
    return t('order.deliveryOffline');
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) return t('common.noPermission');
    return err.message;
  }
  return err instanceof Error ? err.message : fallback;
}
