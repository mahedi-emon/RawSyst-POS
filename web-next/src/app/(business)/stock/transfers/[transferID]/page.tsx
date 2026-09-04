'use client';

// One transfer, and whatever it is waiting for.
//
// # One button at a time, because a transfer has one next step
//
// Requested waits for approval, approved waits for dispatch, in transit waits
// for receipt. Showing all three with two greyed out would ask the reader to
// work out which is live; showing the one that is live says it.
//
// # Approving your own is refused, and the screen says so first
//
// The server answers "You raised TRF-000001, so somebody else has to approve
// it." That is a segregation rule, not a glitch, so the reason is on the screen
// beside the absent button rather than arriving as an error after a press.
// The frontend cannot know who raised it any better than by comparing names,
// so it does not guess: it shows the rule, and the server remains the boundary.
//
// # Dispatch and receipt may amend the quantities
//
// Both take a list of lines, and sending less than was asked for is ordinary —
// the shelf had four when the paperwork said five. The boxes default to what
// the document says and stay editable.

import Link from 'next/link';
import { use, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { TableSkeleton } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { TRANSFER_STATUS, nextStep, type Transfer } from '@/lib/stock/stock';

const STEP_LABEL: Record<'approve' | 'dispatch' | 'receive', Key> = {
  approve: 'nx.trf.approve',
  dispatch: 'nx.trf.dispatch',
  receive: 'nx.trf.receive',
};

function TransferScreen({ transferID }: { transferID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApi<Transfer>(
    scope ? `/stock/transfers/${transferID}` : null,
    scope ?? undefined,
  );

  const [qty, setQty] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  // Defaulted to what the document says, and editable: sending four when the
  // paperwork says five is ordinary.
  useEffect(() => {
    if (!data?.lines) return;
    setQty((current) => {
      if (Object.keys(current).length > 0) return current;
      const next: Record<string, string> = {};
      for (const l of data.lines ?? []) next[l.variant_id] = l.qty_requested;
      return next;
    });
  }, [data]);

  async function step(what: 'approve' | 'dispatch' | 'receive' | 'cancel') {
    if (!scope) return;
    setBusy(what);
    setActionError(null);
    try {
      const c = `company_id=${scope.company_id}`;
      const body =
        what === 'dispatch' || what === 'receive'
          ? {
              lines: Object.entries(qty)
                .filter(([, v]) => v.trim() !== '')
                .map(([variant_id, v]) => ({ variant_id, qty: v })),
            }
          : {};
      await api.post(`/stock/transfers/${transferID}/${what}?${c}`, body);
      await refetch();
    } catch (e) {
      // The segregation refusal lands here when somebody presses anyway.
      setActionError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.trf.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.trf.title')} />
        <TableSkeleton columns={4} />
      </>
    );
  }

  const status = TRANSFER_STATUS[data.status];
  const step_ = nextStep(data.status);
  const editable = step_ === 'dispatch' || step_ === 'receive';
  const mayAct =
    step_ === 'approve'
      ? grants.can('inventory.approve_transfer')
      : grants.can('inventory.transfer_stock');

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{data.transfer_no}</span>
            {status ? <Badge tone={status.tone}>{t(status.key)}</Badge> : null}
          </span>
        }
        description={[`${data.from} → ${data.to}`, data.note].filter(Boolean).join(' · ')}
        actions={
          <Link
            href="/stock/transfers"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.trf.back')}
          </Link>
        }
      />

      <FormError message={actionError} className="mb-4" />

      <Panel flush>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[34rem] border-collapse text-body">
            <caption className="sr-only">{t('nx.trf.linesCaption')}</caption>
            <thead>
              <tr className="border-b border-line">
                <th scope="col" className="px-4 py-2 text-start text-label text-muted">
                  {t('nx.npo.colProduct')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.trf.colAsked')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.trf.colSent')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {editable ? t(STEP_LABEL[step_]) : t('nx.trf.colArrived')}
                </th>
              </tr>
            </thead>
            <tbody>
              {(data.lines ?? []).map((l) => (
                <tr key={l.variant_id} className="border-b border-line last:border-0">
                  <th scope="row" className="px-4 py-2 text-start font-normal">
                    <span className="flex flex-col gap-0.5">
                      <span>{l.product}</span>
                      <span className="num text-caption text-muted">{l.sku}</span>
                    </span>
                  </th>
                  <td className="px-4 py-2 text-end">
                    <span className="num text-muted">
                      {formatQuantity(l.qty_requested)}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-end">
                    {l.qty_dispatched ? (
                      <span className="num">{formatQuantity(l.qty_dispatched)}</span>
                    ) : (
                      <span className="text-subtle">—</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-end">
                    {editable && mayAct ? (
                      <Input
                        value={qty[l.variant_id] ?? ''}
                        onChange={(e) =>
                          setQty((c) => ({ ...c, [l.variant_id]: e.target.value }))
                        }
                        inputMode="decimal"
                        autoComplete="off"
                        aria-label={`${t(STEP_LABEL[step_])} ${l.product}`}
                        className="w-24 text-end"
                      />
                    ) : l.qty_received ? (
                      <span className="num">{formatQuantity(l.qty_received)}</span>
                    ) : (
                      <span className="text-subtle">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>

      {step_ ? (
        <div className="mt-6">
          {step_ === 'approve' ? (
            // Stated before the press, not after: the server refuses your own
            // and the reason is a rule rather than a failure.
            <p className="mb-3 max-w-prose text-body text-muted">
              {t('nx.trf.ownRequest')}
            </p>
          ) : null}
          {editable ? (
            <p className="mb-3 text-caption text-muted">{t('nx.trf.amend')}</p>
          ) : null}

          {mayAct ? (
            <div className="flex flex-wrap gap-2">
              <Button
                variant="primary"
                busy={busy === step_}
                disabled={busy !== null}
                onClick={() => void step(step_)}
              >
                {t(STEP_LABEL[step_])}
              </Button>
              <Button
                variant="ghost"
                busy={busy === 'cancel'}
                disabled={busy !== null}
                onClick={() => void step('cancel')}
              >
                {t('nx.trf.cancelIt')}
              </Button>
            </div>
          ) : null}
        </div>
      ) : null}
    </>
  );
}

export default function TransferPage({
  params,
}: {
  params: Promise<{ transferID: string }>;
}) {
  const { transferID } = use(params);
  return (
    <RequirePermission anyOf={['inventory.view']}>
      <TransferScreen transferID={transferID} />
    </RequirePermission>
  );
}
