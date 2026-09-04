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

import { Building2 } from 'lucide-react';
import { Suspense } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { useT } from '@/lib/i18n/locale';

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
        // `GET /platform/tenants` takes no search parameter and returns every
        // tenant in one answer, so the filtering happens where the rows are.
        // Name and market, because those are what an operator recognises an
        // account by; the id is not something anybody types from memory.
        filterRow={(x, term) =>
          x.name.toLowerCase().includes(term) ||
          (x.market ?? '').toLowerCase().includes(term)
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
