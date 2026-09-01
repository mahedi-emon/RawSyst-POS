// Compliance, privacy, the register, the storefront and the document store
// (blueprint D6, E4, E5, E7).
//
// # Five tabs that share one question
//
// They are all "what is this shop obliged to do, and has it". The compliance
// screen reads every module and reports; the other four are where the facts it
// reads are recorded. Somebody arriving at an overdue data-subject request on
// the first tab moves to the second to answer it.
//
// # Compliance comes first
//
// It is the one tab a person opens without already knowing what they are
// looking for.

import { useState } from 'react';

import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { CompliancePanel } from './CompliancePanel';
import { DocumentsPanel } from './DocumentsPanel';
import { PrivacyPanel } from './PrivacyPanel';
import { RegisterPanel } from './RegisterPanel';
import { StorefrontPanel } from './StorefrontPanel';

type Tab = 'compliance' | 'privacy' | 'register' | 'storefront' | 'documents';

export function GovernanceArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const tabs: Array<{ key: Tab; label: Key; shown: boolean }> = [
    {
      key: 'compliance',
      label: 'gov.compliance',
      shown: can('compliance.view'),
    },
    { key: 'privacy', label: 'gov.privacy', shown: can('privacy.view') },
    { key: 'register', label: 'gov.register', shown: can('privacy.view') },
    { key: 'storefront', label: 'gov.storefront', shown: can('privacy.view') },
    { key: 'documents', label: 'gov.documents', shown: can('document.view') },
  ];
  const visible = tabs.filter((x) => x.shown);
  const [tab, setTab] = useState<Tab>(visible[0]?.key ?? 'documents');

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('gov.title')}</h1>
          <p className="ds-caption">{t('gov.intro')}</p>
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

      {tab === 'compliance' && <CompliancePanel companyId={companyId} />}
      {tab === 'privacy' && <PrivacyPanel companyId={companyId} />}
      {tab === 'register' && <RegisterPanel companyId={companyId} />}
      {tab === 'storefront' && <StorefrontPanel companyId={companyId} />}
      {tab === 'documents' && <DocumentsPanel companyId={companyId} />}
    </main>
  );
}
