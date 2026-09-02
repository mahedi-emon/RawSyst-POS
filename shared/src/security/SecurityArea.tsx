// The second factor, the caller's own sessions, and the role builder
// (blueprint H1, A6.2).
//
// # Two of these are about you and one is about everybody
//
// The second factor and the session list are personal: they are scoped to the
// caller's own token and there is no version of them that shows somebody else.
// The role builder is the opposite — it decides what every member of staff can
// do — so it sits behind `identity.manage_roles` and appears only for the
// people who hold it.
//
// They share a screen because they share a question: who can get in, and what
// can they do once they are.

import { useState } from 'react';

import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { MFAPanel } from './MFAPanel';
import { RoleBuilderPanel } from './RoleBuilderPanel';
import { SessionsPanel } from './SessionsPanel';

type Tab = 'factor' | 'sessions' | 'roles';

export function SecurityArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    { key: 'factor', label: 'sec.factor', shown: true },
    { key: 'sessions', label: 'sec.sessions', shown: true },
    { key: 'roles', label: 'sec.roles', shown: can('identity.manage_roles') },
  ];
  const visible = tabs.filter((x) => x.shown);
  const [tab, setTab] = useState<Tab>('factor');

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('sec.title')}</h1>
          <p className="ds-caption">{t('sec.intro')}</p>
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

      {tab === 'factor' && <MFAPanel />}
      {tab === 'sessions' && <SessionsPanel />}
      {tab === 'roles' && can('identity.manage_roles') && (
        <RoleBuilderPanel companyId={companyId} />
      )}
    </main>
  );
}
