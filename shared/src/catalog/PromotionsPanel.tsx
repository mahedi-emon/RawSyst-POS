// Campaigns (blueprint B9).
//
// # The row leads with what the campaign has cost
//
// A promotions list that showed only the rules would answer "what are we
// running" and not "is it working" — and the second is the question that gets
// asked. So every row carries what the campaign has given away and what it has
// sold, which is D2's campaign analytics on the row rather than behind a
// separate report nobody opens.
//
// # The form changes shape with the kind
//
// A percentage needs one number. Buy-X-get-Y needs two. A bundle needs a
// quantity and a price. Showing all four fields and letting three be ignored is
// how a promotion gets saved with a value nobody meant — so the form asks for
// exactly what the chosen kind uses, and the server refuses anything else.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { localName, money, shortDate } from '../ui/format';
import {
  createPromotion,
  listPromotions,
  setPromotionActive,
  type Promotion,
  type PromotionKind,
} from '../api/promotions';
import { describeKind, isLive } from './promotions';

export function PromotionsPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('promotion.manage');
  const [adding, setAdding] = useState(false);
  const [showFinished, setShowFinished] = useState(false);

  const load = useCallback(
    () => listPromotions(client, companyId, showFinished),
    [client, companyId, showFinished],
  );
  const { remote, reload } = useRemote(load);

  return (
    <>
      {adding && (
        <PromotionForm
          companyId={companyId}
          onCancel={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('promo.title')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('promo.title')}</h2>
            <p className="ds-caption">{t('promo.floorNote')}</p>
          </div>
          <div className="promo__actions">
            <label className="promo__check">
              <input
                type="checkbox"
                checked={showFinished}
                onChange={(e) => setShowFinished(e.target.checked)}
              />
              <span>{t('promo.showFinished')}</span>
            </label>
            {mayManage && !adding && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setAdding(true)}
              >
                {t('promo.add')}
              </button>
            )}
          </div>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Promotion[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('promo.noneTitle')}
                  body={t('promo.noneBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('promo.campaign')}</th>
                      <th scope="col">{t('promo.offers')}</th>
                      <th scope="col">{t('promo.appliesTo')}</th>
                      <th scope="col">{t('promo.running')}</th>
                      <th scope="col" className="num">
                        {t('promo.used')}
                      </th>
                      <th scope="col" className="num">
                        {t('promo.givenAway')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((p) => (
                      <PromotionRow
                        key={p.id}
                        companyId={companyId}
                        promotion={p}
                        mayManage={mayManage}
                        locale={locale}
                        onChanged={reload}
                      />
                    ))}
                  </tbody>
                </table>
              </div>
            )
          }
        </RemoteBody>
      </section>
    </>
  );
}

function PromotionRow({
  companyId,
  promotion,
  mayManage,
  locale,
  onChanged,
}: {
  companyId: string;
  promotion: Promotion;
  mayManage: boolean;
  locale: Parameters<typeof localName>[0];
  onChanged: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const live = isLive(promotion);

  async function toggle() {
    setBusy(true);
    setFailure(null);
    try {
      await setPromotionActive(
        client,
        companyId,
        promotion.id,
        !promotion.is_active,
      );
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <tr className={promotion.is_active ? undefined : 'detail__row--aside'}>
      <td>
        <span className="detail__strong">
          {localName(locale, promotion.name, promotion.name_ar)}
        </span>
        <span className="ds-caption">{promotion.code}</span>
        {promotion.coupon_code && (
          <span className="ds-badge">{promotion.coupon_code}</span>
        )}
        {failure && (
          <span className="form__error" role="alert">
            {failure}
          </span>
        )}
      </td>
      <td>{describeKind(promotion, t)}</td>
      <td>{promotion.applies_to || t('promo.everything')}</td>
      <td>
        <span
          className={`ds-badge ds-badge--${
            live === 'running'
              ? 'success'
              : live === 'scheduled'
                ? 'info'
                : 'muted'
          }`}
        >
          {t(`promo.state.${live}` as Key)}
        </span>
        {(promotion.starts_on || promotion.ends_on) && (
          <span className="ds-caption">
            {promotion.starts_on ? shortDate(promotion.starts_on, locale) : ''}
            {promotion.ends_on ? ' — ' + shortDate(promotion.ends_on, locale) : ''}
          </span>
        )}
      </td>
      <td className="num">{promotion.times_used}</td>
      <td className="num">
        {money(promotion.discount_given, { currency: promotion.currency })}
        {/* What it sold, beside what it cost. Either figure alone is the wrong
            half of the question a campaign is judged on. */}
        <span className="ds-caption">
          {t('promo.onSalesOf', {
            amount: money(promotion.sales_generated, {
              currency: promotion.currency,
            }),
          })}
        </span>
      </td>
      <td>
        {mayManage && (
          <button
            className="ds-btn ds-btn--quiet"
            disabled={busy}
            onClick={() => void toggle()}
          >
            {t(promotion.is_active ? 'promo.stop' : 'promo.resume')}
          </button>
        )}
      </td>
    </tr>
  );
}

function PromotionForm({
  companyId,
  onCancel,
  onCreated,
}: {
  companyId: string;
  onCancel: () => void;
  onCreated: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [kind, setKind] = useState<PromotionKind>('percentage');
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [value, setValue] = useState('');
  const [buyQty, setBuyQty] = useState('');
  const [getQty, setGetQty] = useState('');
  const [startsOn, setStartsOn] = useState('');
  const [endsOn, setEndsOn] = useState('');
  const [minPurchase, setMinPurchase] = useState('');
  const [coupon, setCoupon] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await createPromotion(client, companyId, {
        code,
        name,
        kind,
        value,
        buy_qty: buyQty,
        get_qty: getQty,
        starts_on: startsOn,
        ends_on: endsOn,
        min_purchase: minPurchase,
        coupon_code: coupon,
      });
      onCreated();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel promo__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('promo.add')}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="segmented" role="group" aria-label={t('promo.offers')}>
          {(
            [
              ['percentage', 'promo.kind.percentage'],
              ['amount', 'promo.kind.amount'],
              ['buy_x_get_y', 'promo.kind.buy_x_get_y'],
              ['bundle_price', 'promo.kind.bundle_price'],
            ] as const
          ).map(([k, label]) => (
            <button
              key={k}
              type="button"
              className={`segmented__btn${kind === k ? ' segmented__btn--on' : ''}`}
              aria-pressed={kind === k}
              onClick={() => setKind(k)}
            >
              {t(label as Key)}
            </button>
          ))}
        </div>

        <div className="form__grid">
          <Field label={t('promo.code')} htmlFor="promo-code" required>
            <TextInput id="promo-code" value={code} onChange={setCode} />
          </Field>
          <Field label={t('promo.name')} htmlFor="promo-name" required>
            <TextInput id="promo-name" value={name} onChange={setName} />
          </Field>

          {/* Exactly the fields the chosen kind uses. Showing all four and
              ignoring three is how a promotion gets saved with a value nobody
              meant. */}
          {kind === 'percentage' && (
            <Field label={t('promo.percentOff')} htmlFor="promo-value" required>
              <TextInput
                id="promo-value"
                value={value}
                onChange={setValue}
                inputMode="decimal"
              />
            </Field>
          )}
          {kind === 'amount' && (
            <Field
              label={t('promo.amountOff')}
              hint={t('promo.amountOffHint')}
              htmlFor="promo-value"
              required
            >
              <TextInput
                id="promo-value"
                value={value}
                onChange={setValue}
                inputMode="decimal"
              />
            </Field>
          )}
          {kind === 'buy_x_get_y' && (
            <>
              <Field label={t('promo.theyBuy')} htmlFor="promo-buy" required>
                <TextInput
                  id="promo-buy"
                  value={buyQty}
                  onChange={setBuyQty}
                  inputMode="numeric"
                />
              </Field>
              <Field label={t('promo.theyGet')} htmlFor="promo-get" required>
                <TextInput
                  id="promo-get"
                  value={getQty}
                  onChange={setGetQty}
                  inputMode="numeric"
                />
              </Field>
            </>
          )}
          {kind === 'bundle_price' && (
            <>
              <Field label={t('promo.howMany')} htmlFor="promo-buy" required>
                <TextInput
                  id="promo-buy"
                  value={buyQty}
                  onChange={setBuyQty}
                  inputMode="numeric"
                />
              </Field>
              <Field label={t('promo.forHowMuch')} htmlFor="promo-value" required>
                <TextInput
                  id="promo-value"
                  value={value}
                  onChange={setValue}
                  inputMode="decimal"
                />
              </Field>
            </>
          )}

          <Field label={t('promo.from')} htmlFor="promo-from">
            <input
              id="promo-from"
              type="date"
              className="field__input"
              value={startsOn}
              max={endsOn || undefined}
              onChange={(e) => setStartsOn(e.target.value)}
            />
          </Field>
          <Field label={t('promo.until')} htmlFor="promo-until">
            <input
              id="promo-until"
              type="date"
              className="field__input"
              value={endsOn}
              min={startsOn || undefined}
              onChange={(e) => setEndsOn(e.target.value)}
            />
          </Field>

          <Field
            label={t('promo.minPurchase')}
            hint={t('promo.minPurchaseHint')}
            htmlFor="promo-min"
          >
            <TextInput
              id="promo-min"
              value={minPurchase}
              onChange={setMinPurchase}
              inputMode="decimal"
            />
          </Field>

          <Field
            label={t('promo.coupon')}
            hint={t('promo.couponHint')}
            htmlFor="promo-coupon"
          >
            <TextInput id="promo-coupon" value={coupon} onChange={setCoupon} />
          </Field>
        </div>

        <FormActions
          submitLabel={t('promo.add')}
          busy={busy}
          disabled={code.trim() === '' || name.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
