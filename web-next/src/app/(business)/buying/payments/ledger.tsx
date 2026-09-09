'use client';

// What has been paid, and taking one back.
//
// # Why this could not be done before
//
// `POST /purchasing/payments/{id}/reverse` has been live since payables landed
// and nothing in the product could name a payment id. The route was tested,
// correct, and reachable from no screen: a screen would have had to ask
// somebody to type a uuid. `GET /purchasing/payments` is the half that was
// missing and this is what it is for.
//
// # A reversal is a new document, never an edit
//
// Design 02 §111: "Corrections happen only by posting a reversing entry with
// reverses_id set. There is no code path — and no database permission — that
// edits posted history." So both documents stay in the list, each says which it
// is, and the panel states the accounting effect BEFORE the button rather than
// reporting it afterwards.
//
// # The reason goes into the trail, not onto the payment
//
// The document is a fact about money and carries no editorial. The reason is
// written into the audit entry, which is the register somebody reads months
// later when they ask why a supplier was paid and then unpaid.
//
// # One press, one reversal
//
// The uuid is minted when the confirmation opens and the route is idempotent on
// it, so a second press on a bad connection returns the reversal already made
// rather than making another. Pressing Reverse on a different payment mints a
// new one, because a uuid already spent on one document is refused for a
// second — and saying so is better than quietly reversing the wrong thing.

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
  billsSettled,
  canReverse,
  paymentState,
  reversalBlock,
  type ListedPayment,
  type PaymentState,
} from '@/lib/purchasing/payments';

const STATE_TONE: Record<PaymentState, Tone> = {
  live: 'neutral',
  reversed: 'caution',
  reversal: 'info',
  unposted: 'critical',
};

const STATE_LABEL: Record<PaymentState, Key> = {
  live: 'nx.pay.stateLive',
  reversed: 'nx.pay.stateReversed',
  reversal: 'nx.pay.stateReversal',
  unposted: 'nx.pay.stateUnposted',
};

const BLOCK_REASON: Record<string, Key> = {
  is_a_reversal: 'nx.pay.blockIsReversal',
  already_reversed: 'nx.pay.blockAlreadyReversed',
  never_posted: 'nx.pay.blockNeverPosted',
};

export function PaymentLedger({
  companyId,
  currency,
  market,
  mayReverse,
  /** Bumped by the parent when a payment is made, so the list catches up. */
  refreshSignal,
}: {
  companyId: string;
  currency: string;
  market: MarketCode;
  mayReverse: boolean;
  refreshSignal: number;
}) {
  const t = useT();

  const { data, isLoading, error, refetch } = useApi<{ data: ListedPayment[] }>(
    '/purchasing/payments',
    { company_id: companyId, limit: 100 },
  );

  // A payment made on the form above belongs in this list immediately.
  useEffect(() => {
    if (refreshSignal > 0) void refetch();
    // Only when the parent says something changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshSignal]);

  const [confirming, setConfirming] = useState<ListedPayment | null>(null);
  const [reason, setReason] = useState('');
  const [docUUID, setDocUUID] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [note, setNote] = useState<string | null>(null);

  const money = (v: string, own?: string) =>
    formatMoney(v, { currency: own || currency, market });

  function open(payment: ListedPayment) {
    setConfirming(payment);
    setReason('');
    setFailure(null);
    setFields({});
    setNote(null);
    // One identifier per reversal, minted here. Reusing the previous one would
    // be refused by the route with "that identifier already belongs to a
    // different payment", which is the right refusal and the wrong experience.
    setDocUUID(crypto.randomUUID());
  }

  async function reverse() {
    if (!confirming) return;
    setBusy(true);
    setFailure(null);
    setFields({});
    try {
      const made = await api.post<{ payment_number: string; amount: string }>(
        `/purchasing/payments/${confirming.id}/reverse?company_id=${companyId}`,
        { uuid: docUUID, reason: reason.trim() },
      );
      setNote(
        t('nx.pay.reversed', {
          number: made.payment_number,
          original: confirming.payment_number,
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

  const columns: Column<ListedPayment>[] = [
    {
      key: 'payment',
      header: t('nx.pay.colPayment'),
      primary: true,
      cell: (p) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{p.payment_number}</span>
          <span className="text-caption text-muted">{p.supplier}</span>
        </span>
      ),
    },
    {
      key: 'paid_on',
      header: t('nx.pay.colPaidOn'),
      width: 'w-32',
      cell: (p) => <time dateTime={p.paid_on}>{p.paid_on}</time>,
    },
    {
      key: 'bills',
      header: t('nx.pay.colAgainst'),
      secondary: true,
      cell: (p) =>
        billsSettled(p) ? (
          <span className="num text-muted">{billsSettled(p)}</span>
        ) : (
          <span className="text-muted">{t('nx.pay.noBills')}</span>
        ),
    },
    {
      key: 'method',
      header: t('nx.pay.colMethod'),
      secondary: true,
      width: 'w-36',
      cell: (p) => (
        <span className="flex flex-col gap-0.5">
          <span>{p.method}</span>
          {p.reference ? (
            <span className="num text-caption text-muted">{p.reference}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.pay.colAmount'),
      numeric: true,
      width: 'w-36',
      cell: (p) => (
        // A reversed payment keeps its figure and is struck through: the money
        // did leave, and then came back. Hiding the amount would make the
        // reversal beside it unexplainable.
        <span className={p.reversed ? 'line-through text-muted' : undefined}>
          {money(p.amount, p.currency)}
        </span>
      ),
    },
    {
      key: 'state',
      header: t('nx.pay.colState'),
      width: 'w-32',
      cell: (p) => (
        <Badge tone={STATE_TONE[paymentState(p)]}>{t(STATE_LABEL[paymentState(p)])}</Badge>
      ),
    },
    ...(mayReverse
      ? [
          {
            key: 'act',
            header: t('nx.pay.colAct'),
            width: 'w-32',
            cell: (p: ListedPayment) => {
              const block = reversalBlock(p);
              if (block) {
                // Named rather than hidden. "Why can I not undo this one?" is
                // the question a blank cell leaves somebody with.
                return (
                  <span className="text-caption text-muted">
                    {t(BLOCK_REASON[block] as Key)}
                  </span>
                );
              }
              return (
                <Button size="sm" variant="ghost" onClick={() => open(p)}>
                  <Undo2 aria-hidden="true" />
                  {t('nx.pay.reverse')}
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
        {t('nx.pay.ledgerTitle')}
      </h2>
      <p className="mb-3 max-w-prose text-caption text-muted">
        {t('nx.pay.ledgerHint')}
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
          title={t('nx.pay.confirmTitle', { number: confirming.payment_number })}
          description={t('nx.pay.confirmHint')}
        >
          <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {[
              [t('nx.pay.confirmSupplier'), confirming.supplier],
              [t('nx.pay.confirmAmount'), money(confirming.amount, confirming.currency)],
              [t('nx.pay.confirmPaidOn'), confirming.paid_on],
              [
                t('nx.pay.confirmAgainst'),
                billsSettled(confirming) || t('nx.pay.noBills'),
              ],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className="text-label text-muted">{label}</dt>
                <dd className="num mt-0.5 text-body text-fg">{value}</dd>
              </div>
            ))}
          </dl>

          {/* The effect, stated before the button. */}
          <p className="mt-4 max-w-prose text-body text-caution-fg">
            {t('nx.pay.confirmEffect', {
              amount: money(confirming.amount, confirming.currency),
              supplier: confirming.supplier,
            })}
          </p>

          <div className="mt-4 max-w-prose">
            <Field
              name="reason"
              label={t('nx.pay.reason')}
              hint={t('nx.pay.reasonHint')}
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
              busyLabel={t('nx.pay.reversing')}
              onClick={() => void reverse()}
            >
              {t('nx.pay.confirmReverse')}
            </Button>
            <Button variant="ghost" onClick={() => setConfirming(null)}>
              {t('nx.pay.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={6} rows={5} /> : null}

      {!isLoading && !error && rows.length === 0 ? (
        <EmptyState
          icon={Undo2}
          title={t('nx.pay.noneTitle')}
          description={t('nx.pay.noneDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.pay.ledgerTitle')}
          columns={columns}
          rows={rows}
          rowKey={(p) => p.id}
          isSelected={(p) => p.id === confirming?.id}
        />
      ) : null}
    </section>
  );
}
