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
import { useT } from '../i18n/locale';

type Load =
  | { state: 'loading' }
  | { state: 'ready'; data: Overview }
  | { state: 'denied' }
  | { state: 'offline' }
  | { state: 'error'; message: string };

/** Where a widget drills through to. A8 requires every one of them to open. */
export type DrillTarget =
  | { screen: 'sales'; date: string }
  | { screen: 'expenses'; date: string }
  | { screen: 'compliance' }
  | { screen: 'stock'; filter: 'low' | 'out' }
  // UI spec §5. Reached from a row in the sales drill-through rather than from
  // the dashboard itself: nobody opens an invoice without first knowing which
  // one, and the day's list is where they find out.
  | { screen: 'invoice'; invoiceId: string };

export function Dashboard({
  companyId,
  onOpen,
}: {
  companyId: string;
  onOpen: (target: DrillTarget) => void;
}) {
  const t = useT();
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
          message: err instanceof Error ? err.message : t('dash.didNotLoad'),
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
          <h1 className="ds-h1">{t('dash.today')}</h1>
          <p className="ds-caption">{d.date}</p>
        </div>

        {/* A date field, not a range picker. The question this screen answers
            is about one day; comparing periods is a report, and pretending
            otherwise here would make the tiles ambiguous. */}
        <label className="dash__date">
          <span className="ds-caption">{t('common.showing')}</span>
          <input
            type="date"
            value={date ?? d.date}
            max={d.date >= new Date().toISOString().slice(0, 10) ? undefined : undefined}
            onChange={(e) => setDate(e.target.value || undefined)}
          />
        </label>
      </header>

      {/* A quiet day replaces the FIGURES, not the screen.
       *
       * It used to replace everything, which meant a shop that had bought
       * stock and not yet sold any saw "no sales today" and none of its money
       * position — including what it now owes for the delivery. A browser check
       * caught it. Cash, stock and what needs attention are all meaningful
       * before the first sale of the day. */}
      {nothingYet && <NoTradingYet date={d.date} />}

      {!nothingYet && (
        <>
          <section className="dash__kpis" aria-label={t('dash.todayAtAGlance')}>
            <Kpi
              label={t('common.sales')}
              value={money(d.sales.total, { currency })}
              onOpen={() => onOpen({ screen: 'sales', date: d.date })}
              opens="the invoices behind today's sales"
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
              label={t('dash.grossProfit')}
              value={money(d.profit.gross, { currency })}
              onOpen={() => onOpen({ screen: 'sales', date: d.date })}
              opens="the sales this profit came from"
              foot={
                d.profit.margin_pct === null ? (
                  <span className="ds-subtle">{t('dash.noSalesToMeasure')}</span>
                ) : (
                  <span className="ds-muted">{percent(d.profit.margin_pct)} margin</span>
                )
              }
            />

            <Kpi
              label={t('common.expenses')}
              value={money(d.expenses.total, { currency })}
              onOpen={() => onOpen({ screen: 'expenses', date: d.date })}
              opens="the postings behind today's expenses"
              foot={
                d.expenses.by_account.length === 0 ? (
                  <span className="ds-subtle">{t('dash.nothingPostedToday')}</span>
                ) : (
                  <span className="ds-muted">
                    {t('dash.acrossAccounts').replace(
                      '{n}',
                      String(d.expenses.by_account.length),
                    )}
                  </span>
                )
              }
            />

            <Kpi
              label={t('dash.cashAndBank')}
              value={money(d.money.total, { currency })}
              foot={
                isZero(d.money.unsettled) ? (
                  <span className="ds-subtle">{t('dash.allSettled')}</span>
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
        </>
      )}

      <>
          <div className="dash__grid">
            <TenderMix tenders={d.tenders} currency={currency} />

            <section className="ds-panel" aria-label={t('dash.whereTheMoneyIs')}>
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('dash.whereTheMoneyIs')}</h2>
              </div>
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <tbody>
                    <MoneyRow label={t('common.cash')} amount={d.money.cash} currency={currency} />
                    <MoneyRow label={t('common.bank')} amount={d.money.bank} currency={currency} />
                    <MoneyRow
                      label={t('dash.withCardProcessor')}
                      note={t('dash.takenNotPaidOut')}
                      amount={d.money.unsettled}
                      currency={currency}
                    />
                    <MoneyRow
                      label={t('dash.owedByCustomers')}
                      note={t('dash.notInTotal')}
                      amount={d.money.receivable}
                      currency={currency}
                      excluded
                    />
                    <MoneyRow
                      label={t('dash.storeCreditHeld')}
                      note={t('dash.notInTotal')}
                      amount={d.money.store_credit}
                      currency={currency}
                      excluded
                    />
                    {/* Not counted either: it is not money the shop holds, it
                        is money the shop will owe once the invoice arrives.
                        Shown because an owner reading their payables without it
                        would think they owed less than they do. */}
                    <MoneyRow
                      label={t('dash.grniShort')}
                      note={t('dash.youWillOweThis')}
                      amount={d.money.accrued_purchases}
                      currency={currency}
                      excluded
                    />
                  </tbody>
                  <tfoot>
                    <tr>
                      <td>{t('common.available')}</td>
                      <td className="num">{money(d.money.total, { currency })}</td>
                    </tr>
                  </tfoot>
                </table>
              </div>
            </section>

            <section className="ds-panel" aria-label={t('common.stock')}>
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('common.stock')}</h2>
                <span className="ds-caption">{t('common.atCost')}</span>
              </div>
              <div className="ds-panel__body">
                <p className="dash__figure num">
                  {money(d.inventory.value, { currency })}
                </p>
                <p className="ds-body-sm ds-muted">
                  {t('dash.acrossItems').replace(
                    '{n}',
                    String(d.inventory.variant_count),
                  )}
                </p>
                {(d.inventory.out_of_stock > 0 || d.inventory.low_stock > 0) && (
                  <p className="dash__stockline ds-body-sm">
                    {d.inventory.out_of_stock > 0 && (
                      <button
                        className="ds-badge ds-badge--danger badge--opens"
                        onClick={() => onOpen({ screen: 'stock', filter: 'out' })}
                      >
                        {t('dash.nOutOfStock').replace('{n}', String(d.inventory.out_of_stock))}
                      </button>
                    )}
                    {d.inventory.low_stock > 0 && (
                      <button
                        className="ds-badge ds-badge--warning badge--opens"
                        onClick={() => onOpen({ screen: 'stock', filter: 'low' })}
                      >
                        {d.inventory.low_stock} low
                      </button>
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

          <AttentionList items={d.attention} onOpen={onOpen} />
      </>

      <NotBuiltYet modules={d.unbuilt} />
    </main>
  );
}

/** One headline figure.
 *
 * The label is small and the number is large, because the reader is scanning
 * for the number and already knows what they came to look at.
 *
 * A tile with somewhere to go is a button wrapping the whole tile, not a small
 * link tucked inside it. A8 promises one CLICK, and a 14-pixel "view details"
 * in the corner of a large target is a miss waiting to happen on a tablet. A
 * tile with nowhere to go stays inert rather than pretending — a control that
 * looks pressable and is not teaches people to stop pressing.
 */
function Kpi({
  label,
  value,
  foot,
  chart,
  onOpen,
  opens,
}: {
  label: string;
  value: string;
  foot: React.ReactNode;
  chart?: React.ReactNode;
  onOpen?: () => void;
  opens?: string;
}) {
  const body = (
    <>
      <span className="kpi__label ds-caption">{label}</span>
      <span className="kpi__value num">{value}</span>
      <span className="kpi__foot ds-body-sm">{foot}</span>
      {chart && <div className="kpi__chart">{chart}</div>}
    </>
  );

  if (!onOpen) return <div className="kpi">{body}</div>;

  return (
    <button
      className="kpi kpi--opens"
      onClick={onOpen}
      // The label says what opens, because "Sales SAR 48,290.00" read aloud
      // gives no clue that it is actionable.
      aria-label={opens ? `${label}. Open ${opens}` : `Open ${label}`}
    >
      {body}
      <span className="kpi__chev" aria-hidden="true">
        ›
      </span>
    </button>
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
  const t = useT();
  return (
    <section className="ds-panel" aria-label={t('dash.expensesToday')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('dash.expensesToday')}</h2>
      </div>
      <div className="ds-panel__body ds-scroll-x">
        {lines.length === 0 ? (
          <div className="ds-state">
            <p className="ds-state__title">{t('dash.nothingPostedToday')}</p>
            <p className="ds-state__body">
              {t('dash.expensesAppearHere')}
            </p>
          </div>
        ) : (
          <table className="ds-table">
            <thead>
              <tr>
                <th scope="col">{t('common.account')}</th>
                <th scope="col" className="num">{t('common.amount')}</th>
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
                <td>{t('common.total')}</td>
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
  const t = useT();
  if (modules.length === 0) return null;
  const names: Record<string, string> = {
    purchases: t('module.purchases'),
    suppliers: t('common.suppliers'),
    payables: t('module.payables'),
    customers: t('common.customers'),
    employees: t('module.employees'),
  };
  return (
    <section className="dash__soon" aria-label={t('dash.notAvailableYet')}>
      <h2 className="ds-caption">{t('dash.comingLater')}</h2>
      <p className="ds-body-sm ds-muted">
        {modules.map((m) => names[m] ?? m).join(' · ')}{' '}
        {t('dash.notInReleaseSuffix')}
      </p>
    </section>
  );
}

function NoTradingYet({ date }: { date: string }) {
  const t = useT();
  return (
    <div className="ds-panel">
      <div className="ds-state">
        <p className="ds-state__title">
          {t('dash.noSalesOn')} {shortDate(date)}
        </p>
        <p className="ds-state__body">{t('dash.tillNotStarted')}</p>
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
  const t = useT();
  return (
    <main className="dash" aria-busy="true" aria-label={t('common.loading')}>
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
  const t = useT();
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">{t('dash.noFiguresAccess')}</p>
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
  const t = useT();
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">{t('common.noConnection')}</p>
          <p className="ds-state__body">{t('dash.figuresNeedServer')}</p>
          <button className="ds-btn ds-btn--secondary" onClick={onRetry}>
            {t('common.tryAgain')}
          </button>
        </div>
      </div>
    </main>
  );
}

function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  const t = useT();
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">{t('common.didNotLoad')}</p>
          <p className="ds-state__body">{message}</p>
          <button className="ds-btn ds-btn--secondary" onClick={onRetry}>
            {t('common.tryAgain')}
          </button>
        </div>
      </div>
    </main>
  );
}

export { tenderName };
