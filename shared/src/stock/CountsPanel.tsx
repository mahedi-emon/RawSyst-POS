// The physical count.
//
// # The counter is not shown what the system expects
//
// The sheet lists products and an empty box. It does not show the system
// quantity, and that is the single most important decision on this screen: a
// person told the system expects fourteen counts to fourteen. Not dishonestly —
// the eye finds what it is looking for — and a count that confirms the records
// is worth nothing at all.
//
// The expected figure and the variance appear after posting, which is when they
// become information rather than a hint.
//
// # A blank box is silence, not zero
//
// Somebody who counts three aisles and goes home has said nothing about the
// fourth. Reading silence as "none" would write off the entire aisle, so an
// untouched line is left alone by the server and shown as untouched here.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormError } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import { money, shortDate } from '../ui/format';
import {
  cancelStockCount,
  listAdjustments,
  openStockCount,
  postStockCount,
  readAdjustment,
  saveStockCount,
  type Adjustment,
  type AdjustmentLine,
  type StockLocation,
} from '../api/stock';
import { isZero, variance, varianceTone } from './stock';

export function CountsPanel({
  companyId,
  locations,
}: {
  companyId: string;
  locations: StockLocation[];
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [open, setOpen] = useState<string | null>(null);

  const load = useCallback(
    () => listAdjustments(client, companyId, { kind: 'count' }),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <CountSheet
        companyId={companyId}
        countId={open}
        onDone={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <>
      <StartCount
        companyId={companyId}
        locations={locations}
        onStarted={(id) => {
          setOpen(id);
          reload();
        }}
      />

      <section className="ds-panel" aria-label={t('stock.counts')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('stock.pastCounts')}</h2>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Adjustment[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('stock.noCountsTitle')}
                  body={t('stock.noCountsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('stock.voucher')}</th>
                      <th scope="col">{t('stock.when')}</th>
                      <th scope="col">{t('stock.location')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col" className="num">
                        {t('stock.valueMoved')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((a) => (
                      <tr key={a.id}>
                        <td className="detail__strong">{a.adjustment_no}</td>
                        <td>{shortDate(a.created_at, locale)}</td>
                        <td>{a.location}</td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${
                              a.status === 'posted'
                                ? 'success'
                                : a.status === 'draft'
                                  ? 'warning'
                                  : 'muted'
                            }`}
                          >
                            {t(
                              a.status === 'posted'
                                ? 'stock.posted'
                                : a.status === 'draft'
                                  ? 'stock.counting'
                                  : 'stock.cancelled',
                            )}
                          </span>
                        </td>
                        <td className="num">
                          {a.status !== 'posted' ? (
                            <span aria-hidden="true">—</span>
                          ) : isZero(a.value) ? (
                            <span className="ds-badge">
                              {t('stock.everythingAgreed')}
                            </span>
                          ) : (
                            money(a.value, { currency: a.currency })
                          )}
                        </td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setOpen(a.id)}
                          >
                            {t(
                              a.status === 'draft'
                                ? 'stock.continueCount'
                                : 'action.view',
                            )}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          }
        </RemoteBody>
      </section>
    </>
  );
}

function StartCount({
  companyId,
  locations,
  onStarted,
}: {
  companyId: string;
  locations: StockLocation[];
  onStarted: (id: string) => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const [locationId, setLocationId] = useState(locations[0]?.id ?? '');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function start() {
    if (!locationId) return;
    setBusy(true);
    setFailure(null);
    try {
      const sheet = await openStockCount(client, companyId, {
        location_id: locationId,
      });
      onStarted(sheet.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('stock.startCount')}>
      <div className="ds-panel__body stock__startrow">
        <FormError message={failure} />
        <Field label={t('stock.countWhere')} htmlFor="count-location" required>
          <select
            id="count-location"
            className="input"
            value={locationId}
            onChange={(e) => setLocationId(e.target.value)}
          >
            {locations.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </select>
        </Field>
        <button
          className="ds-btn ds-btn--primary"
          disabled={busy || !locationId}
          onClick={() => void start()}
        >
          {t('stock.startCount')}
        </button>
      </div>
    </section>
  );
}

function CountSheet({
  companyId,
  countId,
  onDone,
}: {
  companyId: string;
  countId: string;
  onDone: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => readAdjustment(client, companyId, countId),
    [client, companyId, countId],
  );
  const { remote, reload } = useRemote(load);

  const [counted, setCounted] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function save(sheet: Adjustment) {
    const lines = Object.entries(counted)
      .filter(([, qty]) => qty.trim() !== '')
      .map(([variant_id, counted_qty]) => ({ variant_id, counted_qty }));
    if (lines.length === 0) return;
    setBusy(true);
    setFailure(null);
    try {
      await saveStockCount(client, companyId, sheet.id, lines);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function post(sheet: Adjustment) {
    setBusy(true);
    setFailure(null);
    try {
      const lines = Object.entries(counted)
        .filter(([, qty]) => qty.trim() !== '')
        .map(([variant_id, counted_qty]) => ({ variant_id, counted_qty }));
      if (lines.length > 0) {
        await saveStockCount(client, companyId, sheet.id, lines);
      }
      await postStockCount(client, companyId, sheet.id);
      onDone();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function abandon(sheet: Adjustment) {
    setBusy(true);
    try {
      await cancelStockCount(client, companyId, sheet.id);
      onDone();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(sheet: Adjustment) => {
        const draft = sheet.status === 'draft';
        return (
          <section className="ds-panel" aria-label={sheet.adjustment_no}>
            <div className="ds-panel__head">
              <div>
                <h2 className="ds-h3">{sheet.adjustment_no}</h2>
                <p className="ds-caption">
                  {sheet.location} · {shortDate(sheet.created_at, locale)}
                </p>
              </div>
              <div className="stock__headactions">
                <button className="ds-btn ds-btn--quiet" onClick={onDone}>
                  {t('action.back')}
                </button>
                {draft && (
                  <>
                    <button
                      className="ds-btn ds-btn--quiet"
                      disabled={busy}
                      onClick={() => void abandon(sheet)}
                    >
                      {t('stock.abandonCount')}
                    </button>
                    <button
                      className="ds-btn ds-btn--quiet"
                      disabled={busy}
                      onClick={() => void save(sheet)}
                    >
                      {t('stock.saveCount')}
                    </button>
                    <button
                      className="ds-btn ds-btn--primary"
                      disabled={busy}
                      onClick={() => void post(sheet)}
                    >
                      {t('stock.postCount')}
                    </button>
                  </>
                )}
              </div>
            </div>

            <div className="ds-panel__body">
              <FormError message={failure} />
              {draft && (
                <p className="ds-body-sm" role="note">
                  {t('stock.countBlind')}
                </p>
              )}

              <div className="ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('stock.product')}</th>
                      <th scope="col" className="num">
                        {t('stock.counted')}
                      </th>
                      {!draft && (
                        <>
                          <th scope="col" className="num">
                            {t('stock.expected')}
                          </th>
                          <th scope="col" className="num">
                            {t('stock.variance')}
                          </th>
                        </>
                      )}
                    </tr>
                  </thead>
                  <tbody>
                    {(sheet.lines ?? []).map((l) => (
                      <CountRow
                        key={l.variant_id}
                        line={l}
                        draft={draft}
                        value={counted[l.variant_id] ?? l.counted_qty ?? ''}
                        onChange={(v) =>
                          setCounted({ ...counted, [l.variant_id]: v })
                        }
                      />
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </section>
        );
      }}
    </RemoteBody>
  );
}

function CountRow({
  line,
  draft,
  value,
  onChange,
}: {
  line: AdjustmentLine;
  draft: boolean;
  value: string;
  onChange: (v: string) => void;
}) {
  const t = useT();
  // Before posting the sheet does not carry the system figure, so there is
  // nothing to compare against and nothing is shown. Afterwards the server has
  // sent both and the variance is the one it actually posted.
  const delta = draft ? null : (line.delta ?? variance(line.system_qty, line.counted_qty ?? ''));
  const tone = varianceTone(delta);

  return (
    <tr>
      <td>
        <span className="detail__strong">{line.product}</span>
        <span className="ds-caption">{line.sku}</span>
      </td>
      <td className="num">
        {draft ? (
          <input
            className="input input--num"
            inputMode="decimal"
            value={value}
            aria-label={line.product}
            onChange={(e) => onChange(e.target.value)}
          />
        ) : (
          (line.counted_qty ?? <span aria-hidden="true">—</span>)
        )}
      </td>
      {!draft && (
        <>
          <td className="num">
            {line.system_qty}
            {line.moved_while_counting && (
              <span className="ds-caption">{t('stock.movedWhileCounting')}</span>
            )}
          </td>
          <td className="num">
            {delta === null || tone === 'flat' ? (
              <span aria-hidden="true">—</span>
            ) : (
              <span className={`stock__qty stock__qty--${tone}`}>{delta}</span>
            )}
          </td>
        </>
      )}
    </tr>
  );
}
