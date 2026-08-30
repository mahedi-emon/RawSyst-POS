// What is on the shelf, and how it got there.
//
// Two views of the same stock: the level now, and the movements that produced
// it. They are a pair rather than two screens because the question people
// actually ask is "why is this three" — which needs both, one after the other.
//
// # No cost, anywhere on this panel
//
// Levels are quantities. A person who may see stock is not thereby a person who
// may see what it cost: `catalog.view_cost_price` is a separate permission, and
// the till's own product type has no cost field at all so that a terminal
// cannot receive one. Showing a value column here would route around both.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { shortDate } from '../ui/format';
import {
  listStockMovements,
  listStockOnHand,
  type StockLine,
  type StockLocation,
  type StockMovement,
} from '../api/stock';
import { isNegative, isZero } from './stock';

export function LevelsPanel({
  companyId,
  locations,
}: {
  companyId: string;
  locations: StockLocation[];
}) {
  const t = useT();
  const [where, setWhere] = useState('');
  const [search, setSearch] = useState('');
  const [lowOnly, setLowOnly] = useState(false);
  const [history, setHistory] = useState(false);

  return (
    <>
      <section className="ds-panel stock__filters">
        <div className="ds-panel__body stock__filterrow">
          <label className="stock__filter">
            <span className="ds-caption">{t('stock.location')}</span>
            <select
              className="input"
              value={where}
              onChange={(e) => setWhere(e.target.value)}
            >
              <option value="">{t('stock.everywhere')}</option>
              {locations.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name}
                </option>
              ))}
            </select>
          </label>

          <label className="stock__filter stock__filter--wide">
            <span className="ds-caption">{t('stock.find')}</span>
            <input
              className="input"
              type="search"
              value={search}
              placeholder={t('stock.findHint')}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>

          <div className="segmented" role="group" aria-label={t('common.whatToShow')}>
            <button
              className={`segmented__btn${!history ? ' segmented__btn--on' : ''}`}
              aria-pressed={!history}
              onClick={() => setHistory(false)}
            >
              {t('stock.onHand')}
            </button>
            <button
              className={`segmented__btn${history ? ' segmented__btn--on' : ''}`}
              aria-pressed={history}
              onClick={() => setHistory(true)}
            >
              {t('stock.history')}
            </button>
          </div>

          {!history && (
            <label className="stock__check">
              <input
                type="checkbox"
                checked={lowOnly}
                onChange={(e) => setLowOnly(e.target.checked)}
              />
              <span>{t('stock.lowOnly')}</span>
            </label>
          )}
        </div>
      </section>

      {history ? (
        <History companyId={companyId} where={where} search={search} />
      ) : (
        <Levels
          companyId={companyId}
          where={where}
          search={search}
          lowOnly={lowOnly}
        />
      )}
    </>
  );
}

function Levels({
  companyId,
  where,
  search,
  lowOnly,
}: {
  companyId: string;
  where: string;
  search: string;
  lowOnly: boolean;
}) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () =>
      listStockOnHand(client, companyId, {
        location_id: where || undefined,
        q: search || undefined,
        low: lowOnly,
      }),
    [client, companyId, where, search, lowOnly],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('stock.onHand')}>
      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: StockLine[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('stock.noStockTitle')}
                body={t('stock.noStockBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('stock.product')}</th>
                    <th scope="col">{t('stock.location')}</th>
                    <th scope="col" className="num">
                      {t('stock.qty')}
                    </th>
                    <th scope="col" className="num">
                      {t('stock.reorderAt')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((l) => (
                    <tr key={l.variant_id + l.location}>
                      <td>
                        <span className="detail__strong">{l.product}</span>
                        <span className="ds-caption">{l.sku}</span>
                      </td>
                      <td>{l.location}</td>
                      <td className="num">
                        {/* Below zero is its own state, not a small number.
                            C13 lets a shop sell past what it has where the
                            policy allows it, and the cost of those units is
                            provisional until the next delivery — so the figure
                            is worth marking rather than reading past. */}
                        <span
                          className={
                            isNegative(l.on_hand)
                              ? 'stock__qty stock__qty--below'
                              : l.below_minimum
                                ? 'stock__qty stock__qty--low'
                                : 'stock__qty'
                          }
                        >
                          {l.on_hand}
                        </span>
                      </td>
                      <td className="num">
                        {l.reorder_level ?? <span aria-hidden="true">—</span>}
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

function History({
  companyId,
  where,
  search,
}: {
  companyId: string;
  where: string;
  search: string;
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () =>
      listStockMovements(client, companyId, {
        location_id: where || undefined,
        q: search || undefined,
      }),
    [client, companyId, where, search],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('stock.history')}>
      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: StockMovement[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('stock.noHistoryTitle')}
                body={t('stock.noHistoryBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('stock.when')}</th>
                    <th scope="col">{t('stock.product')}</th>
                    <th scope="col">{t('stock.location')}</th>
                    <th scope="col">{t('stock.why')}</th>
                    <th scope="col" className="num">
                      {t('stock.change')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((m, i) => (
                    <tr key={m.occurred_at + m.sku + i}>
                      <td>{shortDate(m.occurred_at, locale)}</td>
                      <td>
                        <span className="detail__strong">{m.product}</span>
                        <span className="ds-caption">{m.sku}</span>
                      </td>
                      <td>{m.location}</td>
                      <td>
                        <span className="detail__strong">
                          {reasonLabel(m.reason, t)}
                        </span>
                        {m.document && (
                          <span className="ds-caption">{m.document}</span>
                        )}
                      </td>
                      <td className="num">
                        <span
                          className={
                            isNegative(m.delta)
                              ? 'stock__qty stock__qty--out'
                              : isZero(m.delta)
                                ? 'stock__qty'
                                : 'stock__qty stock__qty--in'
                          }
                        >
                          {m.delta}
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

/** The reason a movement gives, in words.
 *
 *  Falls back to the raw reason rather than to a blank, the same way
 *  `permissionLabel` does on the staff screen: a reason added to migration 0020
 *  and not yet to the catalogue should read as itself, not vanish. */
function reasonLabel(reason: string, t: (k: Key) => string): string {
  const key = `stock.reason.${reason}` as Key;
  const named = t(key);
  return named === key ? reason : named;
}
