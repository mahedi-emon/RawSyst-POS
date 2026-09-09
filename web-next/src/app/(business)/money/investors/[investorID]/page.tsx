'use client';

// One investor's capital account, over a period.
//
// # Why this is a screen of its own rather than a panel on the register
//
// C3.2 asks for a statement an investor can be given access to and read for
// themselves — "each investor can (if given access) see only their own
// contribution/return history" — and the route enforces exactly that: staff
// holding `investor.manage` may read anybody's, and a person linked to an
// investor record may read one, their own. A panel inside the register would
// put that reader behind a list of every other partner's holdings.
//
// So it has a URL. It is reached from a row on the register, and it is a link
// somebody can be sent.
//
// # The period is the server's, and so is every figure on it
//
// `from` and `to` go to the route; the opening balance, the two totals and the
// closing balance come back. The only arithmetic done here is the running
// balance down the rows, and it is done in decimal — see `lib/money/investors`.
//
// # There is no download, because there is no export route
//
// `GET /reports/{kind}/export` serves eight named reports and a capital account
// is not one of them. A button that produced a file this side would be a second
// statement, formatted differently from the one the server can vouch for.

import { ArrowLeft, HandCoins } from 'lucide-react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isNegative } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  closesWhereItSays,
  movedInPeriod,
  periodProblem,
  periodQuery,
  withRunningBalance,
  type InvestorStatement,
  type StatementRow,
} from '@/lib/money/investors';
import { useUrlState } from '@/lib/url-state';

const PROBLEM: Record<'backwards' | 'unreadable', Key> = {
  backwards: 'nx.inv.periodBackwards',
  unreadable: 'nx.inv.periodUnreadable',
};

function StatementScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const params = useParams<{ investorID: string }>();
  const investorID = params.investorID;

  // In the URL, so a period can be sent to somebody and survives a refresh.
  const [from, setFrom] = useUrlState('from');
  const [to, setTo] = useUrlState('to');

  // The boxes are local so every keystroke paints; the URL catches up when the
  // period is applied, which is also when the request is made. A statement that
  // refetched on each digit of a date would fire four requests for one period.
  const [draftFrom, setDraftFrom] = useState(from);
  const [draftTo, setDraftTo] = useState(to);

  const problem = periodProblem({ from: draftFrom, to: draftTo });
  const applied = { from, to };

  const { data, isLoading, error, refetch } = useApi<{ data: InvestorStatement }>(
    scope && investorID ? `/investors/${investorID}/statement` : null,
    scope ? { ...scope, ...periodQuery(applied) } : undefined,
  );

  const statement = data?.data;
  const money = (v: string) => formatMoney(v, { currency: statement?.currency || currency, market });
  const rows = statement ? withRunningBalance(statement) : [];

  const columns: Column<StatementRow>[] = [
    {
      key: 'when',
      header: t('nx.inv.colWhen'),
      primary: true,
      width: 'w-36',
      cell: (r) => <time dateTime={r.moved_on}>{r.moved_on}</time>,
    },
    {
      key: 'what',
      header: t('nx.inv.colWhat'),
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span>{r.outward ? t('nx.inv.drawing') : t('nx.inv.capital')}</span>
          {r.note ? <span className="text-caption text-muted">{r.note}</span> : null}
        </span>
      ),
    },
    {
      key: 'account',
      header: t('nx.inv.colAccount'),
      secondary: true,
      // The cash or bank account the money actually moved through. Empty when
      // the account has since been removed, which is a fact rather than a gap.
      cell: (r) => <span className="text-muted">{r.account || '—'}</span>,
    },
    {
      key: 'reference',
      header: t('nx.inv.colReference'),
      secondary: true,
      cell: (r) => <span className="num text-muted">{r.reference || '—'}</span>,
    },
    {
      key: 'in',
      header: t('nx.inv.colIn'),
      numeric: true,
      width: 'w-36',
      cell: (r) => (r.outward ? <span className="text-disabled">—</span> : money(r.amount)),
    },
    {
      key: 'out',
      header: t('nx.inv.colOut'),
      numeric: true,
      width: 'w-36',
      cell: (r) => (r.outward ? money(r.amount) : <span className="text-disabled">—</span>),
    },
    {
      key: 'balance',
      header: t('nx.inv.colBalance'),
      numeric: true,
      width: 'w-40',
      cell: (r) => (
        <span className={isNegative(r.balance) ? 'text-critical-fg' : undefined}>
          {money(r.balance)}
        </span>
      ),
    },
  ];

  function apply() {
    if (problem) return;
    setFrom(draftFrom);
    setTo(draftTo);
  }

  function clear() {
    setDraftFrom('');
    setDraftTo('');
    setFrom('');
    setTo('');
  }

  return (
    <>
      <PageHeader
        breadcrumb={
          <Link
            href="/money/investors"
            className="mb-1 inline-flex items-center gap-1 text-label text-muted hover:text-fg"
          >
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            {t('nx.inv.backToRegister')}
          </Link>
        }
        title={statement?.investor ?? t('nx.inv.statementTitle')}
        description={t('nx.inv.statementSubtitle')}
      />

      <Panel className="mb-5" title={t('nx.inv.periodTitle')} description={t('nx.inv.periodHint')}>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4 lg:items-end">
          <Field name="from" label={t('nx.inv.from')}>
            <Input
              type="date"
              value={draftFrom}
              onChange={(e) => setDraftFrom(e.target.value)}
              className="num"
            />
          </Field>
          <Field name="to" label={t('nx.inv.to')}>
            <Input
              type="date"
              value={draftTo}
              onChange={(e) => setDraftTo(e.target.value)}
              className="num"
            />
          </Field>
          <div className="flex gap-2">
            <Button variant="primary" disabled={problem !== null} onClick={apply}>
              {t('nx.inv.applyPeriod')}
            </Button>
            {from || to ? (
              <Button variant="ghost" onClick={clear}>
                {t('nx.inv.wholeHistory')}
              </Button>
            ) : null}
          </div>
        </div>
        {problem ? (
          <p className="mt-3 text-caption text-critical-fg" role="alert">
            {t(PROBLEM[problem])}
          </p>
        ) : null}
      </Panel>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      {isLoading && !statement ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-20" />
          ))}
        </div>
      ) : null}

      {statement ? (
        <>
          <section className="grid gap-5 rounded-md border border-line bg-surface p-4 sm:grid-cols-2 lg:grid-cols-4">
            <Figure
              label={t('nx.inv.opening')}
              value={money(statement.opening)}
              caption={
                statement.from
                  ? t('nx.inv.asAt', { date: statement.from })
                  : t('nx.inv.beforeAnything')
              }
            />
            <Figure label={t('nx.inv.putIn')} value={money(statement.contributed)} />
            <Figure label={t('nx.inv.tookOut')} value={money(statement.withdrawn)} />
            <Figure
              label={t('nx.inv.closing')}
              value={money(statement.closing)}
              caption={t('nx.inv.movedNet', { net: money(movedInPeriod(statement)) })}
              tone={isNegative(statement.closing) ? 'critical' : undefined}
            />
          </section>

          {!closesWhereItSays(statement) ? (
            // Two figures on one document that disagree is what somebody signs
            // and then has to explain. Said here rather than left to be found
            // by adding up a column.
            <p className="mt-3 text-body text-caution-fg" role="alert">
              {t('nx.inv.doesNotClose')}
            </p>
          ) : null}

          <div className="mt-8 mb-3 flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-card-title font-semibold text-fg">{t('nx.inv.movements')}</h2>
            <Badge tone="neutral">
              {statement.from || statement.to
                ? t('nx.inv.periodBadge', {
                    from: statement.from || t('nx.inv.theStart'),
                    to: statement.to || t('nx.inv.today'),
                  })
                : t('nx.inv.wholeHistoryBadge')}
            </Badge>
          </div>

          {rows.length === 0 ? (
            <EmptyState
              icon={HandCoins}
              title={t('nx.inv.noMovementsTitle')}
              description={t('nx.inv.noMovementsDesc')}
              action={
                <Button asChild variant="secondary">
                  <Link href="/money/investors">{t('nx.inv.backToRegister')}</Link>
                </Button>
              }
            />
          ) : (
            <DataTable
              caption={t('nx.inv.movements')}
              columns={columns}
              rows={rows}
              rowKey={(r) => r.id}
              totals={
                <>
                  <td className="px-3 py-2.5">{t('nx.inv.closing')}</td>
                  <td className="px-3 py-2.5" />
                  <td className="hidden px-3 py-2.5 md:table-cell" />
                  <td className="hidden px-3 py-2.5 md:table-cell" />
                  <td className="num px-3 py-2.5 text-end">{money(statement.contributed)}</td>
                  <td className="num px-3 py-2.5 text-end">{money(statement.withdrawn)}</td>
                  <td className="num px-3 py-2.5 text-end">{money(statement.closing)}</td>
                </>
              }
            />
          )}
        </>
      ) : null}

      {isLoading && !statement ? <TableSkeleton columns={7} rows={4} /> : null}
    </>
  );
}

export default function InvestorStatementPage() {
  return (
    <RequirePermission anyOf={['investor.view']} backHref="/money/investors">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <StatementScreen />
      </Suspense>
    </RequirePermission>
  );
}
