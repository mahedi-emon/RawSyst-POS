// The Owner Dashboard.
//
// Blueprint A2 #10 states the job in one sentence: answer "where is my money
// going" in one click. Everything on this screen serves that, and anything that
// did not has been left off.
//
// # What it deliberately is not
//
// It is not a wall of tiles. Four figures answer the question an owner arrives
// with — what did I sell, what did I make on it, what did it cost me, what have
// I actually got — and everything else on the screen is context for those four.
// A dashboard showing twenty equally-weighted numbers has decided nothing on
// the reader's behalf, which is the work it exists to do.
//
// It carries no decorative chart. The sparkline exists because a day's takings
// mean nothing without the fortnight around them; the tender bars exist because
// the mix is a real operational fact with a fee attached to it. There is no
// third chart, because there was no third question.
//
// # Nothing here calculates
//
// Every figure arrives formatted-ready from the server, computed by the same
// posting engine the trial balance reads. That is not laziness: it means a
// number an owner disputes traces to journal entries rather than to browser
// code nobody can audit, and that the Arabic build and the English one cannot
// disagree about revenue.

import { useCallback, useEffect, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { fetchOverview, type Overview } from '../api/dashboard';
import { useAuth } from '../auth/session';
import { money, percent, direction, isZero, tenderName, shortDate } from '../ui/format';
import { Sparkline } from './Sparkline';
import { TenderMix } from './TenderMix';
import { AttentionList } from './AttentionList';

type Load =
  | { state: 'loading' }
  | { state: 'ready'; data: Overview }
  | { state: 'denied' }
  | { state: 'offline' }
  | { state: 'error'; message: string };

export function Dashboard({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const [load, setLoad] = useState<Load>({ state: 'loading' });
  const [date, setDate] = useState<string | undefined>(undefined);

  const reload = useCallback(async () => {
    setLoad({ state: 'loading' });
    try {
      setLoad({ state: 'ready', data: await fetchOverview(client, companyId, date) });
    } catch (err) {
      if (err instanceof Offline) {
        setLoad({ state: 'offline' });
      } else if (err instanceof RequestFailed && err.status === 403) {
        setLoad({ state: 'denied' });
      } else {
        setLoad({
          state: 'error',
          message: err instanceof Error ? err.message : 'That did not load.',
        });
      }
    }
  }, [client, companyId, date]);

  useEffect(() => {
    void reload();
  }, [reload]);

  if (load.state === 'loading') return <DashboardSkeleton />;
  if (load.state === 'denied') return <Denied />;
  if (load.state === 'offline') return <OfflineState onRetry={() => void reload()} />;
  if (load.state === 'error') {
    return <ErrorState message={load.message} onRetry={() => void reload()} />;
  }

  const d = load.data;
  const currency = d.base_currency;
  const nothingYet =
    isZero(d.sales.total) && isZero(d.profit.revenue) && d.sales.invoice_count === 0;

  return (
    <main className="dash">
      <header className="dash__head">
        <div>
          <h1 className="ds-h1">Today</h1>
          <p className="ds-caption">{d.date}</p>
        </div>

        {/* A date field, not a range picker. The question this screen answers
            is about one day; comparing periods is a report, and pretending
            otherwise here would make the tiles ambiguous. */}
        <label className="dash__date">
          <span className="ds-caption">Showing</span>
          <input
            type="date"
            value={date ?? d.date}
            max={d.date >= new Date().toISOString().slice(0, 10) ? undefined : undefined}
            onChange={(e) => setDate(e.target.value || undefined)}
          />
        </label>
      </header>

      {nothingYet ? (
        <NoTradingYet date={d.date} />
      ) : (
        <>
          <section className="dash__kpis" aria-label="Today at a glance">
            <Kpi
              label="Sales"
              value={money(d.sales.total, { currency })}
              foot={
                d.sales.change_pct === null ? (
                  <span className="ds-subtle">
                    {d.sales.invoice_count} sale{d.sales.invoice_count === 1 ? '' : 's'}
                  </span>
                ) : (
                  <Change pct={d.sales.change_pct} suffix="vs yesterday" />
                )
              }
              chart={<Sparkline points={d.sales.trend} />}
            />

            <Kpi
              label="Gross profit"
              value={money(d.profit.gross, { currency })}
              foot={
                d.profit.margin_pct === null ? (
                  <span className="ds-subtle">No sales to measure against</span>
                ) : (
                  <span className="ds-muted">{percent(d.profit.margin_pct)} margin</span>
                )
              }
            />

            <Kpi
              label="Expenses"
              value={money(d.expenses.total, { currency })}
              foot={
                d.expenses.by_account.length === 0 ? (
                  <span className="ds-subtle">Nothing posted today</span>
                ) : (
                  <span className="ds-muted">
                    across {d.expenses.by_account.length} account
                    {d.expenses.by_account.length === 1 ? '' : 's'}
                  </span>
                )
              }
            />

            <Kpi
              label="Cash and bank"
              value={money(d.money.total, { currency })}
              foot={
                isZero(d.money.unsettled) ? (
                  <span className="ds-subtle">All settled</span>
                ) : (
                  // C12. An owner counting the drawer and the bank would
                  // otherwise conclude a day of card takings had vanished.
                  <span className="ds-muted">
                    {money(d.money.unsettled, { currency })} not yet settled
                  </span>
                )
              }
            />
          </section>

          <div className="dash__grid">
            <TenderMix tenders={d.tenders} currency={currency} />

            <section className="ds-panel" aria-label="Where the money is">
              <div className="ds-panel__head">
                <h2 className="ds-h3">Where the money is</h2>
              </div>
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <tbody>
                    <MoneyRow label="Cash" amount={d.money.cash} currency={currency} />
                    <MoneyRow label="Bank" amount={d.money.bank} currency={currency} />
                    <MoneyRow
                      label="With the card processor"
                      note="Taken, not yet paid out"
                      amount={d.money.unsettled}
                      currency={currency}
                    />
                    <MoneyRow
                      label="Owed by customers"
                      note="Not counted in the total"
                      amount={d.money.receivable}
                      currency={currency}
                      excluded
                    />
                    <MoneyRow
                      label="Store credit held by customers"
                      note="Not counted in the total"
                      amount={d.money.store_credit}
                      currency={currency}
                      excluded
                    />
                  </tbody>
                  <tfoot>
                    <tr>
                      <td>Available</td>
                      <td className="num">{money(d.money.total, { currency })}</td>
                    </tr>
                  </tfoot>
                </table>
              </div>
            </section>

            <section className="ds-panel" aria-label="Stock">
              <div className="ds-panel__head">
                <h2 className="ds-h3">Stock</h2>
                <span className="ds-caption">at cost</span>
              </div>
              <div className="ds-panel__body">
                <p className="dash__figure num">
                  {money(d.inventory.value, { currency })}
                </p>
                <p className="ds-body-sm ds-muted">
                  across {d.inventory.variant_count} item
                  {d.inventory.variant_count === 1 ? '' : 's'}
                </p>
                {(d.inventory.out_of_stock > 0 || d.inventory.low_stock > 0) && (
                  <p className="dash__stockline ds-body-sm">
                    {d.inventory.out_of_stock > 0 && (
                      <span className="ds-badge ds-badge--danger">
                        {d.inventory.out_of_stock} out of stock
                      </span>
                    )}
                    {d.inventory.low_stock > 0 && (
                      <span className="ds-badge ds-badge--warning">
                        {d.inventory.low_stock} low
                      </span>
                    )}
                  </p>
                )}
              </div>
            </section>

            <ExpenseBreakdown
              lines={d.expenses.by_account}
              total={d.expenses.total}
              currency={currency}
            />
          </div>

          <AttentionList items={d.attention} />
        </>
      )}

      <NotBuiltYet modules={d.unbuilt} />
    </main>
  );
}

/** One headline figure.
 *
 * The label is small and the number is large, because the reader is scanning
 * for the number and already knows what they came to look at. */
function Kpi({
  label,
  value,
  foot,
  chart,
}: {
  label: string;
  value: string;
  foot: React.ReactNode;
  chart?: React.ReactNode;
}) {
  return (
    <div className="kpi">
      <span className="kpi__label ds-caption">{label}</span>
      <span className="kpi__value num">{value}</span>
      <span className="kpi__foot ds-body-sm">{foot}</span>
      {chart && <div className="kpi__chart">{chart}</div>}
    </div>
  );
}

/** A change against yesterday, with an arrow so colour is never the only
 *  signal — roughly 1 in 12 men has a colour vision deficiency. */
function Change({ pct, suffix }: { pct: string; suffix: string }) {
  const way = direction(pct);
  const arrow = way === 'up' ? '▲' : way === 'down' ? '▼' : '■';
  const tone = way === 'up' ? 'ds-up' : way === 'down' ? 'ds-down' : 'ds-muted';
  return (
    <span className={tone}>
      <span aria-hidden="true">{arrow}</span> {percent(pct)}{' '}
      <span className="ds-muted">{suffix}</span>
    </span>
  );
}

function MoneyRow({
  label,
  note,
  amount,
  currency,
  excluded,
}: {
  label: string;
  note?: string;
  amount: string;
  currency: string;
  excluded?: boolean;
}) {
  return (
    <tr className={excluded ? 'dash__row--aside' : undefined}>
      <td>
        {label}
        {note && <span className="dash__note ds-caption">{note}</span>}
      </td>
      <td className="num">{money(amount, { currency })}</td>
    </tr>
  );
}

function ExpenseBreakdown({
  lines,
  total,
  currency,
}: {
  lines: Overview['expenses']['by_account'];
  total: string;
  currency: string;
}) {
  return (
    <section className="ds-panel" aria-label="Expenses today">
      <div className="ds-panel__head">
        <h2 className="ds-h3">Expenses today</h2>
      </div>
      <div className="ds-panel__body ds-scroll-x">
        {lines.length === 0 ? (
          <div className="ds-state">
            <p className="ds-state__title">Nothing posted today</p>
            <p className="ds-state__body">
              Expenses appear here as they are recorded against the books.
            </p>
          </div>
        ) : (
          <table className="ds-table">
            <thead>
              <tr>
                <th scope="col">Account</th>
                <th scope="col" className="num">Amount</th>
              </tr>
            </thead>
            <tbody>
              {lines.map((line) => (
                <tr key={line.account_id}>
                  <td>
                    {line.name}
                    <span className="dash__note ds-caption">{line.code}</span>
                  </td>
                  <td className="num">{money(line.amount, { currency })}</td>
                </tr>
              ))}
            </tbody>
            <tfoot>
              <tr>
                <td>Total</td>
                <td className="num">{money(total, { currency })}</td>
              </tr>
            </tfoot>
          </table>
        )}
      </div>
    </section>
  );
}

/** The honest statement about Phase 2 and 3.
 *
 * Named rather than hidden. An owner who cannot find payables should be told it
 * is coming, not left wondering whether they have looked in the wrong place —
 * and must never be shown a zero they would read as "I owe nobody". */
function NotBuiltYet({ modules }: { modules: string[] }) {
  if (modules.length === 0) return null;
  const names: Record<string, string> = {
    purchases: 'Purchases',
    suppliers: 'Suppliers',
    payables: 'Payables',
    customers: 'Customers',
    employees: 'Employees',
  };
  return (
    <section className="dash__soon" aria-label="Not available yet">
      <h2 className="ds-caption">Coming in a later release</h2>
      <p className="ds-body-sm ds-muted">
        {modules.map((m) => names[m] ?? m).join(' · ')} are not part of this
        release. They are absent rather than empty — nothing here is showing you
        a zero for them.
      </p>
    </section>
  );
}

function NoTradingYet({ date }: { date: string }) {
  return (
    <div className="ds-panel">
      <div className="ds-state">
        <p className="ds-state__title">No sales on {shortDate(date)}</p>
        <p className="ds-state__body">
          Once the till starts ringing up, today's takings, profit and payment
          mix appear here. Nothing is wrong.
        </p>
      </div>
    </div>
  );
}

/** The loading state mirrors the real layout.
 *
 * Deliberately the same shape as the loaded screen, so the page does not jump
 * when data arrives — a dashboard that reflows under the reader is the fastest
 * way to make a product feel unreliable. */
function DashboardSkeleton() {
  return (
    <main className="dash" aria-busy="true" aria-label="Loading">
      <header className="dash__head">
        <div className="ds-skeleton" style={{ inlineSize: 120, blockSize: 28 }} />
      </header>
      <section className="dash__kpis">
        {[0, 1, 2, 3].map((i) => (
          <div className="kpi" key={i}>
            <div className="ds-skeleton" style={{ inlineSize: 72, blockSize: 12 }} />
            <div
              className="ds-skeleton"
              style={{ inlineSize: '70%', blockSize: 30, marginBlockStart: 10 }}
            />
            <div
              className="ds-skeleton"
              style={{ inlineSize: '45%', blockSize: 12, marginBlockStart: 10 }}
            />
          </div>
        ))}
      </section>
      <div className="dash__grid">
        {[0, 1].map((i) => (
          <div className="ds-panel" key={i}>
            <div className="ds-panel__body">
              <div className="ds-skeleton" style={{ blockSize: 140 }} />
            </div>
          </div>
        ))}
      </div>
    </main>
  );
}

function Denied() {
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">You do not have access to the figures</p>
          <p className="ds-state__body">
            The dashboard shows revenue, margin and cash position. Your role does
            not include permission to view the accounts. An owner can change that
            under Settings &gt; People.
          </p>
        </div>
      </div>
    </main>
  );
}

/** Offline is a neutral operating state, never an error.
 *
 * Red is reserved for things that are actually wrong; a dashboard that cannot
 * reach the server is a dashboard waiting, and colouring it red teaches owners
 * to ignore red. */
function OfflineState({ onRetry }: { onRetry: () => void }) {
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">No connection to the server</p>
          <p className="ds-state__body">
            These figures are calculated from your books on the server, so they
            need a connection. Selling is unaffected — the till keeps working
            offline and sends its sales when the connection returns.
          </p>
          <button className="ds-btn ds-btn--secondary" onClick={onRetry}>
            Try again
          </button>
        </div>
      </div>
    </main>
  );
}

function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">That did not load</p>
          <p className="ds-state__body">{message}</p>
          <button className="ds-btn ds-btn--secondary" onClick={onRetry}>
            Try again
          </button>
        </div>
      </div>
    </main>
  );
}

export { tenderName };
