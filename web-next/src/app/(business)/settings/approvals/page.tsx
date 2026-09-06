'use client';

// Approvals, configured — F1's workflow engine.
//
// # Two tabs, two audiences, two permissions
//
// The rules are the owner's: thresholds, what they route to, and in what
// order. Cover is the approver's: who decides while somebody is away. They are
// different jobs done by different people, and the server already says so —
// rules are `approval.manage_rules`, the cover list is `approval.view` and
// arranging cover is `approval.decide`.
//
// So the PAGE is gated on `approval.view`, the lowest bar of the three, and
// each tab carries its own guard. An approver arranging their own cover is not
// shown a rules tab that would refuse them, and an owner sees both. Gating the
// whole page on `approval.manage_rules` would have been simpler and would have
// hidden cover from every person who actually needs it.
//
// # Why this is not part of /approvals
//
// That screen is the inbox: what is waiting for a signature right now. This one
// is why anything is waiting at all. Somebody clearing a queue at nine in the
// morning should not have the thresholds that filled it one tab away from the
// approve button.

import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PageHeader } from '@/components/ui/panel';
import { Tabs, TabPanel } from '@/components/ui/tabs';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

import { CoverTab } from './cover';
import { RulesTab } from './rules';

type View = 'rules' | 'cover';

function ApprovalSettings() {
  const t = useT();
  const [raw, setView] = useUrlState('on', 'rules');
  const view: View = raw === 'cover' ? 'cover' : 'rules';

  return (
    <>
      <PageHeader title={t('nx.aprc.title')} description={t('nx.aprc.subtitle')} />

      <Tabs<View>
        label={t('nx.aprc.title')}
        value={view}
        onChange={setView}
        items={[
          { id: 'rules', label: t('nx.aprc.tabRules') },
          { id: 'cover', label: t('nx.aprc.tabCover') },
        ]}
      />

      <TabPanel id={view}>
        {view === 'rules' ? <RulesTab /> : <CoverTab />}
      </TabPanel>
    </>
  );
}

export default function ApprovalSettingsPage() {
  return (
    <RequirePermission anyOf={['approval.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ApprovalSettings />
      </Suspense>
    </RequirePermission>
  );
}
