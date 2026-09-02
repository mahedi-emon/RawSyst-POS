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

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { scan } from '../api/pos';
import { handleCounterKey } from '@rawsyst/shared/pos/keys';
import { Offline, RequestFailed } from '@rawsyst/shared/api/client';
import { useAuth } from '@rawsyst/shared/auth/session';
import { money, shortDate } from '@rawsyst/shared/ui/format';
import { useLocale, useT } from '@rawsyst/shared/i18n/locale';
import { currentShift, type ShiftSession } from '@rawsyst/shared/api/shift';
import { openedAtTime } from './shift';
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
} from '@rawsyst/shared/pos/cart';
import type { OfflineSalePayload } from '../offline/queue';
import type { HeldCart } from '@rawsyst/shared/pos/held';
import { CustomerPicker } from './CustomerPicker';
import {
  accountTender,
  creditVerdict,
  mayOfferAccount,
  type CounterCustomer,
} from './customer';
import { major, minor } from '@rawsyst/shared/receivables/receivables';
import { buildReceipt, renderReceipt, type Receipt } from '@rawsyst/shared/pos/receipt';

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
  const t = useT();
  const { locale } = useLocale();
  const { can, me, client } = useAuth();
  const terminal = useTerminal();

  const [lines, setLines] = useState<CartLine[]>([]);
  const [tenders, setTenders] = useState<CartTender[]>([]);
  const [scanInput, setScanInput] = useState('');
  const scanBox = useRef<HTMLInputElement>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [parked, setParked] = useState<HeldCart[]>([]);
  const [receipt, setReceipt] = useState<Receipt | null>(null);

  // Who the sale is for. Absent for the overwhelming majority of sales — a
  // shop does not ask a name to sell a bottle of water — and REQUIRED the
  // moment any part of it goes on account.
  const [customer, setCustomer] = useState<CounterCustomer | null>(null);
  const [picking, setPicking] = useState(false);

  // Whether this till has a drawer somebody has counted into.
  //
  // Asked because the alternative is worse than a round trip. Every sale goes
  // into the local queue first and reaches the server later, and the server
  // refuses a sale from a till with no open session — so without this a
  // cashier takes the money, hands over the goods, and the sale surfaces as a
  // failed queue item at the end of the day with the customer long gone.
  //
  // `undefined` means not yet known, and is treated as permissive: a till that
  // cannot reach the server must keep trading, which is the whole offline
  // design. The check is a courtesy to catch the ordinary case — nobody opened
  // the till this morning — not a second gate.
  const [shift, setShift] = useState<ShiftSession | null | undefined>(undefined);

  const checkShift = useCallback(() => {
    currentShift(client)
      .then(setShift)
      .catch(() => setShift(undefined));
  }, [client]);

  useEffect(() => {
    checkShift();
  }, [checkShift]);

  // Re-asked when the till regains the network, because "no session" may have
  // been answered while it was offline, or a supervisor may have closed the
  // shift from elsewhere.
  useEffect(() => {
    if (terminal.network.reachable) checkShift();
  }, [terminal.network.reachable, checkShift]);

  const totals = useMemo(() => totalCart(lines, DISPLAY_RATE), [lines]);
  const owed = outstanding(totals.totalInclusive, tenders);

  // What the shop keeps its books in — SAR, BDT, USD.
  //
  // The counter used to print "Total 0.00" with no code at all, which is
  // legible only to somebody who already knows which country the till is in.
  // This product is sold into three, and a shop that switched its books to
  // taka would have seen no difference anywhere on this screen.
  //
  // Taken from the cached stationery, so it survives the connection going: it
  // is the same fact the receipt has to carry and is cached for the same
  // reason. Empty on a till that has never been online, which shows the bare
  // amount rather than inventing a currency.
  const currency = terminal.stationery?.baseCurrency || undefined;
  // `shift === null` is the one refusal here: the server has answered, and the
  // answer was that this till has no open session. `undefined` — unasked or
  // unreachable — does not block, because an offline till must keep trading.
  const tillIsOpen = shift !== null;
  const canFinish =
    lines.length > 0 && settled(totals.totalInclusive, tenders) && !busy && tillIsOpen;

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
  const verdict = creditVerdict(customer, major(onAccount), t);

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
        setNotice(t('till.barcodeUnknownOffline', { barcode }));
      } else if (err instanceof RequestFailed && err.status === 404) {
        setNotice(t('till.barcodeUnknown', { barcode }));
      } else {
        setNotice(err instanceof Error ? err.message : t('till.scanFailed'));
      }
    }
  }

  function addVariant(v: CachedVariant) {
    if (!v.isActive) {
      // Distinct from "not found". A withdrawn item is one the cashier is
      // holding and cannot sell; an unknown barcode is one they have probably
      // scanned by mistake.
      setNotice(t('till.withdrawn'));
      return;
    }
    // What the cached stock figure has to say, if anything.
    //
    // A WARNING and never a refusal. The main stock ledger is the single
    // source of truth and this is a copy of part of it that goes stale the
    // moment another till sells something — design 03 is explicit that an
    // offline terminal cannot prevent overselling and that the product chooses
    // accurate detection over false confidence. A stale figure turning a real
    // customer away at the counter is exactly that false confidence.
    //
    // So the line is added either way, and the notice is set first so it is
    // already on screen when it appears.
    const short = terminal.stock?.shortfall(v.id, '1');
    if (short) {
      setNotice(
        t(
          short.kind === 'none-left'
            ? 'till.stockNoneLeft'
            : short.kind === 'not-enough'
              ? 'till.stockNotEnough'
              : 'till.stockBelowReorder',
          { count: short.onHand, when: shortDate(short.asOf, locale) },
        ),
      );
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
      await held.hold(lines, tenders, '', totals.totalInclusive, t);
      setParked(await held.list());
      setLines([]);
      setTenders([]);
      setNotice(t('till.held'));
    } catch (err) {
      setNotice(
        err instanceof Error ? err.message : t('till.holdFailed'),
      );
    }
  }

  async function resumeCart(id: string) {
    if (!held) return;
    if (lines.length > 0) {
      // Refused rather than merged. Two customers' items in one cart is a
      // mistake nobody notices until the total is already wrong.
      setNotice(t('till.finishFirst'));
      return;
    }

    const cart = await held.resume(id);
    setParked(await held.list());
    if (!cart) {
      setNotice(t('till.heldGone'));
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
          // The shop's own stationery (I2), held on the terminal so it prints
          // with no network. A till that has never been online falls back to
          // the RawSyst default rather than to nothing.
          //
          // Knowing the seller's name and VAT number does not make this a
          // simplified tax invoice — that waits on the P1 signing gate — and
          // it still prints "this is not a tax invoice" in the meantime.
          header: terminal.stationery,
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
          ? t('till.saleCompleteOnAccount', {
              amount: major(onAccount),
              customer: customer.name,
            })
          : t('till.saleComplete'),
      );
    } catch (err) {
      setNotice(
        err instanceof Error
          ? err.message
          : t('till.saleNotRecorded'),
      );
    } finally {
      setBusy(false);
    }
  }

  // The counter's keyboard.
  //
  // UI spec §1 names seven shortcuts and says the counter is "fully operable
  // with no mouse". None of them existed. The same section makes "no field
  // focus needed to scan" a non-negotiable and writes the reason beside it:
  // requiring a click loses sales at a busy counter. `autoFocus` alone does not
  // deliver that — it focuses the box once, and the first tender button a
  // cashier presses takes focus away for good.
  //
  // Registered on the document with `capture: true` so a shortcut is not
  // swallowed by whatever has focus, and rebuilt whenever the actions it calls
  // change, because they close over the cart.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const { handled } = handleCounterKey(e, {
        focusScan: () => scanBox.current?.focus(),
        chooseCustomer: () => setPicking(true),
        hold: () => {
          if (lines.length > 0 && terminal.held) void holdCart();
        },
        resume: () => {
          // The most recently parked cart, which is the one a cashier who
          // just held one wants back. Nothing to do when there is none.
          const last = parked[parked.length - 1];
          if (last) void resumeCart(last.id);
        },
        pay: () => {
          if (canFinish) void finishSale();
        },
        cancel: () => {
          // Back out of what is open before clearing anything. Escape on a
          // dialog closes the dialog; Escape on the counter clears the cart,
          // which is destructive and must not also be the way a dialog is
          // dismissed.
          if (receipt) {
            setReceipt(null);
            return;
          }
          if (picking) {
            setPicking(false);
            return;
          }
          if (notice) {
            setNotice(null);
            return;
          }
          setLines([]);
          setTenders([]);
          setCustomer(null);
        },
        scanned: (char) => {
          scanBox.current?.focus();
          setScanInput((held) => held + char);
        },
      });
      if (handled) e.preventDefault();
    }

    document.addEventListener('keydown', onKey, { capture: true });
    return () => document.removeEventListener('keydown', onKey, { capture: true });
  }, [
    lines.length,
    parked,
    canFinish,
    receipt,
    picking,
    notice,
    terminal.held,
  ]);

  return (
    <main className="counter">
      <section className="counter__cart" aria-label={t('pos.currentSale')}>
        <form
          className="scan"
          onSubmit={(e) => {
            e.preventDefault();
            void addByScan(scanInput);
          }}
        >
          <input
            ref={scanBox}
            className="scan__input"
            // A barcode scanner types and presses Enter, so this holds focus
            // for the whole sale. A cashier should never have to click the box
            // between items — and when something else takes focus anyway, the
            // document-level capture in `keys.ts` puts the scan back here.
            autoFocus
            placeholder={t('pos.scanPlaceholder')}
            value={scanInput}
            onChange={(e) => setScanInput(e.target.value)}
            aria-label={t('pos.scanBarcode')}
          />
        </form>

        {/* Who the sale is for. Always present, so a cashier never has to
            hunt for it mid-sale, and never insistent — the overwhelming
            majority of sales have no customer and the row stays quiet
            about it. */}
        <div className={`who${customer ? ' who--chosen' : ''}`}>
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
                {t('common.change')}
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
                {t('common.remove')}
              </button>
            </>
          ) : (
            <button
              className="button button--quiet who__choose"
              onClick={() => setPicking(true)}
            >
              {t('till.addCustomer')}
            </button>
          )}
        </div>

        {/* The drawer this sale would go into. Blueprint C8 ties every sale to
            a counted session, so a till with none cannot take money — and
            saying so before the cart is full is the difference between a
            cashier opening the till and a customer standing at the counter
            while somebody works out what went wrong. */}
        {shift === null ? (
          <p className="counter__shift counter__shift--none" role="alert">
            <strong>{t('pos.tillNotOpen')}</strong> {t('pos.tillNotOpenWhy')}
          </p>
        ) : (
          shift && (
            <p className="counter__shift" role="status">
              {t('pos.shiftOpenSince', {
                no: shift.session_no,
                time: openedAtTime(shift.opened_at),
              })}
            </p>
          )
        )}

        {notice && (
          <p className="counter__notice" role="status" aria-live="polite">
            {notice}
          </p>
        )}

        {/* Parked carts. Shown only when there are any and the counter is
            clear, so the list never competes with a sale in progress. */}
        {parked.length > 0 && lines.length === 0 && (
          <section className="held" aria-label={t('pos.heldSales')}>
            <h2 className="held__title">{t('pos.heldSales')}</h2>
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
          <p className="counter__empty">{t('pos.scanToBegin')}</p>
        ) : (
          <table className="cart">
            <thead>
              <tr>
                <th scope="col">{t('pos.item')}</th>
                <th scope="col">{t('pos.qty')}</th>
                <th scope="col" className="num">{t('pos.price')}</th>
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
                      aria-label={t('pos.quantityOf', { item: l.description })}
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
                      aria-label={t('pos.removeItem', { item: l.description })}
                    >
                      {t('common.remove')}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <aside className="counter__totals" aria-label={t('pos.totalsAndPayment')}>
        <QueueStatus terminal={terminal} />

        <dl className="totals">
          <div>
            <dt>{t('pos.subtotal')}</dt>
            <dd className="num">{money(totals.subtotalNet, { currency })}</dd>
          </div>
          <div>
            <dt>{t('pos.vat')}</dt>
            <dd className="num">{money(totals.taxTotal, { currency })}</dd>
          </div>
          <div className="totals__grand">
            <dt>{t('pos.total')}</dt>
            <dd className="num">
              {money(totals.totalInclusive, { currency })}
            </dd>
          </div>
          {tenders.length > 0 && (
            <div className={owed === '0.00' ? '' : 'totals__owed'}>
              <dt>{t('pos.outstanding')}</dt>
              <dd className="num">{money(owed, { currency })}</dd>
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
              {method === 'cash' ? t('tender.cash') : t('tender.mada')}
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
                  ? t('till.atLimit')
                  : undefined
              }
            >
              {t('tender.onAccount')}
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
          <ul className="taken" aria-label={t('pos.paymentsTaken')}>
            {tenders.map((taken, i) => (
              <li key={`${taken.method}-${i}`} className="taken__row">
                <span>{tenderLabel(taken.method)}</span>
                <span className="num">{taken.amount}</span>
                <button
                  className="button button--quiet"
                  onClick={() =>
                    setTenders((prev) => prev.filter((_, j) => j !== i))
                  }
                  aria-label={t('pos.removePayment', { method: tenderLabel(taken.method) })}
                >
                  {t('common.remove')}
                </button>
              </li>
            ))}
          </ul>
        )}

        {/* Shown only to a cashier who holds the permission. The server
            refuses it regardless of what this screen offered. */}
        {can('sales.discount') && (
          <button className="button button--quiet" disabled={lines.length === 0}>
            {t('common.discount')}
          </button>
        )}

        <button
          className="button button--primary button--large"
          disabled={!canFinish}
          onClick={() => void finishSale()}
          title={
            !tillIsOpen
              ? t('till.noSession')
              : canFinish
                ? undefined
                : t('till.mustSettleExactly')
          }
        >
          {busy ? t('till.recording') : t('till.finishSale')}
        </button>

        {/* Holding is not finishing. No invoice, no ICV, no stock, no journal
            entry — a note on this terminal about what somebody was buying. */}
        <button
          className="button button--quiet"
          disabled={lines.length === 0 || !terminal.held}
          onClick={() => void holdCart()}
        >
          {t('till.holdSale')}
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
          {t('till.clear')}
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
            <pre className="receipt__paper">{renderReceipt(receipt, undefined, t)}</pre>
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
              {t('common.done')}
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
