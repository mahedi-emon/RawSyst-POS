// The billing counter.
//
// The one screen a shop cannot trade without, so it is built for the counter
// rather than for a demo: the scan box holds focus because a barcode scanner
// is a keyboard, the cart is legible across a metre of floor, and the total is
// the largest thing on screen because it is what the customer leans over to
// read.
//
// # Finishing a sale never waits on the network
//
// Finish writes the sale to local storage and returns. The push happens after,
// and its success or failure changes nothing about whether the sale happened.
// That ordering is the offline-first design: a cashier presses Finish, the
// sale is durable, the customer leaves.
//
// # Permissions shape it, but do not secure it
//
// A cashier without `sales.discount` is not shown the discount control. That
// is a courtesy — the server refuses whatever the screen offered, and QA gate
// M7 proves it. Blueprint A6.2: a hidden button is never real security.

import { useMemo, useState } from 'react';

import { scan } from '../api/pos';
import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import { useTerminal } from '../offline/useTerminal';
import { describeVariant, type CachedVariant } from '../offline/catalogue';
import { QueueStatus } from '../ui/QueueStatus';
import {
  emptyLine,
  outstanding,
  settled,
  totalCart,
  type CartLine,
  type CartTender,
} from './cart';
import type { OfflineSalePayload } from '../offline/queue';

/** The VAT rate shown while ringing up.
 *
 * For DISPLAY only. The server resolves the real rate from the Regulatory Rule
 * Registry at the transaction date and recomputes every figure — a till does
 * not get to be the authority on a legal value, and a terminal running an old
 * build must not quietly charge last year's rate. Where the two differ, the
 * server is right.
 */
const DISPLAY_RATE = '0.15';

export function PosCounter() {
  const { can, me, client } = useAuth();
  const terminal = useTerminal();

  const [lines, setLines] = useState<CartLine[]>([]);
  const [tenders, setTenders] = useState<CartTender[]>([]);
  const [scanInput, setScanInput] = useState('');
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const totals = useMemo(() => totalCart(lines, DISPLAY_RATE), [lines]);
  const owed = outstanding(totals.totalInclusive, tenders);
  const canFinish =
    lines.length > 0 && settled(totals.totalInclusive, tenders) && !busy;

  // The scan path. Local cache FIRST, network only as a fallback.
  //
  // Not "local if offline". Asking the server first would make every scan wait
  // out a timeout whenever the connection is merely slow, which is the common
  // case in a shop with poor signal and far worse for a queue of customers
  // than a price that is a day old. The cache is a cache: the server reprices
  // every line on replay, so a stale row costs a corrected receipt, never a
  // wrong invoice or a wrong journal.
  //
  // The network is still tried when the cache misses, because that covers a
  // product added since the last catalogue pull.
  async function addByScan(code: string) {
    const barcode = code.trim();
    if (!barcode) return;
    setNotice(null);
    setScanInput('');

    const cached = await terminal.catalogue?.lookup(barcode);
    if (cached) {
      addVariant(cached);
      return;
    }

    try {
      const variant = await scan(client, barcode);
      addVariant({
        id: variant.id,
        productId: '',
        sku: variant.sku,
        barcode,
        name: '',
        nameAr: '',
        attributes: variant.attributes,
        price: variant.price,
        taxTreatment: 'standard',
        isActive: variant.is_active,
        updatedAt: '',
      });
    } catch (err) {
      if (err instanceof Offline) {
        // Offline AND not cached. Says which, because the two have different
        // answers: one waits for the network, the other is a product this
        // terminal has genuinely never been told about.
        setNotice(
          `${barcode} is not in this terminal's catalogue, and the server ` +
            'cannot be reached to look it up. Sales already in the cart are safe.',
        );
      } else if (err instanceof RequestFailed && err.status === 404) {
        setNotice(`Nothing in this catalogue carries the barcode ${barcode}.`);
      } else {
        setNotice(err instanceof Error ? err.message : 'That scan did not work.');
      }
    }
  }

  function addVariant(v: CachedVariant) {
    if (!v.isActive) {
      // Distinct from "not found". A withdrawn item is one the cashier is
      // holding and cannot sell; an unknown barcode is one they have probably
      // scanned by mistake.
      setNotice('That item has been withdrawn from sale.');
      return;
    }
    setLines((prev) => [
      ...prev,
      emptyLine({
        variantId: v.id,
        sku: v.sku,
        description: describeVariant(v),
        unitPrice: v.price,
        // The server validates the treatment against the country's registry
        // list on every sale regardless of what the cache says.
        taxTreatment: v.taxTreatment || 'standard',
      }),
    ]);
  }
  async function finishSale() {
    if (!canFinish || !me) return;
    setBusy(true);
    setNotice(null);
    try {
      await terminal.record(buildPayload(lines, tenders, me.user_id));
      // The sale is durable at this point. Whether it has reached the server
      // is a separate question the queue answers on its own.
      setLines([]);
      setTenders([]);
      setNotice('Sale complete.');
    } catch (err) {
      setNotice(
        err instanceof Error
          ? err.message
          : 'That sale could not be recorded on this terminal.',
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="counter">
      <section className="counter__cart" aria-label="Current sale">
        <form
          className="scan"
          onSubmit={(e) => {
            e.preventDefault();
            void addByScan(scanInput);
          }}
        >
          <input
            className="scan__input"
            // A barcode scanner types and presses Enter, so this holds focus
            // for the whole sale. A cashier should never have to click the box
            // between items.
            autoFocus
            placeholder="Scan or type a barcode"
            value={scanInput}
            onChange={(e) => setScanInput(e.target.value)}
            aria-label="Scan a barcode"
          />
        </form>

        {notice && (
          <p className="counter__notice" role="status" aria-live="polite">
            {notice}
          </p>
        )}

        {lines.length === 0 ? (
          <p className="counter__empty">Scan an item to begin.</p>
        ) : (
          <table className="cart">
            <thead>
              <tr>
                <th scope="col">Item</th>
                <th scope="col">Qty</th>
                <th scope="col" className="num">Price</th>
                <th scope="col" />
              </tr>
            </thead>
            <tbody>
              {lines.map((l, i) => (
                <tr key={`${l.variantId}-${i}`}>
                  <td>
                    <span className="cart__name">{l.description}</span>
                    <span className="cart__sku">{l.sku}</span>
                  </td>
                  <td>
                    <input
                      className="cart__qty"
                      inputMode="decimal"
                      value={l.qty}
                      onChange={(e) =>
                        setLines((prev) =>
                          prev.map((line, j) =>
                            j === i ? { ...line, qty: e.target.value } : line,
                          ),
                        )
                      }
                      aria-label={`Quantity of ${l.description}`}
                    />
                  </td>
                  {/* The string as it came from the server. Never parsed into
                      a number on the way to the screen. */}
                  <td className="num">{l.unitPrice}</td>
                  <td>
                    <button
                      className="button button--quiet"
                      onClick={() =>
                        setLines((prev) => prev.filter((_, j) => j !== i))
                      }
                      aria-label={`Remove ${l.description}`}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <aside className="counter__totals" aria-label="Totals and payment">
        <QueueStatus terminal={terminal} />

        <dl className="totals">
          <div>
            <dt>Subtotal</dt>
            <dd className="num">{totals.subtotalNet}</dd>
          </div>
          <div>
            <dt>VAT</dt>
            <dd className="num">{totals.taxTotal}</dd>
          </div>
          <div className="totals__grand">
            <dt>Total</dt>
            <dd className="num">{totals.totalInclusive}</dd>
          </div>
          {tenders.length > 0 && (
            <div className={owed === '0.00' ? '' : 'totals__owed'}>
              <dt>Outstanding</dt>
              <dd className="num">{owed}</dd>
            </div>
          )}
        </dl>

        <div className="tenders">
          {(['cash', 'mada'] as const).map((method) => (
            <button
              key={method}
              className="button button--large"
              disabled={lines.length === 0 || owed === '0.00'}
              onClick={() =>
                setTenders((prev) => [...prev, { method, amount: owed }])
              }
            >
              {method === 'cash' ? 'Cash' : 'Mada'}
            </button>
          ))}
        </div>

        {/* Shown only to a cashier who holds the permission. The server
            refuses it regardless of what this screen offered. */}
        {can('sales.discount') && (
          <button className="button button--quiet" disabled={lines.length === 0}>
            Discount
          </button>
        )}

        <button
          className="button button--primary button--large"
          disabled={!canFinish}
          onClick={() => void finishSale()}
          title={
            canFinish ? undefined : 'The payments must settle the sale exactly.'
          }
        >
          {busy ? 'Recording…' : 'Finish sale'}
        </button>

        <button
          className="button button--quiet"
          onClick={() => {
            setLines([]);
            setTenders([]);
            setNotice(null);
          }}
        >
          Clear
        </button>
      </aside>
    </main>
  );
}


/** Builds exactly what POST /api/v1/sync/push expects.
 *
 * Note what is absent: no company, no store, no warehouse, no VAT rate, no
 * currency, no totals. The server resolves every one of those from the device
 * and the registry. The terminal states only what it alone knows — which
 * items, at what prices, paid how, and when.
 */
function buildPayload(
  lines: CartLine[],
  tenders: CartTender[],
  cashierId: string,
): OfflineSalePayload {
  return {
    invoice_uuid: crypto.randomUUID(),
    doc_type: 'simplified',
    issued_at: new Date().toISOString(),
    cashier_id: cashierId,
    prices_include_tax: true,
    lines: lines.map((l) => ({
      variant_id: l.variantId,
      description: l.description,
      qty: l.qty,
      unit_price: l.unitPrice,
      line_discount: l.lineDiscount,
      tax_treatment: l.taxTreatment,
    })),
    tenders: tenders.map((t) => ({ method: t.method, amount: t.amount })),
  };
}
