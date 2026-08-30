// Correcting stock, and writing it off.
//
// # Two buttons, not one form with a dropdown
//
// An adjustment and a wastage are the same shape and mean opposite things. An
// adjustment says the records were wrong; a wastage says the goods were
// destroyed. Only the second is a loss, and only the second is the one somebody
// might reach for to make a theft disappear.
//
// A single form with a kind selector would let a person start typing one and
// submit the other without noticing. Two entry points, each labelled with what
// it is, makes the choice happen before the typing.
//
// # The reason is a list and the note is required
//
// B4: "mandatory reason + category". A free-text reason box produces a hundred
// spellings of "damaged" and no report anybody can run, so the category is a
// short fixed list. The sentence underneath is still required, because the
// category says what KIND of loss and the note says what happened.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { money, shortDate } from '../ui/format';
import {
  listAdjustments,
  listStockOnHand,
  recordAdjustment,
  type Adjustment,
  type StockLine,
  type StockLocation,
} from '../api/stock';
import { isZero } from './stock';

/** At least one category, always. Typed as a non-empty tuple so the form can
 *  open on the first one without asking whether there is a first one — there is
 *  no kind of voucher with nothing to say about why. */
type Categories = readonly [string, ...string[]];

/** The categories the server accepts, per kind. Kept in step with
 *  `reasonsByKind` in the Go service; a category this list carries and the
 *  server does not is refused with the list it does accept. */
const REASONS: Record<'adjustment' | 'wastage', Categories> = {
  wastage: ['damaged', 'expired', 'stolen', 'sample', 'internal_use', 'other'],
  adjustment: ['correction', 'found', 'data_entry', 'other'],
};

export function AdjustmentsPanel({
  companyId,
  locations,
}: {
  companyId: string;
  locations: StockLocation[];
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayAdjust = can('inventory.adjust_stock');

  const [recording, setRecording] = useState<'adjustment' | 'wastage' | null>(
    null,
  );

  const load = useCallback(
    () => listAdjustments(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <>
      {recording && (
        <RecordForm
          companyId={companyId}
          kind={recording}
          locations={locations}
          onCancel={() => setRecording(null)}
          onRecorded={() => {
            setRecording(null);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('stock.adjustments')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('stock.adjustments')}</h2>
          {mayAdjust && !recording && (
            <div className="stock__headactions">
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setRecording('adjustment')}
              >
                {t('stock.correctStock')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setRecording('wastage')}
              >
                {t('stock.recordWastage')}
              </button>
            </div>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Adjustment[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('stock.noVouchersTitle')}
                  body={t('stock.noVouchersBody')}
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
                      <th scope="col">{t('stock.why')}</th>
                      <th scope="col">{t('stock.who')}</th>
                      <th scope="col" className="num">
                        {t('stock.valueMoved')}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((a) => (
                      <tr key={a.id}>
                        <td>
                          <span className="detail__strong">
                            {a.adjustment_no}
                          </span>
                          <span className="ds-caption">
                            {t(`stock.kind.${a.kind}` as Key)}
                          </span>
                        </td>
                        <td>{shortDate(a.created_at, locale)}</td>
                        <td>{a.location}</td>
                        <td>
                          <span className="detail__strong">
                            {reasonName(a.reason, t)}
                          </span>
                          {a.note && <span className="ds-caption">{a.note}</span>}
                        </td>
                        <td>{a.created_by}</td>
                        <td className="num">
                          {/* Zero is the outcome to hope for on a count and
                              deserves saying so, rather than a bare 0.00 that
                              reads like a figure nobody filled in. */}
                          {isZero(a.value) ? (
                            <span className="ds-badge">{t('stock.noChange')}</span>
                          ) : (
                            money(a.value, { currency: a.currency })
                          )}
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

function RecordForm({
  companyId,
  kind,
  locations,
  onCancel,
  onRecorded,
}: {
  companyId: string;
  kind: 'adjustment' | 'wastage';
  locations: StockLocation[];
  onCancel: () => void;
  onRecorded: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [locationId, setLocationId] = useState(locations[0]?.id ?? '');
  const [reason, setReason] = useState(REASONS[kind][0]);
  const [note, setNote] = useState('');
  const [search, setSearch] = useState('');
  const [picked, setPicked] = useState<Array<{ line: StockLine; delta: string }>>(
    [],
  );
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  // The voucher's identity is decided when the form opens, not when it is
  // submitted, so pressing Save twice on a slow connection sends the same one
  // and the server hands back the first rather than writing the stock off
  // again.
  const [voucherId] = useState(() => crypto.randomUUID());

  const loadStock = useCallback(
    () =>
      listStockOnHand(client, companyId, {
        location_id: locationId || undefined,
        q: search || undefined,
      }),
    [client, companyId, locationId, search],
  );
  const stock = useRemote(loadStock);
  const available: StockLine[] =
    stock.remote.state === 'ready' ? stock.remote.data.data : [];

  function add(line: StockLine) {
    if (picked.some((p) => p.line.variant_id === line.variant_id)) return;
    setPicked([...picked, { line, delta: '' }]);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const lines = picked
      .filter((p) => !isZero(p.delta))
      .map((p) => ({
        variant_id: p.line.variant_id,
        // A wastage is always a reduction. Asking a person to type a minus sign
        // to record damage is a way of getting a positive number by accident,
        // so the sign comes from the kind of voucher rather than from the
        // keyboard.
        delta: kind === 'wastage' ? '-' + p.delta.replace(/^-/, '') : p.delta,
      }));

    if (lines.length === 0) {
      setFailure(t('stock.nothingToRecord'));
      return;
    }

    setBusy(true);
    setFailure(null);
    try {
      await recordAdjustment(client, companyId, {
        uuid: voucherId,
        location_id: locationId,
        kind,
        reason,
        note,
        lines,
      });
      onRecorded();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel stock__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">
          {t(kind === 'wastage' ? 'stock.recordWastage' : 'stock.correctStock')}
        </h2>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('stock.location')} htmlFor="adj-location" required>
            <select
              id="adj-location"
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

          <Field label={t('stock.category')} htmlFor="adj-reason" required>
            <select
              id="adj-reason"
              className="input"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            >
              {REASONS[kind].map((r) => (
                <option key={r} value={r}>
                  {reasonName(r, t)}
                </option>
              ))}
            </select>
          </Field>
        </div>

        <Field
          label={t('stock.note')}
          hint={t('stock.noteHint')}
          htmlFor="adj-note"
          required
        >
          <TextInput id="adj-note" value={note} onChange={setNote} />
        </Field>

        <Field label={t('stock.find')} htmlFor="adj-search">
          <TextInput
            id="adj-search"
            value={search}
            onChange={setSearch}
            placeholder={t('stock.findHint')}
          />
        </Field>

        {search !== '' && (
          <ul className="stock__picklist">
            {available.slice(0, 8).map((l) => (
              <li key={l.variant_id + l.location}>
                <button
                  type="button"
                  className="ds-btn ds-btn--quiet stock__pick"
                  onClick={() => add(l)}
                >
                  <span className="detail__strong">{l.product}</span>
                  <span className="ds-caption">
                    {l.sku} · {l.on_hand}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}

        {picked.length > 0 && (
          <div className="ds-scroll-x">
            <table className="ds-table">
              <thead>
                <tr>
                  <th scope="col">{t('stock.product')}</th>
                  <th scope="col" className="num">
                    {t('stock.onHand')}
                  </th>
                  <th scope="col" className="num">
                    {t(kind === 'wastage' ? 'stock.qtyLost' : 'stock.qtyOut')}
                  </th>
                  <th scope="col">
                    <span className="ds-visually-hidden">
                      {t('common.actions')}
                    </span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {picked.map((p, i) => (
                  <tr key={p.line.variant_id}>
                    <td>
                      <span className="detail__strong">{p.line.product}</span>
                      <span className="ds-caption">{p.line.sku}</span>
                    </td>
                    <td className="num">{p.line.on_hand}</td>
                    <td className="num">
                      <input
                        className="input input--num"
                        inputMode="decimal"
                        value={p.delta}
                        aria-label={p.line.product}
                        onChange={(e) => {
                          const next = [...picked];
                          next[i] = { ...p, delta: e.target.value };
                          setPicked(next);
                        }}
                      />
                    </td>
                    <td>
                      <button
                        type="button"
                        className="ds-btn ds-btn--quiet"
                        onClick={() =>
                          setPicked(picked.filter((_, j) => j !== i))
                        }
                      >
                        {t('action.remove')}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <FormActions
          submitLabel={t('stock.record')}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

/** A reason category in words, falling back to its own name. */
function reasonName(reason: string, t: (k: Key) => string): string {
  const key = `stock.reason.${reason}` as Key;
  const named = t(key);
  return named === key ? reason : named;
}
