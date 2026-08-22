// What has run out, and what is about to.
//
// Two lists on one screen with a filter between them, because the question
// behind both is the same — what do I need to order — and an owner who has just
// read the out-of-stock list wants the nearly-out one next, not a second
// navigation.
//
// Quantities are summed across the company's warehouses. Comparing per
// warehouse would report a shop as low on stock while a full box sat in the
// back room.

import { useCallback, useState } from 'react';

import { fetchStock, type StockRow } from '../api/drilldown';
import { useAuth } from '../auth/session';
import { DetailScreen, EmptyState, RemoteBody } from './DetailScreen';
import { useRemote } from './useRemote';
import { trimQuantity } from './drilldown';
import { useT } from '../i18n/locale';

type Filter = 'low' | 'out';

export function StockScreen({
  companyId,
  initialFilter,
  onBack,
}: {
  companyId: string;
  initialFilter: Filter;
  onBack: () => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const [filter, setFilter] = useState<Filter>(initialFilter);

  const load = useCallback(
    () => fetchStock(client, companyId, filter),
    [client, companyId, filter],
  );
  const { remote, reload, refreshing } = useRemote(load);

  return (
    <DetailScreen
      title={t('dash.stockToReorder')}
      subtitle="Counted across every warehouse in this business"
      onBack={onBack}
      onRefresh={reload}
      refreshing={refreshing}
      actions={
        <div className="segmented" role="group" aria-label={t('dash.whichStock')}>
          {(['out', 'low'] as const).map((f) => (
            <button
              key={f}
              className={`segmented__btn${filter === f ? ' segmented__btn--on' : ''}`}
              aria-pressed={filter === f}
              onClick={() => setFilter(f)}
            >
              {f === 'out' ? 'Out of stock' : 'Below reorder level'}
            </button>
          ))}
        </div>
      }
    >
      <RemoteBody remote={remote} onRetry={reload}>
        {(d) => (
          <div className="ds-panel">
            <div className="ds-panel__head">
              <h2 className="ds-h3">
                {filter === 'out' ? 'Out of stock' : 'Below reorder level'}
              </h2>
              <span className="ds-caption">
                {d.count} item{d.count === 1 ? '' : 's'}
              </span>
            </div>

            <div className="ds-panel__body ds-scroll-x">
              {d.rows.length === 0 ? (
                <EmptyState
                  title={
                    filter === 'out'
                      ? 'Everything is in stock'
                      : 'Nothing is below its reorder level'
                  }
                  body={
                    filter === 'out'
                      ? 'No active item has run out.'
                      : 'Items appear here once they fall to the reorder level set against them. Items with no reorder level are never listed.'
                  }
                />
              ) : (
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('common.item')}</th>
                      <th scope="col">{t('dash.barcode')}</th>
                      <th scope="col" className="num">{t('dash.onHand')}</th>
                      <th scope="col" className="num">{t('dash.reorderAt')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {d.rows.map((row) => (
                      <Row key={row.variant_id} row={row} outOfStock={filter === 'out'} />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        )}
      </RemoteBody>
    </DetailScreen>
  );
}

function Row({ row, outOfStock }: { row: StockRow; outOfStock: boolean }) {
  return (
    <tr>
      <td>
        <span className="detail__strong">{row.name}</span>
        <span className="ds-caption">{row.sku}</span>
      </td>
      <td className="num">
        {row.barcode || <span className="ds-subtle">—</span>}
      </td>
      {/* Quantity, not money. A negative on-hand is possible where the company
          allows selling past zero, and it reads as a debt rather than as an
          error — which is what it is. */}
      <td className={`num${outOfStock ? ' ds-down' : ''}`}>{trimQuantity(row.on_hand)}</td>
      <td className="num">
        {row.reorder_level ? trimQuantity(row.reorder_level) : <span className="ds-subtle">not set</span>}
      </td>
    </tr>
  );
}
