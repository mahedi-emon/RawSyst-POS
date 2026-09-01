// Business analytics and forecasting (blueprint D2).
//
// # A ratio with nothing underneath it shows a dash
//
// A shop with no sales in the period has no gross margin. The server sends an
// empty string rather than "0.0", and this screen renders a dash — because 0.0%
// reads as "we made nothing on everything we sold", which is a different and
// alarming claim.
//
// # Dead stock is the same list as fast movers
//
// One measurement, sorted two ways. Two screens would be two definitions of
// velocity, free to disagree the day somebody changes one.

import { useCallback, useState } from 'react';

import {
  forecast,
  kpis,
  movers,
  profitability,
  type Forecast,
  type KPIs,
  type Movement,
  type Ranked,
} from '../api/studio';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useT } from '../i18n/locale';
import { SavedReportsPanel } from './SavedReportsPanel';
import type { Key } from '../i18n/strings';
import { money } from '../ui/format';

type Tab = 'kpis' | 'movers' | 'dead' | 'forecast' | 'profit' | 'saved';

export function AnalyticsArea({ companyId }: { companyId: string }) {
  const t = useT();
  const [tab, setTab] = useState<Tab>('kpis');

  const tabs: Array<{ key: Tab; label: Key }> = [
    { key: 'kpis', label: 'anl.kpis' },
    { key: 'movers', label: 'anl.fastMoving' },
    { key: 'dead', label: 'anl.deadStock' },
    { key: 'forecast', label: 'anl.forecast' },
    { key: 'profit', label: 'anl.profitability' },
    // Last, because it is where somebody goes once they know which of the
    // figures above they want again next month.
    { key: 'saved', label: 'anl.saved' },
  ];

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('anl.title')}</h1>
          <p className="ds-caption">{t('anl.intro')}</p>
        </div>

        <div className="detail__actions">
          <div
            className="segmented"
            role="group"
            aria-label={t('common.whatToShow')}
          >
            {tabs.map((x) => (
              <button
                key={x.key}
                className={`segmented__btn${tab === x.key ? ' segmented__btn--on' : ''}`}
                aria-pressed={tab === x.key}
                onClick={() => setTab(x.key)}
              >
                {t(x.label)}
              </button>
            ))}
          </div>
        </div>
      </header>

      {tab === 'kpis' && <KPIPanel companyId={companyId} />}
      {(tab === 'movers' || tab === 'dead') && (
        <MoversPanel companyId={companyId} dead={tab === 'dead'} />
      )}
      {tab === 'forecast' && <ForecastPanel companyId={companyId} />}
      {tab === 'profit' && <ProfitPanel companyId={companyId} />}
      {tab === 'saved' && <SavedReportsPanel companyId={companyId} />}
    </main>
  );
}

function KPIPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(() => kpis(client, companyId, {}), [client, companyId]);
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(k: KPIs) => (
        <>
          <section className="ds-panel">
            <div className="ds-panel__head">
              <div>
                <h2 className="ds-h3">{t('anl.kpis')}</h2>
                <p className="ds-caption">
                  {t('anl.period', { from: k.from, to: k.to })}
                </p>
              </div>
            </div>

            <div className="ds-panel__body anl__tiles">
              <Tile label={t('anl.revenue')} value={money(k.revenue, { currency: k.currency })} big />
              <Tile
                label={t('anl.grossProfit')}
                value={money(k.gross_profit, { currency: k.currency })}
                big
              />
              <Tile label={t('anl.grossMargin')} value={pct(k.gross_margin_pct)} />
              <Tile label={t('anl.orders')} value={String(k.orders)} />
              <Tile
                label={t('anl.averageOrder')}
                value={amountOrDash(k.average_order_value, k.currency)}
              />
              <Tile
                label={t('anl.unitsPerSale')}
                value={k.units_per_transaction || '—'}
              />
              <Tile label={t('anl.discountRatio')} value={pct(k.discount_ratio_pct)} />
              <Tile label={t('anl.returnRate')} value={pct(k.return_rate_pct)} />
              <Tile
                label={t('anl.inventoryTurn')}
                value={k.inventory_turnover || '—'}
              />
              <Tile label={t('anl.repeatRate')} value={pct(k.repeat_customer_pct)} />
              <Tile
                label={t('anl.customerValue')}
                value={amountOrDash(k.customer_lifetime_value, k.currency)}
              />
              <Tile
                label={t('anl.perStore')}
                value={amountOrDash(k.sales_per_store, k.currency)}
              />
              <Tile
                label={t('anl.perPerson')}
                value={amountOrDash(k.sales_per_employee, k.currency)}
              />
            </div>
          </section>
        </>
      )}
    </RemoteBody>
  );
}

function Tile({
  label,
  value,
  big,
}: {
  label: string;
  value: string;
  big?: boolean;
}) {
  return (
    <div className={`anl__tile${big ? ' anl__tile--big' : ''}`}>
      <span className="anl__tilelabel">{label}</span>
      <span className="anl__tilevalue">{value}</span>
    </div>
  );
}

// A dash, not a zero. See the file note.
function pct(value: string): string {
  return value === '' ? '—' : `${value}%`;
}

function amountOrDash(value: string, currency: string): string {
  return value === '' ? '—' : money(value, { currency });
}

function MoversPanel({
  companyId,
  dead,
}: {
  companyId: string;
  dead: boolean;
}) {
  const { client } = useAuth();
  const t = useT();

  const [days, setDays] = useState(90);
  const load = useCallback(
    () => movers(client, companyId, days),
    [client, companyId, days],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section
      className="ds-panel"
      aria-label={t(dead ? 'anl.deadStock' : 'anl.fastMoving')}
    >
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t(dead ? 'anl.deadStock' : 'anl.fastMoving')}</h2>
          <p className="ds-caption">
            {t(dead ? 'anl.deadStockHint' : 'anl.fastMovingHint')}
          </p>
        </div>
        <div className="anl__actions">
          <select
            className="input"
            aria-label={t('anl.window')}
            value={days}
            onChange={(e) => setDays(Number(e.target.value))}
          >
            {[30, 60, 90, 180].map((d) => (
              <option key={d} value={d}>
                {t('anl.lastDays', { days: String(d) })}
              </option>
            ))}
          </select>
        </div>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Movement[] }) => {
          // The same measurement, sorted two ways. Dead stock is what has not
          // moved, ordered by how long it has not moved for — with "never
          // sold" first, because that is worse than old.
          const rows = dead
            ? payload.data
                .filter((m) => m.sold_qty === '0' || Number(m.sold_qty) <= 0)
                .sort((a, b) =>
                  a.days_since_sold === b.days_since_sold
                    ? Number(b.on_hand) - Number(a.on_hand)
                    : a.days_since_sold === -1
                      ? -1
                      : b.days_since_sold === -1
                        ? 1
                        : b.days_since_sold - a.days_since_sold,
                )
            : payload.data.filter((m) => Number(m.sold_qty) > 0);

          return rows.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t(dead ? 'anl.noDeadTitle' : 'anl.noMoversTitle')}
                body={t(dead ? 'anl.noDeadBody' : 'anl.noMoversBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('anl.product')}</th>
                    <th scope="col" className="num">
                      {t('anl.sold')}
                    </th>
                    <th scope="col" className="num">
                      {t('anl.onHand')}
                    </th>
                    {dead ? (
                      <th scope="col">{t('anl.lastSold')}</th>
                    ) : (
                      <>
                        <th scope="col" className="num">
                          {t('anl.perDay')}
                        </th>
                        <th scope="col" className="num">
                          {t('anl.daysCover')}
                        </th>
                        <th scope="col">{t('anl.reorderBy')}</th>
                      </>
                    )}
                    <th scope="col" className="num">
                      {t('anl.profit')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rows.slice(0, 100).map((m) => (
                    <tr key={m.variant_id}>
                      <td>
                        <span className="detail__strong">{m.product}</span>
                        <span className="ds-caption">
                          {m.sku}
                          {m.category ? ` · ${m.category}` : ''}
                        </span>
                      </td>
                      <td className="num">{m.sold_qty}</td>
                      <td className="num">{m.on_hand}</td>
                      {dead ? (
                        <td>
                          {m.days_since_sold === -1 ? (
                            <span className="ds-badge ds-badge--danger">
                              {t('anl.neverSold')}
                            </span>
                          ) : (
                            t('anl.daysAgo', { days: String(m.days_since_sold) })
                          )}
                        </td>
                      ) : (
                        <>
                          <td className="num">{m.velocity}</td>
                          <td className="num">
                            {m.days_cover != null ? m.days_cover : '—'}
                          </td>
                          <td>
                            {/* Only when the shop has SET a reorder level.
                                A predicted date against a level nobody chose
                                would be this screen inventing the policy. */}
                            {m.reorder_on ? m.reorder_on : '—'}
                          </td>
                        </>
                      )}
                      <td className="num">
                        {money(m.profit, { currency: m.currency })}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        }}
      </RemoteBody>
    </section>
  );
}

function ForecastPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const [ahead, setAhead] = useState(30);
  const load = useCallback(
    () => forecast(client, companyId, { window_days: 90, forecast_days: ahead }),
    [client, companyId, ahead],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('anl.forecast')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('anl.forecast')}</h2>
          {/* Says what it is. An owner ordering against a number has to know
              it is last quarter repeated rather than a model. */}
          <p className="ds-caption">{t('anl.forecastHint')}</p>
        </div>
        <div className="anl__actions">
          <select
            className="input"
            aria-label={t('anl.lookAhead')}
            value={ahead}
            onChange={(e) => setAhead(Number(e.target.value))}
          >
            {[30, 60, 90].map((d) => (
              <option key={d} value={d}>
                {t('anl.nextDays', { days: String(d) })}
              </option>
            ))}
          </select>
        </div>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Forecast[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('anl.noForecastTitle')}
                body={t('anl.noForecastBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('anl.product')}</th>
                    <th scope="col" className="num">
                      {t('anl.soldInWindow')}
                    </th>
                    <th scope="col" className="num">
                      {t('anl.expected')}
                    </th>
                    <th scope="col" className="num">
                      {t('anl.onHand')}
                    </th>
                    <th scope="col" className="num">
                      {t('anl.shortBy')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data
                    .filter((f) => Number(f.shortfall) > 0)
                    .concat(payload.data.filter((f) => Number(f.shortfall) <= 0))
                    .slice(0, 100)
                    .map((f) => (
                      <tr
                        key={f.variant_id}
                        className={
                          Number(f.shortfall) > 0 ? 'anl__row--short' : undefined
                        }
                      >
                        <td>
                          <span className="detail__strong">{f.product}</span>
                          <span className="ds-caption">{f.sku}</span>
                        </td>
                        <td className="num">{f.sold_in_window}</td>
                        <td className="num">{f.expected_demand}</td>
                        <td className="num">{f.on_hand}</td>
                        <td className="num">
                          {/* Zero rather than negative when there is enough:
                              "you are 40 short" and "you have 40 spare" are
                              different sentences and this column says the
                              first. */}
                          {Number(f.shortfall) > 0 ? f.shortfall : '—'}
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

function ProfitPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const [by, setBy] = useState('category');
  const load = useCallback(
    () => profitability(client, companyId, { by }),
    [client, companyId, by],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('anl.profitability')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('anl.profitability')}</h2>
          <p className="ds-caption">{t('anl.profitabilityHint')}</p>
        </div>
        <div className="anl__actions">
          <div className="segmented" role="group" aria-label={t('anl.groupBy')}>
            {['category', 'brand', 'product'].map((x) => (
              <button
                key={x}
                className={`segmented__btn${by === x ? ' segmented__btn--on' : ''}`}
                aria-pressed={by === x}
                onClick={() => setBy(x)}
              >
                {t(`anl.by.${x}` as Key)}
              </button>
            ))}
          </div>
        </div>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Ranked[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('anl.noProfitTitle')}
                body={t('anl.noProfitBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t(`anl.by.${by}` as Key)}</th>
                    <th scope="col" className="num">
                      {t('anl.units')}
                    </th>
                    <th scope="col" className="num">
                      {t('anl.revenue')}
                    </th>
                    <th scope="col" className="num">
                      {t('anl.cost')}
                    </th>
                    <th scope="col" className="num">
                      {t('anl.profit')}
                    </th>
                    <th scope="col" className="num">
                      {t('anl.margin')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((r) => (
                    <tr key={r.id || r.label}>
                      <td>{r.label}</td>
                      <td className="num">{r.units}</td>
                      <td className="num">
                        {money(r.revenue, { currency: r.currency })}
                      </td>
                      <td className="num">
                        {money(r.cost, { currency: r.currency })}
                      </td>
                      <td className="num">
                        {money(r.profit, { currency: r.currency })}
                      </td>
                      <td className="num">{pct(r.margin_pct)}</td>
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
