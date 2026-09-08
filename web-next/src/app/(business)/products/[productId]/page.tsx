'use client';

// One product, and the variants that are actually sold.
//
// # A product is not sold; a variant is
//
// The barcode, the price and the stock figure all live on the variant, which is
// why a product with no variants cannot be sold at all and why the empty state
// here says so rather than offering a cheerful "add one". The matrix is the
// screen: everything else about a product is a name and a tax category.
//
// # Stock is on this screen because it answers the next question
//
// Somebody opening a product is usually about to ask "have we got any". The
// matrix carries `on_hand` and `reorder_level`, so the answer is here rather
// than one navigation away, and a line at or below its reorder level is marked.

import { ArrowLeft, Boxes } from 'lucide-react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useState } from 'react';

import { Can, RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

import { PricingPanel } from './pricing';

interface Variant {
  id: string;
  sku: string;
  attributes: Record<string, string>;
  /** Retail. The other three tiers are beside it. */
  price: string;
  // Absent means "not sold at this tier", which is not the same as zero: a
  // variant with no wholesale price is not sold to the trade at all, and one
  // priced at zero is given away.
  price_wholesale?: string;
  price_dealer?: string;
  price_floor: string;
  is_active: boolean;
  on_hand?: string;
  reorder_level?: string;
  last_sold_at?: string;
}

/** Compares two decimal strings without widening either through a float. */
function atOrBelow(value: string | undefined, threshold: string | undefined): boolean {
  if (!value || !threshold) return false;
  const norm = (s: string) => {
    const [w = '0', f = ''] = s.split('.');
    return [w.padStart(20, '0'), f.padEnd(6, '0')].join('');
  };
  return norm(value) <= norm(threshold);
}

function ProductScreen() {
  const t = useT();
  const params = useParams<{ productId: string }>();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const { data, isLoading, error, refetch } = useApiList<Variant>(
    scope && params.productId
      ? `/catalog/products/${params.productId}/matrix`
      : null,
    scope ?? undefined,
  );

  const variants = data?.data ?? [];

  // Which variant is being priced, and any refusal from retiring one. Both are
  // local: this screen is a grid with one editor open at a time, and a second
  // open editor would let somebody save two prices from one reading of the
  // page.
  const [pricing, setPricing] = useState<Variant | null>(null);
  const [retiring, setRetiring] = useState<Variant | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  async function retire() {
    if (!scope || !retiring) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.delete(
        `/catalog/variants/${retiring.id}?company_id=${scope.company_id}`,
      );
      setRetiring(null);
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const describe = (v: Variant) => {
    const detail = Object.values(v.attributes ?? {}).filter(Boolean).join(' · ');
    return detail || v.sku;
  };

  const columns: Column<Variant>[] = [
    {
      key: 'variant',
      header: t('nx.prod.colVariant'),
      primary: true,
      cell: (v) => (
        <span className="flex items-center gap-2">
          {describe(v)}
          {!v.is_active && <Badge>{t('nx.prod.inactive')}</Badge>}
        </span>
      ),
    },
    {
      key: 'sku',
      header: t('nx.prod.colSku'),
      secondary: true,
      cell: (v) => <span className="num text-muted">{v.sku}</span>,
    },
    {
      key: 'price',
      header: t('nx.prod.colPrice'),
      numeric: true,
      width: 'w-28',
      cell: (v) => formatMoney(v.price, { currency, market, bare: true }),
    },
    {
      key: 'wholesale',
      header: t('nx.prod.colWholesale'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      // An em dash would read as "zero"; the trade price being absent is a
      // decision, so it says so.
      cell: (v) =>
        v.price_wholesale ? (
          formatMoney(v.price_wholesale, { currency, market, bare: true })
        ) : (
          <span className="text-subtle">{t('nx.prod.notSoldToTrade')}</span>
        ),
    },
    {
      key: 'dealer',
      header: t('nx.prod.colDealer'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (v) =>
        v.price_dealer
          ? formatMoney(v.price_dealer, { currency, market, bare: true })
          : '—',
    },
    {
      key: 'on_hand',
      header: t('nx.prod.colOnHand'),
      numeric: true,
      width: 'w-32',
      cell: (v) => {
        if (v.on_hand === undefined) return '—';
        const empty = formatQuantity(v.on_hand, market) === '0';
        const low = !empty && atOrBelow(v.on_hand, v.reorder_level);
        return (
          <span className="inline-flex items-center justify-end gap-2">
            {formatQuantity(v.on_hand, market)}
            {empty && <Badge tone="critical">{t('nx.stock.out')}</Badge>}
            {low && <Badge tone="caution">{t('nx.stock.low')}</Badge>}
          </span>
        );
      },
    },
    {
      key: 'reorder',
      header: t('nx.prod.colReorder'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (v) =>
        v.reorder_level ? formatQuantity(v.reorder_level, market) : '—',
    },
    {
      key: 'actions',
      header: '',
      width: 'w-40',
      cell: (v) => (
        <span className="flex justify-end gap-1">
          <Can permission="catalog.edit">
            <Button size="sm" variant="ghost" onClick={() => setPricing(v)}>
              {t('nx.prod.editPrices')}
            </Button>
          </Can>
          {/* Retiring is `catalog.delete` rather than `catalog.edit`: taking a
              line out of the catalogue is not the same decision as changing
              its price, and the server gates them apart. */}
          {v.is_active && (
            <Can permission="catalog.delete">
              <Button size="sm" variant="ghost" onClick={() => setRetiring(v)}>
                {t('nx.prod.retire')}
              </Button>
            </Can>
          )}
        </span>
      ),
    },
    {
      key: 'last_sold',
      header: t('nx.prod.colLastSold'),
      secondary: true,
      width: 'w-32',
      cell: (v) =>
        v.last_sold_at ? (
          <time dateTime={v.last_sold_at}>{v.last_sold_at}</time>
        ) : (
          <span className="text-subtle">{t('nx.prod.neverSold')}</span>
        ),
    },
  ];

  return (
    <>
      <PageHeader
        breadcrumb={
          <Link
            href="/products"
            className="mb-1 inline-flex items-center gap-1 text-label text-muted hover:text-fg"
          >
            <ArrowLeft className="size-3.5 rtl:rotate-180" aria-hidden="true" />
            {t('nx.prod.backToList')}
          </Link>
        }
        title={t('nx.prod.variantsTitle')}
        description={t('nx.prod.variantsDesc')}
      />

      {error && <ErrorState error={error} onRetry={() => void refetch()} />}

      {pricing && scope && (
        <PricingPanel
          variant={pricing}
          companyId={scope.company_id}
          onSaved={() => {
            setPricing(null);
            void refetch();
          }}
          onCancel={() => setPricing(null)}
        />
      )}

      {retiring && (
        <Panel
          title={t('nx.prod.retireTitle', { sku: retiring.sku })}
          className="mb-4"
        >
          <p className="text-body text-muted">{t('nx.prod.retireBody')}</p>
          {actionError && <FormError message={actionError} />}
          <div className="mt-4 flex flex-wrap gap-2">
            <Button
              variant="destructive"
              onClick={() => void retire()}
              disabled={busy}
            >
              {t('nx.prod.retireConfirm')}
            </Button>
            <Button
              variant="ghost"
              onClick={() => setRetiring(null)}
              disabled={busy}
            >
              {t('nx.prod.cancel')}
            </Button>
          </div>
        </Panel>
      )}

      <Panel
        flush
        footer={
          variants.length > 0
            ? variants.length === 1
              ? t('nx.prod.variantCountOne')
              : t('nx.prod.variantCount', { count: variants.length })
            : undefined
        }
      >
        {isLoading && <TableSkeleton columns={8} rows={4} />}

        {!isLoading && !error && variants.length === 0 && (
          <div className="p-4">
            <EmptyState
              icon={Boxes}
              title={t('nx.prod.noVariantsTitle')}
              description={t('nx.prod.noVariantsDesc')}
            />
          </div>
        )}

        {variants.length > 0 && (
          <DataTable
            caption={t('nx.prod.matrixCaption')}
            columns={columns}
            rows={variants}
            rowKey={(v) => v.id}
            className="rounded-none border-0"
          />
        )}
      </Panel>
    </>
  );
}

export default function ProductDetailPage() {
  return (
    <RequirePermission anyOf={['catalog.view']}>
      <ProductScreen />
    </RequirePermission>
  );
}
