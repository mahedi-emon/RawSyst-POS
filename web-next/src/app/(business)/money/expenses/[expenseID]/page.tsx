'use client';

// One expense voucher.
//
// # The tax is split on every line, because it is split in the books
//
// A line whose category allows recovery puts its tax into Input VAT
// Receivable; a line whose category does not absorbs it into the expense. Both
// are shown, per line, because a voucher totalling 450 with 67.50 of tax reads
// completely differently depending on which of those two it was.

import Link from 'next/link';
import { use } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { Expense, ExpenseLine } from '@/lib/money/expenses';

function ExpenseScreen({ expenseID }: { expenseID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<Expense>(
    scope ? `/expenses/${expenseID}` : null,
    scope ?? undefined,
  );

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.exp.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.exp.title')} />
        <TableSkeleton columns={5} />
      </>
    );
  }

  const money = (v: string) =>
    formatMoney(v, { currency: data.currency || currency, market });

  const columns: Column<ExpenseLine>[] = [
    {
      key: 'head',
      header: t('nx.exp.head'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>{l.head ?? '—'}</span>
          {l.description ? (
            <span className="text-caption text-muted">{l.description}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'net',
      header: t('nx.exp.net'),
      numeric: true,
      width: 'w-32',
      cell: (l) => <span className="num">{money(l.net_amount)}</span>,
    },
    {
      key: 'tax',
      header: t('nx.exp.colTax'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (l) => <span className="num text-muted">{money(l.tax_amount)}</span>,
    },
    {
      key: 'reclaim',
      header: t('nx.exp.colReclaim'),
      numeric: true,
      width: 'w-32',
      // The distinction the whole screen exists to make: an absorbed tax is
      // money gone, and a recoverable one comes back.
      cell: (l) =>
        isZero(l.tax_amount) ? (
          <span className="text-subtle">—</span>
        ) : isZero(l.tax_recoverable) ? (
          <span className="text-caption text-caution-fg">
            {t('nx.exp.notReclaimable')}
          </span>
        ) : (
          <span className="num text-positive-fg">{money(l.tax_recoverable)}</span>
        ),
    },
    {
      key: 'gross',
      header: t('nx.exp.colTotal'),
      numeric: true,
      width: 'w-32',
      // What lands in the expense account: net plus any tax that could not
      // be reclaimed, which is the whole point of the split.
      cell: (l) => <span className="num font-medium">{money(l.charge_amount)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={<span className="num">{data.expense_no}</span>}
        description={[data.description, data.expense_date, data.paid_from, data.reference]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/money/expenses"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.exp.back')}
          </Link>
        }
      />

      <Panel flush>
        <DataTable
          caption={t('nx.exp.linesCaption')}
          columns={columns}
          rows={data.lines ?? []}
          rowKey={(l) => `${l.head_id}-${l.net_amount}`}
          className="rounded-none border-0"
        />
        <dl className="flex flex-col gap-1 border-t border-line px-4 py-3 text-body">
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t('nx.exp.net')}</dt>
            <dd className="num">{money(data.subtotal_net)}</dd>
          </div>
          <div className="flex justify-between gap-4">
            <dt className="text-muted">{t('nx.exp.recoverable')}</dt>
            <dd className="num">{money(data.tax_recoverable)}</dd>
          </div>
          {!isZero(data.tax_absorbed) ? (
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.exp.absorbed')}</dt>
              <dd className="num text-caution-fg">{money(data.tax_absorbed)}</dd>
            </div>
          ) : null}
          <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
            <dt>{t('nx.exp.colTotal')}</dt>
            <dd className="num">{money(data.total_inclusive)}</dd>
          </div>
        </dl>
      </Panel>

      {!isZero(data.tax_absorbed) ? (
        <p className="mt-4 max-w-prose text-caption text-muted">
          {t('nx.exp.absorbedHint')}
        </p>
      ) : null}


    </>
  );
}

export default function ExpensePage({
  params,
}: {
  params: Promise<{ expenseID: string }>;
}) {
  const { expenseID } = use(params);
  return (
    <RequirePermission anyOf={['expense.view']}>
      <ExpenseScreen expenseID={expenseID} />
    </RequirePermission>
  );
}
