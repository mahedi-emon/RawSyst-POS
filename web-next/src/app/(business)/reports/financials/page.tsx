'use client';

// The four statements.
//
// # One screen, because they are read together
//
// An accountant asks "how did we do" and then "so what do we own" and then
// "where did the cash go", for the same period, in that order. Four sidebar
// entries would make that three navigations and lose the period each time.
//
// # Two of them stand at a date and two cover a range
//
// A balance sheet says what the business owns on the 31st; a profit and loss
// says what it earned between two dates. So the from-date is offered only where
// it changes something, rather than sitting there greyed or — worse — accepted
// and ignored.
//
// # Nothing here adds anything up
//
// The server draws every figure from the ledger, decides whether the balance
// sheet balances, and says so. A client that recomputed any of it would be a
// second answer that could disagree with the first, on the numbers a business
// is taxed on.

import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Input } from '@/components/ui/field';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { Tabs, TabPanel } from '@/components/ui/tabs';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isNegative, isZero } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  STATEMENTS,
  isPeriodic,
  netMovement,
  yearToDate,
  type BalanceSheet,
  type CashFlow,
  type ProfitAndLoss,
  type StatementKind,
  type StatementLine,
  type TrialBalance,
  type TrialBalanceRow,
} from '@/lib/reports/statements';
import { useUrlState } from '@/lib/url-state';

const TAB_LABEL: Record<StatementKind, Key> = {
  profit: 'nx.fin.tProfit',
  balance: 'nx.fin.tBalance',
  cash: 'nx.fin.tCash',
  trial: 'nx.fin.tTrial',
};

const PATH: Record<StatementKind, string> = {
  profit: '/reports/profit-and-loss',
  balance: '/reports/balance-sheet',
  cash: '/reports/cash-flow',
  trial: '/reports/trial-balance',
};

function FinancialsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const period = yearToDate();
  const [rawKind, setKind] = useUrlState('on', 'profit');
  const [from, setFrom] = useUrlState('from', period.from);
  const [to, setTo] = useUrlState('to', period.to);

  const kind = (STATEMENTS as readonly string[]).includes(rawKind)
    ? (rawKind as StatementKind)
    : 'profit';
  const periodic = isPeriodic(kind);

  const { data, isLoading, error, refetch } = useApi<
    ProfitAndLoss | BalanceSheet | CashFlow | TrialBalance
  >(
    scope ? PATH[kind] : null,
    scope
      ? {
          ...scope,
          // A date-stamped statement is asked for `as_of`; a periodic one for
          // both ends. Sending `from` to a balance sheet would be sending a
          // parameter it has no use for.
          ...(periodic ? { from, to } : { as_of: to }),
        }
      : undefined,
  );

  const money = (v: string) =>
    formatMoney(v, {
      currency: (data as { base_currency?: string })?.base_currency || currency,
      market,
    });

  return (
    <>
      <PageHeader title={t('nx.fin.title')} description={t('nx.fin.subtitle')} />

      <Tabs<StatementKind>
        label={t('nx.fin.title')}
        value={kind}
        onChange={setKind}
        items={STATEMENTS.map((s) => ({ id: s, label: t(TAB_LABEL[s]) }))}
      />

      <TabPanel id={kind}>
        <div className="mb-5 flex flex-wrap items-end gap-3">
          {periodic ? (
            <label className="flex flex-col gap-1">
              <span className="text-label text-muted">{t('nx.fin.from')}</span>
              <Input
                type="date"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
                className="w-auto"
              />
            </label>
          ) : null}
          <label className="flex flex-col gap-1">
            <span className="text-label text-muted">{t('nx.fin.to')}</span>
            <Input
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="w-auto"
            />
          </label>
          {!periodic ? (
            <p className="max-w-prose pb-2 text-caption text-muted">
              {t('nx.fin.atDate')}
            </p>
          ) : null}
        </div>

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={3} /> : null}

        {data && kind === 'profit' ? (
          <ProfitPanel data={data as ProfitAndLoss} money={money} />
        ) : null}
        {data && kind === 'balance' ? (
          <BalancePanel data={data as BalanceSheet} money={money} />
        ) : null}
        {data && kind === 'cash' ? (
          <CashPanel data={data as CashFlow} money={money} />
        ) : null}
        {data && kind === 'trial' ? (
          <TrialPanel data={data as TrialBalance} money={money} />
        ) : null}
      </TabPanel>
    </>
  );
}

type Money = (v: string) => string;

/** A block of statement lines with its own total. */
function Section({
  title,
  lines,
  total,
  money,
  strong,
}: {
  title: string;
  lines: StatementLine[];
  /** Omitted where the server states no total for the section. */
  total?: string;
  money: Money;
  strong?: boolean;
}) {
  const t = useT();
  const columns: Column<StatementLine>[] = [
    {
      key: 'account',
      header: t('nx.fin.colAccount'),
      primary: true,
      cell: (l) => (
        <span>
          <span className="num text-muted">{l.code}</span> {l.name}
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.fin.colAmount'),
      numeric: true,
      width: 'w-44',
      cell: (l) => <span className="num">{money(l.amount)}</span>,
    },
  ];

  return (
    <Panel flush title={title} className="mb-5">
      {lines.length > 0 ? (
        <DataTable
          caption={title}
          columns={columns}
          rows={lines}
          rowKey={(l) => l.account_id ?? l.code}
          className="rounded-none border-0"
        />
      ) : null}
      {total !== undefined ? (
        <div
          className={
            'flex items-baseline justify-between gap-4 px-4 py-3 ' +
            (strong
              ? 'border-t-[3px] border-double border-line-strong font-semibold'
              : 'border-t border-line')
          }
        >
          <span className="text-body">{title}</span>
          <span className="num text-body">{money(total)}</span>
        </div>
      ) : null}
    </Panel>
  );
}

function ProfitPanel({ data, money }: { data: ProfitAndLoss; money: Money }) {
  const t = useT();
  const loss = isNegative(data.net_profit);

  if (
    data.revenue.length === 0 &&
    data.cost_of_sales.length === 0 &&
    data.expenses.length === 0
  ) {
    return (
      <EmptyState title={t('nx.fin.nothing')} description={t('nx.fin.nothingDesc')} />
    );
  }

  return (
    <>
      <Section
        title={t('nx.fin.revenue')}
        lines={data.revenue}
        total={data.revenue_total}
        money={money}
      />
      <Section
        title={t('nx.fin.costOfSales')}
        lines={data.cost_of_sales}
        total={data.cost_of_sales_total}
        money={money}
      />
      <Panel className="mb-5">
        <Figure label={t('nx.fin.grossProfit')} value={money(data.gross_profit)} />
      </Panel>
      <Section
        title={t('nx.fin.expenses')}
        lines={data.expenses}
        total={data.expenses_total}
        money={money}
      />
      <Panel>
        {/* A loss is named a loss. "Net profit: −11,385" makes somebody read
            the minus sign to find out, and the minus sign is the thing that
            gets missed. */}
        <Figure
          label={loss ? t('nx.fin.netLoss') : t('nx.fin.netProfit')}
          value={money(data.net_profit)}
          tone={loss ? 'critical' : 'positive'}
        />
      </Panel>
    </>
  );
}

function BalancePanel({ data, money }: { data: BalanceSheet; money: Money }) {
  const t = useT();
  return (
    <>
      <p className="mb-4 text-label text-muted">
        {t('nx.fin.asOf', { date: data.as_of })}
      </p>
      <Section
        title={t('nx.fin.assets')}
        lines={data.assets}
        total={data.assets_total}
        money={money}
        strong
      />
      <Section
        title={t('nx.fin.liabilities')}
        lines={data.liabilities}
        total={data.liabilities_total}
        money={money}
      />
      <Section
        title={t('nx.fin.equity')}
        lines={data.equity}
        total={data.equity_total}
        money={money}
      />
      <Panel className="mb-5">
        {/* This year's profit is equity that has not been closed off yet, and
            leaving it out is why a balance sheet appears not to balance. */}
        <Figure
          label={t('nx.fin.currentEarnings')}
          value={money(data.current_earnings)}
        />
      </Panel>

      <div className="rounded-md border border-line bg-surface p-4">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <span className="text-body font-semibold">{t('nx.fin.liabilities')}</span>
          <span className="num text-body font-semibold">
            {money(data.equity_and_liabilities)}
          </span>
        </div>
        <p className="mt-2">
          {/* The SERVER's answer. Recomputing it here would be a second
              opinion on whether the books hold together. */}
          {data.balanced ? (
            <Badge tone="positive">{t('nx.fin.balanced')}</Badge>
          ) : (
            <Badge tone="critical">
              {t('nx.fin.notBalanced', { amount: money(data.difference) })}
            </Badge>
          )}
        </p>
        {!data.balanced ? (
          <p className="mt-2 max-w-prose text-caption text-critical-fg">
            {t('nx.fin.notBalancedHint')}
          </p>
        ) : null}
      </div>
    </>
  );
}

function CashPanel({ data, money }: { data: CashFlow; money: Money }) {
  const t = useT();
  return (
    <>
      <div className="mb-5 grid gap-4 sm:grid-cols-2">
        <Panel>
          <Figure label={t('nx.fin.opening')} value={money(data.opening)} />
        </Panel>
        <Panel>
          <Figure
            label={t('nx.fin.closing')}
            value={money(data.closing)}
            tone={isNegative(data.closing) ? 'critical' : undefined}
          />
        </Panel>
      </div>
      {/* No section totals. The payload carries none, and summing the lines
          here would be a second answer to a question the two balances above
          already settle -- one that could disagree the moment the server
          groups a line differently. */}
      <Section title={t('nx.fin.cashIn')} lines={data.in} money={money} />
      <Section title={t('nx.fin.cashOut')} lines={data.out} money={money} />

      <Panel>
        <Figure
          label={t('nx.fin.movement')}
          value={money(netMovement(data))}
          tone={isNegative(netMovement(data)) ? 'critical' : 'positive'}
        />
      </Panel>
    </>
  );
}

function TrialPanel({ data, money }: { data: TrialBalance; money: Money }) {
  const t = useT();
  const columns: Column<TrialBalanceRow>[] = [
    {
      key: 'account',
      header: t('nx.fin.colAccount'),
      primary: true,
      cell: (r) => (
        <span>
          <span className="num text-muted">{r.code}</span> {r.name}
        </span>
      ),
    },
    {
      key: 'debit',
      header: t('nx.fin.colDebit'),
      numeric: true,
      width: 'w-40',
      // An empty cell, not a zero. A trial balance is read by scanning which
      // side each account sits on, and a column of 0.00 destroys that.
      cell: (r) =>
        isZero(r.debit) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num">{money(r.debit)}</span>
        ),
    },
    {
      key: 'credit',
      header: t('nx.fin.colCredit'),
      numeric: true,
      width: 'w-40',
      cell: (r) =>
        isZero(r.credit) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num">{money(r.credit)}</span>
        ),
    },
  ];

  if (data.rows.length === 0) {
    return (
      <EmptyState title={t('nx.fin.nothing')} description={t('nx.fin.nothingDesc')} />
    );
  }

  return (
    <>
      <p className="mb-4 text-label text-muted">
        {t('nx.fin.asOf', { date: data.as_of })}
      </p>
      <DataTable
        caption={t('nx.fin.tTrial')}
        columns={columns}
        rows={data.rows}
        rowKey={(r) => r.account_id}
      />
      <p className="mt-4 max-w-prose text-caption text-muted">
        {t('nx.fin.trialHint')}
      </p>
    </>
  );
}

export default function FinancialsPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <FinancialsScreen />
      </Suspense>
    </RequirePermission>
  );
}
