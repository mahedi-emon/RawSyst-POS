'use client';

// One consignment, and every step it has been through.
//
// # The route said what this was for, and nothing called it
//
// `GET /deliveries/{deliveryID}` is described as "one consignment and every
// step it has been through". The list carries the same fields, so nothing here
// is new data — what was missing is the READING: four timestamps in a row of a
// table are four columns nobody has room for, and laid out as a sequence they
// are the answer to "where is my order", which is the question this module
// exists for.
//
// # A driver may read only their own run
//
// The route enforces it, and answers "not found" rather than "forbidden" for
// somebody else's — confirming a consignment exists would tell a driver what to
// ask about next. Nothing here works around that; a refusal is shown as the
// server wrote it.
//
// # An absent timestamp is a step that has not happened
//
// Not an error, and not a dash in a column of dates. The step is drawn unfilled
// so the sequence reads as a progress rather than as a table with holes in it.

import { Check } from 'lucide-react';

import { Badge, Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { useApi } from '@/lib/api/hooks';
import { formatMoney, type MarketCode } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

/** One consignment, as `GET /deliveries/{id}` answers it. */
export interface Consignment {
  id: string;
  delivery_no: string;
  order_id: string;
  order_no?: string;
  status: string;
  customer?: string;
  driver_id?: string;
  driver_name?: string;
  address: string;
  phone?: string;
  fee: string;
  is_cod: boolean;
  cod_amount: string;
  cod_collected_at?: string;
  assigned_at?: string;
  picked_up_at?: string;
  delivered_at?: string;
  failure_reason?: string;
  attempt_count?: number;
  note?: string;
  created_at: string;
  currency?: string;
}

/** The pipeline, in the order it happens. */
const STEPS: ReadonlyArray<{ key: keyof Consignment; label: Key }> = [
  { key: 'created_at', label: 'nx.del.stepRaised' },
  { key: 'assigned_at', label: 'nx.del.stepAssigned' },
  { key: 'picked_up_at', label: 'nx.del.stepPickedUp' },
  { key: 'delivered_at', label: 'nx.del.stepDelivered' },
];

export function ConsignmentPanel({
  companyId,
  deliveryId,
  currency,
  market,
}: {
  companyId: string;
  deliveryId: string;
  currency: string;
  market: MarketCode;
}) {
  const t = useT();
  const { data, isLoading, error, refetch } = useApi<Consignment>(
    `/deliveries/${deliveryId}`,
    { company_id: companyId },
  );

  const money = (v: string) =>
    formatMoney(v, { currency: data?.currency || currency, market });

  return (
    <Panel
      className="mb-5"
      title={t('nx.del.detailTitle', { no: data?.delivery_no ?? '' })}
      description={t('nx.del.detailHint')}
    >
      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <Skeleton className="h-32" /> : null}

      {data ? (
        <>
          <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-label text-muted">{t('nx.del.detailCustomer')}</dt>
              <dd className="mt-0.5 text-body">{data.customer || '—'}</dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.del.detailAddress')}</dt>
              <dd className="mt-0.5 text-body">{data.address}</dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.del.detailDriver')}</dt>
              <dd className="mt-0.5 text-body">
                {data.driver_name || t('nx.del.detailNoDriver')}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.del.detailFee')}</dt>
              <dd className="num mt-0.5 text-body">{money(data.fee)}</dd>
            </div>
          </dl>

          {data.is_cod ? (
            <p className="mt-4 text-body">
              <Badge tone={data.cod_collected_at ? 'positive' : 'caution'}>
                {data.cod_collected_at ? t('nx.del.codTaken') : t('nx.del.codDue')}
              </Badge>{' '}
              <span className="num">{money(data.cod_amount)}</span>
            </p>
          ) : null}

          {/* The sequence, rather than four columns nobody has room for. */}
          <ol className="mt-5 flex flex-col gap-3">
            {STEPS.map((step) => {
              const when = data[step.key] as string | undefined;
              return (
                <li key={String(step.key)} className="flex items-start gap-3">
                  <span
                    className={cn(
                      'mt-0.5 grid size-5 shrink-0 place-items-center rounded-full border',
                      when
                        ? 'border-positive/40 bg-positive-subtle text-positive-fg'
                        : 'border-line bg-surface-sunken text-disabled',
                    )}
                    aria-hidden="true"
                  >
                    {when ? <Check className="size-3" /> : null}
                  </span>
                  <span className="flex flex-col gap-0.5">
                    <span className={cn('text-body', !when && 'text-muted')}>
                      {t(step.label)}
                    </span>
                    <span className="num text-caption text-muted">
                      {when
                        ? when.slice(0, 16).replace('T', ' ')
                        : t('nx.del.stepNotYet')}
                    </span>
                  </span>
                </li>
              );
            })}
          </ol>

          {data.attempt_count ? (
            <p className="mt-4 text-body text-muted">
              {t('nx.del.attempts', { n: String(data.attempt_count) })}
            </p>
          ) : null}
          {data.failure_reason ? (
            <p className="mt-2 max-w-prose text-body text-critical-fg">
              {t('nx.del.failedBecause', { why: data.failure_reason })}
            </p>
          ) : null}
        </>
      ) : null}
    </Panel>
  );
}
