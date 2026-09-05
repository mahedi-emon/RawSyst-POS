'use client';

// The chart of accounts.
//
// # Grouped by kind, in balance-sheet order
//
// Assets, liabilities, equity, revenue, expenses. Not alphabetical and not by
// code: the order is what makes the chart readable as a set of statements
// rather than as a numbered list, and it is the order an accountant already
// carries in their head.
//
// # A header is shown, and marked
//
// "5000 Operating Expenses" is what makes "5100 Cost of Goods Sold" legible.
// Hiding headers would leave a flat list of codes; offering them for posting
// would be offering the mistake that makes a chart stop adding up. So they are
// present, quiet, and labelled.
//
// # The balance is signed, not normalised
//
// Debits less credits, as the ledger holds it. A liability showing a debit
// balance and an asset showing a credit one are both worth seeing at a glance,
// and flipping the sign per account type would hide exactly those two.

import { BookOpen } from 'lucide-react';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Checkbox } from '@/components/ui/field';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import {
  ACCOUNT_TYPES,
  type Account,
  type AccountType,
} from '@/lib/accounting/journals';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { useUrlFlag } from '@/lib/url-state';

const TYPE_LABEL: Record<AccountType, Key> = {
  asset: 'nx.coa.tAsset',
  liability: 'nx.coa.tLiability',
  equity: 'nx.coa.tEquity',
  revenue: 'nx.coa.tRevenue',
  expense: 'nx.coa.tExpense',
};

function ChartScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const [retired, setRetired] = useUrlFlag('retired');

  const { data, isLoading, error, refetch } = useApiList<Account>(
    scope ? '/accounting/chart' : null,
    scope ? { ...scope, include_inactive: retired ? 'true' : undefined } : undefined,
  );

  const rows = data?.data ?? [];
  const money = (v: string) => formatMoney(v, { currency, market });

  const columns: Column<Account>[] = [
    {
      key: 'code',
      header: t('nx.coa.colCode'),
      width: 'w-24',
      cell: (a) => <span className="num text-muted">{a.code}</span>,
    },
    {
      key: 'name',
      header: t('nx.coa.colName'),
      primary: true,
      cell: (a) => (
        <span className="flex flex-wrap items-center gap-2">
          {/* A header is not indented -- it is the thing others sit under.
              Its children are, so the shape of the chart is visible without
              a tree control nobody needs on a hundred rows. */}
          <span className={a.is_postable ? 'ps-4' : 'font-medium'}>{a.name}</span>
          {!a.is_postable ? <Badge>{t('nx.coa.header')}</Badge> : null}
          {a.is_control ? <Badge tone="info">{t('nx.coa.control')}</Badge> : null}
          {!a.is_active ? <Badge tone="neutral">{t('nx.coa.retired')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'role',
      header: t('nx.coa.colRole'),
      secondary: true,
      width: 'w-48',
      // What the posting rules call it. This is what makes the chart legible
      // as a system: an owner asking "where does VAT go" is asking which
      // account holds `output_vat`.
      cell: (a) =>
        a.role ? (
          <code className="num text-caption text-muted">{a.role}</code>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'balance',
      header: t('nx.coa.colBalance'),
      numeric: true,
      width: 'w-40',
      cell: (a) =>
        isZero(a.balance) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num">{money(a.balance)}</span>
        ),
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.coa.title')} description={t('nx.coa.subtitle')} />

      <div className="mb-4">
        <Checkbox
          label={t('nx.coa.showRetired')}
          checked={retired}
          onChange={(e) => setRetired(e.target.checked)}
        />
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={4} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={BookOpen}
          title={t('nx.coa.emptyTitle')}
          description={t('nx.coa.emptyDesc')}
        />
      ) : null}

      {/* One table per kind, in balance-sheet order. A single table sorted by
          code would put revenue between two expense ranges and read as a list
          of numbers rather than as a set of statements. */}
      {ACCOUNT_TYPES.map((kind) => {
        const group = rows.filter((a) => a.type === kind);
        if (group.length === 0) return null;
        return (
          <section key={kind} className="mb-8">
            <h2 className="mb-2 text-card-title font-semibold text-fg">
              {t(TYPE_LABEL[kind])}
            </h2>
            <DataTable
              caption={`${t(TYPE_LABEL[kind])} — ${t('nx.coa.caption')}`}
              columns={columns}
              rows={group}
              rowKey={(a) => a.id}
            />
          </section>
        );
      })}

      {/* A footnote rather than a tooltip on the badge. `title` is not
          reachable by keyboard, is not announced reliably, and never appears
          on a touch screen -- which is where half of this product is read. */}
      {rows.some((a) => a.is_control) ? (
        <p className="max-w-prose text-caption text-muted">
          <strong className="font-medium">{t('nx.coa.control')}</strong>{' '}
          {t('nx.coa.controlHint')}
        </p>
      ) : null}
    </>
  );
}

export default function ChartPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ChartScreen />
      </Suspense>
    </RequirePermission>
  );
}
