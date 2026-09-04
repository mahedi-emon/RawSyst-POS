'use client';

// Products.
//
// # The pattern every list screen in this product follows
//
// Search that debounces and goes to the SERVER, cursor pagination with a "Show
// more" rather than page numbers, and four distinct states -- loading, empty,
// no-matches, failed -- that say different things because they are different
// situations. Nothing loads the whole catalogue into the browser to filter it
// there; a shop with forty thousand lines would never finish.
//
// # Why "Show more" and not pages
//
// The API is cursor-based, and that is not an implementation detail leaking
// through. Offset pagination skips or repeats rows when the underlying set
// changes, and a catalogue changes while somebody is reading it. A page number
// would be a promise the data cannot keep.

import { PackagePlus, Plus } from 'lucide-react';
import { useEffect, useState } from 'react';

import { RequirePermission, Can } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader } from '@/components/ui/panel';
import { DataTable, LoadMore, TableSkeleton, type Column } from '@/components/ui/table';
import { EmptyState, ErrorState, NoMatches } from '@/components/ui/states';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';

interface Product {
  id: string;
  sku: string;
  name: string;
  tax_treatment: string;
  lifecycle: string;
  variant_count: number;
}

const PAGE_SIZE = 50;

function ProductsScreen() {
  const scope = useCompanyScope();
  const [search, setSearch] = useState('');
  const [debounced, setDebounced] = useState('');
  const [cursor, setCursor] = useState<string | null>(null);
  const [accumulated, setAccumulated] = useState<Product[]>([]);

  useEffect(() => {
    const id = setTimeout(() => {
      setDebounced(search);
      // A new search is a new list, not more of the old one.
      setCursor(null);
      setAccumulated([]);
    }, 250);
    return () => clearTimeout(id);
  }, [search]);

  const { data, isLoading, isFetching, error, refetch } = useApiList<Product>(
    scope ? '/catalog/products' : null,
    { ...scope, search: debounced, limit: PAGE_SIZE, after: cursor },
  );

  useEffect(() => {
    if (!data?.data) return;
    setAccumulated((current) => {
      // Guards against React re-running this on a cache hit and duplicating a
      // page that is already in the list.
      const seen = new Set(current.map((p) => p.id));
      const fresh = data.data.filter((p) => !seen.has(p.id));
      return fresh.length === 0 ? current : [...current, ...fresh];
    });
  }, [data]);

  const rows = cursor === null && data?.data ? (accumulated.length ? accumulated : data.data) : accumulated;
  const hasMore = data?.page?.has_more ?? false;

  const columns: Column<Product>[] = [
    {
      key: 'name',
      header: 'Product',
      primary: true,
      cell: (p) => p.name,
    },
    {
      key: 'sku',
      header: 'Code',
      secondary: true,
      cell: (p) => <span className="num text-muted">{p.sku}</span>,
    },
    {
      key: 'variant_count',
      header: 'Variants',
      numeric: true,
      width: 'w-24',
      headerHint: 'Sizes, colours and other options this product is sold in',
      cell: (p) => p.variant_count,
    },
    {
      key: 'tax_treatment',
      header: 'Tax',
      secondary: true,
      width: 'w-32',
      cell: (p) => (
        // Rendered as written rather than mapped to friendlier words: the
        // treatment is a legal category and its own name is the one a
        // bookkeeper and an auditor both use.
        <span className="text-muted">{p.tax_treatment.replace(/_/g, ' ')}</span>
      ),
    },
    {
      key: 'lifecycle',
      header: 'Status',
      width: 'w-28',
      cell: (p) => (
        <Badge tone={p.lifecycle === 'active' ? 'positive' : 'neutral'}>
          {p.lifecycle}
        </Badge>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title="Products"
        description="What the shop sells, and what it charges. A product holds its variants — the sizes and colours that are actually scanned."
        actions={
          <Can permission="catalog.create">
            <Button variant="primary">
              <Plus aria-hidden="true" />
              New product
            </Button>
          </Can>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by name or code"
          aria-label="Search products"
          type="search"
          className="h-10 w-full max-w-xs rounded-sm border border-input bg-input-bg px-3 text-body placeholder:text-disabled"
        />
      </div>

      {error && <ErrorState error={error} onRetry={() => void refetch()} />}

      {!error && isLoading && rows.length === 0 && (
        <TableSkeleton columns={columns.length} />
      )}

      {!error && !isLoading && rows.length === 0 && debounced === '' && (
        <EmptyState
          icon={PackagePlus}
          title="No products yet"
          description="A product is anything the shop sells. Add the first one, or bring a whole catalogue in from a spreadsheet."
          action={
            <div className="flex flex-wrap items-center justify-center gap-2">
              <Can permission="catalog.create">
                <Button variant="primary">Add a product</Button>
              </Can>
              <Can permission="data.import">
                <Button asChild variant="secondary">
                  <a href="/settings/imports">Import from a file</a>
                </Button>
              </Can>
            </div>
          }
        />
      )}

      {!error && !isLoading && rows.length === 0 && debounced !== '' && (
        <NoMatches what="products" onClear={() => setSearch('')} />
      )}

      {rows.length > 0 && (
        <>
          <DataTable
            caption="Products, with their variant count, tax treatment and status"
            columns={columns}
            rows={rows}
            rowKey={(p) => p.id}
          />
          <LoadMore
            hasMore={hasMore}
            loading={isFetching}
            loadedCount={rows.length}
            onLoadMore={() => {
              const last = rows[rows.length - 1];
              if (last) setCursor(last.id);
            }}
          />
        </>
      )}
    </>
  );
}

export default function ProductsPage() {
  return (
    <RequirePermission anyOf={['catalog.view']}>
      <ProductsScreen />
    </RequirePermission>
  );
}
