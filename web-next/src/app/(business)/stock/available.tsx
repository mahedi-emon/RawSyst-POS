'use client';

// On hand, reserved, and what is actually free to sell.
//
// # The difference this exists to show
//
// `GET /stock/availability` answers three figures, and only the third is the
// one anybody can act on: stock held against an order somebody has already
// placed is not stock a second channel may sell. B13 reserves it for exactly
// that reason, and the route was live and reachable from nothing — so the
// on-hand column was the only figure the product showed, and it is the one
// that over-promises.
//
// # It needs a warehouse, so it is offered when one is chosen
//
// The route takes a variant AND a warehouse, because a reservation is held in
// a place. The stock list shows a location NAME per row and the filter above it
// holds the id, so this is offered while a location is selected and says so
// while one is not. Guessing a warehouse would be answering a different
// question from the one asked.

import { Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { useApi } from '@/lib/api/hooks';
import { formatQuantity, isZero, type MarketCode } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

interface Availability {
  variant_id: string;
  on_hand: string;
  reserved: string;
  available_to_sell: string;
}

export function AvailabilityPanel({
  companyId,
  variantId,
  warehouseId,
  label,
  market,
  onClose,
}: {
  companyId: string;
  variantId: string;
  warehouseId: string;
  label: string;
  market: MarketCode;
  onClose: () => void;
}) {
  const t = useT();
  const { data, isLoading, error, refetch } = useApi<Availability>(
    '/stock/availability',
    { company_id: companyId, variant_id: variantId, warehouse_id: warehouseId },
  );

  return (
    <Panel
      className="mb-5"
      title={t('nx.stock.availTitle', { product: label })}
      description={t('nx.stock.availHint')}
      actions={
        <button
          type="button"
          onClick={onClose}
          className="text-label text-muted underline underline-offset-4 hover:text-fg"
        >
          {t('nx.stock.availClose')}
        </button>
      }
    >
      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <Skeleton className="h-20" /> : null}
      {data ? (
        <dl className="grid gap-4 sm:grid-cols-3">
          <div>
            <dt className="text-label text-muted">{t('nx.stock.availOnHand')}</dt>
            <dd className="num mt-0.5 text-section font-semibold">
              {formatQuantity(data.on_hand, market)}
            </dd>
          </div>
          <div>
            <dt className="text-label text-muted">{t('nx.stock.availReserved')}</dt>
            <dd className="num mt-0.5 text-section font-semibold">
              {formatQuantity(data.reserved, market)}
            </dd>
          </div>
          <div>
            <dt className="text-label text-muted">{t('nx.stock.availFree')}</dt>
            {/* The figure that matters. Nothing free to sell with stock on the
                shelf is the state the on-hand column alone cannot show. */}
            <dd
              className={`num mt-0.5 text-section font-semibold ${
                isZero(data.available_to_sell) ? 'text-critical-fg' : ''
              }`}
            >
              {formatQuantity(data.available_to_sell, market)}
            </dd>
          </div>
        </dl>
      ) : null}
    </Panel>
  );
}
