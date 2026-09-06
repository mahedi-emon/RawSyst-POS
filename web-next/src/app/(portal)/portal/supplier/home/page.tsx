'use client';

// What a supplier signs in to find out — F3.
//
// Two questions, and the screen leads with the first: how many orders are
// waiting for an answer, and how much the shop owes. F3's whole argument is
// that a purchase order sitting in an inbox is a purchase order nobody has
// accepted, so "awaiting response" is the figure this page exists to make
// impossible to miss — and it is a link, because a number somebody cannot act
// on is trivia.

import { Figure, Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { useT } from '@/lib/i18n/locale';
import { usePortal, usePortalGuard } from '@/lib/portal/hooks';
import type { SupplierHome } from '@/lib/portal/types';

export default function SupplierHomePage() {
  const t = useT();
  const waiting = usePortalGuard('/portal/supplier');
  const { data, isLoading, error, refetch } = usePortal<{ home: SupplierHome }>(
    waiting ? null : '/portal/supplier/home',
  );

  if (waiting) return <Skeleton className="h-48" />;
  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;
  if (isLoading || !data) return <Skeleton className="h-48" />;

  const home = data.home;

  return (
    <div className="flex flex-col gap-6">
      <Panel title={home.supplier_name} description={home.contact_name}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Figure
            label={t('nx.sp.awaitingResponse')}
            value={home.awaiting_response}
            caption={t('nx.sp.awaitingCaption')}
            tone={home.awaiting_response > 0 ? 'critical' : undefined}
            href="/portal/supplier/orders"
          />
          <Figure
            label={t('nx.sp.openOrders')}
            value={home.open_orders}
            href="/portal/supplier/orders"
          />
          <Figure
            label={t('nx.sp.owedToYou')}
            value={home.outstanding}
            currency={home.currency}
            href="/portal/supplier/bills"
          />
          <Figure
            label={t('nx.sp.overdue')}
            value={home.overdue}
            currency={home.currency}
            tone={home.overdue !== '0.00' ? 'critical' : undefined}
            href="/portal/supplier/bills"
          />
        </div>
      </Panel>

      {home.open_rfqs > 0 ? (
        <Panel title={t('nx.sp.rfqsTitle')}>
          <p className="text-body">
            {t('nx.sp.openRfqs', { count: String(home.open_rfqs) })}{' '}
            <a className="underline" href="/portal/supplier/rfqs">
              {t('nx.sp.seeRfqs')}
            </a>
          </p>
        </Panel>
      ) : null}
    </div>
  );
}
