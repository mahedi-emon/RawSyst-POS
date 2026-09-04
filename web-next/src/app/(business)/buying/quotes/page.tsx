'use client';

// Asking several suppliers what they would charge.
//
// # How many replied is the column that matters
//
// An RFQ sitting with three suppliers and no replies needs chasing; one with
// three replies needs deciding. Those are different actions, and the number of
// each is what tells them apart at a glance — so the row carries both rather
// than a status alone.

import { Scale } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Select } from '@/components/ui/field';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useNamedList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { RFQ_STATUS, type RFQ } from '@/lib/purchasing/sourcing';
import { useUrlState } from '@/lib/url-state';

function RFQsScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const [status, setStatus] = useUrlState('status');

  const { data, isLoading, error, refetch } = useNamedList<RFQ>(
    scope ? '/purchasing/rfqs' : null,
    'rfqs',
    scope ? { ...scope, status: status || undefined } : undefined,
  );

  const rows = data?.data ?? [];

  const columns: Column<RFQ>[] = [
    {
      key: 'number',
      header: t('nx.rfq.colNumber'),
      primary: true,
      width: 'w-56',
      cell: (r) => {
        const shown = RFQ_STATUS[r.status];
        return (
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{r.rfq_number}</span>
            {shown ? <Badge tone={shown.tone}>{t(shown.key)}</Badge> : null}
          </span>
        );
      },
    },
    {
      key: 'closes',
      header: t('nx.rfq.colCloses'),
      secondary: true,
      width: 'w-32',
      cell: (r) =>
        r.closes_on ? (
          <time dateTime={r.closes_on} className="num">
            {r.closes_on}
          </time>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'asked',
      header: t('nx.rfq.colAsked'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      cell: (r) => <span className="num">{r.invited?.length ?? 0}</span>,
    },
    {
      key: 'replied',
      header: t('nx.rfq.colReplies'),
      numeric: true,
      width: 'w-24',
      // The figure that says whether this needs chasing or deciding.
      cell: (r) => (
        <span className="num font-medium">
          {r.invited?.filter((i) => i.quoted).length ?? 0}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.rfq.title')}
        description={t('nx.rfq.subtitle')}
        actions={
          <Button variant="primary" onClick={() => router.push('/buying/quotes/new')}>
            {t('nx.rfq.ask')}
          </Button>
        }
      />

      <div className="mb-4">
        <Select
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          aria-label={t('nx.req.filterLabel')}
          className="h-10 w-auto"
        >
          <option value="">{t('nx.rfq.filterAll')}</option>
          {Object.entries(RFQ_STATUS).map(([value, meta]) => (
            <option key={value} value={value}>
              {t(meta.key)}
            </option>
          ))}
        </Select>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={4} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Scale}
          title={t('nx.rfq.emptyTitle')}
          description={t('nx.rfq.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.rfq.caption')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
          onOpenRow={(r) => router.push(`/buying/quotes/${r.id}`)}
        />
      ) : null}
    </>
  );
}

export default function RFQsPage() {
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <RFQsScreen />
      </Suspense>
    </RequirePermission>
  );
}
