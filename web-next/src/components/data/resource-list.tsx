'use client';

// The list screen, once.
//
// Products proved the shape and Customers is the second use, which is where a
// pattern becomes visible enough to lift: search that debounces and goes to the
// SERVER, cursor pagination, and four states that say four different things.
// Written on the first use it would have been an abstraction over one example.
//
// # The search term lives in the URL
//
// So a filtered list can be sent to somebody, the back button undoes it, and a
// refresh does not throw it away. The text field is driven by local state so
// each keystroke paints immediately; the URL catches up on a debounce, with
// `replace` rather than `push` so five characters do not become five history
// entries. See `lib/url-state.ts`.
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
import { useUrlState } from '@/lib/url-state';

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
  const [urlSearch, setUrlSearch] = useUrlState('q');
  // Seeded from the URL, so a shared link arrives with the box already filled.
  const [typed, setTyped] = useState(urlSearch);
  const [cursor, setCursor] = useState<string | null>(null);
  const [accumulated, setAccumulated] = useState<Row[]>([]);

  // 250ms: long enough that typing a name is one request rather than eight,
  // short enough that it does not feel like waiting.
  useEffect(() => {
    if (typed === urlSearch) return;
    const id = setTimeout(() => setUrlSearch(typed), 250);
    return () => clearTimeout(id);
  }, [typed, urlSearch, setUrlSearch]);

  // A new search or a changed filter is a new list, not more of the old one.
  // Keyed on the serialised query rather than on the object, which is a fresh
  // reference every render.
  const filterKey = JSON.stringify(query ?? {});
  useEffect(() => {
    setCursor(null);
    setAccumulated([]);
  }, [urlSearch, path, filterKey]);

  const { data, isLoading, isFetching, error, refetch } = useApiList<Row>(path, {
    ...query,
    [searchParam]: urlSearch,
    limit: pageSize,
    after: cursor,
  });

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

  function clear() {
    setTyped('');
    setUrlSearch('');
  }

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <input
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          placeholder={searchPlaceholder}
          aria-label={searchLabel}
          type="search"
          // A product code or a customer reference is not a word.
          autoComplete="off"
          spellCheck={false}
          className="h-10 w-full max-w-xs rounded-sm border border-input bg-input-bg px-3 text-body placeholder:text-disabled"
        />
        {filters}
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      {!error && isLoading && rows.length === 0 ? (
        <TableSkeleton columns={columns.length} />
      ) : null}

      {!error && !isLoading && rows.length === 0 && urlSearch === ''
        ? emptyState
        : null}

      {!error && !isLoading && rows.length === 0 && urlSearch !== '' ? (
        <NoMatches what={noun} onClear={clear} />
      ) : null}

      {rows.length > 0 ? (
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
      ) : null}
    </>
  );
}
