'use client';

// D7's global search: one box over everything the caller may already reach.
//
// # It widens nothing
//
// The route's own comment: "every branch is filtered by the permission guarding
// the thing it finds, so this widens nothing." A cashier searching a supplier
// name gets no supplier rows, because `purchasing.view` is what returns them —
// not because this screen hid them. So the empty state does not say "no
// results"; it says nothing matched *that you can see*, which is the true
// sentence and the one that stops somebody concluding a record was deleted.
//
// # Grouped by kind, because the kinds are not interchangeable
//
// A product, a customer and an invoice that all match "Noor" are three
// different answers to three different questions. A single ranked list makes
// somebody read past two of them; grouping lets them go straight to the one
// they meant.
//
// # The term lives in the URL
//
// So a search is a link somebody can send, and the back button returns to the
// results rather than to an empty box.

import { Search } from 'lucide-react';
import Link from 'next/link';
import { Suspense, useEffect, useState } from 'react';

import { Field, Input } from '@/components/ui/field';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

interface Hit {
  kind: string;
  id: string;
  label: string;
  detail?: string;
  amount?: string;
  currency?: string;
}

/** The seven kinds the route can return, in the order a person scans them. */
const KINDS = [
  'product',
  'customer',
  'supplier',
  'invoice',
  'order',
  'employee',
  'serial',
] as const;

/** Where a hit opens. Absent for the kinds that have no detail screen. */
function hrefFor(hit: Hit): string | null {
  switch (hit.kind) {
    case 'product':
      return `/products/${hit.id}`;
    case 'customer':
      return `/customers/${hit.id}`;
    case 'order':
      return `/orders/${hit.id}`;
    default:
      // A supplier, an invoice, an employee and a serial have no detail route
      // that takes an id from here. Rendering a dead link would be worse than
      // rendering a row that does not open.
      return null;
  }
}

function SearchScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const [q, setQ] = useUrlState('q');
  const [typed, setTyped] = useState(q);

  // 250ms, matching the resource lists: long enough that typing a name is one
  // request rather than eight.
  useEffect(() => {
    if (typed === q) return;
    const id = setTimeout(() => setQ(typed), 250);
    return () => clearTimeout(id);
  }, [typed, q, setQ]);

  const term = q.trim();
  const { data, isLoading, error, refetch } = useApiList<Hit>(
    scope && term !== '' ? '/search' : null,
    { company_id: scope?.company_id, q: term },
  );

  const hits = data?.data ?? [];
  const grouped = KINDS.map((kind) => ({
    kind,
    rows: hits.filter((h) => h.kind === kind),
  })).filter((g) => g.rows.length > 0);

  return (
    <>
      <PageHeader title={t('nx.gs.title')} description={t('nx.gs.subtitle')} />

      <div className="mb-4 max-w-xl">
        <Field name="q" label={t('nx.gs.label')}>
          <Input
            type="search"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={t('nx.gs.placeholder')}
            autoFocus
          />
        </Field>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      {term === '' ? (
        <EmptyState
          icon={Search}
          title={t('nx.gs.startTitle')}
          description={t('nx.gs.startDesc')}
        />
      ) : isLoading ? (
        <div className="h-32" aria-busy="true" />
      ) : hits.length === 0 ? (
        // Not "no results". The route filters by permission, so a record the
        // caller may not read is indistinguishable from one that is not there.
        <EmptyState
          icon={Search}
          title={t('nx.gs.noneTitle', { term })}
          description={t('nx.gs.noneDesc')}
        />
      ) : (
        <div className="flex flex-col gap-4">
          {grouped.map((group) => (
            <Panel
              key={group.kind}
              title={t(`nx.gs.kind.${group.kind}` as 'nx.gs.kind.product')}
              flush
            >
              <ul className="divide-y divide-line">
                {group.rows.map((hit) => {
                  const href = hrefFor(hit);
                  const inner = (
                    <span className="flex items-center justify-between gap-3 px-4 py-3">
                      <span className="flex min-w-0 flex-col">
                        <span className="truncate text-body text-fg">{hit.label}</span>
                        {hit.detail ? (
                          <span className="truncate text-caption text-muted">
                            {hit.detail}
                          </span>
                        ) : null}
                      </span>
                      {hit.amount ? (
                        <span className="num shrink-0 text-body text-fg">
                          {formatMoney(hit.amount, {
                            currency: hit.currency || currency,
                            market,
                          })}
                        </span>
                      ) : null}
                    </span>
                  );
                  return (
                    <li key={`${hit.kind}-${hit.id}`}>
                      {href ? (
                        <Link href={href} className="block hover:bg-surface-hover">
                          {inner}
                        </Link>
                      ) : (
                        inner
                      )}
                    </li>
                  );
                })}
              </ul>
            </Panel>
          ))}
        </div>
      )}

      {hits.length > 0 ? (
        <p className="mt-4 text-caption text-muted">
          <Badge tone="neutral">{t('nx.gs.scoped')}</Badge>
        </p>
      ) : null}
    </>
  );
}

export default function SearchPage() {
  return (
    <Suspense fallback={<div className="h-64" aria-busy="true" />}>
      <SearchScreen />
    </Suspense>
  );
}
