'use client';

// Customers.
//
// # What a cashier and an owner both need first
//
// Not the address. What this customer OWES and how much credit is left, which
// is the question asked before a sale goes on account and the question asked
// when somebody rings up about their balance. The API puts both on the list row
// for exactly that reason, so the table leads with them.
//
// # Three permissions, not one
//
// `customers.view` opens the screen. `customers.manage` adds and edits.
// `customers.set_credit_limit` is separate again, because raising a limit is
// how a business loses money slowly and the backend gives it its own route. The
// New customer button is behind `Can`, and the limit control on the detail
// screen is behind its own.

import { UserPlus, Users } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { useState } from 'react';

import { Can, RequirePermission } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import { Checkbox } from '@/components/ui/field';
import type { Column } from '@/components/ui/table';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

export interface CustomerRow {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  customer_type: string;
  phone?: string;
  email?: string;
  payment_terms_days: number;
  /** Empty when no limit is set, which means no credit at all. */
  credit_limit?: string;
  balance: string;
  /** The limit less the balance. Empty when there is no limit. */
  available?: string;
  currency?: string;
  is_active: boolean;
}

function CustomersScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const [includeInactive, setIncludeInactive] = useState(false);

  const money = (v: string | undefined, row: CustomerRow) =>
    formatMoney(v ?? null, {
      currency: row.currency ?? currency,
      market,
      bare: true,
    });

  const columns: Column<CustomerRow>[] = [
    {
      key: 'name',
      header: t('nx.cust.colName'),
      primary: true,
      cell: (c) => (
        <span className="flex items-center gap-2">
          {c.name}
          {c.customer_type === 'wholesale' && (
            <Badge tone="info">{t('nx.cust.wholesale')}</Badge>
          )}
          {!c.is_active && <Badge>{t('nx.cust.inactive')}</Badge>}
        </span>
      ),
    },
    {
      key: 'code',
      header: t('nx.cust.colCode'),
      secondary: true,
      cell: (c) => <span className="num text-muted">{c.code}</span>,
    },
    {
      key: 'phone',
      header: t('nx.cust.colPhone'),
      secondary: true,
      // A phone number is read left to right in every script, like a total.
      cell: (c) => <span className="num text-muted">{c.phone || '—'}</span>,
    },
    {
      key: 'terms',
      header: t('nx.cust.colTerms'),
      secondary: true,
      width: 'w-28',
      cell: (c) =>
        c.payment_terms_days > 0
          ? t('nx.cust.termsDays', { days: c.payment_terms_days })
          : t('nx.cust.termsImmediate'),
    },
    {
      key: 'balance',
      header: t('nx.cust.colBalance'),
      numeric: true,
      width: 'w-32',
      cell: (c) => money(c.balance, c),
    },
    {
      key: 'available',
      header: t('nx.cust.colAvailable'),
      numeric: true,
      width: 'w-32',
      // Empty means no limit is SET, which means no credit at all -- a
      // different thing from a limit of zero, and the reason this shows an em
      // dash rather than 0.00.
      cell: (c) => (
        <span className={c.available ? undefined : 'text-subtle'}>
          {c.available ? money(c.available, c) : '—'}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.cust.title')}
        description={t('nx.cust.subtitle')}
        actions={
          <Can permission="customers.manage">
            <Button variant="primary">
              <UserPlus aria-hidden="true" />
              {t('nx.cust.new')}
            </Button>
          </Can>
        }
      />

      <ResourceList<CustomerRow>
        path={scope ? '/customers' : null}
        query={{
          ...scope,
          // The backend reads the literal string "true"; anything else is
          // false, so the flag is only sent when it is on.
          ...(includeInactive ? { include_inactive: 'true' } : {}),
        }}
        columns={columns}
        rowKey={(c) => c.id}
        onOpenRow={(c) => router.push(`/customers/${c.id}`)}
        caption={t('nx.cust.caption')}
        searchPlaceholder={t('nx.cust.searchPlaceholder')}
        searchLabel={t('nx.cust.searchLabel')}
        noun={t('nx.cust.customers')}
        filters={
          <Checkbox
            checked={includeInactive}
            onChange={(e) => setIncludeInactive(e.target.checked)}
            label={t('nx.cust.showInactive')}
            className="ms-1"
          />
        }
        emptyState={
          <EmptyState
            icon={Users}
            title={t('nx.cust.emptyTitle')}
            description={t('nx.cust.emptyDesc')}
            action={
              <Can permission="customers.manage">
                <Button variant="primary">{t('nx.cust.new')}</Button>
              </Can>
            }
          />
        }
      />
    </>
  );
}

export default function CustomersPage() {
  return (
    <RequirePermission anyOf={['customers.view']}>
      <CustomersScreen />
    </RequirePermission>
  );
}
