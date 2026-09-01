// Consignments and the pipeline they move along (blueprint B13).
//
// # The pipeline is drawn, and the next step is one button
//
// B13's statuses are a sequence, and a driver holding a phone in a doorway
// should press one thing. A dropdown of seven statuses is how a delivery gets
// marked "returned" when the driver meant "delivered".
//
// # Cash on delivery is money, so it is asked about explicitly
//
// A driver marking a COD consignment delivered is saying two things: the goods
// arrived, and they are holding the customer's cash. The second gets its own
// checkbox rather than being assumed, because a delivery marked complete with
// the money still uncollected is a shortfall nobody attributes.

import { useCallback, useState } from 'react';

import {
  advanceDelivery,
  listDeliveries,
  readDelivery,
  type Delivery,
  type DeliveryStatus,
} from '../api/aftersales';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError, TextInput } from '../ui/Form';
import { money, shortDate } from '../ui/format';

// The pipeline, in order. `failed` and `returned` leave it rather than
// continuing along it, which is why they are not in this list.
const PIPELINE: DeliveryStatus[] = [
  'pending',
  'assigned',
  'picked_up',
  'out_for_delivery',
  'delivered',
];

function nextStep(status: DeliveryStatus): DeliveryStatus | null {
  const at = PIPELINE.indexOf(status);
  if (at < 0) return null;
  return PIPELINE[at + 1] ?? null;
}

export function DeliveriesPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [open, setOpen] = useState<string | null>(null);
  const [showFinished, setShowFinished] = useState(false);

  const load = useCallback(
    () => listDeliveries(client, companyId, {}),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <DeliveryDetail
        companyId={companyId}
        deliveryId={open}
        onBack={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <section className="ds-panel" aria-label={t('after.deliveries')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('after.deliveries')}</h2>
        <button
          className="ds-btn ds-btn--quiet"
          onClick={() => setShowFinished(!showFinished)}
        >
          {t(showFinished ? 'after.showOnTheRoad' : 'after.showEverything')}
        </button>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Delivery[] }) => {
          const rows = showFinished
            ? payload.data
            : payload.data.filter(
                (d) => !['delivered', 'returned'].includes(d.status),
              );

          return rows.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('after.noDeliveriesTitle')}
                body={t('after.noDeliveriesBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('after.consignment')}</th>
                    <th scope="col">{t('after.deliverTo')}</th>
                    <th scope="col">{t('after.driver')}</th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col" className="num">
                      {t('after.cod')}
                    </th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((d) => (
                    <tr key={d.id}>
                      <td>
                        <span className="detail__strong">{d.delivery_no}</span>
                        <span className="ds-caption">
                          {d.order_no ?? ''} · {shortDate(d.created_at, locale)}
                        </span>
                      </td>
                      <td>
                        {d.customer ?? '—'}
                        <span className="ds-caption">{d.address}</span>
                        {d.phone && (
                          <span className="ds-caption">{d.phone}</span>
                        )}
                      </td>
                      <td>
                        {d.driver_name ?? t('after.noDriverYet')}
                        {d.attempt_count > 1 && (
                          <span className="ds-caption">
                            {t('after.attempts', {
                              count: String(d.attempt_count),
                            })}
                          </span>
                        )}
                      </td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${deliveryBadge(d.status)}`}
                        >
                          {t(`after.delivery.${d.status}` as Key)}
                        </span>
                        {d.failure_reason && (
                          <span className="ds-caption">{d.failure_reason}</span>
                        )}
                      </td>
                      <td className="num">
                        {d.is_cod ? (
                          <>
                            {money(d.cod_amount, { currency: d.currency })}
                            <span className="ds-caption">
                              {t(
                                d.cod_collected_at
                                  ? 'after.codCollected'
                                  : 'after.codOutstanding',
                              )}
                            </span>
                          </>
                        ) : (
                          '—'
                        )}
                      </td>
                      <td>
                        <button
                          className="ds-btn ds-btn--quiet"
                          onClick={() => setOpen(d.id)}
                        >
                          {t('action.view')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        }}
      </RemoteBody>
    </section>
  );
}

function deliveryBadge(status: DeliveryStatus): string {
  switch (status) {
    case 'delivered':
      return 'success';
    case 'failed':
    case 'returned':
      return 'danger';
    case 'out_for_delivery':
      return 'info';
    default:
      return 'neutral';
  }
}

function DeliveryDetail({
  companyId,
  deliveryId,
  onBack,
}: {
  companyId: string;
  deliveryId: string;
  onBack: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayDeliver = can('delivery.deliver');
  const load = useCallback(
    () => readDelivery(client, companyId, deliveryId),
    [client, companyId, deliveryId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [note, setNote] = useState('');
  const [collected, setCollected] = useState(false);

  async function move(d: Delivery, status: DeliveryStatus) {
    setBusy(true);
    setFailure(null);
    try {
      await advanceDelivery(client, companyId, d.id, {
        status,
        note: note || undefined,
        collected_cod:
          d.is_cod && status === 'delivered' ? collected : undefined,
      });
      setNote('');
      setCollected(false);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(d: Delivery) => {
        const next = nextStep(d.status);
        const at = PIPELINE.indexOf(d.status);

        return (
          <>
            <section className="ds-panel">
              <div className="ds-panel__head">
                <div>
                  <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                    {t('action.back')}
                  </button>
                  <h2 className="ds-h3">{d.delivery_no}</h2>
                  <p className="ds-caption">
                    {d.customer ?? ''} · {d.address}
                  </p>
                </div>

                <div className="after__actions">
                  {mayDeliver && next && (
                    <button
                      className="ds-btn ds-btn--primary"
                      disabled={busy}
                      onClick={() => void move(d, next)}
                    >
                      {t('after.markAs', {
                        status: t(`after.delivery.${next}` as Key),
                      })}
                    </button>
                  )}
                  {mayDeliver && d.status !== 'delivered' && at >= 0 && (
                    <button
                      className="ds-btn ds-btn--quiet"
                      disabled={busy || note.trim() === ''}
                      onClick={() => void move(d, 'failed')}
                    >
                      {t('after.markFailed')}
                    </button>
                  )}
                </div>
              </div>

              <div className="ds-panel__body">
                <FormError message={failure} />

                {/* Drawn rather than described. Somebody picking up another
                    driver's run needs to know where it got to first. */}
                {at >= 0 ? (
                  <ol className="after__track" aria-label={t('common.status')}>
                    {PIPELINE.map((step, i) => (
                      <li
                        key={step}
                        className={`after__step${i < at ? ' after__step--done' : ''}${
                          i === at ? ' after__step--now' : ''
                        }`}
                        aria-current={i === at ? 'step' : undefined}
                      >
                        {t(`after.delivery.${step}` as Key)}
                      </li>
                    ))}
                  </ol>
                ) : (
                  <p className="after__stopped" role="status">
                    {t('after.deliveryStopped', {
                      status: t(`after.delivery.${d.status}` as Key),
                      reason: d.failure_reason ?? '',
                    })}
                  </p>
                )}

                {mayDeliver && (
                  <div className="after__advance">
                    <TextInput
                      id="dl-note"
                      value={note}
                      onChange={setNote}
                      placeholder={t('after.noteForStep')}
                    />
                    {/* Money is money. A delivery marked complete with the cash
                        still uncollected is a shortfall nobody attributes. */}
                    {d.is_cod && next === 'delivered' && (
                      <label className="after__check">
                        <input
                          type="checkbox"
                          checked={collected}
                          onChange={(e) => setCollected(e.target.checked)}
                        />
                        <span>
                          {t('after.collectedCod', {
                            amount: money(d.cod_amount, {
                              currency: d.currency,
                            }),
                          })}
                        </span>
                      </label>
                    )}
                  </div>
                )}

                <dl className="after__facts">
                  <div>
                    <dt>{t('after.driver')}</dt>
                    <dd>{d.driver_name ?? t('after.noDriverYet')}</dd>
                  </div>
                  <div>
                    <dt>{t('after.fee')}</dt>
                    <dd className="num">
                      {money(d.fee, { currency: d.currency })}
                    </dd>
                  </div>
                  {d.is_cod && (
                    <div>
                      <dt>{t('after.cod')}</dt>
                      <dd className="num">
                        {money(d.cod_amount, { currency: d.currency })}
                      </dd>
                    </div>
                  )}
                  <div>
                    <dt>{t('after.attemptsMade')}</dt>
                    <dd>{d.attempt_count}</dd>
                  </div>
                </dl>
              </div>
            </section>

            <section className="ds-panel" aria-label={t('after.history')}>
              <div className="ds-panel__head">
                <h3 className="ds-h3">{t('after.history')}</h3>
              </div>
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('common.when')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">{t('common.who')}</th>
                      <th scope="col">{t('common.note')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(d.events ?? []).map((e, i) => (
                      <tr key={i}>
                        <td>{shortDate(e.recorded_at, locale)}</td>
                        <td>{t(`after.delivery.${e.status}` as Key)}</td>
                        <td>{e.recorded_by ?? '—'}</td>
                        <td>{e.note ?? '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          </>
        );
      }}
    </RemoteBody>
  );
}
