// Integration, migration, backups and support (blueprint H4, H6, H7, H10).
//
// # Four tabs that belong together
//
// All four are what an owner does to the system rather than to the business:
// what leaves it (webhooks, exports), what comes into it (an import), whether
// it can be recovered (backups), and who to ask when it will not (support).
//
// # The key appears once, and the screen says so
//
// The minted key is shown in a panel that has to be dismissed deliberately,
// with the reason written next to it. A key quietly listed in a table is a key
// somebody assumes they can come back for.

import { useState } from 'react';

import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { BackupsPanel } from './BackupsPanel';
import { IntegrationPanel } from './IntegrationPanel';
import { MigrationPanel } from './MigrationPanel';
import { SupportPanel } from './SupportPanel';

type Tab = 'integration' | 'data' | 'backups' | 'support';

export function AdminArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    {
      key: 'integration',
      label: 'adm.integration',
      shown: can('integration.view'),
    },
    { key: 'data', label: 'adm.data', shown: can('data.export') },
    { key: 'backups', label: 'adm.backups', shown: can('backup.view') },
    { key: 'support', label: 'adm.support', shown: can('support.raise') },
  ];
  const visible = tabs.filter((x) => x.shown);
  const [tab, setTab] = useState<Tab>(visible[0]?.key ?? 'support');

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('adm.title')}</h1>
          <p className="ds-caption">{t('adm.intro')}</p>
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

      {tab === 'integration' && <IntegrationPanel companyId={companyId} />}
      {tab === 'data' && <MigrationPanel companyId={companyId} />}
      {tab === 'backups' && <BackupsPanel companyId={companyId} />}
      {tab === 'support' && <SupportPanel companyId={companyId} />}
    </main>
  );
}
