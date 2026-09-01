// What a customer sees (blueprint F2).
//
// # The balance is the first thing on the page
//
// What they owe, what the shop holds for them, and what their points come to.
// Those three are why somebody signs in; the lists below them are why they stay
// signed in.
//
// # An expired session sends them back to the door
//
// Every call can fail with 401 once the thirty days are up. Rather than showing
// an error on each panel, the first one to see it calls `onExpired` and the
// whole page returns to the sign-in box, which is what the person needs to do
// anyway.

import { useCallback, useEffect, useState } from 'react';

import {
  askToReturn,
  portalAddresses,
  portalInvoices,
  portalMe,
  portalOrders,
  portalReturns,
  portalWarranty,
  removePortalAddress,
  savePortalAddress,
  PortalFailed,
  type PortalAddress,
  type PortalInvoice,
  type PortalMe,
  type PortalOrder,
  type PortalReturnRequest,
  type PortalShop,
  type PortalWarranty,
} from '../api/portal';
import { LabelledText } from '../governance/fields';
import { useLocale, useT } from '../i18n/locale';
import type { Key, Locale } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { money, shortDate } from '../ui/format';

type Tab = 'receipts' | 'orders' | 'warranty' | 'addresses' | 'returns';

export function CustomerPortal({
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

  const [me, setMe] = useState<PortalMe | null>(null);
  const [tab, setTab] = useState<Tab>('receipts');
  const [failure, setFailure] = useState<string | null>(null);

  // Every panel funnels its failures through here, so an expired session is
  // handled once rather than five times.
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

  useEffect(() => {
    portalMe(shop, token)
      .then((out) => setMe(out.me))
      .catch(handle);
  }, [shop, token, handle]);

  const tabs: Array<{ key: Tab; label: Key }> = [
    { key: 'receipts', label: 'ptl.receipts' },
    { key: 'orders', label: 'ptl.orders' },
    { key: 'warranty', label: 'ptl.warranty' },
    { key: 'addresses', label: 'ptl.addresses' },
    { key: 'returns', label: 'ptl.myReturns' },
  ];

  return (
    <div className="ptl__body">
      {me && (
        <section className="ptl__summary" aria-label={t('ptl.yourAccount')}>
          <Figure
            label={t('ptl.youOwe')}
            value={money(me.outstanding, { currency: me.currency })}
          />
          <Figure
            label={t('ptl.storeCredit')}
            value={money(me.store_credit, { currency: me.currency })}
          />
          <Figure
            label={t('ptl.giftCards')}
            value={money(me.gift_card_balance, { currency: me.currency })}
          />
          {me.loyalty_enrolled && (
            <Figure label={t('ptl.points')} value={String(me.points)} />
          )}
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

      {tab === 'receipts' && (
        <Receipts shop={shop} token={token} onFail={handle} locale={locale} />
      )}
      {tab === 'orders' && (
        <Orders shop={shop} token={token} onFail={handle} locale={locale} />
      )}
      {tab === 'warranty' && (
        <WarrantyCheck
          shop={shop}
          token={token}
          onFail={handle}
          locale={locale}
        />
      )}
      {tab === 'addresses' && (
        <Addresses shop={shop} token={token} onFail={handle} />
      )}
      {tab === 'returns' && (
        <Returns shop={shop} token={token} onFail={handle} locale={locale} />
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

interface PanelProps {
  shop: PortalShop;
  token: string;
  onFail: (err: unknown) => void;
  locale?: Locale;
}

function Receipts({ shop, token, onFail, locale }: PanelProps) {
  const t = useT();
  const [rows, setRows] = useState<PortalInvoice[]>([]);

  useEffect(() => {
    portalInvoices(shop, token)
      .then((out) => setRows(out.data))
      .catch(onFail);
  }, [shop, token, onFail]);

  if (rows.length === 0) {
    return <p className="ptl__empty">{t('ptl.noReceipts')}</p>;
  }

  return (
    <ul className="ptl__list">
      {rows.map((r) => (
        <li className="ptl__row" key={r.id}>
          <div className="ptl__rowMain">
            <span className="ptl__rowTitle">{r.human_number}</span>
            <span className="ds-caption">
              {shortDate(r.issued_at, locale ?? 'en')}
            </span>
          </div>
          <div className="ptl__rowSide">
            <span className="num">
              {money(r.total, { currency: r.currency })}
            </span>
            {Number(r.outstanding) > 0 && (
              <span className="ds-badge ds-badge--warning">
                {t('ptl.stillOwing', {
                  amount: money(r.outstanding, { currency: r.currency }),
                })}
              </span>
            )}
          </div>
        </li>
      ))}
    </ul>
  );
}

function Orders({ shop, token, onFail, locale }: PanelProps) {
  const t = useT();
  const [rows, setRows] = useState<PortalOrder[]>([]);

  useEffect(() => {
    portalOrders(shop, token)
      .then((out) => setRows(out.data))
      .catch(onFail);
  }, [shop, token, onFail]);

  if (rows.length === 0) {
    return <p className="ptl__empty">{t('ptl.noOrders')}</p>;
  }

  return (
    <ul className="ptl__list">
      {rows.map((o) => (
        <li className="ptl__row" key={o.id}>
          <div className="ptl__rowMain">
            <span className="ptl__rowTitle">{o.order_no}</span>
            <span className="ds-caption">
              {shortDate(o.placed_at, locale ?? 'en')}
            </span>
            {o.delivery_status && (
              <span className="ds-caption">
                {t(`ptl.delivery.${o.delivery_status}` as Key)}
                {o.driver_name ? ` · ${o.driver_name}` : ''}
              </span>
            )}
          </div>
          <div className="ptl__rowSide">
            <span className="num">
              {money(o.total, { currency: o.currency })}
            </span>
            <span className="ds-badge ds-badge--neutral">
              {t(`ptl.orderState.${o.state}` as Key)}
            </span>
          </div>
        </li>
      ))}
    </ul>
  );
}

function WarrantyCheck({ shop, token, onFail, locale }: PanelProps) {
  const t = useT();
  const [serial, setSerial] = useState('');
  const [found, setFound] = useState<PortalWarranty | null>(null);
  const [missing, setMissing] = useState(false);

  return (
    <div className="ptl__panel">
      <LabelledText
        id="ptl-serial"
        label={t('ptl.serialNumber')}
        hint={t('ptl.serialHint')}
        value={serial}
        onChange={setSerial}
      />
      <div className="form__actions">
        <button
          className="ds-btn ds-btn--primary"
          disabled={serial.trim() === ''}
          onClick={() => {
            setFound(null);
            setMissing(false);
            portalWarranty(shop, token, serial)
              .then((out) => setFound(out.warranty))
              .catch((err) => {
                if (err instanceof PortalFailed && err.status === 404) {
                  setMissing(true);
                  return;
                }
                onFail(err);
              });
          }}
        >
          {t('ptl.check')}
        </button>
      </div>

      {missing && <p className="ptl__empty">{t('ptl.serialNotYours')}</p>}

      {found && (
        <div className="ptl__answer">
          <p className="ptl__rowTitle">{found.product || found.serial_no}</p>
          <p>
            {found.in_warranty
              ? t('ptl.covered', {
                  when: shortDate(found.expires_on ?? '', locale ?? 'en'),
                })
              : t('ptl.notCovered')}
          </p>
          {found.sold_on && (
            <p className="ds-caption">
              {t('ptl.boughtOn', {
                when: shortDate(found.sold_on, locale ?? 'en'),
              })}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

const BLANK_ADDRESS: PortalAddress = {
  label: '',
  line1: '',
  is_default: false,
};

function Addresses({ shop, token, onFail }: PanelProps) {
  const t = useT();
  const [rows, setRows] = useState<PortalAddress[]>([]);
  const [draft, setDraft] = useState<PortalAddress | null>(null);

  useEffect(() => {
    portalAddresses(shop, token)
      .then((out) => setRows(out.data))
      .catch(onFail);
  }, [shop, token, onFail]);

  const set = (patch: Partial<PortalAddress>) =>
    setDraft((d) => (d ? { ...d, ...patch } : d));

  return (
    <div className="ptl__panel">
      <ul className="ptl__list">
        {rows.map((a) => (
          <li className="ptl__row" key={a.id}>
            <div className="ptl__rowMain">
              <span className="ptl__rowTitle">
                {a.label}
                {a.is_default && (
                  <span className="ds-badge ds-badge--success">
                    {t('ptl.usual')}
                  </span>
                )}
              </span>
              <span className="ds-caption">
                {[a.line1, a.line2, a.district, a.city]
                  .filter(Boolean)
                  .join(', ')}
              </span>
            </div>
            <div className="ptl__rowSide">
              <button
                className="ds-btn ds-btn--quiet ds-btn--sm"
                onClick={() => setDraft(a)}
              >
                {t('action.edit')}
              </button>
              <button
                className="ds-btn ds-btn--quiet ds-btn--sm"
                onClick={() =>
                  void removePortalAddress(shop, token, a.id as string)
                    .then(() => portalAddresses(shop, token))
                    .then((out) => setRows(out.data))
                    .catch(onFail)
                }
              >
                {t('action.remove')}
              </button>
            </div>
          </li>
        ))}
      </ul>

      {draft ? (
        <div className="ptl__form">
          <LabelledText
            id="pa-label"
            label={t('ptl.addressName')}
            value={draft.label}
            onChange={(v) => set({ label: v })}
          />
          <LabelledText
            id="pa-line1"
            label={t('ptl.addressLine1')}
            value={draft.line1}
            onChange={(v) => set({ line1: v })}
          />
          <LabelledText
            id="pa-line2"
            label={t('ptl.addressLine2')}
            value={draft.line2 ?? ''}
            onChange={(v) => set({ line2: v })}
          />
          <LabelledText
            id="pa-district"
            label={t('ptl.district')}
            value={draft.district ?? ''}
            onChange={(v) => set({ district: v })}
          />
          <LabelledText
            id="pa-city"
            label={t('ptl.city')}
            value={draft.city ?? ''}
            onChange={(v) => set({ city: v })}
          />
          <label className="ds-check">
            <input
              type="checkbox"
              checked={draft.is_default}
              onChange={(e) => set({ is_default: e.target.checked })}
            />
            {t('ptl.makeUsual')}
          </label>
          <div className="form__actions">
            <button
              className="ds-btn ds-btn--primary"
              disabled={draft.label.trim() === '' || draft.line1.trim() === ''}
              onClick={() =>
                void savePortalAddress(shop, token, draft)
                  .then((out) => {
                    setRows(out.data);
                    setDraft(null);
                  })
                  .catch(onFail)
              }
            >
              {t('action.save')}
            </button>
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setDraft(null)}
            >
              {t('action.cancel')}
            </button>
          </div>
        </div>
      ) : (
        <div className="form__actions">
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => setDraft({ ...BLANK_ADDRESS })}
          >
            {t('ptl.addAddress')}
          </button>
        </div>
      )}
    </div>
  );
}

function Returns({ shop, token, onFail, locale }: PanelProps) {
  const t = useT();
  const [rows, setRows] = useState<PortalReturnRequest[]>([]);
  const [asking, setAsking] = useState(false);
  const [kind, setKind] = useState('return');
  const [items, setItems] = useState('');
  const [reason, setReason] = useState('');

  const load = useCallback(() => {
    portalReturns(shop, token)
      .then((out) => setRows(out.data))
      .catch(onFail);
  }, [shop, token, onFail]);

  useEffect(load, [load]);

  return (
    <div className="ptl__panel">
      <ul className="ptl__list">
        {rows.map((r) => (
          <li className="ptl__row" key={r.id}>
            <div className="ptl__rowMain">
              <span className="ptl__rowTitle">{r.items}</span>
              <span className="ds-caption">
                {r.request_no} · {shortDate(r.created_at, locale ?? 'en')}
              </span>
              {r.decision_note && (
                <span className="ds-caption">{r.decision_note}</span>
              )}
            </div>
            <div className="ptl__rowSide">
              <span
                className={`ds-badge ds-badge--${
                  r.status === 'requested'
                    ? 'warning'
                    : r.status === 'refused'
                      ? 'danger'
                      : 'success'
                }`}
              >
                {t(`ptl.status.${r.status}` as Key)}
              </span>
            </div>
          </li>
        ))}
      </ul>

      {asking ? (
        <div className="ptl__form">
          <div
            className="segmented"
            role="group"
            aria-label={t('ptl.returnOrExchange')}
          >
            <button
              className={`segmented__btn${kind === 'return' ? ' segmented__btn--on' : ''}`}
              aria-pressed={kind === 'return'}
              onClick={() => setKind('return')}
            >
              {t('ptl.wantMoneyBack')}
            </button>
            <button
              className={`segmented__btn${kind === 'exchange' ? ' segmented__btn--on' : ''}`}
              aria-pressed={kind === 'exchange'}
              onClick={() => setKind('exchange')}
            >
              {t('ptl.wantToSwap')}
            </button>
          </div>
          <LabelledText
            id="pr-items"
            label={t('ptl.whatItem')}
            hint={t('ptl.whatItemHint')}
            value={items}
            onChange={setItems}
          />
          <LabelledText
            id="pr-reason"
            label={t('ptl.whyBack')}
            value={reason}
            onChange={setReason}
          />
          <div className="form__actions">
            <button
              className="ds-btn ds-btn--primary"
              disabled={items.trim() === '' || reason.trim() === ''}
              onClick={() =>
                void askToReturn(shop, token, { kind, items, reason })
                  .then(() => {
                    setAsking(false);
                    setItems('');
                    setReason('');
                    load();
                  })
                  .catch(onFail)
              }
            >
              {t('ptl.askShop')}
            </button>
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setAsking(false)}
            >
              {t('action.cancel')}
            </button>
          </div>
        </div>
      ) : (
        <div className="form__actions">
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => setAsking(true)}
          >
            {t('ptl.sendSomethingBack')}
          </button>
        </div>
      )}
    </div>
  );
}
