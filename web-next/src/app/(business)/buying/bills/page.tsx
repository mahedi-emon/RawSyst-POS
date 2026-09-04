'use client';

// What suppliers have asked to be paid.
//
// # Held back is the row that matters
//
// A blocked invoice is one the three-way match refused: it is recorded, and it
// is deliberately NOT in the ledger, so nothing is owed on it until somebody
// puts their name to accepting the difference. That is the only state on this
// list that needs a person, so it is the one the eye should find first.
//
// # Still owed is not the total
//
// A part-paid invoice reads differently from an unpaid one, and both read
// differently from a settled one. The list carries `outstanding` for exactly
// that reason, and shows a dash rather than a zero once nothing is left.

import { Receipt } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Select } from '@/components/ui/field';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { BILL_STATUS, BILL_STATUSES, type Bill } from '@/lib/purchasing/bills';
import { useUrlState } from '@/lib/url-state';

function BillsScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const [status, setStatus] = useUrlState('status');

  const columns: Column<Bill>[] = [
    {
      key: 'ref',
      header: t('nx.bill.colRef'),
      primary: true,
      cell: (b) => {
        const shown = BILL_STATUS[b.status];
        return (
          <span className="flex flex-wrap items-center gap-2">
            {/* The supplier's own number, which is how they will refer to it
                on the phone. Shown exactly as they wrote it. */}
            <span className="num">{b.supplier_ref}</span>
            {shown ? <Badge tone={shown.tone}>{t(shown.key)}</Badge> : null}
          </span>
        );
      },
    },
    {
      key: 'supplier',
      header: t('nx.bill.colSupplier'),
      cell: (b) => b.supplier,
    },
    {
      key: 'po',
      header: t('nx.bill.colOrder'),
      secondary: true,
      width: 'w-40',
      cell: (b) =>
        // A bill with no order behind it is ordinary -- rent, a utility -- so
        // an em dash here is a fact, not a gap.
        b.po_number ? (
          <span className="num text-muted">{b.po_number}</span>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
    {
      key: 'due',
      header: t('nx.bill.colDue'),
      secondary: true,
      width: 'w-32',
      cell: (b) => (
        <time dateTime={b.due_date} className="num text-muted">
          {b.due_date}
        </time>
      ),
    },
    {
      key: 'total',
      header: t('nx.bill.colTotal'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (b) => (
        <span className="num text-muted">
          {formatMoney(b.total_inclusive, {
            currency: b.currency || currency,
            market,
          })}
        </span>
      ),
    },
    {
      key: 'outstanding',
      header: t('nx.bill.colOutstanding'),
      numeric: true,
      width: 'w-36',
      cell: (b) =>
        isZero(b.outstanding) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num font-medium">
            {formatMoney(b.outstanding, {
              currency: b.currency || currency,
              market,
            })}
          </span>
        ),
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.bill.title')} description={t('nx.bill.subtitle')} />

      <ResourceList<Bill>
        path={scope ? '/purchasing/bills' : null}
        query={{ ...scope, status: status || undefined }}
        columns={columns}
        rowKey={(b) => b.id}
        onOpenRow={(b) => router.push(`/buying/bills/${b.id}`)}
        caption={t('nx.bill.caption')}
        searchPlaceholder={t('nx.bill.search')}
        searchLabel={t('nx.bill.searchLabel')}
        noun={t('nx.bill.noun')}
        // `ListBills` takes a status and a limit and no search term, so the
        // rows it sends are all of them and the box filters where they are.
        filterRow={(b, term) =>
          b.supplier_ref.toLowerCase().includes(term) ||
          b.supplier.toLowerCase().includes(term) ||
          (b.po_number ?? '').toLowerCase().includes(term)
        }
        filters={
          <Select
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            aria-label={t('nx.bill.filterLabel')}
            className="h-10 w-auto"
          >
            <option value="">{t('nx.bill.filterAll')}</option>
            {BILL_STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(BILL_STATUS[s]!.key)}
              </option>
            ))}
          </Select>
        }
        emptyState={
          <EmptyState
            icon={Receipt}
            title={t('nx.bill.emptyTitle')}
            description={t('nx.bill.emptyDesc')}
          />
        }
      />
    </>
  );
}

export default function BillsPage() {
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <BillsScreen />
      </Suspense>
    </RequirePermission>
  );
}
