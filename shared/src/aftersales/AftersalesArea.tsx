// What happens after the goods leave the counter (blueprint B13, B14, B15).
//
// # Four tabs, one subject
//
// A delivery carries a unit, a serial names it, a repair fixes it, and an
// instalment plan is how it was paid for. They share a customer and often the
// same physical thing, and splitting them across the navigation would mean
// somebody answering "where is my order and when is my next payment" opening
// two sections.
//
// # Each tab is a permission
//
// A driver holds `delivery.view` and nothing else, and gets one tab. A service
// technician holds `service.view`. The section does not offer a tab that would
// refuse the person looking at it.

import { useState } from 'react';

import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { DeliveriesPanel } from './DeliveriesPanel';
import { InstalmentsPanel } from './InstalmentsPanel';
import { SerialsPanel } from './SerialsPanel';
import { ServicePanel } from './ServicePanel';

type Tab = 'deliveries' | 'serials' | 'service' | 'instalments';

export function AftersalesArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    { key: 'deliveries', label: 'after.deliveries', shown: can('delivery.view') },
    { key: 'serials', label: 'after.serials', shown: can('serial.view') },
    { key: 'service', label: 'after.service', shown: can('service.view') },
    {
      key: 'instalments',
      label: 'after.instalments',
      shown: can('installment.view'),
    },
  ];
  const visible = tabs.filter((x) => x.shown);
  const [tab, setTab] = useState<Tab>(visible[0]?.key ?? 'deliveries');

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('after.title')}</h1>
          <p className="ds-caption">{t('after.intro')}</p>
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

      {tab === 'deliveries' && <DeliveriesPanel companyId={companyId} />}
      {tab === 'serials' && <SerialsPanel companyId={companyId} />}
      {tab === 'service' && <ServicePanel companyId={companyId} />}
      {tab === 'instalments' && <InstalmentsPanel companyId={companyId} />}
    </main>
  );
}
