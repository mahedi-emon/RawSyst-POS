'use client';

// One adjustment.
//
// # Correcting it means writing the opposite, and the screen offers that
//
// Posted history is never edited. Design 02 §111 is absolute — "Corrections
// happen only by posting a reversing entry with reverses_id set. There is no
// code path — and no database permission — that edits posted history." So there
// is no edit button here and there never will be; there is a button that writes
// the opposite entry, which is the legal correction and the only one.
//
// The reversal is read from what was actually posted rather than rebuilt from
// the request, so an entry corrected after somebody amended something else
// still reverses what it really did.

import Link from 'next/link';
import { use, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import type { Journal, JournalLine } from '@/lib/accounting/journals';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

function JournalScreen({ journalID }: { journalID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApi<Journal>(
    scope ? `/accounting/journals/${journalID}` : null,
    scope ?? undefined,
  );

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  // The reverse route wants its OWN reason: "The ledger will carry an opposite
  // entry, and this is the only place that says what it was for." Driving it
  // with an empty body came back 400, which is the route being right.
  const [undoing, setUndoing] = useState(false);
  const [undoReason, setUndoReason] = useState('');
  const [undoUUID, setUndoUUID] = useState(() => crypto.randomUUID());

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.jnl.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.jnl.title')} />
        <TableSkeleton columns={4} />
      </>
    );
  }

  const money = (v: string) =>
    formatMoney(v, { currency: data.currency || currency, market });

  async function reverse() {
    if (!scope || undoReason.trim() === '') return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(
        `/accounting/journals/${journalID}/reverse?company_id=${scope.company_id}`,
        {
          // Its own id, so a retry after a lost response returns the reversal
          // already written rather than writing a second one.
          uuid: undoUUID,
          reason: undoReason.trim(),
        },
      );
      setUndoing(false);
      setUndoReason('');
      setUndoUUID(crypto.randomUUID());
      await refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<JournalLine>[] = [
    {
      key: 'account',
      header: t('nx.jnl.colAccount'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>
            <span className="num text-muted">{l.account_code}</span> {l.account_name}
          </span>
          {l.memo ? (
            <span className="text-caption text-muted">{l.memo}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'debit',
      header: t('nx.jnl.colDebit'),
      numeric: true,
      width: 'w-36',
      // An empty cell, not a zero. A journal is read by scanning which side
      // each line is on, and a column of 0.00 destroys that at a glance.
      cell: (l) =>
        isZero(l.debit) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num">{money(l.debit)}</span>
        ),
    },
    {
      key: 'credit',
      header: t('nx.jnl.colCredit'),
      numeric: true,
      width: 'w-36',
      cell: (l) =>
        isZero(l.credit) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num">{money(l.credit)}</span>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        title={<span className="num">{data.journal_no}</span>}
        description={[
          data.entry_date,
          t('nx.jnl.entryNo', { no: String(data.entry_no) }),
          data.created_by,
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/money/journals"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.jnl.back')}
          </Link>
        }
      />

      <div className="mb-4 flex flex-wrap gap-2">
        {data.reverses_id ? (
          <Badge tone="info">
            {t('nx.jnl.reversalOf', { no: data.reverses_id.slice(0, 8) })}
          </Badge>
        ) : null}
        {data.reversed_by ? (
          <Badge tone="caution">
            {t('nx.jnl.reversedByEntry', { no: data.reversed_by.slice(0, 8) })}
          </Badge>
        ) : null}
      </div>

      <FormError message={actionError} className="mb-4" />

      <Panel flush>
        <DataTable
          caption={t('nx.jnl.linesCaption')}
          columns={columns}
          rows={data.lines ?? []}
          rowKey={(l, i) => `${l.account_id}-${i}`}
          className="rounded-none border-0"
          totals={
            <>
              <th scope="row" className="px-3 py-2.5 text-start font-medium">
                {t('nx.jnl.colTotal')}
              </th>
              <td className="num px-3 py-2.5 text-end font-semibold tabular-nums">
                {money(data.total)}
              </td>
              <td className="num px-3 py-2.5 text-end font-semibold tabular-nums">
                {money(data.total)}
              </td>
            </>
          }
        />
      </Panel>

      <div className="mt-4 rounded-md border border-line bg-surface p-4">
        <p className="text-label font-medium text-fg">{t('nx.jnl.colReason')}</p>
        <p className="mt-1 max-w-prose text-body">{data.reason}</p>
        {data.memo ? (
          <p className="mt-2 max-w-prose text-caption text-muted">{data.memo}</p>
        ) : null}
      </div>

      {grants.can('accounting.create') ? (
        <div className="mt-6 max-w-prose">
          {undoing ? (
            <Panel title={t('nx.jnl.reverse')}>
              <div className="flex flex-col gap-4">
                {/* Its own reason, not this entry's. The opposite entry is a
                    separate fact in the ledger and "why it was undone" is not
                    the same sentence as "why it was written". */}
                <Field
                  name="reason"
                  label={t('nx.jnl.reverseReason')}
                  hint={t('nx.jnl.reverseReasonHint')}
                  required
                >
                  <Textarea
                    value={undoReason}
                    onChange={(e) => setUndoReason(e.target.value)}
                    rows={3}
                    autoFocus
                  />
                </Field>
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    variant="primary"
                    busy={busy}
                    busyLabel={t('nx.jnl.reversing')}
                    disabled={undoReason.trim() === ''}
                    onClick={() => void reverse()}
                  >
                    {t('nx.jnl.reverse')}
                  </Button>
                  <Button
                    variant="ghost"
                    disabled={busy}
                    onClick={() => {
                      setUndoing(false);
                      setActionError(null);
                    }}
                  >
                    {t('nx.jnl.cancelReverse')}
                  </Button>
                </div>
              </div>
            </Panel>
          ) : (
            <>
              <Button
                variant="secondary"
                // Once, and the badge above says when it has been. A second
                // reversal would be a correction of a correction, which is a
                // new entry rather than this button pressed twice.
                disabled={Boolean(data.reversed_by)}
                onClick={() => setUndoing(true)}
              >
                {t('nx.jnl.reverse')}
              </Button>
              <p className="mt-2 text-caption text-muted">
                {data.reversed_by
                  ? t('nx.jnl.alreadyReversed')
                  : t('nx.jnl.reverseHint')}
              </p>
            </>
          )}
        </div>
      ) : null}
    </>
  );
}

export default function JournalPage({
  params,
}: {
  params: Promise<{ journalID: string }>;
}) {
  const { journalID } = use(params);
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <JournalScreen journalID={journalID} />
    </RequirePermission>
  );
}
