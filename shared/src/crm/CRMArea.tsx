// Loyalty, store credit and gift cards (blueprint B16).
//
// # The scheme is a tab, not a settings page
//
// What a point is worth and what a tier takes are read constantly — by whoever
// is explaining to a customer why they have 40 points and what that buys. Hiding
// them under Settings would put the answer two screens away from the question.
//
// # A card's balance is never computed here
//
// Every figure on this screen is the server's sum of a ledger. A screen that
// added up what it thought a card held would disagree with the shop's books the
// first time two people spent one at once, and the disagreement would be
// invisible until somebody reconciled.

import { useState } from 'react';

import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { GiftCardsPanel } from './GiftCardsPanel';
import { MembersPanel } from './MembersPanel';
import { ProgramPanel } from './ProgramPanel';

type Tab = 'members' | 'cards' | 'program';

export function CRMArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const maySeeWallet = can('wallet.view');
  const mayManageProgram = can('loyalty.manage');
  const [tab, setTab] = useState<Tab>('members');

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    { key: 'members', label: 'crm.members', shown: true },
    { key: 'cards', label: 'crm.giftCards', shown: maySeeWallet },
    { key: 'program', label: 'crm.program', shown: mayManageProgram },
  ];
  const visible = tabs.filter((x) => x.shown);

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('crm.title')}</h1>
          <p className="ds-caption">{t('crm.intro')}</p>
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

      {tab === 'members' && <MembersPanel companyId={companyId} />}
      {tab === 'cards' && maySeeWallet && <GiftCardsPanel companyId={companyId} />}
      {tab === 'program' && mayManageProgram && (
        <ProgramPanel companyId={companyId} />
      )}
    </main>
  );
}
