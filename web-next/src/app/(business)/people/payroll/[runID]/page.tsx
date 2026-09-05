'use client';

// One payroll run.
//
// # A missing social insurance figure is said, never shown as nothing
//
// The run reports `gosi_unavailable` when the rate has not been verified
// against its official source, and the reason in its own words. That warning
// leads, because a screen showing "0.00" in the social insurance box would be
// stating a regulatory liability of nil for the month — a confirmation this
// product must never invent. The wages themselves are still right and the run
// is still usable, which is why it is a warning and not a refusal.
//
// # What it costs is not what it pays
//
// Net is what lands in people's accounts. The cost to the business is the
// gross plus the employer's own contribution, which is never deducted from
// anybody. Both are here, because reading one as the other understates the wage
// bill by the employer's share every single month.
//
// # The wage file refuses, and the refusal is the feature
//
// The Mudad layout is unverified in the regulatory register, so the route will
// not emit a file. A file in the wrong layout is not partly right: it is
// rejected, and a rejected submission can freeze a company's portal access.
// The button is offered because the rest of the check — every employee has an
// IBAN, a GOSI number, an ID — runs and is worth running; what comes back is
// shown as written.

import { useParams, useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, localeTagFor } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  allowed,
  runTone,
  SLIP_LINES,
  totalCost,
  type PayrollRun,
  type Payslip,
} from '@/lib/people/payroll';
import { monthLabel } from '@/lib/people/staff';
import { cn } from '@/lib/utils';

const STATUS_LABEL: Record<string, Key> = {
  draft: 'nx.prl.draft',
  approved: 'nx.prl.approved',
  paid: 'nx.prl.paid',
  cancelled: 'nx.prl.cancelled',
};

const SLIP_LABEL: Record<string, Key> = {
  basic: 'nx.hire.basic',
  housing: 'nx.hire.housing',
  transport: 'nx.hire.transport',
  other_allowance: 'nx.hire.otherAllowance',
  overtime: 'nx.run.overtime',
  commission: 'nx.run.commission',
  bonus: 'nx.run.bonus',
  gross: 'nx.run.gross',
  absence_deduction: 'nx.run.absence',
  gosi_employee: 'nx.run.gosiEmployee',
  advance_recovery: 'nx.run.advanceRecovery',
  other_deduction: 'nx.run.otherDeduction',
  deductions: 'nx.run.deductions',
  net: 'nx.run.net',
};

interface MoneyAccount {
  id: string;
  name: string;
  kind: string;
  currency: string;
}

function SlipCard({
  slip,
  money,
}: {
  slip: Payslip;
  money: (v: string) => string;
}) {
  const t = useT();
  return (
    <Panel title={slip.employee} flush>
      <dl className="text-body">
        {SLIP_LINES.map((line) => {
          const value = slip[line.key] as string;
          return (
            <div
              key={line.key}
              className={cn(
                'flex items-baseline justify-between gap-4 px-4 py-1.5',
                line.kind === 'total' &&
                  'border-y border-line bg-surface-sunken font-medium',
                line.key === 'net' &&
                  'border-t-[3px] border-double border-line-strong font-semibold',
              )}
            >
              <dt
                className={cn(
                  line.kind === 'deduction' ? 'text-muted' : 'text-fg',
                )}
              >
                {t(SLIP_LABEL[line.key as string] ?? 'nx.run.gross')}
              </dt>
              {/* Deductions carry their sign, because "500" against "Absence"
                  reads as pay rather than as pay taken away. */}
              <dd className="num">
                {line.kind === 'deduction' && value !== '0.00'
                  ? `− ${money(value)}`
                  : money(value)}
              </dd>
            </div>
          );
        })}
      </dl>
    </Panel>
  );
}

function RunScreen() {
  const t = useT();
  const router = useRouter();
  const params = useParams<{ runID: string }>();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayApprove = grants.can('payroll.approve');

  const { data, isLoading, error, refetch } = useApi<PayrollRun>(
    scope ? `/payroll/${params.runID}` : null,
    scope ?? undefined,
  );
  const accounts = useApiList<MoneyAccount>(
    scope && mayApprove ? '/treasury/accounts' : null,
    scope ?? undefined,
  );

  const [paying, setPaying] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [accountID, setAccountID] = useState('');
  const [paidOn, setPaidOn] = useState('');
  const [cancelReason, setCancelReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [wageFileNote, setWageFileNote] = useState<string | null>(null);

  const money = (v: string) =>
    formatMoney(v, { currency: data?.currency || currency, market });

  async function act(path: string, body: Record<string, unknown>) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setWageFileNote(null);
    try {
      await api.post(`${path}?company_id=${scope.company_id}`, body);
      setPaying(false);
      setCancelling(false);
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function buildWageFile() {
    if (!scope || !data) return;
    setBusy(true);
    setActionError(null);
    setWageFileNote(null);
    try {
      await api.post(
        `/payroll/${data.id}/wage-file?company_id=${scope.company_id}`,
        {},
      );
      setWageFileNote(t('nx.run.wageFileBuilt'));
    } catch (e) {
      // Shown in the server's words. A refusal here names an unverified
      // regulatory layout or the employees whose details are incomplete, and
      // both are exactly what the person needs to read.
      setWageFileNote(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;
  if (isLoading && !data) return <div className="h-64" aria-busy="true" />;
  if (!data) return null;

  const cost = totalCost(data);
  const slips = data.payslips ?? [];

  return (
    <>
      <PageHeader
        title={t('nx.run.title', { number: data.run_no })}
        description={monthLabel(data.period, localeTagFor(market))}
        actions={
          <Badge tone={runTone(data.status)}>
            {t(STATUS_LABEL[data.status] ?? 'nx.prl.draft')}
          </Badge>
        }
      />

      <FormError message={actionError} className="mb-4" />

      {/* Leads, because it is a figure that is missing rather than nil. */}
      {data.gosi_unavailable ? (
        <section
          className="mb-6 rounded-md border border-caution/25 bg-caution-subtle p-4"
          aria-labelledby="gosi-missing"
        >
          <h2
            id="gosi-missing"
            className="text-card-title font-semibold text-caution-fg"
          >
            {t('nx.run.gosiMissingTitle')}
          </h2>
          <p className="mt-1 max-w-prose text-body text-caution-fg">
            {t('nx.run.gosiMissingBody')}
          </p>
          {data.gosi_blocked_reason ? (
            <p className="mt-2 max-w-prose text-body text-caution-fg">
              {data.gosi_blocked_reason}
            </p>
          ) : null}
        </section>
      ) : null}

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Panel>
          <Figure label={t('nx.run.gross')} value={money(data.gross_total)} />
        </Panel>
        <Panel>
          <Figure
            label={t('nx.run.deductions')}
            value={money(data.deduction_total)}
          />
        </Panel>
        <Panel>
          <Figure label={t('nx.run.netPaid')} value={money(data.net_total)} />
        </Panel>
        <Panel>
          {/* What employing everybody cost, which is more than what was paid
              out. Null when social insurance is unknown — adding a zero would
              state the cost as if it were settled. */}
          <Figure
            label={t('nx.run.cost')}
            value={cost ? money(cost) : t('nx.run.costUnknown')}
            caption={t('nx.run.costHint')}
          />
        </Panel>
      </div>

      {mayApprove ? (
        <Panel className="mb-6" title={t('nx.run.actions')}>
          <div className="flex flex-wrap items-center gap-3">
            <Button
              variant="primary"
              disabled={busy || !allowed(data.status, 'approve')}
              onClick={() => void act(`/payroll/${data.id}/approve`, {})}
            >
              {t('nx.run.approve')}
            </Button>
            <Button
              disabled={busy || !allowed(data.status, 'pay')}
              onClick={() => setPaying((v) => !v)}
            >
              {t('nx.run.pay')}
            </Button>
            <Button
              disabled={busy || !allowed(data.status, 'wage-file')}
              onClick={() => void buildWageFile()}
            >
              {t('nx.run.wageFile')}
            </Button>
            <Button
              variant="ghost"
              disabled={busy || !allowed(data.status, 'cancel')}
              onClick={() => setCancelling((v) => !v)}
            >
              {t('nx.run.cancel')}
            </Button>
          </div>

          <p className="mt-3 max-w-prose text-caption text-muted">
            {data.status === 'draft'
              ? t('nx.run.draftHint')
              : data.status === 'approved'
                ? t('nx.run.approvedHint')
                : data.status === 'paid'
                  ? t('nx.run.paidHint')
                  : t('nx.run.cancelledHint')}
          </p>

          {wageFileNote ? (
            <p className="mt-3 max-w-prose rounded-sm border border-line bg-surface-sunken p-3 text-body">
              {wageFileNote}
            </p>
          ) : null}

          {paying ? (
            <div className="mt-4 grid gap-4 border-t border-line pt-4 sm:grid-cols-[minmax(0,1fr)_12rem_auto] sm:items-end">
              <Field
                name="account_id"
                label={t('nx.run.payFrom')}
                hint={t('nx.run.payFromHint')}
                required
              >
                <Select
                  value={accountID}
                  onChange={(e) => setAccountID(e.target.value)}
                >
                  <option value="">{t('nx.run.chooseAccount')}</option>
                  {(accounts.data?.data ?? []).map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field name="paid_on" label={t('nx.run.paidOn')}>
                <Input
                  type="date"
                  value={paidOn}
                  onChange={(e) => setPaidOn(e.target.value)}
                />
              </Field>
              <Button
                variant="primary"
                disabled={busy || !accountID}
                onClick={() =>
                  void act(`/payroll/${data.id}/pay`, {
                    account_id: accountID,
                    paid_on: paidOn,
                  })
                }
              >
                {t('nx.run.confirmPay')}
              </Button>
            </div>
          ) : null}

          {cancelling ? (
            <div className="mt-4 grid gap-4 border-t border-line pt-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
              <Field
                name="reason"
                label={t('nx.run.cancelReason')}
                hint={t('nx.run.cancelReasonHint')}
                required
              >
                <Input
                  value={cancelReason}
                  onChange={(e) => setCancelReason(e.target.value)}
                />
              </Field>
              <Button
                variant="destructive"
                disabled={busy || cancelReason.trim() === ''}
                onClick={() =>
                  void act(`/payroll/${data.id}/cancel`, {
                    reason: cancelReason.trim(),
                  })
                }
              >
                {t('nx.run.confirmCancel')}
              </Button>
            </div>
          ) : null}
        </Panel>
      ) : null}

      <h2 className="mb-3 text-card-title font-semibold text-fg">
        {t('nx.run.payslips', { count: String(slips.length) })}
      </h2>
      <div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
        {slips.map((s) => (
          <SlipCard key={s.id} slip={s} money={money} />
        ))}
      </div>

      <div className="mt-6">
        <Button variant="ghost" onClick={() => router.push('/people/payroll')}>
          {t('nx.run.backToRuns')}
        </Button>
      </div>
    </>
  );
}

export default function RunPage() {
  return (
    <RequirePermission anyOf={['payroll.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <RunScreen />
      </Suspense>
    </RequirePermission>
  );
}
