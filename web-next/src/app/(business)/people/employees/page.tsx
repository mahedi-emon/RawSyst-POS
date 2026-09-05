'use client';

// The staff directory.
//
// # A permit about to lapse leads, because it stops somebody working
//
// C5 names an Iqama and ID expiry alert, and `GET /employees/expiring` is it.
// A residency permit that runs out is not a reminder to file away — it is a
// person who cannot legally be on shift tomorrow, and a shop that finds out on
// the day has already lost the shift. So the alert sits above the directory
// rather than as a column somebody has to sort by, and it names the people.
//
// # Pay is absent, not zero
//
// A Store Manager holds `hr.view` and not `hr.view_pay`: A6.2 requires them to
// roster their branch without learning what the branch is paid. The server
// enforces it by OMITTING the pay fields. The salary column here is built from
// `maySeePay`, which asks whether the field arrived — never whether it is
// non-zero, because a commission-only salesperson genuinely earns a basic of
// nothing and must read as nothing to somebody entitled to see it.
//
// The column disappears entirely for a reader without the permission. A column
// of dashes would tell them there is a number there and they cannot have it,
// which is a different and less useful sentence than not asking.

import { UserPlus, Users } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  documentState,
  maySeePay,
  monthlyPay,
  type Employee,
} from '@/lib/people/staff';

function ExpiryAlert({ rows }: { rows: Employee[] }) {
  const t = useT();
  const router = useRouter();
  if (rows.length === 0) return null;

  const expired = rows.filter((r) => r.id_expired);
  return (
    <section
      className="mb-6 rounded-md border border-caution/25 bg-caution-subtle p-4"
      aria-labelledby="expiry-alert"
    >
      <h2 id="expiry-alert" className="text-card-title font-semibold text-caution-fg">
        {expired.length > 0
          ? t('nx.staff.expiredTitle', { count: String(expired.length) })
          : t('nx.staff.expiringTitle', { count: String(rows.length) })}
      </h2>
      <p className="mt-1 max-w-prose text-body text-caution-fg">
        {t('nx.staff.expiringBody')}
      </p>
      <ul className="mt-3 flex flex-col gap-1.5">
        {rows.map((r) => (
          <li key={r.id} className="text-body text-caution-fg">
            <button
              type="button"
              onClick={() => router.push(`/people/employees/${r.id}`)}
              className="text-start underline underline-offset-2 hover:no-underline"
            >
              {r.full_name}
            </button>{' '}
            — {r.id_expired ? t('nx.staff.expiredOn') : t('nx.staff.expiresOn')}{' '}
            <time dateTime={r.id_expires_on} className="num">
              {r.id_expires_on}
            </time>
          </li>
        ))}
      </ul>
    </section>
  );
}

function EmployeesScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const [includeLeavers, setIncludeLeavers] = useState(false);

  const { data, isLoading, error, refetch } = useApiList<Employee>(
    scope ? '/employees' : null,
    scope ? { ...scope, include_leavers: includeLeavers } : undefined,
  );
  const expiring = useApiList<Employee>(
    scope ? '/employees/expiring' : null,
    scope ?? undefined,
  );

  const rows = data?.data ?? [];
  // Presence on the first record, not on every one: the permission is the
  // caller's and does not vary row by row.
  const showsPay = rows.some(maySeePay);

  const columns: Column<Employee>[] = [
    {
      key: 'name',
      header: t('nx.staff.colName'),
      primary: true,
      cell: (e) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{e.full_name}</span>
          <span className="num text-caption text-muted">{e.employee_no}</span>
        </span>
      ),
    },
    {
      key: 'position',
      header: t('nx.staff.colPosition'),
      cell: (e) => (
        <span className="flex flex-col gap-0.5">
          <span>{e.position || '—'}</span>
          {e.department ? (
            <span className="text-caption text-muted">{e.department}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'document',
      header: t('nx.staff.colDocument'),
      width: 'w-40',
      cell: (e) => {
        const state = documentState(e);
        if (state === 'none') return <span className="text-muted">—</span>;
        return (
          <span className="flex flex-col gap-1">
            <time dateTime={e.id_expires_on} className="num text-caption">
              {e.id_expires_on}
            </time>
            {state === 'expired' ? (
              <Badge tone="critical">{t('nx.staff.expired')}</Badge>
            ) : null}
            {state === 'expiring' ? (
              <Badge tone="caution">{t('nx.staff.expiringSoon')}</Badge>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'joined',
      header: t('nx.staff.colJoined'),
      secondary: true,
      width: 'w-32',
      cell: (e) => (
        <time dateTime={e.joined_on} className="num text-muted">
          {e.joined_on}
        </time>
      ),
    },
    {
      key: 'status',
      header: t('nx.staff.colStatus'),
      width: 'w-28',
      cell: (e) =>
        e.left_on ? (
          <Badge>{t('nx.staff.left')}</Badge>
        ) : (
          <Badge tone="positive">{t('nx.staff.active')}</Badge>
        ),
    },
  ];

  // Added only when the reader is allowed the figures at all.
  if (showsPay) {
    columns.splice(3, 0, {
      key: 'pay',
      header: t('nx.staff.colPay'),
      numeric: true,
      width: 'w-36',
      cell: (e) => {
        const pay = monthlyPay(e);
        if (pay === null) return <span className="text-muted">—</span>;
        return (
          <span className="num font-medium">
            {formatMoney(pay, { currency: e.currency || currency, market })}
          </span>
        );
      },
    });
  }

  return (
    <>
      <PageHeader
        title={t('nx.staff.title')}
        description={t('nx.staff.subtitle')}
        actions={
          grants.can('hr.manage') ? (
            <Button
              variant="primary"
              onClick={() => router.push('/people/employees/new')}
            >
              <UserPlus aria-hidden="true" className="size-4" />
              {t('nx.staff.hire')}
            </Button>
          ) : null
        }
      />

      <ExpiryAlert rows={expiring.data?.data ?? []} />

      <div className="mb-4">
        <label className="flex items-center gap-2 text-label text-muted">
          <input
            type="checkbox"
            checked={includeLeavers}
            onChange={(e) => setIncludeLeavers(e.target.checked)}
            className="size-4 rounded-xs border border-input accent-primary"
          />
          {t('nx.staff.showLeavers')}
        </label>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={showsPay ? 6 : 5} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Users}
          title={t('nx.staff.emptyTitle')}
          description={t('nx.staff.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.staff.caption')}
          columns={columns}
          rows={rows}
          rowKey={(e) => e.id}
          onOpenRow={(e) => router.push(`/people/employees/${e.id}`)}
        />
      ) : null}
    </>
  );
}

export default function EmployeesPage() {
  return (
    <RequirePermission anyOf={['hr.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <EmployeesScreen />
      </Suspense>
    </RequirePermission>
  );
}
