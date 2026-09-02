// Card providers, and what they said (blueprint E3.3, E3.4).
//
// Two halves of one question. The connections are what a shop CAN take money
// through; the attempts are what happened when it did — and the second is where
// somebody goes when a customer says the card was charged twice or the payment
// never arrived.

import { useState } from 'react';

import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { AttemptsPanel } from './AttemptsPanel';
import { GatewaysPanel } from './GatewaysPanel';

type Tab = 'connections' | 'attempts';

export function PaymentsArea({ companyId }: { companyId: string }) {
  const t = useT();
  const [tab, setTab] = useState<Tab>('connections');

  const tabs: Array<{ key: Tab; label: Key }> = [
    { key: 'connections', label: 'pay.connections' },
    { key: 'attempts', label: 'pay.attempts' },
  ];

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('pay.title')}</h1>
          <p className="ds-caption">{t('pay.intro')}</p>
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

      {tab === 'connections' && <GatewaysPanel companyId={companyId} />}
      {tab === 'attempts' && <AttemptsPanel companyId={companyId} />}
    </main>
  );
}
