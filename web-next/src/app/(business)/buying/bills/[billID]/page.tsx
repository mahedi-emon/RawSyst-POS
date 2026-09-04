'use client';

// One supplier invoice, and what the match found.
//
// # The evidence is the screen
//
// The match compares the invoice against the order and the delivery across four
// dimensions, and the backend KEEPS what it found rather than recomputing it —
// "a control that leaves no record cannot be audited, and recomputing later
// would give a different answer once someone amends the order, which is exactly
// when somebody would want to check what it originally said."
//
// So this screen renders that record, including the dimensions that passed. A
// table showing only the breaches would answer "what is wrong" and not "what
// was checked", and the second question is the one an auditor asks.
//
// # The server explains itself, so this screen does not paraphrase
//
// Each comparison carries a `detail` written by the backend — "Earlier invoices
// have already billed 21 of what was received on this line, so only 3 is still
// outstanding." Rewriting that here would mean two explanations of one control,
// drifting apart. It is shown as sent, like every other message the server
// writes for a person to read.
//
// # Accepting is somebody putting their name to it
//
// `ApproveBill` refuses an empty reason: "Say why this discrepancy is being
// accepted. It is recorded against your name." The whole value of holding an
// invoice back is that an override leaves something behind, so the reason is a
// required field and the button says what pressing it does to the ledger.

import { AlertTriangle, Check, X } from 'lucide-react';
import Link from 'next/link';
import { use, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  BILL_STATUS,
  MATCH_DIMENSION,
  canAccept,
  type Bill,
  type MatchLine,
} from '@/lib/purchasing/bills';

function BillScreen({ billID }: { billID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const {
    data: bill,
    isLoading,
    error,
    refetch,
  } = useApi<Bill>(
    scope ? `/purchasing/bills/${billID}` : null,
    scope ?? undefined,
  );

  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [acceptError, setAcceptError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function accept() {
    if (!scope || reason.trim() === '') return;
    setBusy(true);
    setAcceptError(null);
    setFieldErrors({});
    try {
      await api.post(
        `/purchasing/bills/${billID}/approve?company_id=${scope.company_id}`,
        { reason },
      );
      setReason('');
      await refetch();
    } catch (e) {
      // A 409 lands here when somebody else accepted it first, which is
      // exactly the case a silent failure would hide.
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setAcceptError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.bill.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !bill) {
    return (
      <>
        <PageHeader title={t('nx.bill.title')} />
        <TableSkeleton columns={6} />
      </>
    );
  }

  const money = (v: string) =>
    formatMoney(v, { currency: bill.currency || currency, market });
  const status = BILL_STATUS[bill.status];
  const checks = bill.match ?? [];

  /** A figure that is absent on this dimension, rather than zero. */
  const cell = (v: string | undefined) =>
    v === undefined || v === '' ? (
      <span className="text-subtle">—</span>
    ) : (
      <span className="num">{v}</span>
    );

  const columns: Column<MatchLine>[] = [
    {
      key: 'dimension',
      header: t('nx.bill.colWhat'),
      primary: true,
      cell: (m) => {
        const named = MATCH_DIMENSION[m.dimension];
        return (
          <span className="flex flex-col gap-0.5">
            <span>{named ? t(named) : m.dimension}</span>
            {m.description ? (
              <span className="text-caption text-muted">{m.description}</span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'ordered',
      header: t('nx.bill.colOrdered'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (m) => cell(m.ordered),
    },
    {
      key: 'received',
      header: t('nx.bill.colReceived'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (m) => cell(m.received),
    },
    {
      key: 'before',
      header: t('nx.bill.colBilledBefore'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      cell: (m) => cell(m.previously_billed),
    },
    {
      key: 'billed',
      header: t('nx.bill.colBilled'),
      numeric: true,
      width: 'w-32',
      cell: (m) => cell(m.billed),
    },
    {
      key: 'variance',
      header: t('nx.bill.colOff'),
      numeric: true,
      width: 'w-32',
      cell: (m) => {
        const breach = m.outcome === 'breach';
        return (
          <span className="flex flex-col items-end gap-0.5">
            <span
              className={breach ? 'num font-semibold text-critical-fg' : 'num'}
            >
              {m.variance}
              {m.variance_pct ? ` (${m.variance_pct}%)` : ''}
            </span>
            <Badge tone={breach ? 'critical' : 'positive'}>
              {breach ? (
                <X className="size-3" aria-hidden="true" />
              ) : (
                <Check className="size-3" aria-hidden="true" />
              )}
              {t(breach ? 'nx.bill.outBreach' : 'nx.bill.outPass')}
            </Badge>
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{bill.supplier_ref}</span>
            {status ? <Badge tone={status.tone}>{t(status.key)}</Badge> : null}
            <Badge tone={bill.posted ? 'positive' : 'caution'}>
              {t(bill.posted ? 'nx.bill.postedYes' : 'nx.bill.postedNo')}
            </Badge>
          </span>
        }
        description={[
          bill.supplier,
          t('nx.bill.dated', { date: bill.bill_date, due: bill.due_date }),
          bill.po_number,
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/buying/bills"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.bill.back')}
          </Link>
        }
      />

      <FormError message={acceptError} className="mb-4" />

      {bill.status === 'blocked' ? (
        <Panel className="mb-6">
          <p className="flex items-start gap-2 text-body text-fg">
            <AlertTriangle
              className="mt-0.5 size-4 shrink-0 text-caution-fg"
              aria-hidden="true"
            />
            {t('nx.bill.blockedWhy')}
          </p>
        </Panel>
      ) : null}

      {checks.length > 0 ? (
        <Panel title={t('nx.bill.checks')} flush>
          <DataTable
            caption={t('nx.bill.checksCaption')}
            columns={columns}
            rows={checks}
            rowKey={(m) => `${m.dimension}-${m.description ?? ''}`}
            className="rounded-none border-0"
          />
          {/* The server's own explanation of each comparison, under the table
              rather than squeezed into a cell -- these are sentences, and a
              sentence in a numeric column is unreadable. */}
          <ul className="flex flex-col gap-2 border-t border-line px-4 py-3">
            {checks
              .filter((m) => m.detail)
              .map((m) => {
                const named = MATCH_DIMENSION[m.dimension];
                return (
                  <li
                    key={`${m.dimension}-detail`}
                    className="text-caption text-muted"
                  >
                    <span className="font-medium text-fg">
                      {named ? t(named) : m.dimension}
                    </span>
                    {' — '}
                    {m.detail}
                  </li>
                );
              })}
          </ul>
        </Panel>
      ) : null}

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <Panel>
          <dl className="flex flex-col gap-1 text-body">
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.bill.moneyNet')}</dt>
              <dd className="num">{money(bill.subtotal_net)}</dd>
            </div>
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.bill.moneyTax')}</dt>
              <dd className="num">{money(bill.tax_total)}</dd>
            </div>
            <div className="mt-1 flex justify-between gap-4 border-t-[3px] border-double border-line-strong pt-2 font-semibold">
              <dt>{t('nx.bill.moneyTotal')}</dt>
              <dd className="num">{money(bill.total_inclusive)}</dd>
            </div>
            {!isZero(bill.amount_paid) ? (
              <div className="mt-2 flex justify-between gap-4">
                <dt className="text-muted">{t('nx.bill.moneyPaid')}</dt>
                <dd className="num text-muted">{money(bill.amount_paid)}</dd>
              </div>
            ) : null}
            <div className="flex justify-between gap-4 font-medium">
              <dt>{t('nx.bill.moneyOwed')}</dt>
              <dd className="num">{money(bill.outstanding)}</dd>
            </div>
          </dl>
        </Panel>

        {canAccept(bill.status) ? (
          <Panel title={t('nx.bill.accept')}>
            <Field
              label={t('nx.bill.reason')}
              hint={t('nx.bill.reasonHint')}
              error={fieldErrors.reason}
              required
            >
              <Textarea
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder={t('nx.bill.reasonPlaceholder')}
                rows={3}
              />
            </Field>
            {/* Above the button, because it says what pressing it does and
                that it cannot be undone. */}
            <p className="mt-2 text-caption text-muted">
              {t('nx.bill.acceptEffect')}
            </p>
            <Button
              variant="primary"
              className="mt-3"
              busy={busy}
              disabled={reason.trim() === ''}
              onClick={() => void accept()}
            >
              {t('nx.bill.acceptConfirm')}
            </Button>
          </Panel>
        ) : null}
      </div>
    </>
  );
}

export default function BillPage({
  params,
}: {
  params: Promise<{ billID: string }>;
}) {
  const { billID } = use(params);
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <BillScreen billID={billID} />
    </RequirePermission>
  );
}
