'use client';

// One customer: what they owe, and how they came to owe it.
//
// # The statement is the screen
//
// A customer record is a name and a phone number, and nobody opens it for
// those. They open it to answer "how much, and why" -- so the khata is the body
// of the page, with the running balance beside every row, and the summary above
// it is three figures rather than a form.
//
// # Owed, limit, and left are three different numbers
//
// `balance` is what they owe. `credit_limit` is what they may owe. `available`
// is the difference, and it is ABSENT rather than zero when no limit is set --
// which means no credit at all, not unlimited credit. Getting that backwards
// would let a shop sell on account to somebody who was never approved for it,
// so the screen says which case it is in words.

import { ArrowLeft, ReceiptText } from 'lucide-react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useState } from 'react';

import { Can, RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

import type { CustomerRow } from '../page';

interface LedgerRow {
  date: string;
  kind: string;
  reference: string;
  charged?: string;
  received?: string;
  balance: string;
  due_date?: string;
  source_id?: string;
  reverses_id?: string;
  reversed?: boolean;
}

interface Ledger {
  customer: CustomerRow;
  rows: LedgerRow[];
  closing: string;
  base_currency: string;
}

interface OpenInvoice {
  invoice_id: string;
  human_number?: string;
  issue_date: string;
  due_date: string;
  on_account: string;
  credited: string;
  received: string;
  outstanding: string;
}

function CustomerScreen() {
  const t = useT();
  const params = useParams<{ customerId: string }>();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const id = params.customerId;

  const ledger = useApi<Ledger>(
    scope && id ? `/customers/${id}/ledger` : null,
    scope ?? undefined,
  );
  const open = useApiList<OpenInvoice>(
    scope && id ? `/customers/${id}/open-invoices` : null,
    scope ?? undefined,
  );

  // Declared above the early return below, because a hook cannot be called
  // conditionally and the error branch returns before the rest of the screen.
  const [editingLimit, setEditingLimit] = useState(false);
  const [limit, setLimit] = useState('');
  const [busy, setBusy] = useState(false);
  const [limitError, setLimitError] = useState<string | null>(null);
  const [limitFields, setLimitFields] = useState<Record<string, string> | null>(null);

  async function saveLimit(value: string) {
    if (!scope || !id) return;
    setBusy(true);
    setLimitError(null);
    setLimitFields(null);
    try {
      // An empty string is how the server is told there is no limit, which is
      // not the same as a limit of zero: no limit means nothing may go on
      // account at all, and zero would mean the same thing by accident while
      // reading as a deliberate figure somebody chose.
      await api.post(
        `/customers/${id}/credit-limit?company_id=${scope.company_id}`,
        { credit_limit: value },
      );
      setEditingLimit(false);
      void ledger.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setLimitFields(e.fields);
      setLimitError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const money = (v: string | null | undefined) =>
    formatMoney(v ?? null, {
      currency: ledger.data?.base_currency ?? currency,
      market,
      bare: true,
    });

  if (ledger.error) {
    return <ErrorState error={ledger.error} onRetry={() => void ledger.refetch()} />;
  }

  const customer = ledger.data?.customer;

  const ledgerColumns: Column<LedgerRow>[] = [
    {
      key: 'date',
      header: t('nx.cust.colDate'),
      primary: true,
      width: 'w-32',
      cell: (r) => <time dateTime={r.date}>{r.date}</time>,
    },
    {
      key: 'kind',
      header: t('nx.cust.colKind'),
      cell: (r) => (
        <span className="flex items-center gap-2">
          <span className="capitalize">{r.kind.replace(/_/g, ' ')}</span>
          {r.reversed && <Badge tone="caution">{t('nx.cust.reversed')}</Badge>}
        </span>
      ),
    },
    {
      key: 'reference',
      header: t('nx.cust.colReference'),
      secondary: true,
      cell: (r) => <span className="num text-muted">{r.reference || '—'}</span>,
    },
    {
      key: 'charged',
      header: t('nx.cust.colCharged'),
      numeric: true,
      width: 'w-28',
      cell: (r) => (r.charged && !isZero(r.charged) ? money(r.charged) : '—'),
    },
    {
      key: 'received',
      header: t('nx.cust.colReceived'),
      numeric: true,
      width: 'w-28',
      cell: (r) =>
        r.received && !isZero(r.received) ? (
          <span className="text-positive-fg">{money(r.received)}</span>
        ) : (
          '—'
        ),
    },
    {
      key: 'balance',
      header: t('nx.cust.colRunning'),
      numeric: true,
      width: 'w-32',
      cell: (r) => <span className="font-medium">{money(r.balance)}</span>,
    },
  ];

  const openColumns: Column<OpenInvoice>[] = [
    {
      key: 'invoice',
      header: t('nx.cust.colInvoice'),
      primary: true,
      cell: (i) => (
        <span className="num">{i.human_number || i.invoice_id.slice(0, 8)}</span>
      ),
    },
    {
      key: 'issued',
      header: t('nx.cust.colIssued'),
      secondary: true,
      cell: (i) => <time dateTime={i.issue_date}>{i.issue_date}</time>,
    },
    {
      key: 'due',
      header: t('nx.cust.colDue'),
      cell: (i) => {
        // Compared as ISO date strings, which sort correctly without becoming
        // Date objects and picking up a timezone the shop did not ask for.
        const overdue = i.due_date < new Date().toISOString().slice(0, 10);
        return (
          <span className="flex items-center gap-2">
            <time dateTime={i.due_date}>{i.due_date}</time>
            {overdue && <Badge tone="critical">{t('nx.cust.overdue')}</Badge>}
          </span>
        );
      },
    },
    {
      key: 'outstanding',
      header: t('nx.cust.colOutstanding'),
      numeric: true,
      width: 'w-32',
      cell: (i) => <span className="font-medium">{money(i.outstanding)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        breadcrumb={
          <Link
            href="/customers"
            className="mb-1 inline-flex items-center gap-1 text-label text-muted hover:text-fg"
          >
            <ArrowLeft className="size-3.5 rtl:rotate-180" aria-hidden="true" />
            {t('nx.cust.backToList')}
          </Link>
        }
        title={customer?.name ?? '—'}
        description={
          customer
            ? [
                customer.code,
                customer.phone,
                customer.customer_type === 'wholesale'
                  ? t('nx.cust.wholesale')
                  : t('nx.cust.retail'),
              ]
                .filter(Boolean)
                .join(' · ')
            : undefined
        }
      />

      {/* Three figures, ruled together rather than floated apart: they are one
          answer read across, not three cards. */}
      <section className="mb-6 grid gap-px overflow-hidden rounded-md border border-line bg-line sm:grid-cols-3">
        <Figure
          label={t('nx.cust.owes')}
          value={customer ? money(customer.balance) : '—'}
          currency={ledger.data?.base_currency ?? currency}
        />
        <Figure
          label={t('nx.cust.creditLimit')}
          value={customer?.credit_limit ? money(customer.credit_limit) : '—'}
          currency={ledger.data?.base_currency ?? currency}
          caption={customer && !customer.credit_limit ? t('nx.cust.noLimitSet') : undefined}
        />
        <Figure
          label={t('nx.cust.creditLeft')}
          value={customer?.available ? money(customer.available) : '—'}
          currency={ledger.data?.base_currency ?? currency}
          caption={
            customer
              ? customer.payment_terms_days > 0
                ? t('nx.cust.termsDays', { days: customer.payment_terms_days })
                : t('nx.cust.termsImmediate')
              : undefined
          }
        />
      </section>

      {/* Its own permission, and its own panel.
          `customers.set_credit_limit` is separate from `customers.manage`
          because raising a limit is how a business loses money slowly -- the
          backend gives it its own route and its own grant, and a control
          folded into the general edit form would hand it to everybody who can
          correct a phone number. */}
      <Can permission="customers.set_credit_limit">
        {editingLimit ? (
          <Panel title={t('nx.cust.limitTitle', { name: customer?.name ?? '' })} className="mb-6">
            <Field
              name="credit_limit"
              label={t('nx.cust.limitField')}
              hint={t('nx.cust.limitHint')}
              error={limitFields?.credit_limit}
            >
              <Input
                dir="ltr"
                inputMode="decimal"
                className="num"
                value={limit}
                onChange={(e) => setLimit(e.target.value)}
              />
            </Field>

            {limitError && <FormError message={limitError} />}

            <p className="mt-3 text-caption text-muted">{t('nx.cust.limitWhy')}</p>

            <div className="mt-4 flex flex-wrap gap-2">
              <Button
                variant="primary"
                onClick={() => void saveLimit(limit)}
                disabled={busy}
              >
                {t('nx.cust.limitSave')}
              </Button>
              {customer?.credit_limit ? (
                <Button
                  variant="destructive"
                  onClick={() => void saveLimit('')}
                  disabled={busy}
                >
                  {t('nx.cust.limitClear')}
                </Button>
              ) : null}
              <Button
                variant="ghost"
                onClick={() => setEditingLimit(false)}
                disabled={busy}
              >
                {t('nx.cust.limitCancel')}
              </Button>
            </div>
          </Panel>
        ) : (
          <div className="mb-6">
            <Button
              onClick={() => {
                setLimit(customer?.credit_limit ?? '');
                setLimitError(null);
                setLimitFields(null);
                setEditingLimit(true);
              }}
            >
              {t('nx.cust.setLimit')}
            </Button>
          </div>
        )}
      </Can>

      <div className="flex flex-col gap-6">
        <Panel
          title={t('nx.cust.ledgerTitle')}
          description={t('nx.cust.ledgerDesc')}
          flush
        >
          {ledger.isLoading && <TableSkeleton columns={6} rows={5} />}

          {!ledger.isLoading && (ledger.data?.rows.length ?? 0) === 0 && (
            <div className="p-4">
              <EmptyState
                icon={ReceiptText}
                title={t('nx.cust.ledgerEmptyTitle')}
                description={t('nx.cust.ledgerEmptyDesc')}
              />
            </div>
          )}

          {(ledger.data?.rows.length ?? 0) > 0 && ledger.data && (
            <DataTable
              caption={t('nx.cust.ledgerCaption')}
              columns={ledgerColumns}
              rows={ledger.data.rows}
              rowKey={(r, i) => `${r.date}-${r.reference}-${i}`}
              className="rounded-none border-0"
              totals={
                <>
                  <td className="px-3 py-2.5" colSpan={5}>
                    {t('nx.cust.closing')}
                  </td>
                  <td className="num px-3 py-2.5 text-end tabular-nums">
                    {money(ledger.data.closing)}
                  </td>
                </>
              }
            />
          )}
        </Panel>

        <Panel
          title={t('nx.cust.openInvoices')}
          description={t('nx.cust.openInvoicesDesc')}
          flush
        >
          {open.isLoading && <TableSkeleton columns={4} rows={3} />}

          {!open.isLoading && (open.data?.data.length ?? 0) === 0 && (
            <div className="p-4">
              <EmptyState
                title={t('nx.cust.allSettled')}
                description={t('nx.cust.allSettledDesc')}
              />
            </div>
          )}

          {(open.data?.data.length ?? 0) > 0 && open.data && (
            <DataTable
              caption={t('nx.cust.openInvoices')}
              columns={openColumns}
              rows={open.data.data}
              rowKey={(i) => i.invoice_id}
              className="rounded-none border-0"
            />
          )}
        </Panel>
      </div>
    </>
  );
}

function Figure({
  label,
  value,
  currency,
  caption,
}: {
  label: string;
  value: string;
  currency: string;
  caption?: string;
}) {
  return (
    <div className="bg-surface p-4">
      <p className="text-label text-muted">{label}</p>
      <p
        className={cn(
          'num mt-1 flex items-baseline gap-1.5 text-display font-semibold tabular-nums tracking-tight',
        )}
      >
        {value !== '—' && (
          <span className="text-lede font-medium text-muted">{currency}</span>
        )}
        {value}
      </p>
      {caption && <p className="mt-1 text-caption text-subtle">{caption}</p>}
    </div>
  );
}

export default function CustomerDetailPage() {
  return (
    <RequirePermission anyOf={['customers.view']}>
      <CustomerScreen />
    </RequirePermission>
  );
}
