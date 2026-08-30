// Stock moving between the company's own rooms (blueprint B4).
//
// # The screen is arranged around "whose turn is it"
//
// A transfer passes through four hands: whoever asked, whoever approved,
// whoever loaded the van, whoever unloaded it. The open list is therefore the
// default view, and each row offers exactly the one step THIS person can take
// next — nothing, if the next step is somebody else's.
//
// A greyed-out Approve button on a request you raised yourself would be an
// invitation to keep pressing it. B4 puts approval with a manager precisely so
// that the person who raised the transfer cannot move it on, and the screen
// says that by not offering the button at all.
//
// # A short receipt is reported, not resolved
//
// Four sent, three arrived. The missing one is still the company's stock, still
// in the valuation, and still in transit. Writing it off is a decision with a
// reason attached — a wastage voucher — so this screen states the shortfall and
// does not offer to make it disappear.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useLocale, useT } from '../i18n/locale';
import type { Key, Locale } from '../i18n/strings';
import { money, shortDate } from '../ui/format';
import {
  advanceTransfer,
  listStockOnHand,
  listTransfers,
  requestTransfer,
  type StockLine,
  type StockLocation,
  type Transfer,
} from '../api/stock';
import { isShort, isZero, nextStepFor, stepOf, TRANSFER_STEPS } from './stock';

export function TransfersPanel({
  companyId,
  locations,
}: {
  companyId: string;
  locations: StockLocation[];
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const may = {
    transfer: can('inventory.transfer_stock'),
    approve: can('inventory.approve_transfer'),
  };

  const [showAll, setShowAll] = useState(false);
  const [raising, setRaising] = useState(false);

  const load = useCallback(
    () => listTransfers(client, companyId, showAll ? '' : 'open'),
    [client, companyId, showAll],
  );
  const { remote, reload } = useRemote(load);

  return (
    <>
      {raising && (
        <RaiseTransfer
          companyId={companyId}
          locations={locations}
          onCancel={() => setRaising(false)}
          onRaised={() => {
            setRaising(false);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('stock.transfers')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">
            {t(showAll ? 'stock.allTransfers' : 'stock.openTransfers')}
          </h2>
          <div className="stock__headactions">
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setShowAll(!showAll)}
            >
              {t(showAll ? 'stock.showOpenOnly' : 'stock.showAll')}
            </button>
            {may.transfer && !raising && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setRaising(true)}
              >
                {t('stock.moveStock')}
              </button>
            )}
          </div>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Transfer[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('stock.noTransfersTitle')}
                  body={t('stock.noTransfersBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('stock.transfer')}</th>
                      <th scope="col">{t('stock.route')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col" className="num">
                        {t('stock.onTheVan')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((tr) => (
                      <TransferRow
                        key={tr.id}
                        companyId={companyId}
                        transfer={tr}
                        may={may}
                        locale={locale}
                        onChanged={reload}
                      />
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

function TransferRow({
  companyId,
  transfer,
  may,
  locale,
  onChanged,
}: {
  companyId: string;
  transfer: Transfer;
  may: { transfer: boolean; approve: boolean };
  locale: Locale;
  onChanged: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const next = nextStepFor(transfer.status, may);
  const at = stepOf(transfer.status);

  async function advance(step: 'approve' | 'dispatch' | 'receive' | 'cancel') {
    setBusy(true);
    setFailure(null);
    try {
      await advanceTransfer(client, companyId, transfer.id, step);
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <tr>
      <td>
        <span className="detail__strong">{transfer.transfer_no}</span>
        <span className="ds-caption">
          {shortDate(transfer.requested_at, locale)}
        </span>
      </td>
      <td>
        {/* Written as a sentence rather than two columns: the interesting fact
            is the journey, and splitting it makes a reader join it back up. */}
        {t('stock.fromTo', { from: transfer.from, to: transfer.to })}
      </td>
      <td>
        <ol className="stock__rail" aria-label={t('common.status')}>
          {TRANSFER_STEPS.map((step, i) => (
            <li
              key={step}
              className={`stock__railstep${
                at < 0
                  ? ' stock__railstep--stopped'
                  : i <= at
                    ? ' stock__railstep--done'
                    : ''
              }`}
            >
              {t(`stock.step.${step}` as Key)}
            </li>
          ))}
        </ol>
        {isShort(transfer.short_by) && (
          <span className="ds-badge ds-badge--warning">
            {t('stock.shortBy', { qty: transfer.short_by ?? '' })}
          </span>
        )}
        {failure && (
          <span className="form__error" role="alert">
            {failure}
          </span>
        )}
      </td>
      <td className="num">
        {transfer.value && !isZero(transfer.value)
          ? money(transfer.value, { currency: transfer.currency })
          : ''}
      </td>
      <td>
        <div className="stock__rowactions">
          {next && (
            <button
              className="ds-btn ds-btn--primary"
              disabled={busy}
              onClick={() => void advance(next)}
            >
              {t(`stock.do.${next}` as Key)}
            </button>
          )}
          {may.transfer &&
            (transfer.status === 'requested' ||
              transfer.status === 'approved') && (
              <button
                className="ds-btn ds-btn--quiet"
                disabled={busy}
                onClick={() => void advance('cancel')}
              >
                {t('action.cancel')}
              </button>
            )}
        </div>
      </td>
    </tr>
  );
}

function RaiseTransfer({
  companyId,
  locations,
  onCancel,
  onRaised,
}: {
  companyId: string;
  locations: StockLocation[];
  onCancel: () => void;
  onRaised: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [from, setFrom] = useState(locations[0]?.id ?? '');
  const [to, setTo] = useState(locations[1]?.id ?? '');
  const [note, setNote] = useState('');
  const [search, setSearch] = useState('');
  const [picked, setPicked] = useState<Array<{ line: StockLine; qty: string }>>(
    [],
  );
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const loadStock = useCallback(
    () =>
      listStockOnHand(client, companyId, {
        location_id: from || undefined,
        q: search || undefined,
      }),
    [client, companyId, from, search],
  );
  const stock = useRemote(loadStock);
  const available: StockLine[] =
    stock.remote.state === 'ready' ? stock.remote.data.data : [];

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const lines = picked
      .filter((p) => !isZero(p.qty))
      .map((p) => ({ variant_id: p.line.variant_id, qty: p.qty }));
    if (lines.length === 0) {
      setFailure(t('stock.nothingToMove'));
      return;
    }
    setBusy(true);
    setFailure(null);
    try {
      await requestTransfer(client, companyId, {
        from_location_id: from,
        to_location_id: to,
        note,
        lines,
      });
      onRaised();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel stock__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('stock.moveStock')}</h2>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('stock.from')} htmlFor="trf-from" required>
            <select
              id="trf-from"
              className="input"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            >
              {locations.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name}
                </option>
              ))}
            </select>
          </Field>

          <Field label={t('stock.to')} htmlFor="trf-to" required>
            <select
              id="trf-to"
              className="input"
              value={to}
              onChange={(e) => setTo(e.target.value)}
            >
              {locations
                .filter((l) => l.id !== from)
                .map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.name}
                  </option>
                ))}
            </select>
          </Field>
        </div>

        <Field label={t('stock.note')} htmlFor="trf-note">
          <TextInput id="trf-note" value={note} onChange={setNote} />
        </Field>

        <Field label={t('stock.find')} htmlFor="trf-search">
          <TextInput
            id="trf-search"
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
                  onClick={() =>
                    setPicked((p) =>
                      p.some((x) => x.line.variant_id === l.variant_id)
                        ? p
                        : [...p, { line: l, qty: '' }],
                    )
                  }
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
                    {t('stock.qtyToMove')}
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
                        value={p.qty}
                        aria-label={p.line.product}
                        onChange={(e) => {
                          const next = [...picked];
                          next[i] = { ...p, qty: e.target.value };
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
          submitLabel={t('stock.requestMove')}
          busy={busy}
          disabled={!from || !to || from === to}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
