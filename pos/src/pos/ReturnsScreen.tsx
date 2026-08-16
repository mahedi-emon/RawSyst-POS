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

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import {
  fetchReturnable,
  overReturned,
  refundTotal,
  submitReturn,
  type ReturnableLine,
  type ReturnSelection,
} from './returns';

export function ReturnsScreen() {
  const { client } = useAuth();

  const [invoiceId, setInvoiceId] = useState('');
  const [lines, setLines] = useState<ReturnableLine[] | null>(null);
  const [qty, setQty] = useState<Record<string, string>>({});
  const [method, setMethod] = useState('cash');
  const [reason, setReason] = useState('');
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<string | null>(null);

  const selection: ReturnSelection[] = Object.entries(qty)
    .filter(([, q]) => q.trim() !== '' && Number(q) > 0)
    .map(([lineId, q]) => ({ lineId, qty: q }));

  const invalid = lines ? overReturned(lines, selection) : [];
  const total = lines ? refundTotal(lines, selection) : '0.00';
  const canRefund =
    lines !== null && selection.length > 0 && invalid.length === 0 && !busy;

  async function lookUp() {
    const id = invoiceId.trim();
    if (!id) return;
    setNotice(null);
    setDone(null);
    setLines(null);
    setQty({});
    setBusy(true);
    try {
      const found = await fetchReturnable(client, id);
      if (found.length === 0) {
        setNotice('That sale has no lines left to return.');
        return;
      }
      setLines(found);
    } catch (err) {
      setNotice(explain(err, 'That sale could not be looked up.'));
    } finally {
      setBusy(false);
    }
  }

  async function refund() {
    if (!canRefund || !lines) return;
    setBusy(true);
    setNotice(null);
    try {
      const result = await submitReturn(client, {
        // Generated BEFORE the call, which is what makes a retry safe: a
        // network failure after the server committed would otherwise have the
        // cashier press refund again and give the money back twice.
        creditNoteUuid: crypto.randomUUID(),
        originalInvoiceId: invoiceId.trim(),
        reason: reason.trim(),
        lines: selection,
        refunds: [{ method, amount: total }],
      });
      setDone(result.human_number || result.credit_note_id);
      setLines(null);
      setQty({});
    } catch (err) {
      setNotice(explain(err, 'That refund could not be completed.'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="returns">
      <h1 className="returns__title">Return goods</h1>

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
          placeholder="Scan the receipt or type the sale reference"
          value={invoiceId}
          onChange={(e) => setInvoiceId(e.target.value)}
          aria-label="Sale reference"
        />
      </form>

      {notice && (
        <p className="queue queue--bad" role="status" aria-live="polite">
          {notice}
        </p>
      )}

      {done && (
        <p className="queue queue--ok" role="status" aria-live="polite">
          Refunded. Credit note {done}.
        </p>
      )}

      {lines && (
        <>
          <table className="cart">
            <thead>
              <tr>
                <th scope="col">Item</th>
                <th scope="col" className="num">Left</th>
                <th scope="col">Return</th>
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
                          {line.qty_returned} already returned
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
              More has been entered than that sale can give back.
            </p>
          )}

          <label className="returns__reason">
            Reason
            <input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Why is it coming back?"
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

          <dl className="totals">
            <div className="totals__grand">
              <dt>Refund</dt>
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
            {busy ? 'Refunding…' : `Refund ${total}`}
          </button>
        </>
      )}
    </main>
  );
}

/** Turns a failure into something a cashier can act on. */
function explain(err: unknown, fallback: string): string {
  if (err instanceof Offline) {
    // The honest refusal. Named as a limitation with a reason, not as a
    // generic error, because the cashier needs to know it will work later.
    return (
      'This till cannot reach the server, so a return cannot be processed. ' +
      'Whether some of this sale has already been refunded is not known here, ' +
      'and refunding it twice cannot be undone.'
    );
  }
  if (err instanceof RequestFailed) {
    if (err.status === 404) return 'No sale was found with that reference.';
    if (err.status === 403) {
      return 'This login is not allowed to refund sales.';
    }
    return err.message;
  }
  return err instanceof Error ? err.message : fallback;
}
