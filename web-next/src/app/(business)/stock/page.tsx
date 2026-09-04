'use client';

// Stock on hand.
//
// The dashboard has been linking here since it was built -- its out-of-stock
// and running-low rows both point at this route -- so until now the product
// offered a link to nowhere. That is the reason this screen came before the
// several modules with more surface area: a dead link in the one place the
// product tells somebody what needs attention is worse than a module that has
// not started.
//
// # `low` is the server's filter; `out` is not
//
// The API filters on `low=true`, which means at or below the reorder level and
// therefore includes everything that has run out. There is no separate
// out-of-stock filter, so the screen does not pretend to have one: it marks the
// empty lines within the low set rather than inventing a query parameter. The
// dashboard's `?filter=out` link lands on the low view with those lines
// obvious, which is the honest reading of what it asked for.

import { Boxes, PackageSearch } from 'lucide-react';
import { useSearchParams } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/field';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

interface StockLine {
  variant_id: string;
  sku: string;
  product: string;
  barcode: string;
  location: string;
  on_hand: string;
  reorder_level: string;
}

interface LocationsResponse {
  data: { id: string; code: string; name: string; is_active: boolean }[];
}

/** Compares two decimal strings without widening either through a float. */
function atOrBelow(value: string, threshold: string): boolean {
  const norm = (s: string) => {
    const [w = '0', f = ''] = s.replace('-', '').split('.');
    return [w.padStart(20, '0'), f.padEnd(6, '0')].join('');
  };
  return norm(value) <= norm(threshold);
}

function StockScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { market } = useCompany();
  const params = useSearchParams();

  // The dashboard links here with `filter=low` or `filter=out`. Both start on
  // the low view, because that is the one query the server has and it contains
  // both answers.
  const linked = params.get('filter');
  const [onlyLow, setOnlyLow] = useState(linked === 'low' || linked === 'out');
  const [locationId, setLocationId] = useState('');

  const locations = useApi<LocationsResponse>(
    scope ? '/stock/locations' : null,
    scope ?? undefined,
    { staleTime: 10 * 60 * 1000 },
  );

  const columns: Column<StockLine>[] = [
    {
      key: 'product',
      header: t('nx.stock.colProduct'),
      primary: true,
      cell: (l) => l.product,
    },
    {
      key: 'sku',
      header: t('nx.stock.colSku'),
      secondary: true,
      cell: (l) => <span className="num text-muted">{l.sku}</span>,
    },
    {
      key: 'barcode',
      header: t('nx.stock.colBarcode'),
      secondary: true,
      cell: (l) => <span className="num text-muted">{l.barcode || '—'}</span>,
    },
    {
      key: 'location',
      header: t('nx.stock.colLocation'),
      secondary: true,
      cell: (l) => <span className="text-muted">{l.location}</span>,
    },
    {
      key: 'reorder',
      header: t('nx.stock.colReorder'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (l) => formatQuantity(l.reorder_level, market),
    },
    {
      key: 'on_hand',
      header: t('nx.stock.colOnHand'),
      numeric: true,
      width: 'w-40',
      cell: (l) => {
        const empty = isZero(l.on_hand);
        const low = !empty && atOrBelow(l.on_hand, l.reorder_level);
        return (
          <span className="inline-flex items-center justify-end gap-2">
            <span className={empty ? 'text-critical-fg' : undefined}>
              {formatQuantity(l.on_hand, market)}
            </span>
            {empty && <Badge tone="critical">{t('nx.stock.out')}</Badge>}
            {low && <Badge tone="caution">{t('nx.stock.low')}</Badge>}
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.stock.title')}
        description={t('nx.stock.subtitle')}
      />

      <ResourceList<StockLine>
        path={scope ? '/stock/on-hand' : null}
        // This endpoint searches on `q`, not `search`.
        searchParam="q"
        query={{
          ...scope,
          ...(onlyLow ? { low: 'true' } : {}),
          ...(locationId ? { location_id: locationId } : {}),
        }}
        columns={columns}
        // A variant can sit in more than one location, so the variant id alone
        // is not unique down the list.
        rowKey={(l) => `${l.variant_id}-${l.location}`}
        caption={t('nx.stock.caption')}
        searchPlaceholder={t('nx.stock.searchPlaceholder')}
        searchLabel={t('nx.stock.searchLabel')}
        noun={t('nx.stock.lines')}
        filters={
          <>
            {(locations.data?.data.length ?? 0) > 1 && (
              <select
                value={locationId}
                onChange={(e) => setLocationId(e.target.value)}
                aria-label={t('nx.stock.locationLabel')}
                className="h-10 rounded-sm border border-input bg-input-bg px-2 text-body"
              >
                <option value="">{t('nx.stock.allLocations')}</option>
                {locations.data?.data
                  .filter((l) => l.is_active)
                  .map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.name}
                    </option>
                  ))}
              </select>
            )}
            <Checkbox
              checked={onlyLow}
              onChange={(e) => setOnlyLow(e.target.checked)}
              label={t('nx.stock.onlyLow')}
              className="ms-1"
            />
          </>
        }
        emptyState={
          <EmptyState
            icon={PackageSearch}
            title={t('nx.stock.emptyTitle')}
            description={t('nx.stock.emptyDesc')}
            action={
              <Button asChild variant="secondary">
                <a href="/stock/counts">{t('nx.stock.goToCounts')}</a>
              </Button>
            }
          />
        }
      />
    </>
  );
}

export default function StockPage() {
  return (
    <RequirePermission anyOf={['inventory.view']}>
      {/* `useSearchParams` needs a Suspense boundary to prerender. */}
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <StockScreen />
      </Suspense>
    </RequirePermission>
  );
}
