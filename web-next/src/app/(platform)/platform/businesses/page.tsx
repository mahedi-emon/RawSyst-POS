'use client';

// Every business on the platform.
//
// # Trading is not the same as signed up
//
// The route defines an active tenant as one that sold something in the last
// thirty days, and it is deliberate: counting a signup as active is how a
// platform tells itself a story about its own growth. So "last sold" is a
// column rather than a footnote, and a business that has never traded says so
// in words rather than showing an empty cell somebody has to interpret.
//
// # An unverified backup is the actionable column
//
// A backup nobody has restored is a file. The platform health screen counts
// them; this is where an operator finds out which ones, which is the only form
// of that number anybody can act on.
//
// # The search goes to the server, and it did not used to
//
// This screen used to filter in the browser, because `GET /platform/tenants`
// took no arguments. What it also did was `LIMIT 500`, which the filter had no
// way to know — so an operator searching for a client who had signed up earlier
// than the most recent five hundred was told there were no matches. The
// development database holds nine and a half thousand accounts, of which one
// hundred and thirty are named "Tieout" and not one was reachable.
//
// "No matches" and "not in the half of the table I was sent" are different
// answers, and only one of them was true. The route now searches, filters and
// pages, so the question is asked where the rows are.

import { Building2 } from 'lucide-react';
import { Suspense } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Select } from '@/components/ui/field';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

interface Tenant {
  id: string;
  name: string;
  plan_tier?: string;
  status?: string;
  /** The country this account was sold into. */
  market?: string;
  companies: number;
  users: number;
  created_at: string;
  /** The most recent sale anywhere in the tenant. Absent means never. */
  last_activity?: string;
  /** When it last proved it could restore. Absent is what to act on. */
  backup_verified_at?: string;
}

/** A timestamp trimmed to its date. The time of a sale is not the question. */
function day(value?: string): string | null {
  if (!value) return null;
  return value.slice(0, 10);
}

function BusinessesScreen() {
  const t = useT();
  // In the URL, so a filtered list is a link an operator can send to a
  // colleague rather than a set of steps they have to describe.
  const [market, setMarket] = useUrlState('market');
  const [status, setStatus] = useUrlState('status');

  const columns: Column<Tenant>[] = [
    {
      key: 'name',
      header: t('nx.plat.colBusiness'),
      primary: true,
      cell: (x) => (
        <span className="flex items-center gap-2">
          {x.name}
          {x.status && x.status !== 'active' ? (
            <Badge tone="caution">{x.status}</Badge>
          ) : null}
        </span>
      ),
    },
    {
      key: 'plan',
      header: t('nx.plat.colPlan'),
      width: 'w-28',
      cell: (x) => <span className="capitalize text-muted">{x.plan_tier ?? '—'}</span>,
    },
    {
      key: 'market',
      header: t('nx.plat.colMarket'),
      secondary: true,
      width: 'w-24',
      // An identifier the platform assigns, not prose: shown as written.
      cell: (x) => <span className="num uppercase text-muted">{x.market || '—'}</span>,
    },
    {
      key: 'companies',
      header: t('nx.plat.colCompanies'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      cell: (x) => x.companies,
    },
    {
      key: 'users',
      header: t('nx.plat.colUsers'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      cell: (x) => x.users,
    },
    {
      key: 'last_activity',
      header: t('nx.plat.colLastSold'),
      width: 'w-32',
      cell: (x) => {
        const d = day(x.last_activity);
        // Never traded is a fact about the account, not a missing value.
        return d ? (
          <time dateTime={d}>{d}</time>
        ) : (
          <Badge tone="caution">{t('nx.plat.neverTraded')}</Badge>
        );
      },
    },
    {
      key: 'backup',
      header: t('nx.plat.colBackup'),
      width: 'w-36',
      cell: (x) => {
        const d = day(x.backup_verified_at);
        return d ? (
          <time dateTime={d} className="text-muted">
            {d}
          </time>
        ) : (
          <Badge tone="critical">{t('nx.plat.neverVerified')}</Badge>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.plat.bizTitle')}
        description={t('nx.plat.bizSubtitle')}
      />

      <ResourceList<Tenant>
        path="/platform/tenants"
        columns={columns}
        rowKey={(x) => x.id}
        // Server-side, on the name. The id is not something anybody types from
        // memory, and the market is a filter rather than a search term.
        searchParam="search"
        query={{ market, status }}
        // Keyset, on the id of the last row. The route orders by
        // (created_at, id) descending and resolves the cursor row's pair, so
        // the newest account stays at the top instead of the order being
        // whatever the ids happen to sort as.
        cursorOf={(x) => x.id}
        filters={
          <>
            <Select
              aria-label={t('nx.plat.filterMarket')}
              value={market}
              onChange={(e) => setMarket(e.target.value)}
            >
              <option value="">{t('nx.plat.allMarkets')}</option>
              <option value="sa">{t('plat.marketSa')}</option>
              <option value="bd">{t('plat.marketBd')}</option>
              <option value="us">{t('plat.marketUs')}</option>
            </Select>
            <Select
              aria-label={t('nx.plat.filterStatus')}
              value={status}
              onChange={(e) => setStatus(e.target.value)}
            >
              <option value="">{t('nx.plat.allStatuses')}</option>
              <option value="active">{t('nx.plat.statusActive')}</option>
              <option value="suspended">{t('nx.plat.statusSuspended')}</option>
              <option value="deactivated">{t('nx.plat.statusDeactivated')}</option>
            </Select>
          </>
        }
        caption={t('nx.plat.bizCaption')}
        searchPlaceholder={t('nx.plat.bizSearch')}
        searchLabel={t('nx.plat.bizSearchLabel')}
        noun={t('nx.plat.businesses2')}
        emptyState={
          <EmptyState
            icon={Building2}
            title={t('nx.plat.bizEmptyTitle')}
            description={t('nx.plat.bizEmptyDesc')}
          />
        }
      />
    </>
  );
}

export default function BusinessesPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <BusinessesScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
