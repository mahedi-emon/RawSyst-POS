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

import { useEffect, useMemo, useState } from 'react';

import { scan } from '../api/pos';
import { Offline, RequestFailed } from '@rawsyst/shared/api/client';
import { useAuth } from '@rawsyst/shared/auth/session';
import { useTerminal } from '../offline/useTerminal';
import { describeVariant, type CachedVariant } from '../offline/catalogue';
import { QueueStatus } from './QueueStatus';
import {
  emptyLine,
  outstanding,
  settled,
  totalCart,
  type CartLine,
  type CartTender,
} from './cart';
import type { OfflineSalePayload } from '../offline/queue';
import type { HeldCart } from './held';
import { CustomerPicker } from './CustomerPicker';
import {
  accountTender,
  creditVerdict,
  mayOfferAccount,
  type CounterCustomer,
} from './customer';
import { major, minor } from '@rawsyst/shared/receivables/receivables';
import { buildReceipt, renderReceipt, type Receipt } from './receipt';

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
  const [parked, setParked] = useState<HeldCart[]>([]);
  const [receipt, setReceipt] = useState<Receipt | null>(null);

  // Who the sale is for. Absent for the overwhelming majority of sales — a
  // shop does not ask a name to sell a bottle of water — and REQUIRED the
  // moment any part of it goes on account.
  const [customer, setCustomer] = useState<CounterCustomer | null>(null);
  const [picking, setPicking] = useState(false);

  const totals = useMemo(() => totalCart(lines, DISPLAY_RATE), [lines]);
  const owed = outstanding(totals.totalInclusive, tenders);
  const canFinish =
    lines.length > 0 && settled(totals.totalInclusive, tenders) && !busy;

  // What this sale would put on the customer's account, across however many
  // tenders. Only this part draws down the limit: a sale half in cash and
  // half on account owes the second half, and checking the total would
  // refuse sales that are perfectly affordable.
  //
  // BigInt minor units, never `Number(x) * 100` — float64 cannot hold 0.15,
  // and a credit check that drifted would eventually refuse a sale that
  // exactly reaches the limit, the commonest case in a shop with round
  // limits.
  const onAccount = useMemo(
    () =>
      tenders
        .filter((t) => t.method === 'customer_due')
        .reduce((sum, t) => sum + minor(t.amount), 0n),
    [tenders],
  );
  const accountOffer = accountTender(customer, owed, major(onAccount));
  // Judged against the whole on-account portion, so a second tender cannot
  // slip past a check that only looked at the first.
  const verdict = creditVerdict(customer, major(onAccount));

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
  // The parked-cart list, refreshed whenever it could have changed.
  const held = terminal.held;
  useEffect(() => {
    if (held) void held.list().then(setParked);
  }, [held]);

  async function holdCart() {
    if (!held || lines.length === 0) return;
    try {
      await held.hold(lines, tenders, '', totals.totalInclusive);
      setParked(await held.list());
      setLines([]);
      setTenders([]);
      setNotice('Sale held.');
    } catch (err) {
      setNotice(
        err instanceof Error ? err.message : 'That sale could not be held.',
      );
    }
  }

  async function resumeCart(id: string) {
    if (!held) return;
    if (lines.length > 0) {
      // Refused rather than merged. Two customers' items in one cart is a
      // mistake nobody notices until the total is already wrong.
      setNotice('Finish or hold the current sale before resuming another.');
      return;
    }

    const cart = await held.resume(id);
    setParked(await held.list());
    if (!cart) {
      setNotice('That held sale is no longer there.');
      return;
    }
    setLines(cart.lines);
    setTenders(cart.tenders);
    setNotice(null);
  }

  async function finishSale() {
    if (!canFinish || !me) return;

    // Refused HERE so the goods do not leave before anybody finds out. The
    // server checks again under a row lock on the customer and is the
    // authority — 11-pos-and-sales.md §5 says a breach is refused, not
    // warned about — but a cashier who only discovered it on sync would
    // already have handed the bag over.
    if (onAccount > 0n && verdict.kind !== 'ok') {
      setNotice(verdict.message);
      return;
    }

    setBusy(true);
    setNotice(null);
    try {
      const payload = buildPayload(lines, tenders, me.user_id, customer);
      await terminal.record(payload);
      // The sale is durable at this point. Whether it has reached the server
      // is a separate question the queue answers on its own.
      //
      // The receipt is built from what the TERMINAL recorded, not from a
      // server response, because there may not have been one — a sale finished
      // offline must still put paper in the customer's hand, at the counter,
      // now.
      setReceipt(
        buildReceipt({
          // The seller's trading name and VAT number are not yet known to the
          // terminal; nothing here invents them. See the gap noted in the POS
          // README — a receipt cannot become a simplified tax invoice until
          // both that and the P1 signing gate are resolved, and it prints
          // "this is not a tax invoice" in the meantime.
          header: { storeName: 'RawSyst', vatNumber: '', addressLines: [] },
          reference: payload.invoice_uuid.slice(0, 8),
          issuedAt: payload.issued_at,
          cashier: me.user_id,
          lines,
          totals,
          tenders,
          provisional: true,
        }),
      );
      setLines([]);
      setTenders([]);
      setCustomer(null);
      setNotice(
        onAccount > 0n && customer
          ? `Sale complete. ${major(onAccount)} added to the account of ${customer.name}.`
          : 'Sale complete.',
      );
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

        {/* Who the sale is for. Always present, so a cashier never has to
            hunt for it mid-sale, and never insistent — the overwhelming
            majority of sales have no customer and the row stays quiet
            about it. */}
        <div className="who">
          {customer ? (
            <>
              <span className="who__name">{customer.name}</span>
              <span className="who__meta">
                {customer.creditLimit ? (
                  <>
                    <span className="num">{customer.available || '0.00'}</span>
                    {' available'}
                    {customer.stale && ' (as at last sync)'}
                  </>
                ) : (
                  'Pays at the till'
                )}
              </span>
              <button
                className="button button--quiet"
                onClick={() => setPicking(true)}
              >
                Change
              </button>
              <button
                className="button button--quiet"
                onClick={() => {
                  setCustomer(null);
                  // Any on-account tender belonged to the customer who has
                  // just been removed. Leaving it would attach their debt
                  // to whoever is chosen next, or to nobody at all.
                  setTenders((prev) =>
                    prev.filter((t) => t.method !== 'customer_due'),
                  );
                }}
              >
                Remove
              </button>
            </>
          ) : (
            <button
              className="button button--quiet who__choose"
              onClick={() => setPicking(true)}
            >
              Add a customer
            </button>
          )}
        </div>

        {notice && (
          <p className="counter__notice" role="status" aria-live="polite">
            {notice}
          </p>
        )}

        {/* Parked carts. Shown only when there are any and the counter is
            clear, so the list never competes with a sale in progress. */}
        {parked.length > 0 && lines.length === 0 && (
          <section className="held" aria-label="Held sales">
            <h2 className="held__title">Held sales</h2>
            <ul className="held__list">
              {parked.map((cart) => (
                <li key={cart.id}>
                  <button
                    className="button button--quiet"
                    onClick={() => void resumeCart(cart.id)}
                  >
                    {cart.label || `${cart.itemCount} item${cart.itemCount === 1 ? '' : 's'}`}
                    {' — '}
                    <span className="num">{cart.total}</span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
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

          {/* Offered only to a customer who actually has an account,
              because a button that always refuses teaches a cashier to
              distrust the rest of them. It fills in whichever is SMALLER —
              what is still owed on the sale, or what is left on the account
              — so a part payment on account is one press rather than a
              calculation. */}
          {mayOfferAccount(customer) && (
            <button
              className="button button--large tenders__account"
              disabled={
                lines.length === 0 || owed === '0.00' || accountOffer === '0.00'
              }
              onClick={() =>
                setTenders((prev) => [
                  ...prev,
                  { method: 'customer_due', amount: accountOffer },
                ])
              }
              title={
                accountOffer === '0.00'
                  ? 'Nothing further can go on this account.'
                  : undefined
              }
            >
              On account
              <span className="tenders__hint num">{accountOffer}</span>
            </button>
          )}
        </div>

        {/* The credit position, stated whenever any part of the sale is
            going on account. A cashier must be able to read the reason out
            to the customer rather than saying "the computer says no". */}
        {onAccount > 0n && (
          <p
            className={`credit${verdict.kind === 'ok' ? '' : ' credit--bad'}`}
            role="status"
            aria-live="polite"
          >
            {verdict.message}
          </p>
        )}

        {/* What has been taken so far, so a mistake can be undone before
            the sale is finished rather than reversed after it. */}
        {tenders.length > 0 && (
          <ul className="taken" aria-label="Payments taken">
            {tenders.map((t, i) => (
              <li key={`${t.method}-${i}`} className="taken__row">
                <span>{tenderLabel(t.method)}</span>
                <span className="num">{t.amount}</span>
                <button
                  className="button button--quiet"
                  onClick={() =>
                    setTenders((prev) => prev.filter((_, j) => j !== i))
                  }
                  aria-label={`Remove the ${tenderLabel(t.method)} payment`}
                >
                  Remove
                </button>
              </li>
            ))}
          </ul>
        )}

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

        {/* Holding is not finishing. No invoice, no ICV, no stock, no journal
            entry — a note on this terminal about what somebody was buying. */}
        <button
          className="button button--quiet"
          disabled={lines.length === 0 || !terminal.held}
          onClick={() => void holdCart()}
        >
          Hold sale
        </button>

        <button
          className="button button--quiet"
          onClick={() => {
            setLines([]);
            setTenders([]);
            setCustomer(null);
            setNotice(null);
          }}
        >
          Clear
        </button>

        {picking && (
          <CustomerPicker
            customers={terminal.customers}
            onChoose={(chosen) => {
              setCustomer(chosen);
              setPicking(false);
              setNotice(null);
            }}
            onClose={() => setPicking(false)}
          />
        )}

        {receipt && (
          <section className="receipt" aria-label="Receipt">
            {/* Monospaced and pre-formatted: this is exactly the text the
                thermal printer receives, so what the cashier reads on screen
                and what the customer is handed cannot drift apart. */}
            <pre className="receipt__paper">{renderReceipt(receipt)}</pre>
            <button
              className="button button--quiet"
              onClick={() => window.print()}
            >
              Print
            </button>
            <button
              className="button button--quiet"
              onClick={() => setReceipt(null)}
            >
              Done
            </button>
          </section>
        )}
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
  customer: CounterCustomer | null,
): OfflineSalePayload {
  return {
    invoice_uuid: crypto.randomUUID(),
    doc_type: 'simplified',
    issued_at: new Date().toISOString(),
    cashier_id: cashierId,
    // Sent only when there is one. An empty string would be a malformed
    // identifier rather than an absent customer, and the server rightly
    // refuses to guess which was meant.
    ...(customer ? { customer_id: customer.id } : {}),
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

/** A tender method in the words a cashier uses for it. */
function tenderLabel(method: string): string {
  switch (method) {
    case 'cash':
      return 'Cash';
    case 'mada':
      return 'Mada';
    case 'customer_due':
      return 'On account';
    default:
      return method.replace(/_/g, ' ');
  }
}
