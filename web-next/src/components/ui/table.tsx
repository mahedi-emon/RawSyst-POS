'use client';

// The data table.
//
// # A table, not a grid of cards
//
// Most ERP front ends chop a list into cards and lose the one thing a list is
// for: comparison down a column. A column of totals that share a decimal point
// can be scanned in a second; the same figures in twelve cards cannot be
// scanned at all. So this is a real `<table>`, with real `<th scope>`, and it
// stays a table on a phone by scrolling rather than by becoming something else.
//
// # What happens on a phone
//
// The table scrolls inside its own container. The `primary` column is sticky at
// the inline start so the row can still be identified after scrolling sideways,
// which is the actual problem with a wide table on a small screen -- not the
// width itself, but losing track of which row you are reading.
//
// # The double rule
//
// A total row gets `rule-total`, which is a 3px double border. That is the
// accounting convention for "this is the sum of the rows above", so the rule
// carries information rather than decorating the table. It is not used for any
// row that is not a total.

import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-react';
import type { ReactNode } from 'react';

import { useT } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

export type SortDirection = 'asc' | 'desc';

export interface Column<Row> {
  /** Stable key, also used as the sort field sent to the API. */
  key: string;
  header: ReactNode;
  /** Renders the cell. Given the row and its index. */
  cell: (row: Row, index: number) => ReactNode;
  /** Money and quantities: end-aligned with tabular figures. */
  numeric?: boolean;
  /** Sortable server-side. The caller performs the sort; this only asks. */
  sortable?: boolean;
  /** The column that identifies the row. Sticky while scrolling sideways. */
  primary?: boolean;
  /** Hidden below `md`. For columns that are useful but not identifying. */
  secondary?: boolean;
  /** Fixed width, e.g. `w-28`, where a column would otherwise stretch. */
  width?: string;
  /** A short description read out for the column header. */
  headerHint?: string;
}

export interface TableProps<Row> {
  /** Describes the table for a screen reader. Required; never decorative. */
  caption: string;
  columns: readonly Column<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row, index: number) => string;
  /** Opens the record. Makes the row activatable by click and by keyboard. */
  onOpenRow?: (row: Row) => void;
  /** Marks a row as the current selection, e.g. the one open in a side panel. */
  isSelected?: (row: Row) => boolean;
  sort?: { key: string; direction: SortDirection };
  onSortChange?: (key: string, direction: SortDirection) => void;
  /** A summing row rendered in the foot, under the accounting double rule. */
  totals?: ReactNode;
  className?: string;
}

export function DataTable<Row>({
  caption,
  columns,
  rows,
  rowKey,
  onOpenRow,
  isSelected,
  sort,
  onSortChange,
  totals,
  className,
}: TableProps<Row>) {
  const t = useT();
  return (
    // The scroll container is the table's own, so a wide table never makes the
    // page scroll sideways -- which would move the navigation off screen.
    <div
      className={cn(
        'w-full overflow-x-auto overscroll-x-contain rounded-md border border-line bg-surface',
        className,
      )}
    >
      {/* `rows-lazy` skips layout and paint for rows off screen. A day of
          invoices or a stock list runs to hundreds of rows and every one of
          them was being laid out. */}
      <table className="rows-lazy w-full border-collapse text-body">
        <caption className="sr-only">{caption}</caption>

        <thead>
          <tr className="border-b border-line-strong bg-surface-sunken">
            {columns.map((col) => (
              <HeaderCell
                key={col.key}
                column={col}
                sort={sort}
                onSortChange={onSortChange}
              />
            ))}
          </tr>
        </thead>

        <tbody>
          {rows.map((row, i) => {
            const selected = isSelected?.(row) ?? false;
            const openable = Boolean(onOpenRow);
            return (
              <tr
                key={rowKey(row, i)}
                // A row that opens something is a button in every way that
                // matters, so it takes focus and answers Enter and Space. It is
                // not wrapped in an anchor, because an anchor inside every cell
                // would be read out once per cell.
                {...(openable
                  ? {
                      tabIndex: 0,
                      role: 'button' as const,
                      onClick: () => onOpenRow?.(row),
                      onKeyDown: (e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault();
                          onOpenRow?.(row);
                        }
                      },
                    }
                  : {})}
                aria-current={selected ? 'true' : undefined}
                className={cn(
                  'border-b border-line last:border-b-0',
                  openable && 'cursor-pointer hover:bg-surface-hover',
                  // The focus ring goes inside the row rather than around it,
                  // because an outline around a table row is clipped by the
                  // scroll container on the side that is scrolled away.
                  openable &&
                    'focus-visible:outline-none focus-visible:bg-surface-hover focus-visible:shadow-[inset_0_0_0_2px_var(--ry-focus)]',
                  selected && 'bg-surface-selected',
                )}
              >
                {columns.map((col) => (
                  <td
                    key={col.key}
                    className={cn(
                      'px-3 py-2.5 align-middle',
                      col.numeric && 'num text-end tabular-nums',
                      col.secondary && 'hidden md:table-cell',
                      col.primary &&
                        'sticky start-0 bg-inherit font-medium text-fg',
                      col.width,
                    )}
                  >
                    {col.cell(row, i)}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>

        {totals && (
          <tfoot>
            <tr className="rule-total bg-surface-sunken font-semibold">
              {totals}
            </tr>
          </tfoot>
        )}
      </table>
    </div>
  );
}

function HeaderCell<Row>({
  column,
  sort,
  onSortChange,
}: {
  column: Column<Row>;
  sort?: { key: string; direction: SortDirection };
  onSortChange?: (key: string, direction: SortDirection) => void;
}) {
  const active = sort?.key === column.key;
  const direction = active ? sort.direction : undefined;
  const sortable = column.sortable && onSortChange;

  const classes = cn(
    'px-3 py-2 text-label font-semibold text-muted',
    // Headers align with their column. A start-aligned header over an
    // end-aligned column of figures is the commonest way a money table stops
    // being scannable.
    column.numeric ? 'text-end' : 'text-start',
    column.secondary && 'hidden md:table-cell',
    column.primary && 'sticky start-0 bg-surface-sunken',
    column.width,
  );

  return (
    <th
      scope="col"
      className={classes}
      // Announced by a screen reader as the sort state of this column, which is
      // what `aria-sort` is for. Only the active column carries it.
      aria-sort={
        active ? (direction === 'asc' ? 'ascending' : 'descending') : undefined
      }
      title={column.headerHint}
    >
      {sortable ? (
        <button
          type="button"
          onClick={() =>
            onSortChange(column.key, direction === 'asc' ? 'desc' : 'asc')
          }
          className={cn(
            'inline-flex items-center gap-1 rounded-xs hover:text-fg',
            column.numeric && 'flex-row-reverse',
          )}
        >
          {column.header}
          {!active && (
            <ChevronsUpDown className="size-3.5 opacity-50" aria-hidden="true" />
          )}
          {active && direction === 'asc' && (
            <ArrowUp className="size-3.5" aria-hidden="true" />
          )}
          {active && direction === 'desc' && (
            <ArrowDown className="size-3.5" aria-hidden="true" />
          )}
        </button>
      ) : (
        column.header
      )}
    </th>
  );
}

/**
 * The loading state for a table.
 *
 * Shaped like the table it replaces -- same column count, same row height -- so
 * the page does not reflow when the data lands. A spinner in the middle of an
 * empty box moves everything twice.
 */
export function TableSkeleton({
  columns,
  rows = 8,
}: {
  columns: number;
  rows?: number;
}) {
  const t = useT();
  return (
    <div
      className="w-full overflow-hidden rounded-md border border-line bg-surface"
      // Announced once, not per row.
      role="status"
      aria-label={t('nx.tbl.loading')}
    >
      <div className="h-9 border-b border-line-strong bg-surface-sunken" />
      {Array.from({ length: rows }, (_, r) => (
        <div
          key={r}
          className="flex items-center gap-3 border-b border-line px-3 last:border-b-0"
          style={{ height: '44px' }}
        >
          {Array.from({ length: columns }, (_, c) => (
            <div
              key={c}
              className="h-3 flex-1 animate-pulse rounded-xs bg-surface-sunken"
              // Varied widths so it reads as content rather than as a bar
              // chart, without being random enough to shimmer differently on
              // every render.
              style={{ maxWidth: c === 0 ? '30%' : `${40 + ((c * 17) % 40)}%` }}
            />
          ))}
        </div>
      ))}
    </div>
  );
}

/**
 * Cursor pagination.
 *
 * There is no page number and no total count, because the API is cursor-based:
 * offsets skip or repeat rows when the underlying set changes, and an invoice
 * list changes constantly during trading hours. "Show more" is the honest
 * control for that, and it is what the data supports.
 */
export function LoadMore({
  hasMore,
  loading,
  onLoadMore,
  loadedCount,
}: {
  hasMore: boolean;
  loading: boolean;
  onLoadMore: () => void;
  loadedCount: number;
}) {
  const t = useT();
  return (
    <div className="flex items-center justify-between gap-4 pt-3">
      <p className="text-caption text-muted" aria-live="polite">
        {loadedCount === 1
          ? t('nx.tbl.rowsOne')
          : t('nx.tbl.rowsMany', { count: loadedCount })}
        {hasMore ? ` ${t('nx.tbl.soFar')}` : ''}
      </p>
      {hasMore && (
        <button
          type="button"
          onClick={onLoadMore}
          disabled={loading}
          className={cn(
            'h-9 rounded-sm border border-line-strong bg-surface px-3.5',
            'text-body font-medium hover:bg-surface-hover',
            'disabled:pointer-events-none disabled:opacity-55',
          )}
        >
          {loading ? t('nx.tbl.loading') : t('nx.tbl.showMore')}
        </button>
      )}
    </div>
  );
}
