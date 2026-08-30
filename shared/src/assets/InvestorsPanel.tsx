// Investors and their money (blueprint C3.2).
//
// # The heading says what the percentage means
//
// C3.2 asks for "each investor's proportional share", and the screen shows it —
// but headed "share of capital", never "ownership". Who owns a business is a
// legal fact that lives in a shareholders' agreement, and a percentage printed
// beside somebody's name with no label invites exactly the wrong reading of it.
//
// # A withdrawal is offered beside a contribution
//
// Money coming back out is not an error to be corrected; it is an ordinary
// thing owners do. Making it a separate, harder path would push people towards
// recording it as an expense, which is precisely what C3.2 exists to prevent.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { localName, money, shortDate } from '../ui/format';
import { listMoneyAccounts, type MoneyAccount } from '../api/treasury';
import {
  addInvestor,
  investorStatement,
  listInvestors,
  recordInvestment,
  type Investor,
  type InvestorMovement,
} from '../api/assets';
import { today } from './assets';

export function InvestorsPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('investor.manage');

  const [adding, setAdding] = useState(false);
  const [moving, setMoving] = useState<Investor | null>(null);
  const [open, setOpen] = useState<Investor | null>(null);

  const load = useCallback(
    () => listInvestors(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <StatementPanel
        companyId={companyId}
        investor={open}
        onBack={() => setOpen(null)}
      />
    );
  }

  return (
    <>
      {adding && (
        <InvestorForm
          companyId={companyId}
          onCancel={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            reload();
          }}
        />
      )}

      {moving && (
        <MovementForm
          companyId={companyId}
          investor={moving}
          onCancel={() => setMoving(null)}
          onRecorded={() => {
            setMoving(null);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('assets.investors')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('assets.investors')}</h2>
            {/* Says what the percentage is, on the panel rather than only in a
                column heading. */}
            <p className="ds-caption">{t('inv.shareMeans')}</p>
          </div>
          {mayManage && !adding && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setAdding(true)}
            >
              {t('inv.add')}
            </button>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Investor[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('inv.noneTitle')}
                  body={t('inv.noneBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('inv.investor')}</th>
                      <th scope="col" className="num">
                        {t('inv.contributed')}
                      </th>
                      <th scope="col" className="num">
                        {t('inv.withdrawn')}
                      </th>
                      <th scope="col" className="num">
                        {t('inv.net')}
                      </th>
                      <th scope="col" className="num">
                        {t('inv.share')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((i) => (
                      <tr key={i.id}>
                        <td>
                          <span className="detail__strong">
                            {localName(locale, i.name, i.name_ar)}
                          </span>
                          <span className="ds-caption">
                            {t(`inv.kind.${i.kind}` as Key)}
                          </span>
                        </td>
                        <td className="num">
                          {money(i.contributed, { currency: i.currency })}
                        </td>
                        <td className="num">
                          {money(i.withdrawn, { currency: i.currency })}
                        </td>
                        <td className="num">
                          {money(i.net, { currency: i.currency })}
                        </td>
                        <td className="num">{i.share_of_capital}%</td>
                        <td>
                          <div className="assets__actions">
                            <button
                              className="ds-btn ds-btn--quiet"
                              onClick={() => setOpen(i)}
                            >
                              {t('inv.statement')}
                            </button>
                            {mayManage && (
                              <button
                                className="ds-btn ds-btn--quiet"
                                onClick={() => setMoving(i)}
                              >
                                {t('inv.record')}
                              </button>
                            )}
                          </div>
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

function StatementPanel({
  companyId,
  investor,
  onBack,
}: {
  companyId: string;
  investor: Investor;
  onBack: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => investorStatement(client, companyId, investor.id),
    [client, companyId, investor.id],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={investor.name}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{investor.name}</h2>
          <p className="ds-caption">
            {t('inv.holds', {
              amount: money(investor.net, { currency: investor.currency }),
            })}
          </p>
        </div>
        <button className="ds-btn ds-btn--quiet" onClick={onBack}>
          {t('action.back')}
        </button>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: InvestorMovement[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('inv.noMovementsTitle')}
                body={t('inv.noMovementsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('inv.when')}</th>
                    <th scope="col">{t('inv.what')}</th>
                    <th scope="col">{t('inv.account')}</th>
                    <th scope="col" className="num">
                      {t('inv.amount')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((m) => (
                    <tr key={m.id}>
                      <td>{shortDate(m.moved_on, locale)}</td>
                      <td>
                        <span className="detail__strong">
                          {t(
                            m.direction === 'contribution'
                              ? 'inv.putIn'
                              : 'inv.tookOut',
                          )}
                        </span>
                        {m.note && <span className="ds-caption">{m.note}</span>}
                      </td>
                      <td>{m.account}</td>
                      <td className="num">
                        <span
                          className={
                            m.direction === 'contribution'
                              ? 'inv__in'
                              : 'inv__out'
                          }
                        >
                          {money(m.amount, { currency: m.currency })}
                        </span>
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
  );
}

function InvestorForm({
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
  const [kind, setKind] = useState<'owner' | 'investor'>('investor');
  const [email, setEmail] = useState('');
  const [phone, setPhone] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await addInvestor(client, companyId, { name, kind, email, phone });
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
        <h2 className="ds-h3">{t('inv.add')}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('inv.name')} htmlFor="investor-name" required>
            <TextInput id="investor-name" value={name} onChange={setName} />
          </Field>
          <Field label={t('inv.kindLabel')} htmlFor="investor-kind" required>
            <select
              id="investor-kind"
              className="input"
              value={kind}
              onChange={(e) => setKind(e.target.value as 'owner' | 'investor')}
            >
              <option value="owner">{t('inv.kind.owner')}</option>
              <option value="investor">{t('inv.kind.investor')}</option>
            </select>
          </Field>
          <Field label={t('inv.email')} htmlFor="investor-email">
            <TextInput
              id="investor-email"
              value={email}
              onChange={setEmail}
              type="email"
            />
          </Field>
          <Field label={t('inv.phone')} htmlFor="investor-phone">
            <TextInput id="investor-phone" value={phone} onChange={setPhone} />
          </Field>
        </div>
        <FormActions
          submitLabel={t('inv.add')}
          busy={busy}
          disabled={name.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

function MovementForm({
  companyId,
  investor,
  onCancel,
  onRecorded,
}: {
  companyId: string;
  investor: Investor;
  onCancel: () => void;
  onRecorded: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [direction, setDirection] = useState<'contribution' | 'withdrawal'>(
    'contribution',
  );
  const [amount, setAmount] = useState('');
  const [accountId, setAccountId] = useState('');
  const [movedOn, setMovedOn] = useState(today());
  const [reference, setReference] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  // Fixed when the form opens, so a second press on a slow connection sends
  // the same identifier and the server hands back the first.
  const [movementId] = useState(() => crypto.randomUUID());

  const loadAccounts = useCallback(
    () => listMoneyAccounts(client, companyId),
    [client, companyId],
  );
  const accounts = useRemote(loadAccounts);
  const places: MoneyAccount[] =
    accounts.remote.state === 'ready' ? accounts.remote.data.data : [];

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await recordInvestment(client, companyId, {
        uuid: movementId,
        investor_id: investor.id,
        direction,
        amount,
        moved_on: movedOn,
        money_account_id: accountId,
        reference,
        note,
      });
      onRecorded();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel assets__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('inv.recordFor', { name: investor.name })}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="segmented" role="group" aria-label={t('inv.what')}>
          {(
            [
              ['contribution', 'inv.putIn'],
              ['withdrawal', 'inv.tookOut'],
            ] as const
          ).map(([key, label]) => (
            <button
              key={key}
              type="button"
              className={`segmented__btn${direction === key ? ' segmented__btn--on' : ''}`}
              aria-pressed={direction === key}
              onClick={() => setDirection(key)}
            >
              {t(label as Key)}
            </button>
          ))}
        </div>

        <div className="form__grid">
          <Field label={t('inv.amount')} htmlFor="mv-amount" required>
            <TextInput
              id="mv-amount"
              value={amount}
              onChange={setAmount}
              inputMode="decimal"
            />
          </Field>

          <Field
            label={t(direction === 'contribution' ? 'inv.into' : 'inv.outOf')}
            htmlFor="mv-account"
            required
          >
            <select
              id="mv-account"
              className="input"
              value={accountId}
              onChange={(e) => setAccountId(e.target.value)}
            >
              <option value="">{t('inv.chooseAccount')}</option>
              {places.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>

          <Field label={t('inv.when')} htmlFor="mv-date" required>
            <input
              id="mv-date"
              type="date"
              className="field__input"
              value={movedOn}
              onChange={(e) => setMovedOn(e.target.value)}
            />
          </Field>

          <Field label={t('inv.reference')} htmlFor="mv-ref">
            <TextInput id="mv-ref" value={reference} onChange={setReference} />
          </Field>

          <Field label={t('inv.note')} htmlFor="mv-note">
            <TextInput id="mv-note" value={note} onChange={setNote} />
          </Field>
        </div>

        <FormActions
          submitLabel={t('inv.record')}
          busy={busy}
          disabled={amount.trim() === '' || accountId === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
