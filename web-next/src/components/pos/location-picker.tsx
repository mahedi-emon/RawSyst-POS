'use client';

// Which stock location is this counter selling from?
//
// Asked ONLY when a branch has more than one, and never otherwise. A shop with
// a single stockroom must not be made to answer a question that has one answer:
// the server resolves it, and adding a screen in front of the till for it would
// be adding a step to every shift for nothing.
//
// A branch with two — a shop floor and a back room — has to answer, because
// `POST /pos/sales` refuses without it: "This branch has more than one stock
// location, so the sale must say which one it is selling from." The till was
// sending nothing, so in such a shop nothing could be sold at all.
//
// # Why here and not from the terminal's own setting
//
// I5 puts a default warehouse on the terminal and migration 0009 built the
// column, but `GET /devices/{id}/settings` is `devices.view` — a manager's
// permission. A cashier cannot read their own till's configuration, so the
// answer is given at the counter by the person standing at it.
//
// # Nothing is pre-selected
//
// The two locations in a real shop are a shop floor and a back room, and
// choosing wrongly takes the stock off the wrong shelf. A pre-selected default
// is a choice somebody did not make, and this is the one place in the shift
// where making it deliberately costs a single press.

import { Warehouse } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { useCounter } from '@/lib/pos/counter';
import { cn } from '@/lib/utils';

export interface StockLocation {
  id: string;
  code: string;
  name: string;
  kind: string;
  /** The branch's NAME, which is what the route sends. */
  store: string;
  is_active: boolean;
  holds_stock: boolean;
}

/**
 * The locations this counter could be selling from.
 *
 * Matched on the branch NAME, because that is what `GET /stock/locations`
 * carries — there is no `store_id` on a row. A location belonging to no branch
 * at all is included, because the server accepts one: its rule is
 * `store_id = $1 OR store_id IS NULL`.
 */
export function locationsForCounter(
  locations: readonly StockLocation[],
  store: string,
): StockLocation[] {
  return locations.filter(
    (l) => l.is_active && (l.store === store || !l.store),
  );
}

/**
 * Whether this counter still has to be told where it is selling from.
 *
 * `settled` covers the ordinary shop: one location, nothing to ask, and the
 * server resolves it — so `warehouseId` stays null and no request carries one.
 * `choose` is the two-location branch, and until it is answered every sale
 * would come back 400.
 *
 * A failed lookup settles rather than blocking. The list is `inventory.view`,
 * which every seeded cashier holds, but a role built by hand might not — and a
 * till that refused to open because it could not read a list is worse than one
 * that tries the sale and shows the server's own refusal.
 */
export function useSellFrom(): {
  status: 'resolving' | 'settled' | 'choose';
  warehouseId: string | null;
} {
  const scope = useCompanyScope();
  const { state } = useCounter();
  const open = state.kind === 'open';

  const { data, isLoading, error } = useApiList<StockLocation>(
    scope && open ? '/stock/locations' : null,
    scope ?? undefined,
  );

  if (!open) return { status: 'resolving', warehouseId: null };
  if (state.warehouseId) {
    return { status: 'settled', warehouseId: state.warehouseId };
  }
  if (error) return { status: 'settled', warehouseId: null };
  if (isLoading || !data) return { status: 'resolving', warehouseId: null };

  const here = locationsForCounter(data.data ?? [], state.counter.store);
  return here.length > 1
    ? { status: 'choose', warehouseId: null }
    : { status: 'settled', warehouseId: null };
}

export function LocationPicker() {
  const t = useT();
  const scope = useCompanyScope();
  const { state, sellFrom, leave } = useCounter();

  const { data, isLoading, error, refetch } = useApiList<StockLocation>(
    scope ? '/stock/locations' : null,
    scope ?? undefined,
  );

  if (state.kind !== 'open') return null;
  const here = locationsForCounter(data?.data ?? [], state.counter.store);

  return (
    <div className="mx-auto flex min-h-dvh max-w-2xl flex-col justify-center gap-6 px-4 py-10">
      <div>
        <h1 className="text-page font-semibold text-fg">{t('nx.pos.whereFrom')}</h1>
        <p className="mt-1 text-body text-muted">{t('nx.pos.whereFromBody')}</p>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      {isLoading ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : null}

      {!isLoading && !error && here.length === 0 ? (
        <EmptyState
          icon={Warehouse}
          title={t('nx.pos.noLocationTitle')}
          description={t('nx.pos.noLocationDesc')}
        />
      ) : null}

      <ul className="flex flex-col gap-2">
        {here.map((location) => (
          <li key={location.id}>
            <button
              type="button"
              onClick={() => sellFrom(location.id)}
              className={cn(
                'flex min-h-16 w-full items-center gap-3 rounded-md border px-4',
                'border-line-strong bg-surface text-start',
                'hover:border-primary hover:bg-surface-selected',
              )}
            >
              <Warehouse className="size-5 shrink-0 text-muted" aria-hidden="true" />
              <span className="min-w-0 flex-1">
                <span className="block text-lede font-semibold text-fg">
                  {location.name}
                </span>
                <span className="num block text-label text-muted">
                  {location.code}
                  {/* Whether stock is actually tracked there. A location that
                      holds none can still be sold from -- the server allows
                      it -- and somebody choosing it should know. */}
                  {!location.holds_stock ? ` · ${t('nx.pos.holdsNoStock')}` : ''}
                </span>
              </span>
            </button>
          </li>
        ))}
      </ul>

      <Button variant="ghost" onClick={leave} className="self-center">
        {t('nx.pos.chooseAnotherCounter')}
      </Button>
    </div>
  );
}
