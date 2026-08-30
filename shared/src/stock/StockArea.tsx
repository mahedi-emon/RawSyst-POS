// Stock (blueprint B4).
//
// # Why levels lead
//
// Four things live here — what is on the shelf, what has been corrected, what
// is being counted, and what is moving between branches — and only one of them
// is what a person opening this screen wants nine times out of ten. "Have I got
// any" is the question; the rest are things you come here to DO, deliberately,
// having already decided to.
//
// So levels are the landing view and the others are tabs, rather than four
// equal cards none of which is the answer.
//
// # The tabs a person cannot use are not shown
//
// `inventory.view` reads. Correcting stock and moving it are separate
// permissions held by different people — a keeper moves stock and a manager
// approves it — so an Auditor sees levels and history and nothing else, with no
// buttons greyed out to invite a press that will be refused.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { useRemote } from '../dashboard/useRemote';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { listStockLocations, type StockLocation } from '../api/stock';
import { LevelsPanel } from './LevelsPanel';
import { AdjustmentsPanel } from './AdjustmentsPanel';
import { CountsPanel } from './CountsPanel';
import { TransfersPanel } from './TransfersPanel';
import { LocationsPanel } from './LocationsPanel';

type Tab = 'levels' | 'adjustments' | 'counts' | 'transfers' | 'locations';

export function StockArea({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();

  const mayAdjust = can('inventory.adjust_stock');
  const mayTransfer = can('inventory.transfer_stock');

  const [tab, setTab] = useState<Tab>('levels');

  // Loaded once here and handed down. Every panel needs the list — to filter
  // by, to write to, to move between — and four independent requests for the
  // same short list is three too many.
  const loadLocations = useCallback(
    () => listStockLocations(client, companyId),
    [client, companyId],
  );
  const locations = useRemote(loadLocations);
  const places: StockLocation[] =
    locations.remote.state === 'ready' ? locations.remote.data.data : [];

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    { key: 'levels', label: 'stock.levels', shown: true },
    { key: 'adjustments', label: 'stock.adjustments', shown: true },
    { key: 'counts', label: 'stock.counts', shown: mayAdjust },
    { key: 'transfers', label: 'stock.transfers', shown: mayTransfer },
    { key: 'locations', label: 'stock.locations', shown: true },
  ];
  const visible = tabs.filter((x) => x.shown);

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('stock.title')}</h1>
          <p className="ds-caption">{t('stock.intro')}</p>
        </div>

        <div className="detail__actions">
          <div
            className="segmented"
            role="group"
            aria-label={t('common.whatToShow')}
          >
            {visible.map((x) => (
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

      {tab === 'levels' && (
        <LevelsPanel companyId={companyId} locations={places} />
      )}
      {tab === 'adjustments' && (
        <AdjustmentsPanel companyId={companyId} locations={places} />
      )}
      {tab === 'counts' && mayAdjust && (
        <CountsPanel companyId={companyId} locations={places} />
      )}
      {tab === 'transfers' && mayTransfer && (
        <TransfersPanel companyId={companyId} locations={places} />
      )}
      {tab === 'locations' && (
        <LocationsPanel companyId={companyId} onChanged={locations.reload} />
      )}
    </main>
  );
}
