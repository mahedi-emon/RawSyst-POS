// The fixed asset register (blueprint C7).
//
// # Depreciation is offered a month at a time, and the screen says which
//
// A person's instinct is "bring it up to date", and the product deliberately
// does not offer that: each month's charge belongs in that month's profit and
// loss, so a company that has not run it since March runs it four times and
// each entry lands where it belongs. The button therefore names the month it
// will charge rather than saying "run depreciation".
//
// # Nobody types a gain
//
// The disposal form asks what the business got for the thing. Book value and
// the resulting gain or loss are shown as they will be posted, computed by the
// server from what it has actually depreciated. A field for "loss on disposal"
// would be a field somebody could make say anything.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import { localName, money, shortDate } from '../ui/format';
import { listMoneyAccounts, type MoneyAccount } from '../api/treasury';
import {
  addAsset,
  depreciate,
  disposeAsset,
  listAssets,
  type Asset,
  type Charged,
} from '../api/assets';
import { monthAfter, monthLabelOf, today } from './assets';

export function AssetRegisterPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('asset.manage');

  const [adding, setAdding] = useState(false);
  const [disposing, setDisposing] = useState<Asset | null>(null);
  const [charged, setCharged] = useState<Charged | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [showDisposed, setShowDisposed] = useState(false);

  const load = useCallback(
    () => listAssets(client, companyId, showDisposed),
    [client, companyId, showDisposed],
  );
  const { remote, reload } = useRemote(load);

  const assets: Asset[] = remote.state === 'ready' ? remote.data.data : [];
  // The next month anybody owes. Computed from the register rather than from
  // the clock, so the button charges what is actually outstanding.
  const nextMonth = monthAfter(assets);

  async function runDepreciation() {
    if (!nextMonth) return;
    setBusy(true);
    setFailure(null);
    try {
      const done = await depreciate(client, companyId, nextMonth);
      setCharged(done);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      {adding && (
        <AssetForm
          companyId={companyId}
          onCancel={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            reload();
          }}
        />
      )}

      {disposing && (
        <DisposeForm
          companyId={companyId}
          asset={disposing}
          onCancel={() => setDisposing(null)}
          onDisposed={() => {
            setDisposing(null);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('assets.register')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('assets.register')}</h2>
          <div className="assets__actions">
            <label className="assets__check">
              <input
                type="checkbox"
                checked={showDisposed}
                onChange={(e) => setShowDisposed(e.target.checked)}
              />
              <span>{t('assets.showDisposed')}</span>
            </label>
            {mayManage && nextMonth && (
              // Names the month it will charge. "Run depreciation" would
              // invite the assumption that it brings everything up to date,
              // which it deliberately does not.
              <button
                className="ds-btn ds-btn--quiet"
                disabled={busy}
                onClick={() => void runDepreciation()}
              >
                {t('assets.depreciateMonth', {
                  month: monthLabelOf(nextMonth, locale),
                })}
              </button>
            )}
            {mayManage && !adding && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setAdding(true)}
              >
                {t('assets.add')}
              </button>
            )}
          </div>
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
          {charged && (
            <p className="assets__charged" role="status">
              {charged.assets_charged === 0
                ? t('assets.nothingDue')
                : t('assets.chargedFor', {
                    month: monthLabelOf(charged.month, locale),
                    count: String(charged.assets_charged),
                    total: money(charged.total, { currency: charged.currency }),
                  })}
            </p>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Asset[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('assets.noneTitle')}
                  body={t('assets.noneBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('assets.asset')}</th>
                      <th scope="col">{t('assets.where')}</th>
                      <th scope="col" className="num">
                        {t('assets.cost')}
                      </th>
                      <th scope="col" className="num">
                        {t('assets.depreciated')}
                      </th>
                      <th scope="col" className="num">
                        {t('assets.bookValue')}
                      </th>
                      <th scope="col" className="num">
                        {t('assets.monthsDue')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((a) => (
                      <tr
                        key={a.id}
                        className={
                          a.status === 'disposed' ? 'detail__row--aside' : undefined
                        }
                      >
                        <td>
                          <span className="detail__strong">
                            {localName(locale, a.name, a.name_ar)}
                          </span>
                          <span className="ds-caption">
                            {a.asset_no} · {a.category}
                          </span>
                          {a.serial_number && (
                            <span className="ds-caption">{a.serial_number}</span>
                          )}
                        </td>
                        <td>
                          {a.store}
                          {a.custodian && (
                            <span className="ds-caption">{a.custodian}</span>
                          )}
                        </td>
                        <td className="num">
                          {money(a.cost, { currency: a.currency })}
                        </td>
                        <td className="num">
                          {money(a.depreciated, { currency: a.currency })}
                          <span className="ds-caption">
                            {t('assets.perMonth', {
                              amount: money(a.monthly_charge, {
                                currency: a.currency,
                              }),
                            })}
                          </span>
                        </td>
                        <td className="num">
                          {money(a.book_value, { currency: a.currency })}
                        </td>
                        <td className="num">
                          {/* The only figure on this screen that is somebody's
                              job today. A register that is up to date should
                              look quiet. */}
                          {a.months_due > 0 ? (
                            <span className="ds-badge ds-badge--warning">
                              {a.months_due}
                            </span>
                          ) : (
                            <span aria-hidden="true">—</span>
                          )}
                        </td>
                        <td>
                          {a.status === 'disposed' ? (
                            <span className="ds-caption">
                              {t('assets.disposedOn', {
                                date: shortDate(a.disposed_on ?? '', locale),
                              })}
                            </span>
                          ) : (
                            mayManage && (
                              <button
                                className="ds-btn ds-btn--quiet"
                                onClick={() => setDisposing(a)}
                              >
                                {t('assets.dispose')}
                              </button>
                            )
                          )}
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
    </>
  );
}

function AssetForm({
  companyId,
  onCancel,
  onAdded,
}: {
  companyId: string;
  onCancel: () => void;
  onAdded: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [name, setName] = useState('');
  const [category, setCategory] = useState('');
  const [cost, setCost] = useState('');
  const [residual, setResidual] = useState('0');
  const [life, setLife] = useState('36');
  const [acquired, setAcquired] = useState(today());
  const [serial, setSerial] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await addAsset(client, companyId, {
        name,
        category,
        cost,
        residual_value: residual,
        useful_life_months: Number(life),
        acquired_on: acquired,
        serial_number: serial,
      });
      onAdded();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel assets__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('assets.add')}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('assets.name')} htmlFor="asset-name" required>
            <TextInput id="asset-name" value={name} onChange={setName} />
          </Field>
          <Field
            label={t('assets.category')}
            hint={t('assets.categoryHint')}
            htmlFor="asset-category"
            required
          >
            <TextInput id="asset-category" value={category} onChange={setCategory} />
          </Field>
          <Field label={t('assets.cost')} htmlFor="asset-cost" required>
            <TextInput
              id="asset-cost"
              value={cost}
              onChange={setCost}
              inputMode="decimal"
            />
          </Field>
          <Field
            label={t('assets.residual')}
            hint={t('assets.residualHint')}
            htmlFor="asset-residual"
          >
            <TextInput
              id="asset-residual"
              value={residual}
              onChange={setResidual}
              inputMode="decimal"
            />
          </Field>
          <Field
            label={t('assets.life')}
            hint={t('assets.lifeHint')}
            htmlFor="asset-life"
            required
          >
            <TextInput
              id="asset-life"
              value={life}
              onChange={setLife}
              inputMode="numeric"
            />
          </Field>
          <Field label={t('assets.acquired')} htmlFor="asset-acquired" required>
            <input
              id="asset-acquired"
              type="date"
              className="field__input"
              value={acquired}
              onChange={(e) => setAcquired(e.target.value)}
            />
          </Field>
          <Field label={t('assets.serial')} htmlFor="asset-serial">
            <TextInput id="asset-serial" value={serial} onChange={setSerial} />
          </Field>
        </div>

        <FormActions
          submitLabel={t('assets.add')}
          busy={busy}
          disabled={name.trim() === '' || cost.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

function DisposeForm({
  companyId,
  asset,
  onCancel,
  onDisposed,
}: {
  companyId: string;
  asset: Asset;
  onCancel: () => void;
  onDisposed: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [proceeds, setProceeds] = useState('0');
  const [accountId, setAccountId] = useState('');
  const [disposedOn, setDisposedOn] = useState(today());
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const loadAccounts = useCallback(
    () => listMoneyAccounts(client, companyId),
    [client, companyId],
  );
  const accounts = useRemote(loadAccounts);
  const money_accounts: MoneyAccount[] =
    accounts.remote.state === 'ready' ? accounts.remote.data.data : [];

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await disposeAsset(client, companyId, asset.id, {
        proceeds,
        money_account_id: accountId || undefined,
        disposed_on: disposedOn,
        note,
      });
      onDisposed();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel assets__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('assets.disposeOf', { asset: asset.name })}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        {/* What the books currently say it is worth. Shown because the gain or
            loss is the difference between this and what somebody types below,
            and a person entering a figure should be able to see which way it
            will land. */}
        <p className="ds-body-sm">
          {t('assets.bookValueNow', {
            amount: money(asset.book_value, { currency: asset.currency }),
          })}
        </p>

        <div className="form__grid">
          <Field
            label={t('assets.proceeds')}
            hint={t('assets.proceedsHint')}
            htmlFor="dispose-proceeds"
            required
          >
            <TextInput
              id="dispose-proceeds"
              value={proceeds}
              onChange={setProceeds}
              inputMode="decimal"
            />
          </Field>

          <Field label={t('assets.moneyInto')} htmlFor="dispose-account">
            <select
              id="dispose-account"
              className="input"
              value={accountId}
              onChange={(e) => setAccountId(e.target.value)}
            >
              <option value="">{t('assets.scrapped')}</option>
              {money_accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>

          <Field label={t('assets.disposedOnLabel')} htmlFor="dispose-date" required>
            <input
              id="dispose-date"
              type="date"
              className="field__input"
              value={disposedOn}
              onChange={(e) => setDisposedOn(e.target.value)}
            />
          </Field>

          <Field label={t('assets.note')} htmlFor="dispose-note">
            <TextInput id="dispose-note" value={note} onChange={setNote} />
          </Field>
        </div>

        <FormActions
          submitLabel={t('assets.dispose')}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
