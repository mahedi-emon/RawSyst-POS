// Bank reconciliation (blueprint C11).
//
// # The difference is the headline, not a footnote
//
// C11's sentence is "proves that what the software says is in the bank is
// actually what the bank says". The proof is one number: the closing balance
// the bank states, less the ledger balance, less everything on both exception
// lists. When it is nil the two agree; when it is not, something is wrong that
// neither list explains.
//
// So it sits at the top in words, and the Reconcile button is absent — not
// disabled — while it is not nil. A disabled button invites pressing; an
// explanation invites reading.
//
// # Both exception lists are shown, and the second one matters more
//
// Bank lines nobody matched are the charges and interest the books lack, and
// they are usually a small job. Ledger entries nobody matched are the cheque
// that never cleared and the payment recorded twice — and the second of those
// is the error this whole screen exists to catch.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { money, shortDate } from '../ui/format';
import {
  importStatement,
  listMoneyAccounts,
  listStatements,
  matchStatementLine,
  readStatement,
  reconcileStatement,
  type BankStatement,
  type MoneyAccount,
} from '../api/treasury';
import { isOff, parseStatementCsv, today } from './accounting';

export function ReconcilePanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [open, setOpen] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);

  const load = useCallback(
    () => listStatements(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const loadAccounts = useCallback(
    () => listMoneyAccounts(client, companyId),
    [client, companyId],
  );
  const accounts = useRemote(loadAccounts);
  const bankAccounts: MoneyAccount[] =
    accounts.remote.state === 'ready'
      ? accounts.remote.data.data.filter((a) =>
          ['bank', 'card_settlement', 'gateway'].includes(a.kind),
        )
      : [];

  if (open) {
    return (
      <StatementDetail
        companyId={companyId}
        statementId={open}
        onDone={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <>
      {importing && (
        <ImportForm
          companyId={companyId}
          accounts={bankAccounts}
          onCancel={() => setImporting(false)}
          onImported={(id) => {
            setImporting(false);
            reload();
            setOpen(id);
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('recon.title')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('recon.title')}</h2>
          {!importing && bankAccounts.length > 0 && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setImporting(true)}
            >
              {t('recon.import')}
            </button>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: BankStatement[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('recon.noneTitle')}
                  body={t('recon.noneBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('recon.account')}</th>
                      <th scope="col">{t('recon.period')}</th>
                      <th scope="col" className="num">
                        {t('recon.closing')}
                      </th>
                      <th scope="col" className="num">
                        {t('recon.difference')}
                      </th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((st) => (
                      <tr key={st.id}>
                        <td className="detail__strong">{st.account}</td>
                        <td>
                          {shortDate(st.starts_on, locale)} —{' '}
                          {shortDate(st.ends_on, locale)}
                        </td>
                        <td className="num">
                          {money(st.closing_balance, { currency: st.currency })}
                        </td>
                        <td className="num">
                          {isOff(st.difference) ? (
                            <span className="acct__off">
                              {money(st.difference, { currency: st.currency })}
                            </span>
                          ) : (
                            <span aria-hidden="true">—</span>
                          )}
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${
                              st.status === 'reconciled' ? 'success' : 'warning'
                            }`}
                          >
                            {t(
                              st.status === 'reconciled'
                                ? 'recon.reconciled'
                                : 'recon.inProgress',
                            )}
                          </span>
                        </td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setOpen(st.id)}
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
    </>
  );
}

function StatementDetail({
  companyId,
  statementId,
  onDone,
}: {
  companyId: string;
  statementId: string;
  onDone: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => readStatement(client, companyId, statementId),
    [client, companyId, statementId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [picking, setPicking] = useState<string | null>(null);

  async function sign(st: BankStatement) {
    setBusy(true);
    setFailure(null);
    try {
      await reconcileStatement(client, companyId, st.id);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function match(lineId: string, journalLineId: string) {
    setBusy(true);
    setFailure(null);
    try {
      await matchStatementLine(client, companyId, lineId, journalLineId);
      setPicking(null);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(st: BankStatement) => {
        const draft = st.status === 'draft';
        return (
          <>
            <section className="ds-panel" aria-label={st.account}>
              <div className="ds-panel__head">
                <div>
                  <h2 className="ds-h3">{st.account}</h2>
                  <p className="ds-caption">
                    {shortDate(st.starts_on, locale)} —{' '}
                    {shortDate(st.ends_on, locale)}
                  </p>
                </div>
                <div className="acct__reopenactions">
                  <button className="ds-btn ds-btn--quiet" onClick={onDone}>
                    {t('action.back')}
                  </button>
                  {/* Absent, not disabled, while anything is unexplained. A
                      disabled button invites pressing; the sentence above it
                      invites reading. */}
                  {draft && st.reconciled && (
                    <button
                      className="ds-btn ds-btn--primary"
                      disabled={busy}
                      onClick={() => void sign(st)}
                    >
                      {t('recon.signOff')}
                    </button>
                  )}
                </div>
              </div>

              <div className="ds-panel__body">
                <FormError message={failure} />

                <dl className="recon__totals">
                  <div>
                    <dt className="ds-caption">{t('recon.bankSays')}</dt>
                    <dd className="num">
                      {money(st.closing_balance, { currency: st.currency })}
                    </dd>
                  </div>
                  <div>
                    <dt className="ds-caption">{t('recon.booksSay')}</dt>
                    <dd className="num">
                      {money(st.ledger_balance, { currency: st.currency })}
                    </dd>
                  </div>
                </dl>

                <p
                  className={st.reconciled ? 'acct__reconciled' : 'acct__outofbalance'}
                  role={st.reconciled ? undefined : 'alert'}
                >
                  {st.reconciled
                    ? t('recon.agrees')
                    : t('recon.unexplained', {
                        amount: money(st.difference, { currency: st.currency }),
                      })}
                </p>

                {st.reconciled_by && (
                  <p className="ds-caption">
                    {t('recon.signedBy', { who: st.reconciled_by })}
                  </p>
                )}
              </div>
            </section>

            <section className="ds-panel" aria-label={t('recon.bankLines')}>
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('recon.bankLines')}</h2>
              </div>
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('recon.date')}</th>
                      <th scope="col">{t('recon.description')}</th>
                      <th scope="col" className="num">
                        {t('recon.amount')}
                      </th>
                      <th scope="col">{t('recon.matched')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(st.lines ?? []).map((l) => (
                      <tr key={l.id}>
                        <td>{shortDate(l.value_date, locale)}</td>
                        <td>
                          <span>{l.description}</span>
                          {l.reference && (
                            <span className="ds-caption">{l.reference}</span>
                          )}
                        </td>
                        <td className="num">
                          {money(l.amount, { currency: st.currency })}
                        </td>
                        <td>
                          {l.match_kind ? (
                            <>
                              <span className="ds-badge ds-badge--success">
                                {t(
                                  l.match_kind === 'automatic'
                                    ? 'recon.matchedAutomatically'
                                    : 'recon.matchedByHand',
                                )}
                              </span>
                              {l.matched_to && (
                                <span className="ds-caption">{l.matched_to}</span>
                              )}
                              {draft && (
                                <button
                                  className="ds-btn ds-btn--quiet"
                                  disabled={busy}
                                  onClick={() => void match(l.id, '')}
                                >
                                  {t('recon.unmatch')}
                                </button>
                              )}
                            </>
                          ) : (
                            <>
                              <span className="ds-badge ds-badge--warning">
                                {t('recon.notInBooks')}
                              </span>
                              {draft && (st.unmatched_in_books ?? []).length > 0 && (
                                <button
                                  className="ds-btn ds-btn--quiet"
                                  onClick={() =>
                                    setPicking(picking === l.id ? null : l.id)
                                  }
                                >
                                  {t('recon.matchByHand')}
                                </button>
                              )}
                              {picking === l.id && (
                                <ul className="recon__picklist">
                                  {(st.unmatched_in_books ?? []).map((u) => (
                                    <li key={u.id}>
                                      <button
                                        className="ds-btn ds-btn--quiet recon__pick"
                                        disabled={busy}
                                        onClick={() => void match(l.id, u.id)}
                                      >
                                        <span className="detail__strong">
                                          {money(u.amount, {
                                            currency: st.currency,
                                          })}
                                        </span>
                                        <span className="ds-caption">
                                          {shortDate(u.entry_date, locale)} ·{' '}
                                          {u.entry_no}
                                        </span>
                                      </button>
                                    </li>
                                  ))}
                                </ul>
                              )}
                            </>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            <section className="ds-panel" aria-label={t('recon.notOnStatement')}>
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('recon.notOnStatement')}</h2>
                {/* The list that matters most. See the note at the top. */}
                <p className="ds-caption">{t('recon.notOnStatementWhy')}</p>
              </div>
              {(st.unmatched_in_books ?? []).length === 0 ? (
                <div className="ds-panel__body">
                  <EmptyState
                    title={t('recon.allSeenTitle')}
                    body={t('recon.allSeenBody')}
                  />
                </div>
              ) : (
                <div className="ds-panel__body ds-scroll-x">
                  <table className="ds-table">
                    <thead>
                      <tr>
                        <th scope="col">{t('recon.date')}</th>
                        <th scope="col">{t('recon.entry')}</th>
                        <th scope="col" className="num">
                          {t('recon.amount')}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {(st.unmatched_in_books ?? []).map((u) => (
                        <tr key={u.id}>
                          <td>{shortDate(u.entry_date, locale)}</td>
                          <td>
                            <span className="detail__strong">{u.entry_no}</span>
                            {u.memo && <span className="ds-caption">{u.memo}</span>}
                          </td>
                          <td className="num">
                            {money(u.amount, { currency: st.currency })}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </section>
          </>
        );
      }}
    </RemoteBody>
  );
}

function ImportForm({
  companyId,
  accounts,
  onCancel,
  onImported,
}: {
  companyId: string;
  accounts: MoneyAccount[];
  onCancel: () => void;
  onImported: (id: string) => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [accountId, setAccountId] = useState(accounts[0]?.id ?? '');
  const [startsOn, setStartsOn] = useState('');
  const [endsOn, setEndsOn] = useState(today());
  const [opening, setOpening] = useState('');
  const [closing, setClosing] = useState('');
  const [reference, setReference] = useState('');
  const [csv, setCsv] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const parsed = parseStatementCsv(csv);
  // The parser has no translator, so it returns `what:row` and this turns it
  // into a sentence. A parser that returned English would put a hardcoded
  // string on an Arabic screen.
  const problem = parsed.problem ? csvProblem(parsed.problem, t) : null;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (problem) {
      setFailure(problem);
      return;
    }
    setBusy(true);
    setFailure(null);
    try {
      const st = await importStatement(client, companyId, {
        account_id: accountId,
        starts_on: startsOn,
        ends_on: endsOn,
        opening_balance: opening,
        closing_balance: closing,
        reference,
        lines: parsed.lines,
      });
      onImported(st.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel acct__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('recon.import')}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('recon.account')} htmlFor="imp-account" required>
            <select
              id="imp-account"
              className="input"
              value={accountId}
              onChange={(e) => setAccountId(e.target.value)}
            >
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>

          <Field label={t('common.from')} htmlFor="imp-from" required>
            <input
              id="imp-from"
              type="date"
              className="field__input"
              value={startsOn}
              max={endsOn}
              onChange={(e) => setStartsOn(e.target.value)}
            />
          </Field>

          <Field label={t('common.to')} htmlFor="imp-to" required>
            <input
              id="imp-to"
              type="date"
              className="field__input"
              value={endsOn}
              min={startsOn}
              onChange={(e) => setEndsOn(e.target.value)}
            />
          </Field>

          <Field
            label={t('recon.openingBalance')}
            hint={t('recon.balanceHint')}
            htmlFor="imp-opening"
            required
          >
            <TextInput
              id="imp-opening"
              value={opening}
              onChange={setOpening}
              inputMode="decimal"
            />
          </Field>

          <Field label={t('recon.closingBalance')} htmlFor="imp-closing" required>
            <TextInput
              id="imp-closing"
              value={closing}
              onChange={setClosing}
              inputMode="decimal"
            />
          </Field>

          <Field label={t('recon.reference')} htmlFor="imp-ref">
            <TextInput id="imp-ref" value={reference} onChange={setReference} />
          </Field>
        </div>

        <Field
          label={t('recon.lines')}
          hint={t('recon.linesHint')}
          htmlFor="imp-csv"
          required
        >
          <textarea
            id="imp-csv"
            className="input recon__csv"
            rows={10}
            value={csv}
            onChange={(e) => setCsv(e.target.value)}
          />
        </Field>

        {csv.trim() !== '' && (
          <p className="ds-body-sm" role="status">
            {problem ??
              t('recon.linesRead', { count: String(parsed.lines.length) })}
          </p>
        )}

        <FormActions
          submitLabel={t('recon.import')}
          busy={busy}
          disabled={parsed.lines.length === 0}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

/** The parser's answer, as a sentence.
 *
 *  Falls back to the raw key rather than to a blank: a problem nobody can read
 *  is still better than a form that refuses with no explanation at all. */
function csvProblem(
  problem: string,
  t: (k: Key, vars?: Record<string, string>) => string,
): string {
  const [what, row] = problem.split(':');
  const key = `recon.csvProblem.${what}` as Key;
  const named = t(key, { row: row ?? '' });
  return named === key ? problem : named;
}
