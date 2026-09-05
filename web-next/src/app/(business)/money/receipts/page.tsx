'use client';

// Taking money from a customer.
//
// # The mirror of paying a supplier, and shaped the same way
//
// There is no list of receipts either — `POST /receivables/receipts` and
// `POST .../reverse` are the two routes — so this is not a ledger of payments
// taken. It is the act of taking one, and it starts where the money is owed.
//
// # The allocation is explicit
//
// A customer paying is usually paying particular invoices, and guessing which
// produces a statement they dispute. So the screen offers "settle everything"
// as one press and every figure stays editable.
//
// # A counter sale never appears here
//
// It was paid when it was rung up. Everything on this screen is an invoice on
// account.
//
// # The uuid is minted here, once per receipt
//
// "A retry after a timeout must recognise the payment rather than take it a
// second time." Pressing the button twice on a bad connection takes the money
// once, and the route answers `already_taken` with the original.

import { HandCoins } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { overAllocated, sumOf } from '@/lib/purchasing/allocate';
import { cn } from '@/lib/utils';
import { useUrlState } from '@/lib/url-state';

/** How money arrives. Cash first: it is most of it. */
const METHODS: { id: string; labelKey: Key }[] = [
  { id: 'cash', labelKey: 'nx.rcpt.mCash' },
  { id: 'bank_transfer', labelKey: 'nx.rcpt.mBank' },
  { id: 'card', labelKey: 'nx.rcpt.mCard' },
  { id: 'cheque', labelKey: 'nx.rcpt.mCheque' },
];

interface Customer {
  id: string;
  display_name?: string;
  legal_name?: string;
  name?: string;
}

interface OpenInvoice {
  invoice_id: string;
  human_number?: string;
  issue_date: string;
  due_date: string;
  on_account: string;
  /** Goods brought back, kept apart from money paid. */
  credited: string;
  received: string;
  outstanding: string;
}

interface SettledInvoice {
  invoice_id: string;
  human_number?: string;
  amount: string;
  outstanding: string;
}

interface Receipt {
  id: string;
  receipt_number: string;
  customer: string;
  amount: string;
  currency: string;
  settled: SettledInvoice[];
  already_taken: boolean;
}

function nameOf(c: Customer): string {
  return c.display_name || c.legal_name || c.name || c.id.slice(0, 8);
}

function ReceiptsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  // In the URL, so the ageing screen can send somebody straight to a customer.
  const [customerID, setCustomerID] = useUrlState('customer');
  const [method, setMethod] = useState('cash');
  const [reference, setReference] = useState('');
  const [receivedOn, setReceivedOn] = useState(() =>
    new Date().toISOString().slice(0, 10),
  );
  const [amounts, setAmounts] = useState<Record<string, string>>({});
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState<Receipt | null>(null);

  const customers = useApiList<Customer>(
    scope ? '/customers' : null,
    scope ? { ...scope, limit: 200 } : undefined,
  );
  const open = useApi<{ data: OpenInvoice[] }>(
    scope && customerID ? `/customers/${customerID}/open-invoices` : null,
    scope ?? undefined,
  );

  const invoices = open.data?.data ?? [];
  const taking = sumOf(Object.values(amounts));
  const anything = !isZero(taking);
  const money = (v: string) => formatMoney(v, { currency, market });

  function settleAll() {
    const next: Record<string, string> = {};
    for (const inv of invoices) {
      if (!isZero(inv.outstanding)) next[inv.invoice_id] = inv.outstanding;
    }
    setAmounts(next);
  }

  async function take() {
    if (!scope || !customerID || !anything) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const out = await api.post<Receipt>(
        `/receivables/receipts?company_id=${scope.company_id}`,
        {
          uuid: docUUID,
          customer_id: customerID,
          method,
          reference,
          received_on: receivedOn,
          allocations: Object.entries(amounts)
            .filter(([, amount]) => !isZero(amount) && amount.trim() !== '')
            .map(([invoice_id, amount]) => ({ invoice_id, amount })),
        },
      );
      setDone(out);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  function again() {
    setDocUUID(crypto.randomUUID());
    setAmounts({});
    setReference('');
    setDone(null);
    setError(null);
    void open.refetch();
  }

  const columns: Column<OpenInvoice>[] = [
    {
      key: 'invoice',
      header: t('nx.rcpt.colInvoice'),
      primary: true,
      cell: (i) => (
        <span className="flex flex-col gap-0.5">
          <span className="num">{i.human_number || i.invoice_id.slice(0, 8)}</span>
          <span className="text-caption text-muted">{i.issue_date}</span>
        </span>
      ),
    },
    {
      key: 'due',
      header: t('nx.rcpt.colDue'),
      secondary: true,
      width: 'w-32',
      cell: (i) => (
        <time dateTime={i.due_date} className="num text-muted">
          {i.due_date}
        </time>
      ),
    },
    {
      key: 'credited',
      header: t('nx.rcpt.credited'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      // Goods brought back, apart from money paid: a customer querying their
      // balance needs to tell the two apart.
      cell: (i) =>
        isZero(i.credited) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num text-muted">{money(i.credited)}</span>
        ),
    },
    {
      key: 'outstanding',
      header: t('nx.rcpt.colOutstanding'),
      numeric: true,
      width: 'w-36',
      cell: (i) => <span className="num font-medium">{money(i.outstanding)}</span>,
    },
    {
      key: 'paying',
      header: t('nx.rcpt.colPaying'),
      numeric: true,
      width: 'w-40',
      cell: (i) => {
        const entered = amounts[i.invoice_id] ?? '';
        const over = overAllocated(entered, i.outstanding);
        return (
          <span className="flex flex-col items-end gap-1">
            <input
              value={entered}
              onChange={(e) =>
                setAmounts((a) => ({ ...a, [i.invoice_id]: e.target.value }))
              }
              inputMode="decimal"
              aria-invalid={over || undefined}
              aria-label={`${t('nx.rcpt.colPaying')} ${i.human_number ?? ''}`}
              className={cn(
                'num h-10 w-32 rounded-sm border bg-input-bg px-2',
                'text-end tabular-nums [direction:ltr]',
                over ? 'border-critical' : 'border-input',
              )}
            />
            {over ? (
              <span className="text-caption text-critical-fg">
                {t('nx.rcpt.overAllocated')}
              </span>
            ) : null}
          </span>
        );
      },
    },
  ];

  if (done) {
    return (
      <>
        <PageHeader
          title={t('nx.rcpt.done')}
          description={t('nx.rcpt.receiptNo', { no: done.receipt_number })}
          actions={
            <Button variant="primary" onClick={again}>
              {t('nx.rcpt.another')}
            </Button>
          }
        />
        <Panel>
          <p className="num mb-4 text-figure font-semibold tabular-nums">
            {formatMoney(done.amount, { currency: done.currency || currency, market })}
          </p>
          <DataTable
            caption={t('nx.rcpt.settledCaption')}
            columns={[
              {
                key: 'invoice',
                header: t('nx.rcpt.colInvoice'),
                primary: true,
                cell: (s: SettledInvoice) => (
                  <span className="num">
                    {s.human_number || s.invoice_id.slice(0, 8)}
                  </span>
                ),
              },
              {
                key: 'amount',
                header: t('nx.rcpt.colPaying'),
                numeric: true,
                cell: (s: SettledInvoice) => (
                  <span className="num">{money(s.amount)}</span>
                ),
              },
              {
                key: 'left',
                header: t('nx.rcpt.colOutstanding'),
                numeric: true,
                cell: (s: SettledInvoice) =>
                  isZero(s.outstanding) ? (
                    <span className="text-subtle">—</span>
                  ) : (
                    <span className="num text-muted">{money(s.outstanding)}</span>
                  ),
              },
            ]}
            rows={done.settled}
            rowKey={(s) => s.invoice_id}
          />
        </Panel>
      </>
    );
  }

  return (
    <>
      <PageHeader title={t('nx.rcpt.title')} description={t('nx.rcpt.subtitle')} />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_21rem]">
        <div className="min-w-0">
          {!customerID ? (
            <EmptyState
              icon={HandCoins}
              title={t('nx.rcpt.needCustomer')}
              description={t('nx.rcpt.againstHint')}
            />
          ) : open.error ? (
            <ErrorState error={open.error} onRetry={() => void open.refetch()} />
          ) : open.isLoading ? (
            <TableSkeleton columns={5} />
          ) : invoices.length === 0 ? (
            <EmptyState
              icon={HandCoins}
              title={t('nx.rcpt.nothingOwed')}
              description={t('nx.rage.emptyDesc')}
            />
          ) : (
            <>
              <div className="mb-3 flex flex-wrap items-center gap-2">
                <Button variant="secondary" size="sm" onClick={settleAll}>
                  {t('nx.rcpt.settleAll')}
                </Button>
                <Button variant="ghost" size="sm" onClick={() => setAmounts({})}>
                  {t('nx.rcpt.clear')}
                </Button>
              </div>
              <DataTable
                caption={t('nx.rcpt.against')}
                columns={columns}
                rows={invoices}
                rowKey={(i) => i.invoice_id}
              />
            </>
          )}
        </div>

        <div className="flex flex-col gap-6">
          <Panel>
            <div className="flex flex-col gap-4">
              <Field
                name="customer_id"
                label={t('nx.rcpt.customer')}
                error={fieldErrors.customer_id}
                required
              >
                <Select
                  value={customerID}
                  onChange={(e) => {
                    setCustomerID(e.target.value);
                    // An amount typed against another customer's invoice means
                    // nothing here, and leaving it would send it.
                    setAmounts({});
                  }}
                >
                  <option value="">{t('nx.rcpt.chooseCustomer')}</option>
                  {(customers.data?.data ?? []).map((c) => (
                    <option key={c.id} value={c.id}>
                      {nameOf(c)}
                    </option>
                  ))}
                </Select>
              </Field>

              <Field name="method" label={t('nx.rcpt.method')} error={fieldErrors.method}>
                <Select value={method} onChange={(e) => setMethod(e.target.value)}>
                  {METHODS.map((m) => (
                    <option key={m.id} value={m.id}>
                      {t(m.labelKey)}
                    </option>
                  ))}
                </Select>
              </Field>

              <Field name="received_on" label={t('nx.rcpt.when')} error={fieldErrors.received_on}>
                <Input
                  type="date"
                  value={receivedOn}
                  onChange={(e) => setReceivedOn(e.target.value)}
                />
              </Field>

              <Field label={t('nx.rcpt.reference')}>
                <Input
                  value={reference}
                  onChange={(e) => setReference(e.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
            </div>
          </Panel>

          <div className="rounded-md border border-line bg-surface p-4">
            <p className="text-label text-muted">{t('nx.rcpt.total')}</p>
            <p className="num mt-0.5 text-display font-semibold tabular-nums">
              {money(taking)}
            </p>
            <Button
              variant="primary"
              busy={busy}
              busyLabel={t('nx.rcpt.taking')}
              className="mt-3 w-full"
              disabled={!customerID || !anything}
              onClick={() => void take()}
            >
              {t('nx.rcpt.take')}
            </Button>
            <p className="mt-2 text-caption text-muted">
              {!customerID
                ? t('nx.rcpt.needCustomer')
                : !anything
                  ? t('nx.rcpt.needAllocation')
                  : t('nx.rcpt.takeHint')}
            </p>
          </div>
        </div>
      </div>
    </>
  );
}

export default function ReceiptsPage() {
  return (
    <RequirePermission anyOf={['sales.receive_payment']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ReceiptsScreen />
      </Suspense>
    </RequirePermission>
  );
}
