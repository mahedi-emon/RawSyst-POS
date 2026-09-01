// Multi-company groups and consolidated statements (blueprint F4).
//
// # The arithmetic is shown, not asserted
//
// Each company's share, what elimination removed, and whether any member keeps
// its books in a different currency are all on the face of the statement. A
// consolidated figure a reader has to trust is a figure they will re-derive in
// a spreadsheet, which is the outcome this screen exists to avoid.
//
// # A group balance sheet at partial ownership does not balance
//
// And correctly so: the missing side is the minority interest this product does
// not compute. The screen says that in words rather than showing a red mark
// that reads as a bug.

import { useCallback, useState } from 'react';

import {
  addGroupMember,
  groupStatement,
  listGroups,
  removeGroup,
  removeGroupMember,
  saveGroup,
  type CompanyGroup,
  type ConsolidatedStatement,
} from '../api/billing';
import { listCompanies, type Company } from '../api/companies';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import { FormError } from '../ui/Form';
import { LabelledSelect, LabelledText } from '../governance/fields';
import { money, shortDate } from '../ui/format';

export function GroupsArea({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const mayManage = can('group.manage');

  const load = useCallback(
    () => listGroups(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const companyLoad = useCallback(() => listCompanies(client), [client]);
  const companies = useRemote(companyLoad);

  const [name, setName] = useState('');
  const [currency, setCurrency] = useState('SAR');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [chosen, setChosen] = useState<string | null>(null);

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  const allCompanies: Company[] =
    companies.remote.state === 'ready' ? companies.remote.data : [];

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('grp.title')}</h1>
          <p className="ds-caption">{t('grp.intro')}</p>
        </div>
      </header>

      <section className="ds-panel" aria-label={t('grp.groups')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('grp.groups')}</h2>
            <p className="ds-caption">{t('grp.groupsHint')}</p>
          </div>
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
          {mayManage && (
            <div className="pri__form">
              <LabelledText
                id="grp-name"
                label={t('grp.groupName')}
                value={name}
                onChange={setName}
              />
              <LabelledText
                id="grp-currency"
                label={t('grp.presentationCurrency')}
                hint={t('grp.presentationHint')}
                value={currency}
                onChange={setCurrency}
              />
              <div className="form__actions">
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy || name.trim() === ''}
                  onClick={() =>
                    void run(async () => {
                      await saveGroup(client, companyId, {
                        name,
                        presentation_currency: currency,
                      });
                      setName('');
                    })
                  }
                >
                  {t('grp.createGroup')}
                </button>
              </div>
            </div>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: CompanyGroup[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('grp.noneTitle')}
                  body={t('grp.noneBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body">
                {payload.data.map((g) => (
                  <article className="grp__card" key={g.id}>
                    <div className="grp__cardHead">
                      <div>
                        <h3 className="ds-h3">{g.name}</h3>
                        <p className="ds-caption">
                          {t('grp.presentedIn', {
                            currency: g.presentation_currency,
                          })}
                        </p>
                      </div>
                      <div className="grp__cardActions">
                        <button
                          className="ds-btn ds-btn--quiet ds-btn--sm"
                          onClick={() =>
                            setChosen((c) => (c === g.id ? null : g.id))
                          }
                        >
                          {t('grp.statements')}
                        </button>
                        {mayManage && (
                          <button
                            className="ds-btn ds-btn--quiet ds-btn--sm"
                            disabled={busy}
                            onClick={() =>
                              void run(() =>
                                removeGroup(client, companyId, g.id),
                              )
                            }
                          >
                            {t('action.remove')}
                          </button>
                        )}
                      </div>
                    </div>

                    <table className="ds-table">
                      <thead>
                        <tr>
                          <th scope="col">{t('grp.company')}</th>
                          <th scope="col">{t('grp.books')}</th>
                          <th scope="col">{t('grp.holding')}</th>
                          <th scope="col">
                            <span className="ds-visually-hidden">
                              {t('common.actions')}
                            </span>
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {g.members.map((m) => (
                          <tr key={m.company_id}>
                            <td>
                              {m.name}
                              {m.is_parent && (
                                <span className="ds-badge ds-badge--success">
                                  {t('grp.parent')}
                                </span>
                              )}
                            </td>
                            <td>{m.base_currency}</td>
                            <td className="num">{m.ownership_pct}</td>
                            <td className="ds-table__actions">
                              {mayManage && (
                                <button
                                  className="ds-btn ds-btn--quiet ds-btn--sm"
                                  disabled={busy}
                                  onClick={() =>
                                    void run(() =>
                                      removeGroupMember(
                                        client,
                                        companyId,
                                        g.id,
                                        m.company_id,
                                      ),
                                    )
                                  }
                                >
                                  {t('action.remove')}
                                </button>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>

                    {mayManage && allCompanies.length > 0 && (
                      <AddMember
                        companies={allCompanies.filter(
                          (c) =>
                            !g.members.some((m) => m.company_id === c.id),
                        )}
                        busy={busy}
                        onAdd={(id, pct, parent) =>
                          void run(() =>
                            addGroupMember(client, companyId, g.id, {
                              company_id: id,
                              ownership_pct: pct,
                              is_parent: parent,
                            }),
                          )
                        }
                      />
                    )}

                    {chosen === g.id && (
                      <Statements companyId={companyId} group={g} />
                    )}
                  </article>
                ))}
              </div>
            )
          }
        </RemoteBody>
      </section>
    </main>
  );
}

function AddMember({
  companies,
  busy,
  onAdd,
}: {
  companies: Company[];
  busy: boolean;
  onAdd: (companyId: string, pct: string, parent: boolean) => void;
}) {
  const t = useT();
  const [id, setId] = useState('');
  const [pct, setPct] = useState('100');
  const [parent, setParent] = useState(false);

  if (companies.length === 0) return null;

  return (
    <div className="pri__form">
      <LabelledSelect
        id="grp-add"
        label={t('grp.addCompany')}
        value={id}
        onChange={setId}
        options={[
          { value: '', label: t('grp.chooseCompany') },
          ...companies.map((c) => ({ value: c.id, label: c.legal_name })),
        ]}
      />
      <LabelledText
        id="grp-pct"
        label={t('grp.holding')}
        hint={t('grp.holdingHint')}
        value={pct}
        onChange={setPct}
        inputMode="decimal"
      />
      <label className="ds-check">
        <input
          type="checkbox"
          checked={parent}
          onChange={(e) => setParent(e.target.checked)}
        />
        {t('grp.isParent')}
      </label>
      <div className="form__actions">
        <button
          className="ds-btn ds-btn--primary"
          disabled={busy || id === ''}
          onClick={() => onAdd(id, pct, parent)}
        >
          {t('grp.add')}
        </button>
      </div>
    </div>
  );
}

function Statements({
  companyId,
  group,
}: {
  companyId: string;
  group: CompanyGroup;
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const today = new Date().toISOString().slice(0, 10);
  const yearStart = `${today.slice(0, 4)}-01-01`;

  const [kind, setKind] = useState<'profit_and_loss' | 'balance_sheet'>(
    'profit_and_loss',
  );
  const [from, setFrom] = useState(yearStart);
  const [to, setTo] = useState(today);

  const load = useCallback(
    () =>
      groupStatement(client, companyId, group.id, {
        statement: kind,
        from,
        to,
      }),
    [client, companyId, group.id, kind, from, to],
  );
  const { remote, reload } = useRemote(load);

  return (
    <div className="grp__statement">
      <div className="grp__controls">
        <div
          className="segmented"
          role="group"
          aria-label={t('common.whatToShow')}
        >
          <button
            className={`segmented__btn${kind === 'profit_and_loss' ? ' segmented__btn--on' : ''}`}
            aria-pressed={kind === 'profit_and_loss'}
            onClick={() => setKind('profit_and_loss')}
          >
            {t('grp.profitAndLoss')}
          </button>
          <button
            className={`segmented__btn${kind === 'balance_sheet' ? ' segmented__btn--on' : ''}`}
            aria-pressed={kind === 'balance_sheet'}
            onClick={() => setKind('balance_sheet')}
          >
            {t('grp.balanceSheet')}
          </button>
        </div>
        {kind === 'profit_and_loss' && (
          <LabelledText
            id="grp-from"
            label={t('grp.from')}
            value={from}
            onChange={setFrom}
            type="date"
          />
        )}
        <LabelledText
          id="grp-to"
          label={t('grp.to')}
          value={to}
          onChange={setTo}
          type="date"
        />
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { statement: ConsolidatedStatement }) => {
          const s = payload.statement;
          const cur = s.presentation_currency;
          return (
            <>
              {!s.comparable && (
                <p className="grp__caveat" role="status">
                  {t('grp.notComparable')}
                </p>
              )}

              <dl className="grp__totals">
                {kind === 'profit_and_loss' ? (
                  <>
                    <Total
                      label={t('grp.revenue')}
                      value={money(s.revenue ?? '0', { currency: cur })}
                    />
                    <Total
                      label={t('grp.costOfSales')}
                      value={money(s.cost_of_sales ?? '0', { currency: cur })}
                    />
                    <Total
                      label={t('grp.grossProfit')}
                      value={money(s.gross_profit ?? '0', { currency: cur })}
                    />
                    <Total
                      label={t('grp.expenses')}
                      value={money(s.expenses ?? '0', { currency: cur })}
                    />
                    <Total
                      label={t('grp.netProfit')}
                      value={money(s.net_profit ?? '0', { currency: cur })}
                      strong
                    />
                  </>
                ) : (
                  <>
                    <Total
                      label={t('grp.assets')}
                      value={money(s.assets ?? '0', { currency: cur })}
                    />
                    <Total
                      label={t('grp.liabilities')}
                      value={money(s.liabilities ?? '0', { currency: cur })}
                    />
                    <Total
                      label={t('grp.equity')}
                      value={money(s.equity ?? '0', { currency: cur })}
                      strong
                    />
                  </>
                )}
              </dl>

              {kind === 'balance_sheet' && s.balanced === false && (
                <p className="grp__caveat" role="status">
                  {t('grp.minorityInterest')}
                </p>
              )}

              <p className="ds-caption">
                {t('grp.eliminated', {
                  n: String(s.eliminated_entries),
                  amount: money(s.eliminated_amount, { currency: cur }),
                })}
              </p>

              <div className="ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('grp.account')}</th>
                      {s.companies.map((c) => (
                        <th scope="col" key={c.company_id}>
                          {c.name}
                          {c.currency_differs && (
                            <span className="ds-badge ds-badge--warning">
                              {c.base_currency}
                            </span>
                          )}
                        </th>
                      ))}
                      <th scope="col">{t('grp.group')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {s.lines.map((l) => (
                      <tr key={l.code}>
                        <td>
                          {l.code} {l.name}
                        </td>
                        {s.companies.map((c) => (
                          <td className="num" key={c.company_id}>
                            {money(l.by_company[c.company_id] ?? '0', {
                              currency: cur,
                            })}
                          </td>
                        ))}
                        <td className="num">
                          {money(l.amount, { currency: cur })}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <p className="ds-caption">
                {t('grp.asOf', { when: shortDate(s.to, locale) })}
              </p>
            </>
          );
        }}
      </RemoteBody>
    </div>
  );
}

function Total({
  label,
  value,
  strong,
}: {
  label: string;
  value: string;
  strong?: boolean;
}) {
  return (
    <div className={`grp__total${strong ? ' grp__total--strong' : ''}`}>
      <dt>{label}</dt>
      <dd className="num">{value}</dd>
    </div>
  );
}
