'use client';

// Paying for a sale over months, and what is still owed on each agreement.
//
// # The sale already happened. Only the finance charge is booked here.
//
// A plan turns a credit invoice into a schedule; the goods went and the revenue
// posted when the invoice did. What this screen governs is the markup and the
// late fees — which is why cancelling an agreement is not cancelling a sale,
// and the confirmation says so.
//
// # An instalment's state is computed, never stored
//
// `installment_state` derives paid, partial, unpaid, overdue and waived from
// the money and today's date, "so it cannot go stale the way a stored status
// would overnight." A schedule read at nine in the morning and again at nine at
// night is right both times without anybody running anything.
//
// # Collecting needs a receipt that already exists
//
// `POST .../collect` takes a `receipt_id`, not an amount on its own: the money
// arrives through the receivables side and this marks it against the schedule,
// oldest instalment first. There is no `GET /receivables/receipts`, so this
// screen cannot offer a list to pick from and does not pretend to — it asks for
// the receipt and says where the reference comes from.
//
// # Accruing is company-wide, and says how much it earned
//
// It earns the finance income on instalments now due and charges the late fees.
// Running it twice in a morning is safe and should read as "nothing further was
// due", not as a failure.

import { CalendarClock } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

interface Due {
  id: string;
  seq: number;
  due_on: string;
  amount: string;
  paid: string;
  waived: string;
  late_fee: string;
  state: string;
}

interface Plan {
  id: string;
  plan_no: string;
  status: string;
  customer_id: string;
  customer?: string;
  invoice_id: string;
  principal: string;
  down_payment: string;
  financed: string;
  markup_rate: string;
  markup_amount: string;
  tenure_months: number;
  installment_amount: string;
  late_fee_flat: string;
  late_fee_rate: string;
  grace_days: number;
  currency: string;
  starts_on: string;
  guarantor_name?: string;
  guarantor_phone?: string;
  outstanding: string;
  schedule?: Due[];
}

const PLAN_TONE: Record<string, 'positive' | 'neutral' | 'critical'> = {
  active: 'positive',
  settled: 'neutral',
  defaulted: 'critical',
  cancelled: 'neutral',
};

const DUE_TONE: Record<string, 'positive' | 'caution' | 'critical' | 'neutral'> = {
  paid: 'positive',
  partial: 'caution',
  unpaid: 'neutral',
  overdue: 'critical',
  waived: 'neutral',
};

function InstallmentsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('installment.manage');

  const { data, isLoading, error, refetch } = useApiList<Plan>(
    scope ? '/installments' : null,
    { company_id: scope?.company_id },
  );

  const [openId, setOpenId] = useState<string | null>(null);
  const plan = useApi<Plan>(
    openId && scope ? `/installments/${openId}` : null,
    { company_id: scope?.company_id },
  );

  const [collecting, setCollecting] = useState(false);
  const [receiptId, setReceiptId] = useState('');
  const [amount, setAmount] = useState('');
  const [cancelling, setCancelling] = useState(false);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const rows = data?.data ?? [];
  const open = plan.data;

  function reset() {
    setCollecting(false);
    setCancelling(false);
    setReceiptId('');
    setAmount('');
    setReason('');
    setActionError(null);
    setFieldErrors(null);
  }

  async function collect() {
    if (!scope || !openId) return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    setNote(null);
    try {
      await api.post(`/installments/${openId}/collect?company_id=${scope.company_id}`, {
        receipt_id: receiptId.trim(),
        amount,
      });
      reset();
      void plan.refetch();
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    if (!scope || !openId || reason.trim() === '') return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await api.post(`/installments/${openId}/cancel?company_id=${scope.company_id}`, {
        reason: reason.trim(),
      });
      reset();
      setOpenId(null);
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function accrue() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      const out = await api.post<{ accrued?: number; charged?: number }>(
        `/installments/accrue?company_id=${scope.company_id}`,
        {},
      );
      const n = out.accrued ?? out.charged ?? 0;
      setNote(
        n > 0
          ? t('nx.ins.accrued', { count: String(n) })
          : t('nx.ins.accruedNone'),
      );
      void refetch();
      if (openId) void plan.refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Plan>[] = [
    {
      key: 'plan',
      header: t('nx.ins.plan'),
      primary: true,
      cell: (p) => (
        <span className="flex flex-col">
          <span className="num">{p.plan_no}</span>
          <span className="text-caption text-muted">{p.customer ?? '—'}</span>
        </span>
      ),
    },
    {
      key: 'terms',
      header: t('nx.ins.terms'),
      secondary: true,
      cell: (p) => (
        <span className="text-muted">
          {t('nx.ins.overMonths', { months: String(p.tenure_months) })}
          {' · '}
          <span className="num">
            {formatMoney(p.installment_amount, {
              currency: p.currency || currency,
              market,
              bare: true,
            })}
          </span>
        </span>
      ),
    },
    {
      key: 'financed',
      header: t('nx.ins.financed'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (p) => (
        <span className="num text-muted">
          {formatMoney(p.financed, { currency: p.currency || currency, market, bare: true })}
        </span>
      ),
    },
    {
      key: 'outstanding',
      header: t('nx.ins.outstanding'),
      numeric: true,
      width: 'w-36',
      cell: (p) =>
        isZero(p.outstanding) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num font-medium">
            {formatMoney(p.outstanding, { currency: p.currency || currency, market })}
          </span>
        ),
    },
    {
      key: 'status',
      header: t('nx.ins.status'),
      width: 'w-32',
      cell: (p) => (
        <Badge tone={PLAN_TONE[p.status] ?? 'neutral'}>
          {t(`nx.ins.state.${p.status}` as 'nx.ins.state.active')}
        </Badge>
      ),
    },
    {
      key: 'open',
      header: t('nx.ins.scheduleHeader'),
      width: 'w-28',
      cell: (p) => (
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            setOpenId(p.id);
            reset();
          }}
        >
          {t('nx.ins.openSchedule')}
        </Button>
      ),
    },
  ];

  const dueColumns: Column<Due>[] = [
    {
      key: 'seq',
      header: t('nx.ins.no'),
      width: 'w-16',
      cell: (d) => <span className="num">{d.seq}</span>,
    },
    {
      key: 'due_on',
      header: t('nx.ins.dueOn'),
      width: 'w-32',
      cell: (d) => <time dateTime={d.due_on}>{d.due_on}</time>,
    },
    {
      key: 'amount',
      header: t('nx.ins.amount'),
      numeric: true,
      width: 'w-32',
      cell: (d) => (
        <span className="num">
          {formatMoney(d.amount, { currency: open?.currency || currency, market, bare: true })}
        </span>
      ),
    },
    {
      key: 'paid',
      header: t('nx.ins.paid'),
      numeric: true,
      width: 'w-32',
      cell: (d) => (
        <span className="num text-muted">
          {formatMoney(d.paid, { currency: open?.currency || currency, market, bare: true })}
        </span>
      ),
    },
    {
      key: 'late',
      header: t('nx.ins.lateFee'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      cell: (d) =>
        isZero(d.late_fee) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num text-critical-fg">
            {formatMoney(d.late_fee, {
              currency: open?.currency || currency,
              market,
              bare: true,
            })}
          </span>
        ),
    },
    {
      key: 'state',
      header: t('nx.ins.instalmentState'),
      width: 'w-28',
      cell: (d) => (
        <Badge tone={DUE_TONE[d.state] ?? 'neutral'}>
          {t(`nx.ins.due.${d.state}` as 'nx.ins.due.unpaid')}
        </Badge>
      ),
    },
  ];

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.ins.title')}
        description={t('nx.ins.subtitle')}
        actions={
          mayManage ? (
            <Button
              variant="secondary"
              busy={busy}
              busyLabel={t('nx.ins.accruing')}
              onClick={() => void accrue()}
            >
              {t('nx.ins.accrue')}
            </Button>
          ) : null
        }
      />

      <FormError message={actionError} fields={fieldErrors} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {open ? (
        <Panel
          title={t('nx.ins.scheduleFor', { no: open.plan_no })}
          description={open.customer}
          className="mb-4"
          actions={
            <Button variant="ghost" onClick={() => setOpenId(null)}>
              {t('nx.ins.closePanel')}
            </Button>
          }
        >
          <dl className="mb-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-caption text-muted">{t('nx.ins.financed')}</dt>
              <dd className="num text-fg">
                {formatMoney(open.financed, { currency: open.currency, market })}
              </dd>
            </div>
            <div>
              <dt className="text-caption text-muted">{t('nx.ins.markup')}</dt>
              <dd className="num text-fg">
                {formatMoney(open.markup_amount, { currency: open.currency, market })}
              </dd>
            </div>
            <div>
              <dt className="text-caption text-muted">{t('nx.ins.downPayment')}</dt>
              <dd className="num text-fg">
                {formatMoney(open.down_payment, { currency: open.currency, market })}
              </dd>
            </div>
            <div>
              <dt className="text-caption text-muted">{t('nx.ins.outstanding')}</dt>
              <dd className="num font-medium text-fg">
                {formatMoney(open.outstanding, { currency: open.currency, market })}
              </dd>
            </div>
          </dl>

          {open.guarantor_name ? (
            <p className="mb-4 text-body text-muted">
              {t('nx.ins.guarantor', { name: open.guarantor_name })}
            </p>
          ) : null}

          <DataTable<Due>
            rows={open.schedule ?? []}
            columns={dueColumns}
            rowKey={(d) => d.id}
            caption={t('nx.ins.scheduleCaption')}
          />

          {mayManage && open.status === 'active' ? (
            <div className="mt-4 border-t border-line pt-4">
              {collecting ? (
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field
                    name="receipt_id"
                    label={t('nx.ins.receipt')}
                    hint={t('nx.ins.receiptHint')}
                  >
                    <Input
                      dir="ltr"
                      value={receiptId}
                      onChange={(e) => setReceiptId(e.target.value)}
                      required
                    />
                  </Field>
                  <Field name="amount" label={t('nx.ins.amountTaken')}>
                    <Input
                      inputMode="decimal"
                      dir="ltr"
                      value={amount}
                      onChange={(e) => setAmount(e.target.value)}
                      required
                    />
                  </Field>
                </div>
              ) : null}

              {cancelling ? (
                <Field
                  name="reason"
                  label={t('nx.ins.cancelReason')}
                  hint={t('nx.ins.cancelHint')}
                >
                  <Textarea
                    rows={2}
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                  />
                </Field>
              ) : null}

              <div className="mt-4 flex flex-wrap gap-2">
                {collecting ? (
                  <>
                    <Button
                      busy={busy}
                      disabled={receiptId.trim() === '' || amount.trim() === ''}
                      onClick={() => void collect()}
                    >
                      {t('nx.ins.markCollected')}
                    </Button>
                    <Button variant="ghost" onClick={reset}>
                      {t('nx.ins.cancelAction')}
                    </Button>
                  </>
                ) : cancelling ? (
                  <>
                    <Button
                      variant="destructive"
                      busy={busy}
                      disabled={reason.trim() === ''}
                      onClick={() => void cancel()}
                    >
                      {t('nx.ins.confirmCancel')}
                    </Button>
                    <Button variant="ghost" onClick={reset}>
                      {t('nx.ins.cancelAction')}
                    </Button>
                  </>
                ) : (
                  <>
                    <Button onClick={() => setCollecting(true)}>
                      {t('nx.ins.collect')}
                    </Button>
                    <Button variant="ghost" onClick={() => setCancelling(true)}>
                      {t('nx.ins.cancelPlan')}
                    </Button>
                  </>
                )}
              </div>
            </div>
          ) : null}
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={6} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={CalendarClock}
          title={t('nx.ins.emptyTitle')}
          description={t('nx.ins.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<Plan>
          rows={rows}
          columns={columns}
          rowKey={(p) => p.id}
          caption={t('nx.ins.caption')}
        />
      ) : null}
    </>
  );
}

export default function InstallmentsPage() {
  return (
    <RequirePermission anyOf={['installment.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <InstallmentsScreen />
      </Suspense>
    </RequirePermission>
  );
}
