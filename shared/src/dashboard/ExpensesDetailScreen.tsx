// The postings behind the expenses figure.
//
// Two levels, on one screen. The summary by account is what the tile showed;
// clicking an account narrows the entries below it. Putting both on one screen
// rather than two means an owner comparing accounts never loses the comparison
// by drilling into one of them.

import { useCallback, useState } from 'react';

import { fetchExpenses } from '../api/drilldown';
import { useAuth } from '../auth/session';
import { localName, money, shortDate } from '../ui/format';
import { DetailScreen, EmptyState, RemoteBody } from './DetailScreen';
import { useRemote } from './useRemote';
import { useLocale, useT } from '../i18n/locale';

export function ExpensesDetailScreen({
  companyId,
  date,
  onBack,
}: {
  companyId: string;
  date: string;
  onBack: () => void;
}) {
  const t = useT();
  const { locale } = useLocale();
  const { client } = useAuth();
  const [accountId, setAccountId] = useState<string | undefined>(undefined);

  const load = useCallback(
    () => fetchExpenses(client, companyId, date, accountId),
    [client, companyId, date, accountId],
  );
  const { remote, reload, refreshing } = useRemote(load);

  return (
    <DetailScreen
      title={t('common.expenses')}
      subtitle={shortDate(date, locale)}
      onBack={onBack}
      onRefresh={reload}
      refreshing={refreshing}
      actions={
        accountId && (
          <button
            className="ds-btn ds-btn--secondary"
            onClick={() => setAccountId(undefined)}
          >
            {t('dash.showAllAccounts')}
          </button>
        )
      }
    >
      <RemoteBody remote={remote} onRetry={reload}>
        {(d) =>
          d.by_account.length === 0 && d.entries.length === 0 ? (
            <div className="ds-panel">
              <EmptyState
                title={t('expenses.nonePostedOn', { date: shortDate(date, locale) })}
                body={
                  accountId
                    ? t('expenses.nothingPosted')
                    : 'Expenses appear here as they are recorded against the books. Cost of sales is counted under profit, not here.'
                }
              />
            </div>
          ) : (
            <div className="detail__split">
              <section className="ds-panel" aria-label={t('dash.byAccount')}>
                <div className="ds-panel__head">
                  <h2 className="ds-h3">{t('dash.byAccount')}</h2>
                </div>
                <div className="ds-panel__body ds-scroll-x">
                  <table className="ds-table ds-table--pairs">
                    <tbody>
                      {d.by_account.map((line) => {
                        const active = line.account_id === accountId;
                        return (
                          <tr
                            key={line.account_id}
                            className={active ? 'detail__row--on' : undefined}
                          >
                            <td>
                              {/* The drill-through within the drill-through.
                                  A row, not a button, because the whole row is
                                  the target — a small link inside a wide row is
                                  a miss waiting to happen on a tablet. */}
                              <button
                                className="detail__rowbtn"
                                aria-pressed={active}
                                onClick={() =>
                                  setAccountId(
                                    active ? undefined : line.account_id,
                                  )
                                }
                              >
                                <span className="detail__strong">
                                  {localName(locale, line.name, line.name_ar)}
                                </span>
                                <span className="ds-caption">{line.code}</span>
                              </button>
                            </td>
                            <td className="num">
                              {money(line.amount, {
                                currency: d.base_currency,
                              })}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                    <tfoot>
                      <tr>
                        <td>
                          {accountId ? t('expenses.thisAccount') : 'Total'}
                        </td>
                        <td className="num">
                          {money(d.total, { currency: d.base_currency })}
                        </td>
                      </tr>
                    </tfoot>
                  </table>
                </div>
              </section>

              <section className="ds-panel" aria-label={t('common.entries')}>
                <div className="ds-panel__head">
                  <h2 className="ds-h3">{t('common.entries')}</h2>
                  <span className="ds-caption">
                    {d.entries.length} posting
                    {d.entries.length === 1 ? '' : 's'}
                  </span>
                </div>
                <div className="ds-panel__body ds-scroll-x">
                  {d.entries.length === 0 ? (
                    <EmptyState
                      title={t('dash.nothingPostedAccount')}
                      body={t('expenses.chooseAnother')}
                    />
                  ) : (
                    <table className="ds-table">
                      <thead>
                        <tr>
                          <th scope="col">{t('common.entry')}</th>
                          <th scope="col">{t('common.account')}</th>
                          <th scope="col">{t('common.description')}</th>
                          <th scope="col" className="num">
                            {t('common.amount')}
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {d.entries.map((entry) => (
                          <tr key={`${entry.entry_id}-${entry.account_id}`}>
                            <td className="num">{entry.entry_no || '—'}</td>
                            <td>
                              {entry.account}
                              <span className="ds-caption">{entry.code}</span>
                            </td>
                            <td>
                              {entry.memo || (
                                <span className="ds-subtle">
                                  {t('dash.noDescription')}
                                </span>
                              )}
                              {entry.source_type && (
                                <span className="ds-caption">
                                  from {entry.source_type.replace(/_/g, ' ')}
                                </span>
                              )}
                            </td>
                            <td className="num">
                              {money(entry.amount, {
                                currency: d.base_currency,
                              })}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              </section>
            </div>
          )
        }
      </RemoteBody>
    </DetailScreen>
  );
}
