'use client';

// The configuration every expense depends on.
//
// # Three objects, one screen
//
// A category, a department and a standing cost are three different records, and
// three sidebar entries for them would push the thing an owner actually does
// every day — recording an expense — further down a list. They belong together
// because they are set up together, once, and then hardly touched.
//
// # Behind `expense.manage_heads`, not `expense.view`
//
// Somebody who may look at what the business spent has no business deciding
// which categories reclaim VAT. The chart of accounts this screen picks from
// (`GET /expenses/accounts`) is gated the same way for the same reason, so the
// sidebar entry and the screen behind it agree.
//
// Booking what is due is the exception: it writes expenses, so it is gated on
// `expense.record` and appears only for somebody who holds it.

import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PageHeader } from '@/components/ui/panel';
import { Tabs, TabPanel } from '@/components/ui/tabs';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

import { Categories } from './categories';
import { Departments } from './departments';
import { Standing } from './standing';

type View = 'categories' | 'departments' | 'standing';
const VIEWS: readonly View[] = ['categories', 'departments', 'standing'];

function SetupScreen() {
  const t = useT();
  // In the URL, so the view can be sent to a colleague and Back undoes it.
  const [raw, setView] = useUrlState('on', 'categories');
  const view = (VIEWS as readonly string[]).includes(raw)
    ? (raw as View)
    : 'categories';

  return (
    <>
      <PageHeader title={t('nx.expcfg.title')} description={t('nx.expcfg.subtitle')} />

      <Tabs<View>
        label={t('nx.expcfg.title')}
        value={view}
        onChange={setView}
        items={[
          { id: 'categories', label: t('nx.expcfg.tabHeads') },
          { id: 'departments', label: t('nx.expcfg.tabDepartments') },
          { id: 'standing', label: t('nx.expcfg.tabStanding') },
        ]}
      />

      {/* Only the chosen view is mounted. Three lists fetched at once would be
          three requests for two screens nobody is looking at. */}
      <TabPanel id={view}>
        {view === 'categories' ? <Categories /> : null}
        {view === 'departments' ? <Departments /> : null}
        {view === 'standing' ? <Standing /> : null}
      </TabPanel>
    </>
  );
}

export default function ExpenseSetupPage() {
  return (
    <RequirePermission anyOf={['expense.manage_heads']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SetupScreen />
      </Suspense>
    </RequirePermission>
  );
}
