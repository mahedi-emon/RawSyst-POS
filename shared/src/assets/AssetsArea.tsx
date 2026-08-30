// Assets and investors (blueprint C7 and C3.2).
//
// Its own section rather than a seventh tab under Accounting, because PART K's
// navigation tree puts Assets at the top level and because the two registers
// here are read by different people at different times: an asset register is
// consulted when something goes missing, an investor register when somebody
// asks what they own.
//
// # The number the register leads with
//
// Not the cost. `months_due` — how many months of depreciation are waiting to
// be charged — because that is the only thing on this screen that is somebody's
// job today. A register where everything is up to date should look quiet.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { AssetRegisterPanel } from './AssetRegisterPanel';
import { InvestorsPanel } from './InvestorsPanel';

type Tab = 'assets' | 'investors';

export function AssetsArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const maySeeInvestors = can('investor.view');
  const [tab, setTab] = useState<Tab>('assets');

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    { key: 'assets', label: 'assets.register', shown: true },
    { key: 'investors', label: 'assets.investors', shown: maySeeInvestors },
  ];
  const visible = tabs.filter((x) => x.shown);

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('assets.title')}</h1>
          <p className="ds-caption">{t('assets.intro')}</p>
        </div>

        {visible.length > 1 && (
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
        )}
      </header>

      {tab === 'assets' && <AssetRegisterPanel companyId={companyId} />}
      {tab === 'investors' && maySeeInvestors && (
        <InvestorsPanel companyId={companyId} />
      )}
    </main>
  );
}

/** Kept beside the area it belongs to so the two panels can share it. */
export function useCompanyCallback<T>(
  fn: (companyId: string) => Promise<T>,
  companyId: string,
): () => Promise<T> {
  return useCallback(() => fn(companyId), [fn, companyId]);
}
