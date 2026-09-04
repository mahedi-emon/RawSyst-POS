'use client';

// Products.
//
// The first list screen, now built on the shared one. Everything that made this
// work -- server search, cursor pagination, four distinct states -- moved to
// `ResourceList` when Customers needed the same thing, which is the point at
// which a pattern is a pattern rather than one example.
//
// What stays here is what is actually about products: the columns, the words,
// and the permission that opens each action.

import { PackagePlus, Plus } from 'lucide-react';
import { useRouter } from 'next/navigation';

import { Can, RequirePermission } from '@/components/auth/guard';
import { ResourceList } from '@/components/data/resource-list';
import { Button } from '@/components/ui/button';
import { Badge, PageHeader } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import type { Column } from '@/components/ui/table';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';

interface Product {
  id: string;
  sku: string;
  name: string;
  tax_treatment: string;
  lifecycle: string;
  variant_count: number;
}

function ProductsScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const columns: Column<Product>[] = [
    {
      key: 'name',
      header: t('nx.cat.colProduct'),
      primary: true,
      cell: (p) => p.name,
    },
    {
      key: 'sku',
      header: t('nx.cat.colCode'),
      secondary: true,
      cell: (p) => <span className="num text-muted">{p.sku}</span>,
    },
    {
      key: 'variant_count',
      header: t('nx.cat.colVariants'),
      numeric: true,
      width: 'w-24',
      headerHint: t('nx.cat.variantsHint'),
      cell: (p) => p.variant_count,
    },
    {
      key: 'tax_treatment',
      header: t('nx.cat.colTax'),
      secondary: true,
      width: 'w-32',
      // Rendered as written rather than mapped to friendlier words: the
      // treatment is a legal category and its own name is the one a bookkeeper
      // and an auditor both use.
      cell: (p) => (
        <span className="text-muted">{p.tax_treatment.replace(/_/g, ' ')}</span>
      ),
    },
    {
      key: 'lifecycle',
      header: t('nx.cat.colStatus'),
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
        title={t('nx.cat.title')}
        description={t('nx.cat.subtitle')}
        actions={
          <Can permission="catalog.create">
            <Button variant="primary">
              <Plus aria-hidden="true" />
              {t('nx.cat.newProduct')}
            </Button>
          </Can>
        }
      />

      <ResourceList<Product>
        path={scope ? '/catalog/products' : null}
        query={scope ?? undefined}
        columns={columns}
        rowKey={(p) => p.id}
        // The catalogue pages on the row id, which is what the API's `after`
        // cursor takes.
        cursorOf={(p) => p.id}
        onOpenRow={(p) => router.push(`/products/${p.id}`)}
        caption={t('nx.cat.caption')}
        searchPlaceholder={t('nx.cat.searchPlaceholder')}
        searchLabel={t('nx.cat.searchLabel')}
        noun={t('nx.cat.products')}
        emptyState={
          <EmptyState
            icon={PackagePlus}
            title={t('nx.cat.emptyTitle')}
            description={t('nx.cat.emptyDesc')}
            action={
              <div className="flex flex-wrap items-center justify-center gap-2">
                <Can permission="catalog.create">
                  <Button variant="primary">{t('nx.cat.addProduct')}</Button>
                </Can>
                <Can permission="data.import">
                  <Button asChild variant="secondary">
                    <a href="/settings/imports">{t('nx.cat.importFromFile')}</a>
                  </Button>
                </Can>
              </div>
            }
          />
        }
      />
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
