// The billing counter.
//
// The one screen a shop cannot trade without, so it is built for the counter
// rather than for a demo: the search box holds focus because a barcode scanner
// is a keyboard, the cart is legible across a metre of floor, and the total is
// the largest thing on the screen because it is what the customer looks at.
//
// # Permissions shape it, but do not secure it
//
// A cashier without `sales.discount` is not shown the discount control. That
// is a courtesy — the server refuses the discount whatever the screen offered,
// and QA gate M7 proves it. Blueprint A6.2: a hidden button is never treated
// as real security.

import { useMemo, useState } from 'react';

import { useAuth } from '../auth/session';
import {
  emptyLine,
  outstanding,
  settled,
  totalCart,
  type CartLine,
  type CartTender,
} from './cart';

/** The VAT rate shown while ringing up.
 *
 * For DISPLAY only. The server resolves the real rate from the Regulatory Rule
 * Registry at the transaction date and recomputes every figure — a till does
 * not get to be the authority on a legal value, and a terminal running an old
 * build must not quietly charge last year's rate.
 */
const DISPLAY_RATE = '0.15';

export function PosCounter() {
  const { can } = useAuth();

  const [lines, setLines] = useState<CartLine[]>([]);
  const [tenders, setTenders] = useState<CartTender[]>([]);
  const [scan, setScan] = useState('');

  const totals = useMemo(() => totalCart(lines, DISPLAY_RATE), [lines]);
  const owed = outstanding(totals.totalInclusive, tenders);
  const canFinish = lines.length > 0 && settled(totals.totalInclusive, tenders);

  function addByScan(code: string) {
    if (!code.trim()) return;
    // Placeholder until the catalogue lookup is wired: the scan endpoint
    // (GET /api/v1/catalog/scan) already exists server-side.
    setLines((prev) => [
      ...prev,
      emptyLine({
        variantId: code,
        sku: code,
        description: 'Scanned item',
        unitPrice: '115.00',
        taxTreatment: 'standard',
      }),
    ]);
    setScan('');
  }

  function changeQty(index: number, qty: string) {
    setLines((prev) =>
      prev.map((l, i) => (i === index ? { ...l, qty } : l)),
    );
  }

  function removeLine(index: number) {
    setLines((prev) => prev.filter((_, i) => i !== index));
  }

  function clearSale() {
    setLines([]);
    setTenders([]);
  }

  return (
    <main className="counter">
      <section className="counter__cart" aria-label="Current sale">
        <form
          className="scan"
          onSubmit={(e) => {
            e.preventDefault();
            addByScan(scan);
          }}
        >
          <input
            className="scan__input"
            // A barcode scanner types and presses Enter, so this must hold
            // focus for the whole sale. A cashier should never have to click
            // the box between items.
            autoFocus
            placeholder="Scan or type a barcode"
            value={scan}
            onChange={(e) => setScan(e.target.value)}
            aria-label="Scan a barcode"
          />
        </form>

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
                      onChange={(e) => changeQty(i, e.target.value)}
                      aria-label={`Quantity of ${l.description}`}
                    />
                  </td>
                  {/* Rendered as the string it is. Never parsed into a number
                      on the way to the screen. */}
                  <td className="num">{l.unitPrice}</td>
                  <td>
                    <button
                      className="button button--quiet"
                      onClick={() => removeLine(i)}
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
            <div className={Number(owed) === 0 ? '' : 'totals__owed'}>
              <dt>Outstanding</dt>
              <dd className="num">{owed}</dd>
            </div>
          )}
        </dl>

        <div className="tenders">
          <button
            className="button button--large"
            disabled={lines.length === 0}
            onClick={() =>
              setTenders((prev) => [
                ...prev,
                { method: 'cash', amount: owed },
              ])
            }
          >
            Cash
          </button>
          <button
            className="button button--large"
            disabled={lines.length === 0}
            onClick={() =>
              setTenders((prev) => [
                ...prev,
                { method: 'mada', amount: owed },
              ])
            }
          >
            Mada
          </button>
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
          title={
            canFinish ? undefined : 'The payments must settle the sale exactly.'
          }
        >
          Finish sale
        </button>

        <button className="button button--quiet" onClick={clearSale}>
          Clear
        </button>
      </aside>
    </main>
  );
}
