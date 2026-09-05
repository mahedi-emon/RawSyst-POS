'use client';

// An ageing table, which the shop needs twice.
//
// What suppliers are owed and what customers owe are the same report pointed in
// opposite directions, and they were about to be the same two hundred lines
// written twice. The five buckets, the em dashes, the weighting and the total
// are identical; the first column and the words around it are not.
//
// # Aged from the DUE date
//
// A 60-day invoice raised 45 days ago is not overdue, and ageing it from issue
// would say it was — which puts somebody in a chasing queue they do not belong
// in. Both services do it this way and both screens say so, because the
// distinction is invisible in a table of five columns.
//
// # Zero is an em dash
//
// Five columns of "0.00" with one figure among them is a table nobody can read.
// Five em dashes with one figure among them answers the question from across
// the room.
//
// # The overdue columns carry weight and the not-due one does not
//
// Everything right of "not due yet" is late, and lateness is why somebody
// opened this. So the first column is quiet, the middle three are marked, and
// 90-plus is the one that means somebody has stopped answering the phone.

import { Panel } from '@/components/ui/panel';
import { DataTable, type Column } from '@/components/ui/table';
import { formatMoney, isZero, type MarketCode } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

/** The five buckets every ageing row carries, whichever way it points. */
export interface AgeingBuckets {
  not_due: string;
  days_0_30: string;
  days_31_60: string;
  days_61_90: string;
  days_90_plus: string;
  total: string;
}

export interface AgeingReport<Row extends AgeingBuckets> {
  as_of: string;
  rows: Row[];
  total: string;
  /** The report names its own currency, which is the company's base. */
  base_currency: string;
}

export function AgeingTable<Row extends AgeingBuckets>({
  report,
  party,
  rowKey,
  caption,
  totalLabel,
  market,
}: {
  report: AgeingReport<Row>;
  /** The column that says who. The only part that differs between the two. */
  party: Column<Row>;
  rowKey: (row: Row) => string;
  caption: string;
  totalLabel: string;
  market: MarketCode;
}) {
  const t = useT();

  // Read off the payload rather than the company context, so the figures and
  // the currency code can never disagree.
  const currency = report.base_currency;
  const money = (v: string) =>
    isZero(v) ? null : formatMoney(v, { currency, market });

  function bucket(value: string, late: boolean) {
    const shown = money(value);
    if (!shown) return <span className="text-subtle">—</span>;
    return (
      <span className={late ? 'num font-medium text-caution-fg' : 'num'}>
        {shown}
      </span>
    );
  }

  const columns: Column<Row>[] = [
    party,
    {
      key: 'not_due',
      header: t('nx.age.colNotDue'),
      numeric: true,
      secondary: true,
      cell: (r) => bucket(r.not_due, false),
    },
    {
      key: 'd030',
      header: t('nx.age.col030'),
      numeric: true,
      cell: (r) => bucket(r.days_0_30, true),
    },
    {
      key: 'd3160',
      header: t('nx.age.col3160'),
      numeric: true,
      cell: (r) => bucket(r.days_31_60, true),
    },
    {
      key: 'd6190',
      header: t('nx.age.col6190'),
      numeric: true,
      cell: (r) => bucket(r.days_61_90, true),
    },
    {
      key: 'd90',
      header: t('nx.age.col90'),
      numeric: true,
      // The one that means somebody has stopped answering the phone.
      cell: (r) => {
        const shown = money(r.days_90_plus);
        return shown ? (
          <span className="num font-semibold text-critical-fg">{shown}</span>
        ) : (
          <span className="text-subtle">—</span>
        );
      },
    },
    {
      key: 'total',
      header: t('nx.age.colTotal'),
      numeric: true,
      cell: (r) => <span className="num font-medium">{money(r.total) ?? '—'}</span>,
    },
  ];

  return (
    <Panel flush>
      <DataTable
        caption={caption}
        columns={columns}
        rows={report.rows}
        rowKey={rowKey}
        className="rounded-none border-0"
      />
      <div className="flex items-baseline justify-between gap-4 border-t-[3px] border-double border-line-strong px-4 py-3">
        <span className="text-body font-semibold">{totalLabel}</span>
        <span className="num text-body font-semibold">
          {formatMoney(report.total, { currency, market })}
        </span>
      </div>
    </Panel>
  );
}
