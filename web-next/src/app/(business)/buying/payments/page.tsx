'use client';

// Paying a supplier.
//
// # There is no list of payments, and the screen is shaped by that
//
// `POST /purchasing/payments` and `POST .../reverse` are the only two routes.
// Nothing lists what has been paid, so this is not a ledger of payments — it is
// the act of making one, and it starts where the money is owed.
//
// # The allocation is explicit, and that is the point
//
// `PaySupplier` refuses an empty allocation, and the Go comment says why: "a
// shop paying a supplier is usually paying specific invoices they have agreed,
// and guessing which ones would produce a remittance the supplier disputes —
// which is the thing that turns a payment into a week of emails." So the screen
// offers "settle everything" as one press, and every figure stays editable.
//
// # A held-back invoice cannot be paid
//
// A blocked bill is deliberately outside the ledger: nothing is owed on it
// until somebody accepts the difference. Its row says so and takes no amount,
// rather than accepting one and being refused.
//
// # The uuid is minted here, once per payment
//
// The route is idempotent on it — "a payment must carry an identifier so a
// retry does not pay twice" — and answers `already_paid: true` with the
// original. Pressing the button twice on a bad connection pays once.

import { Banknote } from 'lucide-react';
import Link from 'next/link';
import { Suspense, useMemo, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { sumOf, overAllocated } from '@/lib/purchasing/allocate';
import type { Bill } from '@/lib/purchasing/bills';

interface Supplier {
  id: string;
  code: string;
  legal_name: string;
  outstanding: string;
}

interface SettledBill {
  bill_id: string;
  supplier_ref?: string;
  amount: string;
}

interface Payment {
  id: string;
  payment_number: string;
  supplier: string;
  amount: string;
  currency: string;
  settled: SettledBill[];
  already_paid: boolean;
}

/**
 * How a supplier gets paid.
 *
 * Drawn from `sales_tender_method_valid`, which is the vocabulary this product
 * already uses for money changing hands, narrowed to the four that make sense
 * for a business paying its supplier — mada and Apple Pay are retail tenders.
 * The column itself has no enum, so this is a considered shortlist rather than
 * a constraint, and that is why the field is a select and not a free box: a
 * method typed four different ways is four methods in a report.
 */
const METHODS: ReadonlyArray<{ value: string; key: Key }> = [
  { value: 'bank_transfer', key: 'nx.pay.mBank' },
  { value: 'cheque', key: 'nx.pay.mCheque' },
  { value: 'cash', key: 'nx.pay.mCash' },
  { value: 'sadad', key: 'nx.pay.mSadad' },
];

function PaymentsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const suppliers = useApiList<Supplier>(
    scope ? '/purchasing/suppliers' : null,
    scope ?? undefined,
  );
  // Every bill, filtered to this supplier here: `ListBills` takes a status and
  // a limit and no supplier, so the narrowing happens where the rows are.
  const bills = useApiList<Bill>(
    scope ? '/purchasing/bills' : null,
    scope ? { ...scope, limit: 200 } : undefined,
  );

  const [supplierId, setSupplierId] = useState('');
  const [allocations, setAllocations] = useState<Record<string, string>>({});
  const [method, setMethod] = useState('bank_transfer');
  const [reference, setReference] = useState('');
  const [paidOn, setPaidOn] = useState(() => new Date().toISOString().slice(0, 10));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [paid, setPaid] = useState<Payment | null>(null);
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());

  const supplier = (suppliers.data?.data ?? []).find((s) => s.id === supplierId);

  const owing = useMemo(
    () =>
      (bills.data?.data ?? []).filter(
        (b) => b.supplier_id === supplierId && !isZero(b.outstanding),
      ),
    [bills.data, supplierId],
  );

  const money = (v: string) => formatMoney(v, { currency, market });
  const total = sumOf(Object.values(allocations));

  function settleEverything() {
    const next: Record<string, string> = {};
    for (const b of owing) {
      // A held-back invoice is not payable, so "settle everything" leaves it
      // alone rather than filling in a figure the server will refuse.
      if (b.status === 'blocked') continue;
      next[b.id] = b.outstanding;
    }
    setAllocations(next);
  }

  function reset() {
    setAllocations({});
    setReference('');
    setPaid(null);
    setError(null);
    setDocUUID(crypto.randomUUID());
  }

  async function pay() {
    if (!scope || !supplierId) return;
    setError(null);
    setFieldErrors({});

    const lines = Object.entries(allocations)
      .filter(([, amount]) => !isZero(amount))
      .map(([bill_id, amount]) => ({ bill_id, amount }));

    if (lines.length === 0) return setError(t('nx.pay.needBills'));
    if (method === '') return setError(t('nx.pay.needMethod'));

    setBusy(true);
    try {
      const out = await api.post<Payment>(
        `/purchasing/payments?company_id=${scope.company_id}`,
        {
          uuid: docUUID,
          supplier_id: supplierId,
          method,
          reference,
          paid_on: paidOn,
          allocations: lines,
        },
      );
      setPaid(out);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (paid) {
    return (
      <>
        <PageHeader
          title={t('nx.pay.doneTitle', { number: paid.payment_number })}
          description={paid.supplier}
        />
        {paid.already_paid ? (
          <Panel className="mb-6">
            <p className="text-body text-fg">{t('nx.pay.doneAgain')}</p>
          </Panel>
        ) : null}
        <Panel title={t('nx.pay.settled')} className="mb-6">
          <ul className="flex flex-col gap-2">
            {paid.settled.map((s) => (
              <li
                key={s.bill_id}
                className="flex justify-between gap-4 text-body"
              >
                <span className="num text-muted">
                  {s.supplier_ref || s.bill_id.slice(0, 8)}
                </span>
                <span className="num">{money(s.amount)}</span>
              </li>
            ))}
            <li className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
              <span>{t('nx.pay.total')}</span>
              <span className="num">{money(paid.amount)}</span>
            </li>
          </ul>
        </Panel>
        <div className="flex flex-wrap gap-3">
          <Button variant="primary" onClick={reset}>
            {t('nx.pay.payAnother')}
          </Button>
          <Link
            href="/buying/bills"
            className="inline-flex h-10 items-center rounded-sm border border-line-strong bg-surface px-3 text-body font-medium hover:border-primary"
          >
            {t('nx.pay.viewBills')}
          </Link>
        </div>
      </>
    );
  }

  return (
    <>
      <PageHeader title={t('nx.pay.title')} description={t('nx.pay.subtitle')} />

      <FormError message={error} className="mb-4" />

      <Panel className="mb-6">
        <Field label={t('nx.pay.whichSupplier')} error={fieldErrors.supplier_id}>
          <Select
            value={supplierId}
            onChange={(e) => {
              setSupplierId(e.target.value);
              setAllocations({});
              setDocUUID(crypto.randomUUID());
            }}
          >
            <option value="">{t('nx.pay.chooseSupplier')}</option>
            {(suppliers.data?.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {`${s.code} · ${s.legal_name}`}
              </option>
            ))}
          </Select>
        </Field>
        {supplier && !isZero(supplier.outstanding) ? (
          <p className="mt-2 text-body text-muted">
            {t('nx.pay.owedTotal', { amount: money(supplier.outstanding) })}
          </p>
        ) : null}
      </Panel>

      {supplierId === '' ? (
        <p className="text-body text-muted">{t('nx.pay.pickFirst')}</p>
      ) : null}

      {supplierId !== '' && bills.isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : null}

      {supplierId !== '' && !bills.isLoading && owing.length === 0 ? (
        <EmptyState
          icon={Banknote}
          title={t('nx.pay.nothingOwed')}
          description={t('nx.pay.nothingOwedDesc')}
        />
      ) : null}

      {owing.length > 0 ? (
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
          <Panel
            title={t('nx.pay.whichBills')}
            actions={
              <div className="flex gap-2">
                <Button variant="secondary" size="sm" onClick={settleEverything}>
                  {t('nx.pay.payAll')}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setAllocations({})}
                >
                  {t('nx.pay.payNone')}
                </Button>
              </div>
            }
          >
            <ul className="flex flex-col divide-y divide-line">
              {owing.map((b) => {
                const held = b.status === 'blocked';
                const value = allocations[b.id] ?? '';
                const over = overAllocated(value, b.outstanding);
                return (
                  <li key={b.id} className="py-3 first:pt-0 last:pb-0">
                    <div className="flex flex-wrap items-baseline justify-between gap-2">
                      <span className="num text-body text-fg">
                        {b.supplier_ref}
                      </span>
                      <span className="text-caption text-muted">
                        {t('nx.pay.colDue')}{' '}
                        <time dateTime={b.due_date} className="num">
                          {b.due_date}
                        </time>
                        {' · '}
                        {t('nx.pay.colOwed')}{' '}
                        <span className="num text-fg">{money(b.outstanding)}</span>
                      </span>
                    </div>

                    {held ? (
                      <p className="mt-2">
                        <Badge tone="critical">
                          {t('nx.pay.blockedNotPayable')}
                        </Badge>
                      </p>
                    ) : (
                      <div className="mt-2 max-w-48">
                        <Field
                          label={t('nx.pay.colPaying')}
                          error={over ? t('nx.pay.overAllocated') : undefined}
                        >
                          <Input
                            value={value}
                            onChange={(e) =>
                              setAllocations((a) => ({
                                ...a,
                                [b.id]: e.target.value,
                              }))
                            }
                            inputMode="decimal"
                            autoComplete="off"
                          />
                        </Field>
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          </Panel>

          <div className="flex flex-col gap-6">
            <Panel title={t('nx.pay.howPaid')}>
              <div className="flex flex-col gap-4">
                <Field label={t('nx.pay.method')} error={fieldErrors.method} required>
                  <Select
                    value={method}
                    onChange={(e) => setMethod(e.target.value)}
                  >
                    {METHODS.map((m) => (
                      <option key={m.value} value={m.value}>
                        {t(m.key)}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field
                  label={t('nx.pay.reference')}
                  hint={t('nx.pay.referenceHint')}
                  error={fieldErrors.reference}
                >
                  <Input
                    value={reference}
                    onChange={(e) => setReference(e.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </Field>
                <Field label={t('nx.pay.paidOn')} error={fieldErrors.paid_on}>
                  <Input
                    type="date"
                    value={paidOn}
                    onChange={(e) => setPaidOn(e.target.value)}
                  />
                </Field>
              </div>
            </Panel>

            <Panel>
              <div className="flex items-baseline justify-between gap-4">
                <span className="text-body font-semibold">{t('nx.pay.total')}</span>
                <span className="num text-page-title font-semibold">
                  {money(total)}
                </span>
              </div>
              <Button
                variant="primary"
                className="mt-4 w-full"
                busy={busy}
                disabled={isZero(total)}
                onClick={() => void pay()}
              >
                {t('nx.pay.send')}
              </Button>
            </Panel>
          </div>
        </div>
      ) : null}
    </>
  );
}

export default function PaymentsPage() {
  return (
    <RequirePermission anyOf={['purchasing.pay_supplier']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <PaymentsScreen />
      </Suspense>
    </RequirePermission>
  );
}
