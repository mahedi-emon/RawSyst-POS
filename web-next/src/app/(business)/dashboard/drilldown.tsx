'use client';

// What is behind a figure on the dashboard.
//
// # The three routes this uses, and why they existed unreached
//
// `GET /dashboard/expenses`, `/dashboard/compliance` and `/dashboard/stock`
// answer the postings behind a day's expenses, the invoices that have not
// finished reporting, and the lines running low. All three were live, tested
// and reachable from nothing: the dashboard printed a total and the reader had
// nowhere to click.
//
// A figure a person cannot open is trivia. "Expenses today: 4,120" is a number
// somebody either believes or does not; the four postings behind it are the
// thing they can act on.
//
// # The compliance queue says the honest thing
//
// `signing_available` reports the P1 gate. When it is false the terminal cannot
// yet sign, and that is WHY those invoices are outstanding — not a flaky
// network. The screen says so rather than offering a retry that cannot work,
// because implying that submission is working is the single most damaging thing
// this product could claim while that gate is open.

import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { formatMoney, formatQuantity, type MarketCode } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';

/** One posting behind an expense account. */
interface ExpenseEntry {
  entry_id: string;
  entry_no: string;
  date: string;
  memo: string;
  account_id: string;
  account: string;
  account_ar?: string;
  code: string;
  amount: string;
  /** What caused the posting: a sale, a return, an adjustment. */
  source_type?: string;
}

interface ExpensesDetail {
  date: string;
  entries: ExpenseEntry[];
  total: string;
  base_currency: string;
}

/** One invoice that has not finished reporting. */
interface ComplianceRow {
  invoice_id: string;
  human_number?: string;
  doc_type: string;
  state: string;
  issued_at: string;
  total_inclusive: string;
  /** Its position on the terminal chain. A gap is what tamper detection looks for. */
  icv: number;
  age_hours: number;
  attempts: number;
  last_error?: string;
}

interface ComplianceQueue {
  rows: ComplianceRow[];
  outstanding: number;
  oldest_hours: number;
  base_currency: string;
  /** False means the terminal cannot yet sign, which is why these are waiting. */
  signing_available: boolean;
}

interface StockRow {
  variant_id: string;
  sku: string;
  name: string;
  barcode?: string;
  on_hand: string;
  reorder_level?: string;
  value: string;
}

interface StockDetail {
  filter: string;
  rows: StockRow[];
  count: number;
  base_currency: string;
}

/** Design 08 §4: notice past 12 hours, warning past 24, critical past 72. */
function escalation(hours: number): 'critical' | 'caution' | 'info' {
  if (hours > 72) return 'critical';
  if (hours > 24) return 'caution';
  return 'info';
}

const ESCALATION_LABEL: Record<string, Key> = {
  critical: 'nx.dash.drillOverdue',
  caution: 'nx.dash.drillLate',
  info: 'nx.dash.drillWaiting',
};

export function ExpensesBehind({
  companyId,
  day,
  currency,
  market,
}: {
  companyId: string;
  day: string;
  currency: string;
  market: MarketCode;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const { data, isLoading, error, refetch } = useApi<ExpensesDetail>(
    open ? '/dashboard/expenses' : null,
    { company_id: companyId, day },
  );

  const money = (v: string) =>
    formatMoney(v, { currency: data?.base_currency || currency, market });

  const columns: Column<ExpenseEntry>[] = [
    {
      key: 'entry',
      header: t('nx.dash.drillEntry'),
      primary: true,
      cell: (e) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{e.entry_no}</span>
          <span className="text-caption text-muted">{e.memo || '—'}</span>
        </span>
      ),
    },
    {
      key: 'account',
      header: t('nx.dash.drillAccount'),
      cell: (e) => (
        <span className="flex flex-col gap-0.5">
          <span>{e.account}</span>
          <span className="num text-caption text-muted">{e.code}</span>
        </span>
      ),
    },
    {
      key: 'cause',
      header: t('nx.dash.drillCause'),
      secondary: true,
      width: 'w-40',
      // What created the posting. An owner asking "what is this expense" is
      // usually asking what caused it, not which account it landed in.
      cell: (e) => (
        <span className="text-muted">
          {e.source_type ? e.source_type.replace(/_/g, ' ') : '—'}
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.dash.drillAmount'),
      numeric: true,
      width: 'w-36',
      cell: (e) => money(e.amount),
    },
  ];

  return (
    <Drill
      title={t('nx.dash.expensesBehindTitle')}
      description={t('nx.dash.expensesBehindHint')}
      open={open}
      onToggle={() => setOpen((v) => !v)}
    >
      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <Skeleton className="h-32" /> : null}
      {data && data.entries.length === 0 ? (
        <EmptyState
          title={t('nx.dash.noExpensesTitle')}
          description={t('nx.dash.noExpensesDesc')}
        />
      ) : null}
      {data && data.entries.length > 0 ? (
        <DataTable
          caption={t('nx.dash.expensesBehindTitle')}
          columns={columns}
          rows={data.entries}
          rowKey={(e) => `${e.entry_id}-${e.account_id}`}
          totals={
            <>
              <td className="px-3 py-2.5">{t('nx.dash.drillTotal')}</td>
              <td className="px-3 py-2.5" />
              <td className="hidden px-3 py-2.5 md:table-cell" />
              <td className="num px-3 py-2.5 text-end">{money(data.total)}</td>
            </>
          }
        />
      ) : null}
    </Drill>
  );
}

export function ComplianceBehind({
  companyId,
  currency,
  market,
}: {
  companyId: string;
  currency: string;
  market: MarketCode;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const { data, isLoading, error, refetch } = useApi<ComplianceQueue>(
    open ? '/dashboard/compliance' : null,
    { company_id: companyId, limit: 50 },
  );

  const money = (v: string) =>
    formatMoney(v, { currency: data?.base_currency || currency, market });

  const columns: Column<ComplianceRow>[] = [
    {
      key: 'invoice',
      header: t('nx.dash.drillInvoice'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">
            {r.human_number || r.invoice_id.slice(0, 8)}
          </span>
          <span className="text-caption text-muted">
            {r.doc_type} · {r.issued_at.slice(0, 16).replace('T', ' ')}
          </span>
        </span>
      ),
    },
    {
      key: 'icv',
      header: t('nx.dash.drillIcv'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      // Its position on the terminal chain. A gap in the sequence is exactly
      // what tamper detection looks for.
      cell: (r) => <span className="num">{r.icv}</span>,
    },
    {
      key: 'waiting',
      header: t('nx.dash.drillWaitingFor'),
      width: 'w-44',
      cell: (r) => (
        <span className="flex flex-col items-start gap-1">
          <Badge tone={escalation(r.age_hours)}>
            {t(ESCALATION_LABEL[escalation(r.age_hours)] as Key)}
          </Badge>
          <span className="num text-caption text-muted">
            {t('nx.dash.drillHours', { n: String(r.age_hours) })}
          </span>
        </span>
      ),
    },
    {
      key: 'attempts',
      header: t('nx.dash.drillAttempts'),
      numeric: true,
      secondary: true,
      width: 'w-24',
      cell: (r) => <span className="num">{r.attempts}</span>,
    },
    {
      key: 'total',
      header: t('nx.dash.drillAmount'),
      numeric: true,
      width: 'w-36',
      cell: (r) => money(r.total_inclusive),
    },
  ];

  return (
    <Drill
      title={t('nx.dash.complianceTitle')}
      description={t('nx.dash.complianceHint')}
      open={open}
      onToggle={() => setOpen((v) => !v)}
    >
      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <Skeleton className="h-32" /> : null}

      {data && !data.signing_available ? (
        // The honest sentence. These invoices are not waiting on a network and
        // no retry will move them.
        <p className="mb-4 max-w-prose rounded-sm border border-caution/25 bg-caution-subtle p-3 text-body text-caution-fg">
          {t('nx.dash.signingUnavailable')}
        </p>
      ) : null}

      {data && data.rows.length === 0 ? (
        <EmptyState
          title={t('nx.dash.noComplianceTitle')}
          description={t('nx.dash.noComplianceDesc')}
        />
      ) : null}

      {data && data.rows.length > 0 ? (
        <>
          <p className="mb-3 text-body">
            {t('nx.dash.outstandingCount', {
              n: String(data.outstanding),
              hours: String(data.oldest_hours),
            })}
          </p>
          <DataTable
            caption={t('nx.dash.complianceTitle')}
            columns={columns}
            rows={data.rows}
            rowKey={(r) => r.invoice_id}
          />
        </>
      ) : null}
    </Drill>
  );
}

export function StockBehind({
  companyId,
  currency,
  market,
}: {
  companyId: string;
  currency: string;
  market: MarketCode;
}) {
  const t = useT();
  const [filter, setFilter] = useState<'low' | 'out' | null>(null);
  const { data, isLoading, error, refetch } = useApi<StockDetail>(
    filter ? '/dashboard/stock' : null,
    { company_id: companyId, filter: filter ?? 'low' },
  );

  const money = (v: string) =>
    formatMoney(v, { currency: data?.base_currency || currency, market });

  const columns: Column<StockRow>[] = [
    {
      key: 'product',
      header: t('nx.dash.drillProduct'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{r.name}</span>
          <span className="num text-caption text-muted">{r.sku}</span>
        </span>
      ),
    },
    {
      key: 'on_hand',
      header: t('nx.dash.drillOnHand'),
      numeric: true,
      width: 'w-28',
      cell: (r) => (
        <span className="num">{formatQuantity(r.on_hand, market)}</span>
      ),
    },
    {
      key: 'reorder',
      header: t('nx.dash.drillReorder'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (r) =>
        r.reorder_level ? (
          <span className="num text-muted">{formatQuantity(r.reorder_level, market)}</span>
        ) : (
          <span className="text-muted">—</span>
        ),
    },
    {
      key: 'value',
      header: t('nx.dash.drillValue'),
      numeric: true,
      width: 'w-36',
      cell: (r) => money(r.value),
    },
  ];

  return (
    <Panel
      className="mt-5"
      title={t('nx.dash.stockBehindTitle')}
      description={t('nx.dash.stockBehindHint')}
      actions={
        <span className="flex gap-2">
          <Button
            size="sm"
            variant={filter === 'low' ? 'primary' : 'secondary'}
            onClick={() => setFilter(filter === 'low' ? null : 'low')}
          >
            {t('nx.dash.runningLow')}
          </Button>
          <Button
            size="sm"
            variant={filter === 'out' ? 'primary' : 'secondary'}
            onClick={() => setFilter(filter === 'out' ? null : 'out')}
          >
            {t('stock.outOfStock')}
          </Button>
        </span>
      }
    >
      {!filter ? (
        <p className="text-body text-muted">{t('nx.dash.pickAStockList')}</p>
      ) : null}
      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {filter && isLoading && !data ? <Skeleton className="h-32" /> : null}
      {filter && data && data.rows.length === 0 ? (
        <EmptyState
          title={t('nx.dash.noStockTitle')}
          description={t('nx.dash.noStockDesc')}
        />
      ) : null}
      {filter && data && data.rows.length > 0 ? (
        <DataTable
          caption={t('nx.dash.stockBehindTitle')}
          columns={columns}
          rows={data.rows}
          rowKey={(r) => r.variant_id}
        />
      ) : null}
    </Panel>
  );
}

/** A panel that fetches nothing until somebody asks to see inside it. */
function Drill({
  title,
  description,
  open,
  onToggle,
  children,
}: {
  title: string;
  description: string;
  open: boolean;
  onToggle: () => void;
  children: React.ReactNode;
}) {
  const t = useT();
  return (
    <Panel
      className="mt-5"
      title={title}
      description={description}
      actions={
        <Button size="sm" variant="secondary" onClick={onToggle}>
          {open ? t('nx.dash.drillHide') : t('nx.dash.drillShow')}
        </Button>
      }
    >
      {open ? children : <p className="text-body text-muted">{description}</p>}
    </Panel>
  );
}
