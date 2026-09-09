'use client';

// Matching what the acquirer deposited against what the till took.
//
// # The whole module, in one sentence from the blueprint
//
// "A customer pays SAR 1,000 by card, but the bank deposits only SAR 985 two
// days later. Without this module the books never balance and the Owner never
// knows their real card cost."
//
// # The fee is a fact, not a rate
//
// Nothing here asks for a percentage. The fee is the difference between what
// was taken and what arrived, so the form takes the figure off the bank
// statement and shows the difference it implies BEFORE anything posts. A
// configured rate would be a forecast, and a forecast posted into the ledger
// disagrees with the bank the first time the contract or the scheme mix says
// otherwise — in the one account whose whole job is to reach zero.
//
// # Cash is deliberately absent
//
// It never clears through an acquirer. It goes into a drawer and is counted at
// the end of a shift, which is what the Z report and the cash-over/short
// posting are for. The route omits it; listing it here would invite somebody to
// "settle" it and credit a clearing account it never debited.
//
// # There is no un-record
//
// `settlement_batch_tender` carries a trigger that refuses every DELETE, and
// one tender settles exactly once. A deposit entered wrongly is corrected by a
// journal, not by taking it back, and the screen does not offer a control the
// database would refuse. That is why the confirmation states the figures before
// the button rather than after it.
//
// # Two permissions, one screen
//
// Reading is `accounting.view` and recording is `accounting.create`, because
// reconciling a bank statement and having authority over the ledger are
// different jobs. Somebody who may only read sees the deposits and no form.

import { Landmark } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  bankable,
  depositProblem,
  feeShare,
  impliedFee,
  totalOf,
  FEE_WORTH_QUESTIONING,
  type Batch,
  type ListedBatch,
  type PendingTender,
  type SettledTender,
} from '@/lib/money/settlement';
import { useUrlState } from '@/lib/url-state';

const PROBLEM: Record<string, Key> = {
  nothing_selected: 'nx.set.needSelection',
  no_reference: 'nx.set.needReference',
  no_date: 'nx.set.needDate',
  no_amount: 'nx.set.needAmount',
  not_a_number: 'nx.set.needNumber',
  net_above_gross: 'nx.set.netAboveGross',
  net_not_positive: 'nx.set.netNotPositive',
};

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

function SettlementScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayRecord = grants.can('accounting.create');

  const [open, setOpen] = useUrlState('batch');
  const [chosen, setChosen] = useState<Record<string, boolean>>({});
  const [reference, setReference] = useState('');
  const [depositedOn, setDepositedOn] = useState(today);
  const [netAmount, setNetAmount] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [note, setNote] = useState<string | null>(null);
  // Minted once per deposit. The route is idempotent on it, so pressing the
  // button twice on a bad connection records one deposit and answers with the
  // first — which is the difference between a fee posted once and twice.
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());

  const pending = useApi<{ data: PendingTender[] }>(
    scope ? '/settlement/pending' : null,
    scope ?? undefined,
  );
  const batches = useApi<{ data: ListedBatch[] }>(
    scope ? '/settlement/batches' : null,
    scope ?? undefined,
  );
  const detail = useApi<Batch>(
    scope && open ? `/settlement/batches/${open}` : null,
    scope ?? undefined,
  );

  const unbanked = bankable(pending.data?.data ?? []);
  const selected = unbanked.filter((tender) => chosen[tender.tender_id]);
  const draft = { reference, depositedOn, netAmount, selected };
  const problem = depositProblem(draft);
  const gross = totalOf(selected);
  const share = feeShare(draft);

  const money = (v: string) => formatMoney(v, { currency, market });

  async function record() {
    if (!scope || problem) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    setNote(null);
    try {
      const made = await api.post<Batch>(
        `/settlement/batches?company_id=${scope.company_id}`,
        {
          uuid: docUUID,
          reference: reference.trim(),
          deposited_on: depositedOn,
          net_amount: netAmount.trim(),
          tender_ids: selected.map((tender) => tender.tender_id),
        },
      );
      setNote(
        t('nx.set.recorded', {
          reference: made.reference,
          fee: money(made.fee_amount),
        }),
      );
      // A new identifier for the next deposit, and the form emptied: leaving
      // the old one in place would make the next deposit a replay of this one.
      setDocUUID(crypto.randomUUID());
      setChosen({});
      setReference('');
      setNetAmount('');
      setOpen(made.id);
      await Promise.all([pending.refetch(), batches.refetch()]);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const pendingColumns: Column<PendingTender>[] = [
    ...(mayRecord
      ? [
          {
            key: 'pick',
            header: t('nx.set.colInclude'),
            width: 'w-16',
            cell: (p: PendingTender) => (
              <label className="flex min-h-11 items-center">
                <input
                  type="checkbox"
                  checked={Boolean(chosen[p.tender_id])}
                  onChange={(e) =>
                    setChosen((was) => ({ ...was, [p.tender_id]: e.target.checked }))
                  }
                  aria-label={t('nx.set.includeOne', { invoice: p.invoice_number })}
                  className="size-4 rounded-xs border border-input accent-primary"
                />
              </label>
            ),
          },
        ]
      : []),
    {
      key: 'invoice',
      header: t('nx.set.colInvoice'),
      primary: true,
      cell: (p) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{p.invoice_number || '—'}</span>
          <span className="text-caption text-muted">{p.issued_at.slice(0, 10)}</span>
        </span>
      ),
    },
    {
      key: 'method',
      header: t('nx.set.colMethod'),
      width: 'w-32',
      cell: (p) => p.method,
    },
    {
      key: 'reference',
      header: t('nx.set.colReference'),
      secondary: true,
      cell: (p) => <span className="num text-muted">{p.reference || '—'}</span>,
    },
    {
      key: 'amount',
      header: t('nx.set.colAmount'),
      numeric: true,
      width: 'w-36',
      cell: (p) => formatMoney(p.amount, { currency: p.currency || currency, market }),
    },
  ];

  const batchColumns: Column<ListedBatch>[] = [
    {
      key: 'reference',
      header: t('nx.set.colStatement'),
      primary: true,
      cell: (b) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{b.reference}</span>
          <span className="text-caption text-muted">
            {t('nx.set.coveredCount', { count: String(b.tender_count) })}
          </span>
        </span>
      ),
    },
    {
      key: 'deposited_on',
      header: t('nx.set.colLanded'),
      width: 'w-36',
      cell: (b) => <time dateTime={b.deposited_on}>{b.deposited_on}</time>,
    },
    {
      key: 'gross',
      header: t('nx.set.colTaken'),
      numeric: true,
      width: 'w-36',
      secondary: true,
      cell: (b) => formatMoney(b.gross_amount, { currency: b.currency || currency, market }),
    },
    {
      key: 'fee',
      header: t('nx.set.colFee'),
      numeric: true,
      width: 'w-32',
      cell: (b) => formatMoney(b.fee_amount, { currency: b.currency || currency, market }),
    },
    {
      key: 'net',
      header: t('nx.set.colArrived'),
      numeric: true,
      width: 'w-36',
      cell: (b) => (
        <span className="font-medium">
          {formatMoney(b.net_amount, { currency: b.currency || currency, market })}
        </span>
      ),
    },
    {
      key: 'state',
      header: t('nx.set.colState'),
      width: 'w-32',
      // Posted is not decoration. A deposit with no journal entry has not
      // cleared anything, and showing it beside one that has, with no
      // difference, is how a clearing account stops being trusted.
      cell: (b) =>
        b.posted ? (
          <Badge tone="positive">{t('nx.set.posted')}</Badge>
        ) : (
          <Badge tone="critical">{t('nx.set.notPosted')}</Badge>
        ),
    },
  ];

  const coveredColumns: Column<SettledTender>[] = [
    {
      key: 'invoice',
      header: t('nx.set.colInvoice'),
      primary: true,
      cell: (s) => <span className="num">{s.invoice_number || '—'}</span>,
    },
    {
      key: 'method',
      header: t('nx.set.colMethod'),
      width: 'w-32',
      cell: (s) => s.method,
    },
    {
      key: 'amount',
      header: t('nx.set.colTaken'),
      numeric: true,
      width: 'w-36',
      cell: (s) => money(s.amount),
    },
    {
      key: 'fee',
      header: t('nx.set.colFeeShare'),
      numeric: true,
      width: 'w-36',
      // The share the acquirer's fee cost THIS sale, which is what a
      // margin-by-payment-method report is built from.
      cell: (s) => money(s.fee_amount),
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.set.title')} description={t('nx.set.subtitle')} />

      <FormError message={error} fields={fieldErrors} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {pending.error ? (
        <ErrorState error={pending.error} onRetry={() => void pending.refetch()} />
      ) : null}

      <h2 className="mb-1 text-card-title font-semibold text-fg">
        {t('nx.set.awaitingTitle')}
      </h2>
      <p className="mb-3 max-w-prose text-caption text-muted">
        {t('nx.set.awaitingHint')}
      </p>

      {pending.isLoading && !pending.data ? <TableSkeleton columns={5} /> : null}

      {!pending.isLoading && unbanked.length === 0 ? (
        <EmptyState
          icon={Landmark}
          title={t('nx.set.nothingPendingTitle')}
          description={t('nx.set.nothingPendingDesc')}
        />
      ) : null}

      {unbanked.length > 0 ? (
        <>
          <DataTable
            caption={t('nx.set.awaitingTitle')}
            columns={pendingColumns}
            rows={unbanked}
            rowKey={(p) => p.tender_id}
            totals={
              <>
                {mayRecord ? <td className="px-3 py-2.5" /> : null}
                <td className="px-3 py-2.5">{t('nx.set.awaitingTotal')}</td>
                <td className="px-3 py-2.5" />
                <td className="hidden px-3 py-2.5 md:table-cell" />
                <td className="num px-3 py-2.5 text-end">{money(totalOf(unbanked))}</td>
              </>
            }
          />

          {mayRecord ? (
            <Panel
              className="mt-5"
              title={t('nx.set.recordTitle')}
              description={t('nx.set.recordHint')}
            >
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                <Field
                  name="reference"
                  label={t('nx.set.reference')}
                  hint={t('nx.set.referenceHint')}
                  required
                >
                  <Input
                    value={reference}
                    onChange={(e) => setReference(e.target.value)}
                    placeholder="MADA-20260817-001"
                    className="num"
                  />
                </Field>
                <Field
                  name="deposited_on"
                  label={t('nx.set.landedOn')}
                  hint={t('nx.set.landedOnHint')}
                  required
                >
                  <Input
                    type="date"
                    value={depositedOn}
                    onChange={(e) => setDepositedOn(e.target.value)}
                    className="num"
                  />
                </Field>
                <Field
                  name="net_amount"
                  label={t('nx.set.netAmount')}
                  hint={t('nx.set.netAmountHint')}
                  required
                >
                  <Input
                    numeric
                    inputMode="decimal"
                    value={netAmount}
                    onChange={(e) => setNetAmount(e.target.value)}
                  />
                </Field>
              </div>

              {/* Stated before the button, not reported after it. */}
              <div className="mt-5 grid gap-5 border-t border-line pt-4 sm:grid-cols-3">
                <Figure
                  label={t('nx.set.selectedTaken')}
                  value={gross ? money(gross) : '—'}
                  caption={t('nx.set.selectedCount', {
                    count: String(selected.length),
                  })}
                />
                <Figure
                  label={t('nx.set.impliedFee')}
                  value={problem === null ? money(impliedFee(draft)) : '—'}
                  caption={
                    share !== null
                      ? t('nx.set.feeShare', {
                          share: (share * 100).toFixed(2),
                        })
                      : undefined
                  }
                  tone={
                    share !== null && share > FEE_WORTH_QUESTIONING ? 'critical' : undefined
                  }
                />
                <Figure
                  label={t('nx.set.willArrive')}
                  value={netAmount.trim() ? money(netAmount.trim()) : '—'}
                />
              </div>

              {share !== null && share > FEE_WORTH_QUESTIONING && problem === null ? (
                <p className="mt-3 max-w-prose text-body text-caution-fg" role="status">
                  {t('nx.set.feeLooksHigh', {
                    share: (share * 100).toFixed(2),
                  })}
                </p>
              ) : null}

              {problem ? (
                <p className="mt-3 text-caption text-muted">{t(PROBLEM[problem] as Key)}</p>
              ) : null}

              <div className="mt-4 flex flex-wrap gap-3">
                <Button
                  variant="primary"
                  busy={busy}
                  busyLabel={t('nx.set.recording')}
                  disabled={problem !== null}
                  onClick={() => void record()}
                >
                  {t('nx.set.record')}
                </Button>
                <p className="self-center text-caption text-muted">
                  {t('nx.set.cannotBeUndone')}
                </p>
              </div>
            </Panel>
          ) : null}
        </>
      ) : null}

      <h2 className="mt-8 mb-1 text-card-title font-semibold text-fg">
        {t('nx.set.depositsTitle')}
      </h2>
      <p className="mb-3 max-w-prose text-caption text-muted">
        {t('nx.set.depositsHint')}
      </p>

      {batches.error ? (
        <ErrorState error={batches.error} onRetry={() => void batches.refetch()} />
      ) : null}
      {batches.isLoading && !batches.data ? <TableSkeleton columns={6} /> : null}

      {!batches.isLoading && (batches.data?.data ?? []).length === 0 ? (
        <EmptyState
          icon={Landmark}
          title={t('nx.set.noDepositsTitle')}
          description={t('nx.set.noDepositsDesc')}
        />
      ) : null}

      {(batches.data?.data ?? []).length > 0 ? (
        <DataTable
          caption={t('nx.set.depositsTitle')}
          columns={batchColumns}
          rows={batches.data?.data ?? []}
          rowKey={(b) => b.id}
          isSelected={(b) => b.id === open}
          onOpenRow={(b) => setOpen(b.id === open ? '' : b.id)}
        />
      ) : null}

      {open ? (
        <Panel
          className="mt-5"
          title={t('nx.set.coveredTitle', {
            reference: detail.data?.reference ?? '',
          })}
          description={t('nx.set.coveredHint')}
          actions={
            <Button variant="ghost" onClick={() => setOpen('')}>
              {t('nx.set.close')}
            </Button>
          }
          flush
        >
          {detail.error ? (
            <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
          ) : null}
          {detail.isLoading && !detail.data ? <TableSkeleton columns={4} rows={3} /> : null}
          {detail.data ? (
            <DataTable
              caption={t('nx.set.coveredCaption')}
              columns={coveredColumns}
              rows={detail.data.tenders}
              rowKey={(s) => s.tender_id}
              totals={
                <>
                  <td className="px-3 py-2.5">{t('nx.set.wholeDeposit')}</td>
                  <td className="px-3 py-2.5" />
                  <td className="num px-3 py-2.5 text-end">
                    {money(detail.data.gross_amount)}
                  </td>
                  <td className="num px-3 py-2.5 text-end">
                    {money(detail.data.fee_amount)}
                  </td>
                </>
              }
            />
          ) : null}
        </Panel>
      ) : null}
    </>
  );
}

export default function SettlementPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SettlementScreen />
      </Suspense>
    </RequirePermission>
  );
}
