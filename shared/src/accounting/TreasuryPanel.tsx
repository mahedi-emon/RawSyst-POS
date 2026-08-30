// Cash and bank (blueprint C2).
//
// Where the money is, and moving it between the company's own accounts.
//
// # A transfer form that cannot be submitted twice
//
// The voucher's identity is fixed when the form opens, not when Save is
// pressed. On a slow connection a second press sends the same identifier and
// the server hands back the first transfer rather than banking the takings
// again — which is the failure this form would otherwise produce most often,
// because the thing a person does when a button seems not to have worked is
// press it again.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { localName, money, shortDate } from '../ui/format';
import {
  listMoneyAccounts,
  listMoneyTransfers,
  moveMoney,
  setMoneyAccountActive,
  type MoneyAccount,
  type MoneyTransfer,
} from '../api/treasury';
import { today } from './accounting';

export function TreasuryPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayMove = can('accounting.create');
  const mayManage = can('accounting.manage_accounts');

  const [moving, setMoving] = useState(false);

  const load = useCallback(
    () => listMoneyAccounts(client, companyId, true),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const loadTransfers = useCallback(
    () => listMoneyTransfers(client, companyId),
    [client, companyId],
  );
  const transfers = useRemote(loadTransfers);

  const accounts: MoneyAccount[] =
    remote.state === 'ready' ? remote.data.data : [];

  return (
    <>
      {moving && (
        <MoveForm
          companyId={companyId}
          accounts={accounts.filter((a) => a.is_active)}
          onCancel={() => setMoving(false)}
          onMoved={() => {
            setMoving(false);
            reload();
            transfers.reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('treasury.accounts')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('treasury.accounts')}</h2>
          {mayMove && !moving && accounts.length > 1 && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setMoving(true)}
            >
              {t('treasury.move')}
            </button>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: MoneyAccount[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('treasury.noAccountsTitle')}
                  body={t('treasury.noAccountsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('treasury.account')}</th>
                      <th scope="col">{t('treasury.kind')}</th>
                      <th scope="col">{t('treasury.where')}</th>
                      <th scope="col" className="num">
                        {t('treasury.balance')}
                      </th>
                      <th scope="col" className="num">
                        {t('treasury.unreconciled')}
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
                      <AccountRow
                        key={a.id}
                        companyId={companyId}
                        account={a}
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

      <section className="ds-panel" aria-label={t('treasury.transfers')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('treasury.transfers')}</h2>
        </div>
        <RemoteBody remote={transfers.remote} onRetry={transfers.reload}>
          {(payload: { data: MoneyTransfer[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('treasury.noTransfersTitle')}
                  body={t('treasury.noTransfersBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('treasury.voucher')}</th>
                      <th scope="col">{t('treasury.when')}</th>
                      <th scope="col">{t('treasury.route')}</th>
                      <th scope="col">{t('treasury.who')}</th>
                      <th scope="col" className="num">
                        {t('treasury.amount')}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((tr) => (
                      <tr key={tr.id}>
                        <td className="detail__strong">{tr.transfer_no}</td>
                        <td>{shortDate(tr.moved_on, locale)}</td>
                        <td>
                          <span>
                            {t('treasury.fromTo', { from: tr.from, to: tr.to })}
                          </span>
                          {tr.note && (
                            <span className="ds-caption">{tr.note}</span>
                          )}
                        </td>
                        <td>{tr.created_by}</td>
                        <td className="num">
                          {money(tr.amount, { currency: tr.currency })}
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

function AccountRow({
  companyId,
  account,
  mayManage,
  locale,
  onChanged,
}: {
  companyId: string;
  account: MoneyAccount;
  mayManage: boolean;
  locale: Parameters<typeof localName>[0];
  onChanged: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function toggle() {
    setBusy(true);
    setFailure(null);
    try {
      await setMoneyAccountActive(client, companyId, account.id, !account.is_active);
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <tr className={account.is_active ? undefined : 'detail__row--aside'}>
      <td>
        <span className="detail__strong">
          {localName(locale, account.name, account.name_ar)}
        </span>
        <span className="ds-caption">{account.account_code}</span>
        {account.iban && <span className="ds-caption">{account.iban}</span>}
        {failure && (
          <span className="form__error" role="alert">
            {failure}
          </span>
        )}
      </td>
      <td>{t(`treasury.kind.${account.kind}` as Key)}</td>
      <td>{account.store || t('treasury.wholeBusiness')}</td>
      <td className="num">
        {money(account.balance, { currency: account.currency })}
      </td>
      <td className="num">
        {/* Only asked of an account with a statement to reconcile against. On
            the petty cash tin the number would have no question behind it. */}
        {account.unreconciled ? (
          <span className="ds-badge ds-badge--warning">
            {account.unreconciled}
          </span>
        ) : (
          <span aria-hidden="true">—</span>
        )}
      </td>
      <td>
        {mayManage && (
          <button
            className="ds-btn ds-btn--quiet"
            disabled={busy}
            onClick={() => void toggle()}
          >
            {t(account.is_active ? 'treasury.retire' : 'treasury.bringBack')}
          </button>
        )}
      </td>
    </tr>
  );
}

function MoveForm({
  companyId,
  accounts,
  onCancel,
  onMoved,
}: {
  companyId: string;
  accounts: MoneyAccount[];
  onCancel: () => void;
  onMoved: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [from, setFrom] = useState(accounts[0]?.id ?? '');
  const [to, setTo] = useState(accounts[1]?.id ?? '');
  const [amount, setAmount] = useState('');
  const [movedOn, setMovedOn] = useState(today());
  const [reference, setReference] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  // Fixed when the form opens, not when Save is pressed. See the note at the
  // top of the file.
  const [voucherId] = useState(() => crypto.randomUUID());

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await moveMoney(client, companyId, {
        uuid: voucherId,
        from_account_id: from,
        to_account_id: to,
        amount,
        moved_on: movedOn,
        reference,
        note,
      });
      onMoved();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel acct__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('treasury.move')}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('treasury.from')} htmlFor="tfr-from" required>
            <select
              id="tfr-from"
              className="input"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            >
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>

          <Field label={t('treasury.to')} htmlFor="tfr-to" required>
            <select
              id="tfr-to"
              className="input"
              value={to}
              onChange={(e) => setTo(e.target.value)}
            >
              {accounts
                .filter((a) => a.id !== from)
                .map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
            </select>
          </Field>

          <Field label={t('treasury.amount')} htmlFor="tfr-amount" required>
            <TextInput
              id="tfr-amount"
              value={amount}
              onChange={setAmount}
              inputMode="decimal"
            />
          </Field>

          <Field label={t('treasury.when')} htmlFor="tfr-date" required>
            <input
              id="tfr-date"
              type="date"
              className="field__input"
              value={movedOn}
              onChange={(e) => setMovedOn(e.target.value)}
            />
          </Field>

          <Field label={t('treasury.reference')} htmlFor="tfr-ref">
            <TextInput id="tfr-ref" value={reference} onChange={setReference} />
          </Field>

          <Field label={t('treasury.note')} htmlFor="tfr-note">
            <TextInput id="tfr-note" value={note} onChange={setNote} />
          </Field>
        </div>

        <FormActions
          submitLabel={t('treasury.move')}
          busy={busy}
          disabled={!from || !to || from === to || amount.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
