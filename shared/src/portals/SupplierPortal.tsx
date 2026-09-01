// What a supplier sees (blueprint F3).
//
// # Orders waiting for an answer come first
//
// F3's whole argument is that a purchase order sitting in an inbox is an order
// nobody has accepted. The count is the headline, and the list opens on the
// ones with no response.
//
// # Accepting is not the same as accepting with changes
//
// A supplier who can deliver eighty of a hundred by Thursday has said something
// useful, and a portal that offered only yes and no would make them telephone.
// The third answer carries a comment and a date the buyer can plan against.

import { useCallback, useEffect, useState } from 'react';

import {
  respondToOrder,
  supplierBills,
  supplierHome,
  supplierOrder,
  supplierOrders,
  supplierRFQs,
  PortalFailed,
  type PortalShop,
  type SupplierBill,
  type SupplierHome,
  type SupplierOrder,
  type SupplierRFQ,
} from '../api/portal';
import { LabelledText } from '../governance/fields';
import { useLocale, useT } from '../i18n/locale';
import type { Key, Locale } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { money, shortDate } from '../ui/format';

type Tab = 'orders' | 'bills' | 'rfqs';

export function SupplierPortal({
  shop,
  token,
  onExpired,
}: {
  shop: PortalShop;
  token: string;
  onExpired: () => void;
}) {
  const t = useT();
  const { locale } = useLocale();

  const [home, setHome] = useState<SupplierHome | null>(null);
  const [tab, setTab] = useState<Tab>('orders');
  const [failure, setFailure] = useState<string | null>(null);

  const handle = useCallback(
    (err: unknown) => {
      if (err instanceof PortalFailed && err.status === 401) {
        onExpired();
        return;
      }
      setFailure(
        (err instanceof Error && err.message) || t('common.didNotWork'),
      );
    },
    [onExpired, t],
  );

  const loadHome = useCallback(() => {
    supplierHome(shop, token)
      .then((out) => setHome(out.home))
      .catch(handle);
  }, [shop, token, handle]);

  useEffect(loadHome, [loadHome]);

  const tabs: Array<{ key: Tab; label: Key }> = [
    { key: 'orders', label: 'ptl.theirOrders' },
    { key: 'bills', label: 'ptl.theirBills' },
    { key: 'rfqs', label: 'ptl.theirRFQs' },
  ];

  return (
    <div className="ptl__body">
      {home && (
        <section className="ptl__summary" aria-label={t('ptl.yourAccount')}>
          <Figure
            label={t('ptl.awaitingYou')}
            value={String(home.awaiting_response)}
          />
          <Figure
            label={t('ptl.openOrders')}
            value={String(home.open_orders)}
          />
          <Figure
            label={t('ptl.owedToYou')}
            value={money(home.outstanding, { currency: home.currency })}
          />
          <Figure
            label={t('ptl.overdueToYou')}
            value={money(home.overdue, { currency: home.currency })}
          />
        </section>
      )}

      <div
        className="segmented"
        role="group"
        aria-label={t('common.whatToShow')}
      >
        {tabs.map((x) => (
          <button
            key={x.key}
            className={`segmented__btn${tab === x.key ? ' segmented__btn--on' : ''}`}
            aria-pressed={tab === x.key}
            onClick={() => setTab(x.key)}
          >
            {t(x.label)}
          </button>
        ))}
      </div>

      <FormError message={failure} />

      {tab === 'orders' && (
        <Orders
          shop={shop}
          token={token}
          onFail={handle}
          onAnswered={loadHome}
          locale={locale}
        />
      )}
      {tab === 'bills' && (
        <Bills shop={shop} token={token} onFail={handle} locale={locale} />
      )}
      {tab === 'rfqs' && (
        <RFQs shop={shop} token={token} onFail={handle} locale={locale} />
      )}
    </div>
  );
}

function Figure({ label, value }: { label: string; value: string }) {
  return (
    <div className="ptl__figure">
      <span className="ptl__figureLabel">{label}</span>
      <span className="ptl__figureValue num">{value}</span>
    </div>
  );
}

function Orders({
  shop,
  token,
  onFail,
  onAnswered,
  locale,
}: {
  shop: PortalShop;
  token: string;
  onFail: (err: unknown) => void;
  onAnswered: () => void;
  locale: Locale;
}) {
  const t = useT();
  const [rows, setRows] = useState<SupplierOrder[]>([]);
  const [open, setOpen] = useState<SupplierOrder | null>(null);
  const [answer, setAnswer] = useState('accepted');
  const [comment, setComment] = useState('');
  const [promised, setPromised] = useState('');

  const load = useCallback(() => {
    supplierOrders(shop, token)
      .then((out) => setRows(out.data))
      .catch(onFail);
  }, [shop, token, onFail]);

  useEffect(load, [load]);

  if (rows.length === 0) {
    return <p className="ptl__empty">{t('ptl.noTheirOrders')}</p>;
  }

  return (
    <div className="ptl__panel">
      <ul className="ptl__list">
        {rows.map((o) => (
          <li className="ptl__row" key={o.id}>
            <div className="ptl__rowMain">
              <span className="ptl__rowTitle">{o.po_number}</span>
              <span className="ds-caption">
                {o.ordered_on ? shortDate(o.ordered_on, locale) : ''}
                {o.expected_on
                  ? ` · ${t('ptl.wantedBy', {
                      when: shortDate(o.expected_on, locale),
                    })}`
                  : ''}
              </span>
              {o.response && (
                <span className="ds-caption">
                  {t(`ptl.response.${o.response}` as Key)}
                  {o.comment ? ` · ${o.comment}` : ''}
                </span>
              )}
            </div>
            <div className="ptl__rowSide">
              <span className="num">
                {money(o.total, { currency: o.currency })}
              </span>
              {!o.response ? (
                <span className="ds-badge ds-badge--warning">
                  {t('ptl.needsYourAnswer')}
                </span>
              ) : (
                <span
                  className={`ds-badge ds-badge--${
                    o.response === 'rejected' ? 'danger' : 'success'
                  }`}
                >
                  {t(`ptl.response.${o.response}` as Key)}
                </span>
              )}
              <button
                className="ds-btn ds-btn--quiet ds-btn--sm"
                onClick={() =>
                  void supplierOrder(shop, token, o.id)
                    .then((out) => {
                      setOpen(out.order);
                      setAnswer(out.order.response || 'accepted');
                      setComment(out.order.comment ?? '');
                      setPromised(out.order.promised_on ?? '');
                    })
                    .catch(onFail)
                }
              >
                {t('ptl.openOrder')}
              </button>
            </div>
          </li>
        ))}
      </ul>

      {open && (
        <div className="ptl__form">
          <h3 className="ds-h3">{open.po_number}</h3>

          <div className="ds-scroll-x">
            <table className="ds-table">
              <thead>
                <tr>
                  <th scope="col">{t('ptl.item')}</th>
                  <th scope="col">{t('ptl.asked')}</th>
                  <th scope="col">{t('ptl.receivedByThem')}</th>
                  <th scope="col">{t('ptl.unitCost')}</th>
                  <th scope="col">{t('sub.amount')}</th>
                </tr>
              </thead>
              <tbody>
                {(open.lines ?? []).map((l) => (
                  <tr key={l.line_no}>
                    <td>
                      {l.description}
                      {l.sku && <span className="ds-caption"> {l.sku}</span>}
                    </td>
                    <td className="num">{l.qty_ordered}</td>
                    <td className="num">{l.qty_received}</td>
                    <td className="num">
                      {money(l.unit_cost, { currency: open.currency })}
                    </td>
                    <td className="num">
                      {money(l.gross_amount, { currency: open.currency })}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div
            className="segmented"
            role="group"
            aria-label={t('ptl.yourAnswer')}
          >
            {['accepted', 'accepted_with_changes', 'rejected'].map((a) => (
              <button
                key={a}
                className={`segmented__btn${answer === a ? ' segmented__btn--on' : ''}`}
                aria-pressed={answer === a}
                onClick={() => setAnswer(a)}
              >
                {t(`ptl.response.${a}` as Key)}
              </button>
            ))}
          </div>

          <LabelledText
            id="so-comment"
            label={t('ptl.yourComment')}
            hint={
              answer === 'rejected'
                ? t('ptl.rejectionNeedsReason')
                : t('ptl.commentOptional')
            }
            value={comment}
            onChange={setComment}
          />
          {answer !== 'rejected' && (
            <LabelledText
              id="so-promised"
              label={t('ptl.canDeliverBy')}
              value={promised}
              onChange={setPromised}
              type="date"
            />
          )}

          <div className="form__actions">
            <button
              className="ds-btn ds-btn--primary"
              disabled={answer === 'rejected' && comment.trim() === ''}
              onClick={() =>
                void respondToOrder(shop, token, open.id, {
                  response: answer,
                  comment,
                  promised_on: promised || undefined,
                })
                  .then(() => {
                    setOpen(null);
                    load();
                    onAnswered();
                  })
                  .catch(onFail)
              }
            >
              {t('ptl.sendAnswer')}
            </button>
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setOpen(null)}
            >
              {t('action.cancel')}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function Bills({
  shop,
  token,
  onFail,
  locale,
}: {
  shop: PortalShop;
  token: string;
  onFail: (err: unknown) => void;
  locale: Locale;
}) {
  const t = useT();
  const [rows, setRows] = useState<SupplierBill[]>([]);

  useEffect(() => {
    supplierBills(shop, token)
      .then((out) => setRows(out.data))
      .catch(onFail);
  }, [shop, token, onFail]);

  if (rows.length === 0) {
    return <p className="ptl__empty">{t('ptl.noTheirBills')}</p>;
  }

  return (
    <ul className="ptl__list">
      {rows.map((b) => (
        <li className="ptl__row" key={b.id}>
          <div className="ptl__rowMain">
            <span className="ptl__rowTitle">
              {b.supplier_ref || shortDate(b.bill_date, locale)}
            </span>
            <span className="ds-caption">
              {b.due_on
                ? t('ptl.dueBy', { when: shortDate(b.due_on, locale) })
                : ''}
            </span>
          </div>
          <div className="ptl__rowSide">
            <span className="num">
              {money(b.outstanding, { currency: b.currency })}
            </span>
            <span
              className={`ds-badge ds-badge--${
                Number(b.outstanding) <= 0
                  ? 'success'
                  : b.overdue
                    ? 'danger'
                    : 'neutral'
              }`}
            >
              {Number(b.outstanding) <= 0
                ? t('ptl.settled')
                : b.overdue
                  ? t('ptl.late')
                  : t('ptl.awaitingPayment')}
            </span>
          </div>
        </li>
      ))}
    </ul>
  );
}

function RFQs({
  shop,
  token,
  onFail,
  locale,
}: {
  shop: PortalShop;
  token: string;
  onFail: (err: unknown) => void;
  locale: Locale;
}) {
  const t = useT();
  const [rows, setRows] = useState<SupplierRFQ[]>([]);

  useEffect(() => {
    supplierRFQs(shop, token)
      .then((out) => setRows(out.data))
      .catch(onFail);
  }, [shop, token, onFail]);

  if (rows.length === 0) {
    return <p className="ptl__empty">{t('ptl.noTheirRFQs')}</p>;
  }

  return (
    <ul className="ptl__list">
      {rows.map((r) => (
        <li className="ptl__row" key={r.id}>
          <div className="ptl__rowMain">
            <span className="ptl__rowTitle">{r.rfq_no}</span>
            <span className="ds-caption">
              {r.closes_on
                ? t('ptl.closesOn', { when: shortDate(r.closes_on, locale) })
                : ''}
            </span>
            {r.note && <span className="ds-caption">{r.note}</span>}
          </div>
          <div className="ptl__rowSide">
            {r.quoted ? (
              <>
                {r.quote_total && <span className="num">{r.quote_total}</span>}
                <span className="ds-badge ds-badge--success">
                  {t('ptl.quoted')}
                </span>
              </>
            ) : (
              <span className="ds-badge ds-badge--warning">
                {t('ptl.notQuoted')}
              </span>
            )}
          </div>
        </li>
      ))}
    </ul>
  );
}
