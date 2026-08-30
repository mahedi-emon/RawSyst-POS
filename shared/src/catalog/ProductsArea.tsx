// Products (blueprint B1, B2 and B9).
//
// What the shop sells, and what it charges for it.
//
// The variant matrix and the promotions list are two halves of one question. A
// price tier is what a product costs; a promotion is a reason it costs less
// today. Putting them on separate screens would mean a person setting up a
// campaign cannot see the prices it applies to, and a person setting a price
// cannot see the campaign that is already discounting it.

import { useState } from 'react';

import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { VariantMatrixScreen } from '../inventory/VariantMatrixScreen';
import { PromotionsPanel } from './PromotionsPanel';

type Tab = 'catalogue' | 'promotions';

export function ProductsArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const maySeePromotions = can('promotion.view');
  const [tab, setTab] = useState<Tab>('catalogue');

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    { key: 'catalogue', label: 'nav.inventory', shown: true },
    { key: 'promotions', label: 'promo.title', shown: maySeePromotions },
  ];
  const visible = tabs.filter((x) => x.shown);

  // One tab is not a choice. A shop whose cashier cannot see promotions gets
  // the catalogue with no segmented control above it, rather than a control
  // with a single button in it.
  if (visible.length === 1) {
    return <VariantMatrixScreen companyId={companyId} />;
  }

  return (
    <>
      <div className="products__tabs">
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

      {tab === 'catalogue' ? (
        <VariantMatrixScreen companyId={companyId} />
      ) : (
        <main className="detail">
          <PromotionsPanel companyId={companyId} />
        </main>
      )}
    </>
  );
}
