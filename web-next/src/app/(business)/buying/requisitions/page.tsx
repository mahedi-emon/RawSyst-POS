'use client';

// Somebody asking for stock.
//
// # Asking is not buying, and the two are separate permissions
//
// B5 puts a requisition in reach of any authorised staff: it carries no cost
// and needs no buying permission, because the person who notices the shelf is
// empty is rarely the person who negotiates with suppliers. So this screen
// shows quantities and never prices, and the buyer's side of the module lives
// elsewhere.
//
// # There is no draft
//
// `RaiseRequisition` creates it `submitted`, and its comment says why: "a draft
// that nobody can see is a request that never reaches an approver, and the
// shelf stays empty while the requester believes they have asked." So the
// button says "send", not "save".

import { ClipboardCheck } from 'lucide-react';
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
import {
  REQUISITION_STATUS,
  type Requisition,
} from '@/lib/purchasing/sourcing';
import { useUrlState } from '@/lib/url-state';

function RequisitionsScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const [status, setStatus] = useUrlState('status');

  // `{requisitions: []}`, not `{data: []}` -- see useNamedList.
  const { data, isLoading, error, refetch } = useNamedList<Requisition>(
    scope ? '/purchasing/requisitions' : null,
    'requisitions',
    scope ? { ...scope, status: status || undefined } : undefined,
  );

  const rows = data?.data ?? [];

  const columns: Column<Requisition>[] = [
    {
      key: 'number',
      header: t('nx.req.colNumber'),
      primary: true,
      width: 'w-56',
      cell: (r) => {
        const shown = REQUISITION_STATUS[r.status];
        return (
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{r.requisition_no}</span>
            {shown ? <Badge tone={shown.tone}>{t(shown.key)}</Badge> : null}
          </span>
        );
      },
    },
    {
      key: 'who',
      header: t('nx.req.colWho'),
      cell: (r) => r.requested_by || '—',
    },
    {
      key: 'when',
      header: t('nx.req.colWhen'),
      secondary: true,
      width: 'w-32',
      cell: (r) => (
        <time dateTime={r.requested_at} className="num text-muted">
          {r.requested_at.slice(0, 10)}
        </time>
      ),
    },
    {
      key: 'needed',
      header: t('nx.req.colNeeded'),
      width: 'w-32',
      // A request with no date is not urgent by omission; it is a request
      // without a date, and saying so is different from showing today's.
      cell: (r) =>
        r.needed_by ? (
          <time dateTime={r.needed_by} className="num">
            {r.needed_by}
          </time>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.req.title')}
        description={t('nx.req.subtitle')}
        actions={
          <Button variant="primary" onClick={() => router.push('/buying/requisitions/new')}>
            {t('nx.req.ask')}
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
          <option value="">{t('nx.req.filterAll')}</option>
          {Object.entries(REQUISITION_STATUS).map(([value, meta]) => (
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
          icon={ClipboardCheck}
          title={t('nx.req.emptyTitle')}
          description={t('nx.req.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.req.caption')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
          onOpenRow={(r) => router.push(`/buying/requisitions/${r.id}`)}
        />
      ) : null}
    </>
  );
}

export default function RequisitionsPage() {
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <RequisitionsScreen />
      </Suspense>
    </RequirePermission>
  );
}
