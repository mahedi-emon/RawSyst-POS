'use client';

// The tax authorities on file for a country.
//
// # A country has to be chosen before there is anything to show
//
// `GET /platform/jurisdictions` with no country answers an empty list, not
// every country's authorities. That is a filter, not an absence, and the two
// must not render the same: an operator who lands here and sees "no tax
// authorities recorded" would conclude the registry is empty when in fact
// there are fifteen hundred Californian entries one dropdown away.
//
// So the unchosen state is its own state, with its own sentence, and the empty
// state proper is reached only after a country has been named.
//
// # The search filters in the browser, and here that is right
//
// The route takes no search parameter and returns every jurisdiction for the
// country in one answer — no LIMIT, unlike the tenant list, which truncated at
// five hundred and made exactly this pattern a defect. Since every row is
// already here, filtering where the rows are is not a shortcut; it is the only
// place the question can be answered.
//
// # Levels are a tree, shown as a column rather than as indentation
//
// `parent_id` chains a city to a county to a state to a country. A tree view
// would be the obvious rendering and the wrong one at this size: fifteen
// hundred nodes deep-nested is not scannable, and what an operator actually
// does here is look one authority up by name or code.

import { Landmark } from 'lucide-react';
import { Suspense } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Select } from '@/components/ui/field';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

interface Jurisdiction {
  id: string;
  parent_id?: string;
  country: string;
  level: string;
  code: string;
  name: string;
  is_origin_based?: boolean;
}

const MARKETS = ['sa', 'bd', 'us'] as const;

function JurisdictionsScreen() {
  const t = useT();
  const [country, setCountry] = useUrlState('country');

  const picker = (
    <Select
      aria-label={t('nx.plat.juCountry')}
      value={country}
      onChange={(e) => setCountry(e.target.value)}
    >
      <option value="">{t('nx.plat.juChooseCountry')}</option>
      {MARKETS.map((m) => (
        <option key={m} value={m}>
          {m.toUpperCase()}
        </option>
      ))}
    </Select>
  );

  const columns: Column<Jurisdiction>[] = [
    {
      key: 'name',
      header: t('nx.plat.juName'),
      primary: true,
      cell: (x) => x.name,
    },
    {
      key: 'code',
      header: t('nx.plat.juCode'),
      width: 'w-40',
      // As the authority writes it, so it is shown as written and never mirrored.
      cell: (x) => <span className="num">{x.code}</span>,
    },
    {
      key: 'level',
      header: t('nx.plat.juLevel'),
      width: 'w-28',
      cell: (x) => <span className="capitalize text-muted">{x.level}</span>,
    },
    {
      key: 'basis',
      header: t('nx.plat.juBasis'),
      width: 'w-40',
      // Origin versus destination decides which jurisdiction's rate a sale is
      // taxed at, so it is a column rather than a detail: it is the field an
      // operator is checking when a shop says the wrong rate was charged.
      cell: (x) =>
        x.is_origin_based === undefined ? (
          <span className="text-muted">—</span>
        ) : x.is_origin_based ? (
          <Badge tone="info">{t('nx.plat.juOrigin')}</Badge>
        ) : (
          <Badge tone="neutral">{t('nx.plat.juDestination')}</Badge>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.plat.juTitle')}
        description={t('nx.plat.juSubtitle')}
      />

      {country === '' ? (
        <>
          <div className="mb-3 flex flex-wrap items-center gap-2">{picker}</div>
          {/* Not the empty state: nothing has been asked for yet, and saying
              "no tax authorities recorded" here would be false. */}
          <EmptyState
            icon={Landmark}
            title={t('nx.plat.juPickTitle')}
            description={t('nx.plat.juPickDesc')}
          />
        </>
      ) : (
        <ResourceList<Jurisdiction>
          path="/platform/jurisdictions"
          query={{ country }}
          columns={columns}
          rowKey={(x) => x.id}
          // In the browser, deliberately: the route returns every jurisdiction
          // for the country with no limit, so the rows to search are all here.
          filterRow={(x, term) =>
            x.name.toLowerCase().includes(term) || x.code.toLowerCase().includes(term)
          }
          filters={picker}
          caption={t('nx.plat.juCaption')}
          searchPlaceholder={t('nx.plat.juSearch')}
          searchLabel={t('nx.plat.juSearchLabel')}
          noun={t('nx.plat.juNoun')}
          emptyState={
            <EmptyState
              icon={Landmark}
              title={t('nx.plat.juEmptyTitle')}
              description={t('nx.plat.juEmptyDesc')}
            />
          }
        />
      )}
    </>
  );
}

export default function JurisdictionsPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <JurisdictionsScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
