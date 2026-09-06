'use client';

// What the shop holds for this customer — F2's home.
//
// Four figures and a loyalty line, and the reason they are on one screen is
// that somebody with store credit and an unpaid invoice wants to know they can
// settle one with the other. Splitting them across tabs would make that the
// customer's arithmetic.
//
// # Absent is not zero
//
// `loyalty_enrolled` is false for a shop that runs no scheme, which is a
// different thing from a member with no points. The first has nothing to say;
// the second says nought. A screen that rendered "0 points" for a shop with no
// loyalty programme would be telling the customer they had lost something.

import { Wallet } from 'lucide-react';

import { Figure, Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { useT } from '@/lib/i18n/locale';
import { usePortal, usePortalGuard } from '@/lib/portal/hooks';
import type { Me } from '@/lib/portal/types';

export default function PortalAccountPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal');
  const { data, isLoading, error, refetch } = usePortal<{ me: Me }>(
    waiting ? null : '/portal/me',
  );

  if (waiting) return <Skeleton className="h-48" />;
  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;
  if (isLoading || !data) return <Skeleton className="h-48" />;

  const me = data.me;

  return (
    <div className="flex flex-col gap-6">
      <Panel title={t('nx.sp.accountTitle')}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Figure
            label={t('nx.sp.outstanding')}
            value={me.outstanding}
            currency={me.currency}
          />
          <Figure
            label={t('nx.sp.storeCredit')}
            value={me.store_credit}
            currency={me.currency}
          />
          <Figure
            label={t('nx.sp.giftCards')}
            value={me.gift_card_balance}
            currency={me.currency}
          />
        </div>

        <dl className="mt-6 grid gap-3 border-t border-line pt-4 text-body sm:grid-cols-2">
          {me.phone ? (
            <div>
              <dt className="text-caption text-muted">{t('nx.sp.fPhone')}</dt>
              <dd dir="ltr" className="num">
                {me.phone}
              </dd>
            </div>
          ) : null}
          {me.email ? (
            <div>
              <dt className="text-caption text-muted">{t('nx.sp.email')}</dt>
              <dd dir="ltr">{me.email}</dd>
            </div>
          ) : null}
        </dl>
      </Panel>

      {/* Only where the shop runs a scheme. See the note above. */}
      {me.loyalty_enrolled ? (
        <Panel title={t('nx.sp.loyaltyTitle')}>
          <div className="flex flex-wrap items-baseline gap-6">
            <div>
              <p className="text-caption text-muted">{t('nx.sp.points')}</p>
              <p className="num text-lg font-semibold">{me.points}</p>
            </div>
            {me.tier ? (
              <div>
                <p className="text-caption text-muted">{t('nx.sp.tier')}</p>
                <p className="text-lg font-semibold">{me.tier}</p>
              </div>
            ) : null}
          </div>
        </Panel>
      ) : (
        <p className="flex items-center gap-2 text-caption text-muted">
          <Wallet aria-hidden className="size-4" />
          {t('nx.sp.noLoyalty')}
        </p>
      )}
    </div>
  );
}
