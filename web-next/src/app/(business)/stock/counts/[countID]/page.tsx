'use client';

// Counting the shelf.
//
// # Saving is not posting
//
// A count is the one stock document with a draft, and deliberately: somebody
// walks a shelf over an afternoon, saves what they have, and comes back. Only
// posting writes the difference to stock and to the books. So there are two
// buttons and they say which is which.
//
// # A line left uncounted is left alone
//
// Blank is not zero. Counting nothing and counting none of something are
// different claims, and the second is a write-off. The server treats an
// absent line as untouched, and the screen shows it as "not counted yet"
// rather than as a difference of minus everything.
//
// # The difference is shown as it is typed
//
// The number somebody is checking is not what they counted, it is how far off
// the system was — so it is computed beside the box rather than waiting for a
// round trip.

import { AlertTriangle } from 'lucide-react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { use, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { TableSkeleton } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { differenceOf } from '@/lib/stock/count';
import { ADJUSTMENT_STATUS, type Adjustment } from '@/lib/stock/stock';

function CountScreen({ countID }: { countID: string }) {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const { data, isLoading, error, refetch } = useApi<Adjustment>(
    scope ? `/stock/adjustments/${countID}` : null,
    scope ?? undefined,
  );

  const [counted, setCounted] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<'save' | 'post' | 'cancel' | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  // Seeded from what has already been counted, so coming back to a half-done
  // count shows the work rather than an empty grid.
  useEffect(() => {
    if (!data?.lines) return;
    setCounted((current) => {
      if (Object.keys(current).length > 0) return current;
      const next: Record<string, string> = {};
      for (const l of data.lines ?? []) {
        if (l.delta !== '') {
          // delta = counted - system, so counted = system + delta.
          next[l.variant_id] = String(Number(l.system_qty) + Number(l.delta));
        }
      }
      return next;
    });
  }, [data]);

  async function act(what: 'save' | 'post' | 'cancel') {
    if (!scope) return;
    setBusy(what);
    setActionError(null);
    try {
      const c = `company_id=${scope.company_id}`;
      if (what === 'save') {
        await api.put(`/stock/counts/${countID}?${c}`, {
          lines: Object.entries(counted)
            .filter(([, v]) => v.trim() !== '')
            .map(([variant_id, v]) => ({ variant_id, counted_qty: v })),
        });
        setSaved(true);
      } else if (what === 'post') {
        await api.post(`/stock/counts/${countID}/post?${c}`);
        router.push(`/stock/adjustments/${countID}`);
        return;
      } else {
        await api.post(`/stock/counts/${countID}/cancel?${c}`);
        router.push('/stock/adjustments');
        return;
      }
      await refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.adj.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.cnt.title')} />
        <TableSkeleton columns={4} />
      </>
    );
  }

  const status = ADJUSTMENT_STATUS[data.status];
  const open = data.status === 'draft';
  const lines = data.lines ?? [];
  const countedCount = Object.values(counted).filter((v) => v.trim() !== '').length;

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{data.adjustment_no}</span>
            {status ? <Badge tone={status.tone}>{t(status.key)}</Badge> : null}
            {saved ? <Badge tone="positive">{t('nx.cnt.saved')}</Badge> : null}
          </span>
        }
        description={[data.location, data.note].filter(Boolean).join(' · ')}
        actions={
          <Link
            href="/stock/adjustments"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.adj.back')}
          </Link>
        }
      />

      <FormError message={actionError} className="mb-4" />

      <Panel flush>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[34rem] border-collapse text-body">
            <caption className="sr-only">{t('nx.adj.linesCaption')}</caption>
            <thead>
              <tr className="border-b border-line">
                <th scope="col" className="px-4 py-2 text-start text-label text-muted">
                  {t('nx.npo.colProduct')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.adj.colSystem')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.cnt.colCounted')}
                </th>
                <th scope="col" className="px-4 py-2 text-end text-label text-muted">
                  {t('nx.cnt.colDifference')}
                </th>
              </tr>
            </thead>
            <tbody className="rows-lazy">
              {lines.map((l) => {
                const value = counted[l.variant_id] ?? '';
                const diff = differenceOf(value, l.system_qty);
                return (
                  <tr key={l.variant_id} className="border-b border-line last:border-0">
                    <th scope="row" className="px-4 py-2 text-start font-normal">
                      <span className="flex flex-col gap-0.5">
                        <span>{l.product}</span>
                        <span className="num text-caption text-muted">{l.sku}</span>
                      </span>
                    </th>
                    <td className="px-4 py-2 text-end">
                      <span className="num text-muted">
                        {formatQuantity(l.system_qty)}
                      </span>
                    </td>
                    <td className="px-4 py-2 text-end">
                      {open ? (
                        <Input
                          value={value}
                          onChange={(e) =>
                            setCounted((c) => ({
                              ...c,
                              [l.variant_id]: e.target.value,
                            }))
                          }
                          inputMode="decimal"
                          autoComplete="off"
                          aria-label={`${t('nx.cnt.colCounted')} ${l.product}`}
                          className="w-24 text-end"
                        />
                      ) : (
                        <span className="num">{formatQuantity(l.system_qty)}</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-end">
                      {diff === null ? (
                        <span className="text-caption text-subtle">
                          {t('nx.cnt.notCounted')}
                        </span>
                      ) : diff.startsWith('-') ? (
                        <span className="num font-medium text-critical-fg">{diff}</span>
                      ) : diff === '0' ? (
                        <span className="text-subtle">—</span>
                      ) : (
                        <span className="num font-medium text-positive-fg">+{diff}</span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Panel>

      {open ? (
        <div className="mt-6">
          <p className="flex items-start gap-2 text-body text-muted">
            <AlertTriangle
              className="mt-0.5 size-4 shrink-0 text-caution-fg"
              aria-hidden="true"
            />
            {t('nx.cnt.postHint')}
          </p>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button
              variant="secondary"
              busy={busy === 'save'}
              disabled={busy !== null}
              onClick={() => void act('save')}
            >
              {t('nx.cnt.save')}
            </Button>
            <Button
              variant="primary"
              busy={busy === 'post'}
              disabled={busy !== null || countedCount === 0}
              onClick={() => void act('post')}
            >
              {t('nx.cnt.post')}
            </Button>
            <Button
              variant="ghost"
              busy={busy === 'cancel'}
              disabled={busy !== null}
              onClick={() => void act('cancel')}
            >
              {t('nx.cnt.cancel')}
            </Button>
          </div>
        </div>
      ) : null}
    </>
  );
}

export default function CountPage({
  params,
}: {
  params: Promise<{ countID: string }>;
}) {
  const { countID } = use(params);
  return (
    <RequirePermission anyOf={['inventory.adjust_stock']}>
      <CountScreen countID={countID} />
    </RequirePermission>
  );
}
