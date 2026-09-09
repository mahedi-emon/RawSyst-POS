'use client';

// Money taken from customers, and taking one back.
//
// # The payables screen's mirror, and deliberately the same shape
//
// A person who has learned to reverse a supplier payment should not have to
// learn a second gesture for a customer receipt. Same list, same inline
// confirmation, same reason box, same sentence about the original staying
// exactly as it is.
//
// # What is shown that the payables side does not show
//
// Whether an instalment has already been collected against the receipt. The
// server allows the reversal — the collection is a separate document and stays
// — and that is precisely the case somebody should look at twice. A screen that
// said nothing would be hiding the one fact that makes the decision hard.
//
// # The exchange gain is the server's problem, not this screen's
//
// A receipt that settled a foreign-currency invoice carries a realised gain or
// loss whose size depended on two rates on two days. The service reverses by
// reading the original journal entry and flipping it rather than rebuilding it
// from the rule, so nothing here has an opinion about the accounting and
// nothing here could get it wrong.

import { Undo2 } from 'lucide-react';
import { useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { formatMoney, type MarketCode } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  partlySpent,
  receiptBlock,
  receiptState,
  type ListedReceipt,
  type ReceiptState,
} from '@/lib/receivables/receipts';

const STATE_TONE: Record<ReceiptState, Tone> = {
  live: 'neutral',
  reversed: 'caution',
  reversal: 'info',
};

const STATE_LABEL: Record<ReceiptState, Key> = {
  live: 'nx.rcpt.stateLive',
  reversed: 'nx.rcpt.stateReversed',
  reversal: 'nx.rcpt.stateReversal',
};

const BLOCK_REASON: Record<string, Key> = {
  is_a_reversal: 'nx.rcpt.blockIsReversal',
  already_reversed: 'nx.rcpt.blockAlreadyReversed',
};

export function ReceiptLedger({
  companyId,
  currency,
  market,
  mayReverse,
  refreshSignal,
}: {
  companyId: string;
  currency: string;
  market: MarketCode;
  mayReverse: boolean;
  refreshSignal: number;
}) {
  const t = useT();

  const { data, isLoading, error, refetch } = useApi<{ data: ListedReceipt[] }>(
    '/receivables/receipts',
    { company_id: companyId, limit: 100 },
  );

  useEffect(() => {
    if (refreshSignal > 0) void refetch();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshSignal]);

  const [confirming, setConfirming] = useState<ListedReceipt | null>(null);
  const [reason, setReason] = useState('');
  const [docUUID, setDocUUID] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [note, setNote] = useState<string | null>(null);

  const money = (v: string, own?: string) =>
    formatMoney(v, { currency: own || currency, market });

  function open(r: ListedReceipt) {
    setConfirming(r);
    setReason('');
    setFailure(null);
    setFields({});
    setNote(null);
    // One identifier per reversal. The route is idempotent on it, so a second
    // press on a bad connection returns the reversal already made.
    setDocUUID(crypto.randomUUID());
  }

  async function reverse() {
    if (!confirming) return;
    setBusy(true);
    setFailure(null);
    setFields({});
    try {
      const made = await api.post<{ receipt_number: string }>(
        `/receivables/receipts/${confirming.id}/reverse?company_id=${companyId}`,
        { uuid: docUUID, reason: reason.trim() },
      );
      setNote(
        t('nx.rcpt.reversed', {
          number: made.receipt_number,
          original: confirming.receipt_number,
        }),
      );
      setConfirming(null);
      await refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setFailure(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const rows = data?.data ?? [];

  const columns: Column<ListedReceipt>[] = [
    {
      key: 'receipt',
      header: t('nx.rcpt.colReceipt'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{r.receipt_number}</span>
          <span className="text-caption text-muted">{r.customer}</span>
        </span>
      ),
    },
    {
      key: 'received_on',
      header: t('nx.rcpt.colReceivedOn'),
      width: 'w-32',
      cell: (r) => <time dateTime={r.received_on}>{r.received_on}</time>,
    },
    {
      key: 'method',
      header: t('nx.rcpt.colHow'),
      secondary: true,
      width: 'w-40',
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span>{r.method}</span>
          {r.reference ? (
            <span className="num text-caption text-muted">{r.reference}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.rcpt.colAmount'),
      numeric: true,
      width: 'w-36',
      cell: (r) => (
        <span className={r.reversed ? 'line-through text-muted' : undefined}>
          {money(r.amount, r.currency)}
        </span>
      ),
    },
    {
      key: 'state',
      header: t('nx.rcpt.colState'),
      width: 'w-40',
      cell: (r) => (
        <span className="flex flex-col items-start gap-1">
          <Badge tone={STATE_TONE[receiptState(r)]}>
            {t(STATE_LABEL[receiptState(r)])}
          </Badge>
          {partlySpent(r) ? (
            <span className="text-caption text-caution-fg">
              {t('nx.rcpt.partlyCollected')}
            </span>
          ) : null}
        </span>
      ),
    },
    ...(mayReverse
      ? [
          {
            key: 'act',
            header: t('nx.rcpt.colAct'),
            width: 'w-32',
            cell: (r: ListedReceipt) => {
              const block = receiptBlock(r);
              if (block) {
                return (
                  <span className="text-caption text-muted">
                    {t(BLOCK_REASON[block] as Key)}
                  </span>
                );
              }
              return (
                <Button size="sm" variant="ghost" onClick={() => open(r)}>
                  <Undo2 aria-hidden="true" />
                  {t('nx.rcpt.reverse')}
                </Button>
              );
            },
          },
        ]
      : []),
  ];

  return (
    <section className="mt-8">
      <h2 className="mb-1 text-card-title font-semibold text-fg">
        {t('nx.rcpt.ledgerTitle')}
      </h2>
      <p className="mb-3 max-w-prose text-caption text-muted">
        {t('nx.rcpt.ledgerHint')}
      </p>

      <FormError message={failure} fields={fields} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {confirming ? (
        <Panel
          className="mb-5"
          title={t('nx.rcpt.confirmTitle', { number: confirming.receipt_number })}
          description={t('nx.rcpt.confirmHint')}
        >
          <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {[
              [t('nx.rcpt.confirmCustomer'), confirming.customer],
              [t('nx.rcpt.confirmAmount'), money(confirming.amount, confirming.currency)],
              [t('nx.rcpt.confirmReceivedOn'), confirming.received_on],
              [
                t('nx.rcpt.confirmHow'),
                confirming.reference
                  ? `${confirming.method} · ${confirming.reference}`
                  : confirming.method,
              ],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className="text-label text-muted">{label}</dt>
                <dd className="num mt-0.5 text-body text-fg">{value}</dd>
              </div>
            ))}
          </dl>

          <p className="mt-4 max-w-prose text-body text-caution-fg">
            {t('nx.rcpt.confirmEffect', {
              amount: money(confirming.amount, confirming.currency),
              customer: confirming.customer,
            })}
          </p>

          {partlySpent(confirming) ? (
            <p className="mt-2 max-w-prose text-body text-caution-fg">
              {t('nx.rcpt.confirmPartlyCollected', {
                left: money(confirming.unapplied, confirming.currency),
              })}
            </p>
          ) : null}

          <div className="mt-4 max-w-prose">
            <Field
              name="reason"
              label={t('nx.rcpt.reason')}
              hint={t('nx.rcpt.reasonHint')}
              error={fields.reason}
            >
              <Textarea
                rows={2}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </Field>
          </div>

          <div className="mt-4 flex flex-wrap gap-3">
            <Button
              variant="destructive"
              busy={busy}
              busyLabel={t('nx.rcpt.reversing')}
              onClick={() => void reverse()}
            >
              {t('nx.rcpt.confirmReverse')}
            </Button>
            <Button variant="ghost" onClick={() => setConfirming(null)}>
              {t('nx.rcpt.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={6} rows={5} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Undo2}
          title={t('nx.rcpt.noneTitle')}
          description={t('nx.rcpt.noneDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.rcpt.ledgerTitle')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
          isSelected={(r) => r.id === confirming?.id}
        />
      ) : null}
    </section>
  );
}
