// Taking goods back.
//
// Gated on `sales.refund` by the caller. That gate is a courtesy: the server
// refuses a refund from a cashier who lacks the permission whatever this screen
// offered, and the route is registered with the permission rather than
// `sales.view` precisely so that looking a sale up and being told how much of
// it is still claimable are separate privileges.
//
// # This screen needs the network and says so
//
// Everything else at the counter works offline. This does not, because the
// question it must answer — how much of this invoice has already been given
// back — cannot be answered here. Credit notes raised at another till, or at
// this one while it was offline, are invisible to it. A till that guessed would
// refund the same jacket twice.
//
// So it refuses rather than guesses, and the refusal names the reason. A
// customer told "not until we are back online" has been inconvenienced; one
// refunded twice is a loss the shop absorbs and may never notice.

import { useState } from 'react';

import { Offline, RequestFailed } from '@rawsyst/shared/api/client';
import { useAuth } from '@rawsyst/shared/auth/session';
import type { Translate } from '@rawsyst/shared/i18n/strings';
import { useT } from '@rawsyst/shared/i18n/locale';
import {
  fetchReturnable,
  lookUpSale,
  overReturned,
  refundTotal,
  submitReturn,
  type InvoiceMatch,
  type ReturnableLine,
  type ReturnSelection,
} from './returns';
import {
  previewDifference,
  readyToExchange,
  replacementFrom,
  submitExchange,
  type ReplacementLine,
} from './exchange';
import { useTerminal } from '../offline/useTerminal';

export function ReturnsScreen() {
  const t = useT();
  const { client, can } = useAuth();
  // Only for the catalogue: a replacement is scanned from the local cache the
  // same way a sale line is. The queue is untouched — an exchange goes to the
  // server as one transaction and is never queued, for the same reason a
  // refund is not.
  const terminal = useTerminal();

  const [invoiceId, setInvoiceId] = useState('');
  // The sale the reference resolved to. Every call after the lookup uses
  // `found.id`, never what the cashier typed: the receipt carries the document
  // UUID and the routes take the invoice id, and they are different UUIDs.
  const [found, setFound] = useState<InvoiceMatch | null>(null);
  const [lines, setLines] = useState<ReturnableLine[] | null>(null);
  const [qty, setQty] = useState<Record<string, string>>({});
  const [method, setMethod] = useState('cash');
  const [reason, setReason] = useState('');
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<string | null>(null);

  // Refund or exchange. The two share everything up to the point where the
  // customer walks out with either money or different goods, so they share a
  // screen rather than making the cashier find a second one.
  const [mode, setMode] = useState<'refund' | 'exchange'>('refund');
  const [replacement, setReplacement] = useState<ReplacementLine[]>([]);
  const [scanInput, setScanInput] = useState('');

  const selection: ReturnSelection[] = Object.entries(qty)
    .filter(([, q]) => q.trim() !== '' && Number(q) > 0)
    .map(([lineId, q]) => ({ lineId, qty: q }));

  const invalid = lines ? overReturned(lines, selection) : [];
  const total = lines ? refundTotal(lines, selection) : '0.00';
  const canRefund =
    lines !== null && selection.length > 0 && invalid.length === 0 && !busy;

  // An ESTIMATE, shown so the cashier can tell the customer what to expect
  // before committing. The server prices the replacement from the registry at
  // the transaction date and refuses the exchange if what the till offered
  // does not match, so this figure informs and never decides.
  const preview = lines
    ? previewDifference(lines, selection, replacement)
    : { credit: '0.00', owed: '0.00', customerPays: false };

  const canExchange =
    lines !== null &&
    invalid.length === 0 &&
    !busy &&
    readyToExchange(selection, replacement, reason);

  async function addReplacement(code: string) {
    const barcode = code.trim();
    if (!barcode) return;
    setScanInput('');

    // The local cache, exactly as the counter does it. A replacement is an
    // ordinary sale line and is priced the same way.
    const item = await terminal.catalogue?.lookup(barcode);
    if (!item) {
      setNotice(t('returns.noBarcodeHere', { barcode }));
      return;
    }
    if (!item.isActive) {
      setNotice(t('till.withdrawn'));
      return;
    }
    setNotice(null);
    setReplacement((prev) => [...prev, replacementFrom(item)]);
  }

  async function exchange() {
    if (!canExchange || !lines || !found) return;
    setBusy(true);
    setNotice(null);
    try {
      const result = await submitExchange(client, {
        // Both generated BEFORE the call and deliberately different. A network
        // failure after the server committed would otherwise have the cashier
        // press the button again and issue a second credit note against the
        // same goods.
        creditNoteUuid: crypto.randomUUID(),
        invoiceUuid: crypto.randomUUID(),
        originalInvoiceId: found.id,
        reason: reason.trim(),
        returning: selection,
        replacement,
        // Only the difference, and only when there is one. An even swap sends
        // nothing, because nothing moves.
        settlement:
          preview.owed === '0.00'
            ? []
            : [{ method, amount: preview.owed }],
      });

      const note =
        result.credit_note.human_number ?? result.credit_note.credit_note_id;
      setDone(
        result.customer_paid
          ? t('returns.exchanged', {
              amount: result.difference,
              number: note,
            })
          : t('returns.exchangedRefund', {
              amount: result.difference.replace('-', ''),
              number: note,
            }),
      );
      setLines(null);
      setFound(null);
      setQty({});
      setReplacement([]);
    } catch (err) {
      setNotice(explain(err, t('returns.exchangeFailed'), t));
    } finally {
      setBusy(false);
    }
  }

  async function lookUp() {
    const reference = invoiceId.trim();
    if (!reference) return;
    setNotice(null);
    setDone(null);
    setLines(null);
    setFound(null);
    setQty({});
    setBusy(true);
    try {
      // Two calls, in this order, and the order is the point. The receipt
      // carries the document UUID the till generated; every route that reads a
      // sale takes the invoice id the server minted. Sending the first where
      // the second belongs is how this screen could not find a single sale any
      // terminal had made.
      const sale = await lookUpSale(client, reference);
      const returnable = await fetchReturnable(client, sale.id);
      if (returnable.length === 0) {
        setFound(sale);
        setNotice(t('returns.nothingLeft'));
        return;
      }
      setFound(sale);
      setLines(returnable);
    } catch (err) {
      setNotice(explain(err, t('returns.lookupFailed'), t));
    } finally {
      setBusy(false);
    }
  }

  async function refund() {
    if (!canRefund || !lines || !found) return;
    setBusy(true);
    setNotice(null);
    try {
      const result = await submitReturn(client, {
        // Generated BEFORE the call, which is what makes a retry safe: a
        // network failure after the server committed would otherwise have the
        // cashier press refund again and give the money back twice.
        creditNoteUuid: crypto.randomUUID(),
        originalInvoiceId: found.id,
        reason: reason.trim(),
        lines: selection,
        refunds: [{ method, amount: total }],
      });
      setDone(result.human_number || result.credit_note_id);
      setLines(null);
      setFound(null);
      setQty({});
    } catch (err) {
      setNotice(explain(err, t('returns.refundFailed'), t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="returns">
      <h1 className="returns__title">
        {mode === 'refund'
          ? t('returns.returnGoods')
          : t('returns.exchangeGoods')}
      </h1>

      {/* Exchanging is its own permission. A shop that lets a cashier refund
          does not necessarily let them swap goods, and the server enforces the
          same split on the route.

          `returns__modes`, not `tenders`. Reusing the tender grid made these
          two the size of payment buttons — half the screen each, 72px tall —
          for a choice that is a tab. A control that loud reads as the thing
          the screen is for, and the thing this screen is for is the receipt
          underneath it. */}
      {can('sales.exchange') && (
        <div className="returns__modes" role="tablist">
          {(['refund', 'exchange'] as const).map((m) => (
            <button
              key={m}
              role="tab"
              aria-selected={mode === m}
              className={
                mode === m ? 'button button--primary' : 'button button--quiet'
              }
              onClick={() => {
                setMode(m);
                setReplacement([]);
                setNotice(null);
              }}
            >
              {m === 'refund' ? t('returns.refundTab') : t('returns.exchangeTab')}
            </button>
          ))}
        </div>
      )}

      <form
        className="scan"
        onSubmit={(e) => {
          e.preventDefault();
          void lookUp();
        }}
      >
        <input
          className="scan__input"
          autoFocus
          placeholder={t('returns.scanReceipt')}
          value={invoiceId}
          onChange={(e) => setInvoiceId(e.target.value)}
          aria-label={t('returns.saleReference')}
        />
      </form>

      {notice && (
        <p className="queue queue--bad" role="status" aria-live="polite">
          {notice}
        </p>
      )}

      {done && (
        <p className="queue queue--ok" role="status" aria-live="polite">
          {t('returns.refunded', { number: done })}
        </p>
      )}

      {/* Which sale was found. A cashier who scanned the wrong receipt, or
          whose scan resolved a reference they could not read, has one chance to
          notice before money moves — and the number and total are what they can
          check against the paper in their hand. */}
      {found && (
        <p className="returns__found">
          <strong>{found.human_number ?? found.uuid.slice(0, 8)}</strong>
          <span>{found.issue_date}</span>
          <span>
            {found.total_inclusive} {found.currency}
          </span>
        </p>
      )}

      {lines && (
        <>
          <table className="cart">
            <thead>
              <tr>
                <th scope="col">{t('common.item')}</th>
                <th scope="col" className="num">Left</th>
                <th scope="col">{t('returns.return')}</th>
              </tr>
            </thead>
            <tbody>
              {lines.map((line) => {
                const available = Number(line.qty_returnable);
                return (
                  <tr key={line.line_id}>
                    <td>
                      <span className="cart__name">{line.description}</span>
                      {/* Shown so the cashier can see WHY a line offers less
                          than was bought, rather than being silently unable to
                          refund it. */}
                      {Number(line.qty_returned) > 0 && (
                        <span className="cart__sku">
                          {t('returns.alreadyReturned', {
                            qty: line.qty_returned,
                          })}
                        </span>
                      )}
                    </td>
                    <td className="num">{line.qty_returnable}</td>
                    <td>
                      <input
                        className="cart__qty"
                        inputMode="decimal"
                        // A fully returned line cannot be chosen at all: the
                        // server would refuse it, and offering it invites the
                        // double refund this whole flow exists to prevent.
                        disabled={available <= 0}
                        value={qty[line.line_id] ?? ''}
                        onChange={(e) =>
                          setQty((prev) => ({
                            ...prev,
                            [line.line_id]: e.target.value,
                          }))
                        }
                        aria-label={`Quantity of ${line.description} to return`}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>

          {invalid.length > 0 && (
            <p className="queue queue--warn" role="status" aria-live="polite">
              {t('returns.overReturned')}
            </p>
          )}

          <label className="returns__reason">
            Reason
            <input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t('returns.reason')}
            />
          </label>

          <div className="tenders">
            {(['cash', 'mada'] as const).map((m) => (
              <button
                key={m}
                className={
                  method === m
                    ? 'button button--large button--primary'
                    : 'button button--large'
                }
                onClick={() => setMethod(m)}
              >
                {m === 'cash' ? 'Cash' : 'Mada'}
              </button>
            ))}
          </div>

          {mode === 'refund' ? (
            <>
              <dl className="totals">
                <div className="totals__grand">
                  <dt>{t('returns.refund')}</dt>
                  {/* The terminal's figure, for the cashier to see before they
                      commit. The server recomputes it and is the authority. */}
                  <dd className="num">{total}</dd>
                </div>
              </dl>

              <button
                className="button button--primary button--large"
                disabled={!canRefund}
                onClick={() => void refund()}
              >
                {busy
                  ? t('returns.refunding')
                  : t('returns.refundAmount', { amount: total })}
              </button>
            </>
          ) : (
            <>
              <h2 className="held__title">{t('returns.exchange')}</h2>

              <form
                className="scan"
                onSubmit={(e) => {
                  e.preventDefault();
                  void addReplacement(scanInput);
                }}
              >
                <input
                  className="scan__input"
                  placeholder={t('returns.scanReplacement')}
                  value={scanInput}
                  onChange={(e) => setScanInput(e.target.value)}
                  aria-label={t('returns.scanReplacementItem')}
                />
              </form>

              {replacement.length === 0 ? (
                <p className="counter__empty">
                  {t('returns.scanReplacementPrompt')}
                </p>
              ) : (
                <table className="cart">
                  <tbody>
                    {replacement.map((line, i) => (
                      <tr key={`${line.variantId}-${i}`}>
                        <td>{line.description}</td>
                        <td className="num">
                          {line.qty} × {line.unitPrice}
                        </td>
                        <td>
                          <button
                            className="button button--quiet"
                            onClick={() =>
                              setReplacement((prev) =>
                                prev.filter((_, j) => j !== i),
                              )
                            }
                            aria-label={`Remove ${line.description}`}
                          >
                            {t('common.remove')}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}

              <dl className="totals">
                <div>
                  <dt>{t('returns.credit')}</dt>
                  <dd className="num">{preview.credit}</dd>
                </div>
                <div className="totals__grand">
                  {/* Which way the money goes, said in words. "Difference:
                      115.00" leaves a cashier guessing who owes whom, and they
                      are about to say it out loud to the customer. */}
                  <dt>
                    {preview.owed === '0.00'
                      ? t('returns.nothingToPay')
                      : preview.customerPays
                        ? t('returns.customerPays')
                        : t('returns.refundToCustomer')}
                  </dt>
                  <dd className="num">{preview.owed}</dd>
                </div>
              </dl>

              {/* Labelled as an estimate, because it is one: the server prices
                  the replacement from the registry and may disagree. */}
              <p className="cart__sku">
                {t('returns.anEstimate')}
              </p>

              <button
                className="button button--primary button--large"
                disabled={!canExchange}
                onClick={() => void exchange()}
              >
                {busy
                  ? t('returns.exchanging')
                  : t('returns.completeExchange')}
              </button>
            </>
          )}
        </>
      )}
    </main>
  );
}

/** Turns a failure into something a cashier can act on. */
function explain(err: unknown, fallback: string, t: Translate): string {
  if (err instanceof Offline) {
    // The honest refusal. Named as a limitation with a reason, not as a
    // generic error, because the cashier needs to know it will work later.
    return t('returns.cannotReach');
  }
  if (err instanceof RequestFailed) {
    if (err.status === 404) return t('returns.notFound');
    if (err.status === 403) return t('returns.notAllowed');
    return err.message;
  }
  return err instanceof Error ? err.message : fallback;
}
