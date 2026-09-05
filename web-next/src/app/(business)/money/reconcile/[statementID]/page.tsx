'use client';

// One statement, and the work of pairing it with the books.
//
// # Two lists, side by side, and the gap between them
//
// Left: every line the bank sent. Right: every entry on that account the bank
// has not seen. Pairing one with the other is the whole job, and the figure at
// the top is what is left once both lists are taken off the difference between
// the two balances.
//
// # A match made by a rule is not a match made by a person
//
// The importer pairs lines on exact amount within three days, and the service
// is blunt about what that is: "A match on amount and date within a window is a
// guess. It is usually right and it is occasionally very wrong — two identical
// supplier payments on the same day are indistinguishable to any rule." So
// every row says which kind it is, and either can be undone.
//
// # Signing off is refused while anything is unexplained
//
// That refusal is the feature. The service says so: a reconciliation that can
// be signed with a difference nobody accounts for "is a piece of paper, and the
// auditor who relies on it has been misled by a screen." The button is present
// and refused rather than hidden, so the reason is readable.

import Link from 'next/link';
import { use, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  isSignedOff,
  likelyMatches,
  type LedgerLine,
  type Statement,
  type StatementLine,
} from '@/lib/money/statement';

function StatementScreen({ statementID }: { statementID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<Statement>(
    scope ? `/treasury/statements/${statementID}` : null,
    scope ?? undefined,
  );

  // The bank line currently being paired. One at a time: the act is "this line
  // is that entry", and a multi-select would invite a pairing nobody meant.
  const [pairing, setPairing] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.rec.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.rec.title')} />
        <TableSkeleton columns={4} />
      </>
    );
  }

  const money = (v: string) =>
    formatMoney(v, { currency: data.currency || currency, market });
  const signedOff = isSignedOff(data);
  const bankLines = data.lines ?? [];
  const ledger = data.unmatched_in_books ?? [];
  const chosen = bankLines.find((l) => l.id === pairing) ?? null;
  const suggested = chosen ? new Set(likelyMatches(chosen, ledger).map((l) => l.id)) : null;

  async function pair(lineID: string, journalLineID: string) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(
        `/treasury/lines/${lineID}/match?company_id=${scope.company_id}`,
        // An empty journal line is how the route expresses "undo". One route
        // rather than two, because a person toggles between them.
        { journal_line_id: journalLineID },
      );
      setPairing(null);
      await refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function signOff() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(
        `/treasury/statements/${statementID}/reconcile?company_id=${scope.company_id}`,
        {},
      );
      await refetch();
    } catch (e) {
      // The refusal naming the amount still unexplained arrives here, and it
      // is the most useful sentence on the screen.
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const bankColumns: Column<StatementLine>[] = [
    {
      key: 'date',
      header: t('nx.rec.colDate'),
      width: 'w-28',
      cell: (l) => (
        <time dateTime={l.value_date} className="num text-muted">
          {l.value_date}
        </time>
      ),
    },
    {
      key: 'what',
      header: t('nx.rec.colWhat'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>{l.description}</span>
          {l.reference ? (
            <span className="num text-caption text-muted">{l.reference}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.rec.colAmount'),
      numeric: true,
      width: 'w-32',
      cell: (l) => <span className="num">{money(l.amount)}</span>,
    },
    {
      key: 'paired',
      header: t('nx.rec.colPairedWith'),
      width: 'w-56',
      cell: (l) =>
        l.matched_to ? (
          <span className="flex flex-col gap-0.5">
            <span className="num">{l.matched_to}</span>
            {/* A rule's guess and a person's decision are different claims,
                and the person checking this needs to know which they are
                looking at. */}
            <span className="text-caption text-muted">
              {l.match_kind === 'automatic'
                ? t('nx.rec.byRule')
                : l.matched_by
                  ? t('nx.rec.byPerson', { name: l.matched_by })
                  : t('nx.rec.byPersonAnon')}
            </span>
            {!signedOff ? (
              <Button
                variant="ghost"
                size="sm"
                className="mt-1 self-start"
                disabled={busy}
                onClick={() => void pair(l.id, '')}
              >
                {t('nx.rec.undo')}
              </Button>
            ) : null}
          </span>
        ) : signedOff ? (
          <span className="text-subtle">—</span>
        ) : (
          <Button
            variant={pairing === l.id ? 'primary' : 'secondary'}
            size="sm"
            disabled={busy}
            onClick={() => setPairing(pairing === l.id ? null : l.id)}
          >
            {pairing === l.id ? t('nx.rec.stopPicking') : t('nx.rec.pick')}
          </Button>
        ),
    },
  ];

  const ledgerColumns: Column<LedgerLine>[] = [
    {
      key: 'date',
      header: t('nx.rec.colDate'),
      width: 'w-28',
      cell: (l) => (
        <time dateTime={l.entry_date} className="num text-muted">
          {l.entry_date}
        </time>
      ),
    },
    {
      key: 'entry',
      header: t('nx.rec.colEntry'),
      secondary: true,
      width: 'w-24',
      cell: (l) => <span className="num text-muted">{l.entry_no}</span>,
    },
    {
      key: 'memo',
      header: t('nx.rec.colWhat'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>{l.memo || '—'}</span>
          {/* Marked while a bank line is being paired: the same amount within
              three days is the rule the importer already ran, so anything it
              highlights here is what the rule could not take because
              something else claimed it, or a second candidate. */}
          {suggested?.has(l.id) ? (
            <Badge tone="info">{t('nx.rec.likely')}</Badge>
          ) : null}
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.rec.colAmount'),
      numeric: true,
      width: 'w-32',
      cell: (l) => <span className="num">{money(l.amount)}</span>,
    },
    {
      key: 'pair',
      header: <span className="sr-only">{t('nx.rec.pairWith')}</span>,
      width: 'w-32',
      cell: (l) =>
        chosen && !signedOff ? (
          <Button size="sm" disabled={busy} onClick={() => void pair(chosen.id, l.id)}>
            {t('nx.rec.pairWith')}
          </Button>
        ) : null,
    },
  ];

  return (
    <>
      <PageHeader
        title={data.account}
        description={[
          `${data.starts_on} – ${data.ends_on}`,
          data.reference,
          signedOff && data.reconciled_by
            ? t('nx.rec.signedOffBy', {
                name: data.reconciled_by,
                date: (data.reconciled_at ?? '').slice(0, 10),
              })
            : undefined,
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/money/reconcile"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.rec.back')}
          </Link>
        }
      />

      <div className="mb-6 grid gap-4 sm:grid-cols-3">
        <Panel>
          <Figure label={t('nx.rec.bankBalance')} value={money(data.closing_balance)} />
        </Panel>
        <Panel>
          <Figure label={t('nx.rec.booksBalance')} value={money(data.ledger_balance)} />
        </Panel>
        <Panel>
          {/* The figure the whole exercise is about. */}
          <Figure
            label={t('nx.rec.difference')}
            value={money(data.difference)}
            tone={isZero(data.difference) ? undefined : 'critical'}
          />
        </Panel>
      </div>

      <FormError message={actionError} className="mb-4" />

      {signedOff ? (
        <p className="mb-4 max-w-prose text-caption text-muted">{t('nx.rec.frozen')}</p>
      ) : null}
      {signedOff && !data.reconciled ? (
        // Two different questions, and here they disagree. Reported rather
        // than smoothed over: something on this account has changed since
        // somebody put their name to it, and that is worth knowing.
        <p className="mb-4 max-w-prose text-caption text-caution-fg" role="status">
          {t('nx.rec.stillOff')}
        </p>
      ) : null}

      {chosen ? (
        <p
          role="status"
          className="mb-4 rounded-sm border border-info/25 bg-info-subtle px-3 py-2 text-body text-info-fg"
        >
          {t('nx.rec.picking', {
            what: `${chosen.description} ${money(chosen.amount)}`,
          })}
        </p>
      ) : null}

      <div className="grid gap-6 xl:grid-cols-2">
        <Panel flush title={t('nx.rec.bankSide')}>
          <DataTable
            caption={t('nx.rec.bankCaption')}
            columns={bankColumns}
            rows={bankLines}
            rowKey={(l) => l.id}
            isSelected={(l) => l.id === pairing}
            className="rounded-none border-0"
          />
          {bankLines.every((l) => l.matched_to) ? (
            <p className="border-t border-line px-4 py-3 text-caption text-positive-fg">
              {t('nx.rec.nothingUnmatchedBank')}
            </p>
          ) : null}
        </Panel>

        <Panel flush title={t('nx.rec.booksSide')}>
          {ledger.length > 0 ? (
            <DataTable
              caption={t('nx.rec.booksCaption')}
              columns={ledgerColumns}
              rows={ledger}
              rowKey={(l) => l.id}
              isSelected={(l) => Boolean(suggested?.has(l.id))}
              className="rounded-none border-0"
            />
          ) : (
            <p className="px-4 py-3 text-caption text-positive-fg">
              {t('nx.rec.nothingUnmatchedBooks')}
            </p>
          )}
        </Panel>
      </div>

      {!signedOff ? (
        <div className="mt-6">
          <Button variant="primary" busy={busy} onClick={() => void signOff()}>
            {t('nx.rec.signOff')}
          </Button>
          <p className="mt-2 max-w-prose text-caption text-muted">
            {t('nx.rec.signOffHint')}
          </p>
        </div>
      ) : null}
    </>
  );
}

export default function StatementPage({
  params,
}: {
  params: Promise<{ statementID: string }>;
}) {
  const { statementID } = use(params);
  return (
    <RequirePermission anyOf={['accounting.reconcile']}>
      <StatementScreen statementID={statementID} />
    </RequirePermission>
  );
}
