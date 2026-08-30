// The four statements C9.3 requires, which had routes and no screen.
//
// # Every statement says how much it can be trusted
//
// A profit-and-loss for a month that is still open will be a different number
// tomorrow. A person who prints one and sends it to a bank should be told that
// before they send it, not afterwards — so each statement carries a line saying
// whether the periods it covers are closed.
//
// # An imbalance is shown, never hidden
//
// The trial balance and the balance sheet both come back with a `difference`
// and a `balanced` flag. Refusing to render an unbalanced set of books would
// hide the one thing those two statements exist to reveal, so an imbalance is
// stated at the top, in words, above the figures that produced it.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { localName, money } from '../ui/format';
import {
  balanceSheet,
  cashFlow,
  profitAndLoss,
  trialBalance,
  type BalanceSheet,
  type CashFlow,
  type FiscalCalendar,
  type ProfitAndLoss,
  type StatementLine,
  type TrialBalance,
} from '../api/accounting';
import { isOff, settlednessOf, type Range } from './accounting';

type Statement = 'trial' | 'pnl' | 'balance' | 'cash';

export function StatementsPanel({
  companyId,
  period,
  onPeriod,
  calendar,
}: {
  companyId: string;
  period: Range;
  onPeriod: (r: Range) => void;
  calendar: FiscalCalendar | null;
}) {
  const t = useT();
  const [which, setWhich] = useState<Statement>('pnl');

  const settled = settlednessOf(calendar, period);

  return (
    <>
      <section className="ds-panel acct__controls">
        <div className="ds-panel__body acct__controlrow">
          <div className="segmented" role="group" aria-label={t('acct.statements')}>
            {(
              [
                ['pnl', 'acct.profitAndLoss'],
                ['balance', 'acct.balanceSheet'],
                ['cash', 'acct.cashFlow'],
                ['trial', 'acct.trialBalance'],
              ] as const
            ).map(([key, label]) => (
              <button
                key={key}
                className={`segmented__btn${which === key ? ' segmented__btn--on' : ''}`}
                aria-pressed={which === key}
                onClick={() => setWhich(key)}
              >
                {t(label as Key)}
              </button>
            ))}
          </div>

          {/* A balance sheet and a trial balance are drawn AS AT a date; a P&L
              and a cash flow cover a range. Showing a From box on a statement
              that has no from is worse than showing one fewer control. */}
          {(which === 'pnl' || which === 'cash') && (
            <Field label={t('common.from')} htmlFor="acct-from" required>
              <input
                id="acct-from"
                type="date"
                className="field__input"
                value={period.from}
                max={period.to}
                onChange={(e) => onPeriod({ ...period, from: e.target.value })}
              />
            </Field>
          )}
          <Field
            label={t(which === 'pnl' || which === 'cash' ? 'common.to' : 'acct.asOf')}
            htmlFor="acct-to"
            required
          >
            <input
              id="acct-to"
              type="date"
              className="field__input"
              value={period.to}
              min={period.from}
              onChange={(e) => onPeriod({ ...period, to: e.target.value })}
            />
          </Field>
        </div>

        <div className="ds-panel__body acct__settled">
          <span
            className={`ds-badge ds-badge--${
              settled === 'settled' ? 'success' : settled === 'open' ? 'warning' : 'info'
            }`}
          >
            {t(
              settled === 'settled'
                ? 'acct.settled'
                : settled === 'open'
                  ? 'acct.stillOpen'
                  : settled === 'partly'
                    ? 'acct.partlyClosed'
                    : 'acct.settlednessUnknown',
            )}
          </span>
        </div>
      </section>

      {which === 'pnl' && <PnL companyId={companyId} period={period} />}
      {which === 'balance' && <Balance companyId={companyId} asOf={period.to} />}
      {which === 'cash' && <Cash companyId={companyId} period={period} />}
      {which === 'trial' && <Trial companyId={companyId} asOf={period.to} />}
    </>
  );
}

function PnL({ companyId, period }: { companyId: string; period: Range }) {
  const { client } = useAuth();
  const t = useT();
  const load = useCallback(
    () => profitAndLoss(client, companyId, period.from, period.to),
    [client, companyId, period.from, period.to],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('acct.profitAndLoss')}>
      <RemoteBody remote={remote} onRetry={reload}>
        {(r: ProfitAndLoss) => (
          <div className="ds-panel__body ds-scroll-x">
            <table className="ds-table acct__statement">
              <tbody>
                <Group
                  title={t('acct.revenue')}
                  lines={r.revenue}
                  total={r.revenue_total}
                  currency={r.base_currency}
                />
                <Group
                  title={t('acct.costOfSales')}
                  lines={r.cost_of_sales}
                  total={r.cost_of_sales_total}
                  currency={r.base_currency}
                />
                {/* Gross profit gets its own emphasised row because it is the
                    number a retailer actually manages: it says whether the shop
                    is buying and pricing well, which is a different question
                    from whether it is spending well. */}
                <Total label={t('acct.grossProfit')} amount={r.gross_profit} currency={r.base_currency} strong />
                <Group
                  title={t('acct.expenses')}
                  lines={r.expenses}
                  total={r.expenses_total}
                  currency={r.base_currency}
                />
                <Total label={t('acct.netProfit')} amount={r.net_profit} currency={r.base_currency} strong />
              </tbody>
            </table>
          </div>
        )}
      </RemoteBody>
    </section>
  );
}

function Balance({ companyId, asOf }: { companyId: string; asOf: string }) {
  const { client } = useAuth();
  const t = useT();
  const load = useCallback(
    () => balanceSheet(client, companyId, asOf),
    [client, companyId, asOf],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('acct.balanceSheet')}>
      <RemoteBody remote={remote} onRetry={reload}>
        {(r: BalanceSheet) => (
          <>
            {!r.balanced && <OutOfBalance difference={r.difference} currency={r.base_currency} />}
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table acct__statement">
                <tbody>
                  <Group
                    title={t('acct.assets')}
                    lines={r.assets}
                    total={r.assets_total}
                    currency={r.base_currency}
                  />
                  <Group
                    title={t('acct.liabilities')}
                    lines={r.liabilities}
                    total={r.liabilities_total}
                    currency={r.base_currency}
                  />
                  <Group
                    title={t('acct.equity')}
                    lines={r.equity}
                    total={r.equity_total}
                    currency={r.base_currency}
                  />
                  {/* Profit earned since the books began that has not been
                      closed into retained earnings. Without it the sheet is
                      short by exactly the year's profit on every day of the
                      year, which is the commonest reason a balance sheet
                      appears not to balance. */}
                  <Total label={t('acct.currentEarnings')} amount={r.current_earnings} currency={r.base_currency} />
                  <Total
                    label={t('acct.equityAndLiabilities')}
                    amount={r.equity_and_liabilities}
                    currency={r.base_currency}
                    strong
                  />
                </tbody>
              </table>
            </div>
          </>
        )}
      </RemoteBody>
    </section>
  );
}

function Cash({ companyId, period }: { companyId: string; period: Range }) {
  const { client } = useAuth();
  const t = useT();
  const load = useCallback(
    () => cashFlow(client, companyId, period.from, period.to),
    [client, companyId, period.from, period.to],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('acct.cashFlow')}>
      <RemoteBody remote={remote} onRetry={reload}>
        {(r: CashFlow) => (
          <>
            <div className="ds-panel__body">
              {/* Said on the face of the statement so nobody mistakes it for an
                  IAS 7 indirect one. The indirect method needs every account
                  classified as operating, investing or financing, and this
                  chart does not carry that classification. */}
              <p className="ds-body-sm" role="note">
                {t('acct.directMethod')}
              </p>
            </div>
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table acct__statement">
                <tbody>
                  <Total label={t('acct.opening')} amount={r.opening} currency={r.base_currency} />
                  <Group
                    title={t('acct.moneyIn')}
                    lines={r.in.map(asLine)}
                    total={undefined}
                    currency={r.base_currency}
                  />
                  <Group
                    title={t('acct.moneyOut')}
                    lines={r.out.map(asLine)}
                    total={undefined}
                    currency={r.base_currency}
                  />
                  <Total label={t('acct.netMovement')} amount={r.net_total} currency={r.base_currency} />
                  <Total label={t('acct.closing')} amount={r.closing} currency={r.base_currency} strong />
                </tbody>
              </table>
            </div>
          </>
        )}
      </RemoteBody>
    </section>
  );
}

function Trial({ companyId, asOf }: { companyId: string; asOf: string }) {
  const { client } = useAuth();
  const t = useT();
  const load = useCallback(
    () => trialBalance(client, companyId, asOf),
    [client, companyId, asOf],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('acct.trialBalance')}>
      <RemoteBody remote={remote} onRetry={reload}>
        {(r: TrialBalance) => (
          <>
            {!r.balanced && <OutOfBalance difference={r.difference} currency={r.base_currency} />}
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('acct.account')}</th>
                    <th scope="col" className="num">
                      {t('acct.debit')}
                    </th>
                    <th scope="col" className="num">
                      {t('acct.credit')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {r.rows.map((row) => (
                    <tr key={row.account_id}>
                      <td>
                        <span className="detail__strong">{row.name}</span>
                        <span className="ds-caption">{row.code}</span>
                      </td>
                      <td className="num">{money(row.debit, { currency: r.base_currency })}</td>
                      <td className="num">{money(row.credit, { currency: r.base_currency })}</td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <th scope="row">{t('acct.total')}</th>
                    <td className="num">{money(r.total_debit, { currency: r.base_currency })}</td>
                    <td className="num">{money(r.total_credit, { currency: r.base_currency })}</td>
                  </tr>
                </tfoot>
              </table>
            </div>
          </>
        )}
      </RemoteBody>
    </section>
  );
}

/** A statement that does not add up, said in words above the figures.
 *
 *  The alternative — a red number somewhere in a table — is exactly what a
 *  person scanning a familiar report does not see. */
function OutOfBalance({
  difference,
  currency,
}: {
  difference: string;
  currency: string;
}) {
  const t = useT();
  if (!isOff(difference)) return null;
  return (
    <div className="ds-panel__body">
      <p className="acct__outofbalance" role="alert">
        {t('acct.outOfBalance', { amount: money(difference, { currency }) })}
      </p>
    </div>
  );
}

function Group({
  title,
  lines,
  total,
  currency,
}: {
  title: string;
  lines: StatementLine[];
  total?: string;
  currency: string;
}) {
  const { locale } = useLocale();
  if (lines.length === 0 && !total) return null;
  return (
    <>
      <tr className="acct__group">
        <th scope="rowgroup" colSpan={2}>
          {title}
        </th>
      </tr>
      {lines.map((l) => (
        <tr key={l.account_id + l.code}>
          <td>
            <span>{localName(locale, l.name, l.name_ar)}</span>
            <span className="ds-caption">{l.code}</span>
          </td>
          <td className="num">{money(l.amount, { currency })}</td>
        </tr>
      ))}
      {total !== undefined && (
        <tr className="acct__subtotal">
          <th scope="row">{title}</th>
          <td className="num">{money(total, { currency })}</td>
        </tr>
      )}
    </>
  );
}

function Total({
  label,
  amount,
  currency,
  strong,
}: {
  label: string;
  amount: string;
  currency: string;
  strong?: boolean;
}) {
  return (
    <tr className={strong ? 'acct__total acct__total--strong' : 'acct__total'}>
      <th scope="row">{label}</th>
      <td className="num">{money(amount, { currency })}</td>
    </tr>
  );
}

/** A cash-flow line has no account id; the code is unique enough within one
 *  statement to key a row by. */
function asLine(l: { code: string; name: string; amount: string }): StatementLine {
  return { account_id: l.code, code: l.code, name: l.name, amount: l.amount };
}
