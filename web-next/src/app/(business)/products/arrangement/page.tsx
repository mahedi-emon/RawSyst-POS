'use client';

// How the catalogue is arranged: departments, brands, units.
//
// Three tables a product hangs off, none of which had a route or a screen
// while `POST /catalog/products` accepted all three ids. A shop could not put
// its stock into departments, could not record a brand, and had no unit on any
// invoice line — not because the feature was missing, but because the only way
// to name one was to already know a UUID.
//
// # One screen, three tabs, for the same reason expense setup is one screen
//
// They are set up together, once, and then hardly touched. Three sidebar
// entries would push the thing somebody does every day — adding a product —
// further down the list.
//
// # Reading is catalog.view, changing is catalog.edit
//
// The page is gated on `catalog.view` because a person who may look at the
// catalogue may look at how it is arranged. The buttons that change it appear
// only for `catalog.edit`, and the server refuses regardless — arranging the
// catalogue is a different job from adding stock to it.
//
// # Nothing is deleted
//
// The foreign keys are ON DELETE RESTRICT, so a department anything has ever
// been filed under cannot be removed. Retiring is what somebody pressing
// delete actually wants — stop offering it — and it leaves every historical
// product still explicable. The row carries its product count so the question
// "may I retire this" is answered before it is asked.

import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PageHeader } from '@/components/ui/panel';
import { TabPanel, Tabs } from '@/components/ui/tabs';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

import { Brands } from './brands';
import { Categories } from './categories';
import { Units } from './units';

type View = 'categories' | 'brands' | 'units';
const VIEWS: readonly View[] = ['categories', 'brands', 'units'];

function ArrangementScreen() {
  const t = useT();
  const [raw, setView] = useUrlState('on', 'categories');
  const view = (VIEWS as readonly string[]).includes(raw)
    ? (raw as View)
    : 'categories';

  return (
    <>
      <PageHeader title={t('nx.arr.title')} description={t('nx.arr.subtitle')} />

      <Tabs<View>
        label={t('nx.arr.title')}
        value={view}
        onChange={setView}
        items={[
          { id: 'categories', label: t('nx.arr.tabCategories') },
          { id: 'brands', label: t('nx.arr.tabBrands') },
          { id: 'units', label: t('nx.arr.tabUnits') },
        ]}
      />

      {/* Only the chosen view is mounted: three lists fetched at once would be
          three requests for two screens nobody is looking at. */}
      <TabPanel id={view}>
        {view === 'categories' ? <Categories /> : null}
        {view === 'brands' ? <Brands /> : null}
        {view === 'units' ? <Units /> : null}
      </TabPanel>
    </>
  );
}

export default function ArrangementPage() {
  return (
    <RequirePermission anyOf={['catalog.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ArrangementScreen />
      </Suspense>
    </RequirePermission>
  );
}
