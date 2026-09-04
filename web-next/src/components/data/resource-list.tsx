'use client';

// The list screen, once.
//
// Products proved the shape and Customers is the second use, which is where a
// pattern becomes visible enough to lift: search that debounces and goes to the
// SERVER, cursor pagination, and four states that say four different things.
// Written on the first use it would have been an abstraction over one example.
//
// # Why "Show more" and not page numbers
//
// The API is cursor-based, and that is not an implementation detail leaking
// through. Offset pagination skips or repeats rows when the underlying set
// changes, and a catalogue or a customer list changes while somebody is reading
// it. A page number would be a promise the data cannot keep.
//
// # Not every collection paginates
//
// `/catalog/products` answers with `{data, page}`; `/customers` answers with
// `{data}` and nothing else. Verified against a live server, not assumed. So
// the `page` envelope is optional throughout and its absence means "this is all
// of it" rather than "something went wrong".

import { useEffect, useState, type ReactNode } from 'react';

import {
  DataTable,
  LoadMore,
  TableSkeleton,
  type Column,
} from '@/components/ui/table';
import { ErrorState, NoMatches } from '@/components/ui/states';
import { useApiList } from '@/lib/api/hooks';

export interface ResourceListProps<Row> {
  /** The API path. Null defers the request until the company scope resolves. */
  path: string | null;
  /** Scope and filters merged into the query alongside search and paging. */
  query?: Record<string, string | number | boolean | undefined | null>;
  /** The query parameter this endpoint searches on -- `search` or `q`. */
  searchParam?: string;
  columns: readonly Column<Row>[];
  rowKey: (row: Row) => string;
  /** Cursor value for the next page, read off the last row. */
  cursorOf?: (row: Row) => string;
  onOpenRow?: (row: Row) => void;
  /** Screen-reader description of the table. Required. */
  caption: string;
  searchPlaceholder: string;
  searchLabel: string;
  /** Shown when the unfiltered list is genuinely empty. */
  emptyState: ReactNode;
  /** Plural noun for the no-matches state, already translated. */
  noun: string;
  /** Extra controls beside the search box -- a status filter, say. */
  filters?: ReactNode;
  pageSize?: number;
}

export function ResourceList<Row>({
  path,
  query,
  searchParam = 'search',
  columns,
  rowKey,
  cursorOf,
  onOpenRow,
  caption,
  searchPlaceholder,
  searchLabel,
  emptyState,
  noun,
  filters,
  pageSize = 50,
}: ResourceListProps<Row>) {
  const [search, setSearch] = useState('');
  const [debounced, setDebounced] = useState('');
  const [cursor, setCursor] = useState<string | null>(null);
  const [accumulated, setAccumulated] = useState<Row[]>([]);

  // 250ms: long enough that typing a name is one request rather than eight,
  // short enough that it does not feel like waiting.
  useEffect(() => {
    const id = setTimeout(() => {
      setDebounced(search);
      // A new search is a new list, not more of the old one.
      setCursor(null);
      setAccumulated([]);
    }, 250);
    return () => clearTimeout(id);
  }, [search]);

  const { data, isLoading, isFetching, error, refetch } = useApiList<Row>(
    path,
    { ...query, [searchParam]: debounced, limit: pageSize, after: cursor },
  );

  useEffect(() => {
    if (!data?.data) return;
    setAccumulated((current) => {
      // Guards against React re-running this on a cache hit and duplicating a
      // page that is already in the list.
      const seen = new Set(current.map((r) => rowKey(r)));
      const fresh = data.data.filter((r) => !seen.has(rowKey(r)));
      return fresh.length === 0 ? current : [...current, ...fresh];
    });
    // `rowKey` is a fresh closure on every render; depending on it would rerun
    // this on every render and defeat the guard above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const rows =
    cursor === null && data?.data
      ? accumulated.length > 0
        ? accumulated
        : data.data
      : accumulated;

  // Absent means "this is all of it", not "unknown". `cursorOf` is what makes
  // a next page reachable at all, so an endpoint that cannot supply one has no
  // more pages by construction.
  const hasMore = Boolean(cursorOf) && (data?.page?.has_more ?? false);

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-2">
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={searchPlaceholder}
            aria-label={searchLabel}
            type="search"
            className="h-10 w-full max-w-xs rounded-sm border border-input bg-input-bg px-3 text-body placeholder:text-disabled"
          />
          {filters}
      </div>

      {error && <ErrorState error={error} onRetry={() => void refetch()} />}

      {!error && isLoading && rows.length === 0 && (
        <TableSkeleton columns={columns.length} />
      )}

      {!error && !isLoading && rows.length === 0 && debounced === '' && emptyState}

      {!error && !isLoading && rows.length === 0 && debounced !== '' && (
        <NoMatches what={noun} onClear={() => setSearch('')} />
      )}

      {rows.length > 0 && (
        <>
          <DataTable
            caption={caption}
            columns={columns}
            rows={rows}
            rowKey={(r) => rowKey(r)}
            onOpenRow={onOpenRow}
          />
          <LoadMore
            hasMore={hasMore}
            loading={isFetching}
            loadedCount={rows.length}
            onLoadMore={() => {
              const last = rows[rows.length - 1];
              if (last && cursorOf) setCursor(cursorOf(last));
            }}
          />
        </>
      )}
    </>
  );
}
