'use client';

// The till.
//
// # It is built for one posture
//
// A cashier stands, with a queue, and works through a scanner and a keyboard.
// So the scan box holds focus permanently and takes it back after every action;
// Enter rings the item; the cart is a plain ruled list rather than cards; and
// the figure the customer is about to be told is the largest thing on screen.
// Nothing here fades in, and nothing animates on scroll.
//
// # The sale id is minted before the first line
//
// `invoice_uuid` is created when the cart starts, not when Pay is pressed. That
// is what makes a retry after a lost response safe: the same sale carries the
// same id and the server recognises it rather than ringing it up twice. Minting
// it at Pay would give a double press two ids and two sales.
//
// # What the till computes and what it does not
//
// It computes the running total, because a customer standing at a counter is
// owed a figure before being asked to pay. It does not compute tax, does not
// allocate the invoice discount across lines and does not decide the invoice
// number -- the server does all three, and the till shows what comes back.

import {
  ArrowLeft,
  Delete,
  Loader2,
  ScanLine,
  Trash2,
  UserRound,
  X,
} from 'lucide-react';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from 'react';

import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useCompany } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import {
  addScanned,
  balanceOf,
  exceedsStock,
  lineNet,
  newSaleId,
  removeLine,
  setQty,
  tenderedTotal,
  totalsFor,
  type CartLine,
} from '@/lib/pos/cart';
import { useCounter } from '@/lib/pos/counter';
import { cn } from '@/lib/utils';

import { CustomerPicker, type PosCustomer } from './customer-picker';

interface ScannedVariant {
  id: string;
  sku: string;
  attributes: Record<string, string>;
  price: string;
  is_active: boolean;
}

interface Tender {
  method: string;
  amount: string;
  reference?: string;
}

interface CompletedSale {
  human_number?: string;
  id?: string;
  total_inclusive?: string;
}

/** The tenders a counter offers. Cash first: it is most of them. */
const TENDER_METHODS = [
  { id: 'cash', label: 'Cash' },
  { id: 'card', label: 'Card' },
  { id: 'bank_transfer', label: 'Transfer' },
  { id: 'customer_due', label: 'On account' },
] as const;

export function Till() {
  const { state, leave } = useCounter();
  const { currency, market } = useCompany();

  const [saleId, setSaleId] = useState(() => newSaleId());
  const [lines, setLines] = useState<CartLine[]>([]);
  const [invoiceDiscount, setInvoiceDiscount] = useState('0');
  const [customer, setCustomer] = useState<PosCustomer | null>(null);
  const [tenders, setTenders] = useState<Tender[]>([]);
  const [barcode, setBarcode] = useState('');
  const [scanning, setScanning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [paying, setPaying] = useState(false);
  const [completed, setCompleted] = useState<CompletedSale | null>(null);
  const [pickingCustomer, setPickingCustomer] = useState(false);

  const scanRef = useRef<HTMLInputElement>(null);

  const totals = useMemo(
    () => totalsFor(lines, invoiceDiscount),
    [lines, invoiceDiscount],
  );
  const balance = balanceOf(totals.net, tenders);
  const owing = !balance.startsWith('-') && balance !== '0.00';
  const change = balance.startsWith('-');

  const money = useCallback(
    (v: string) => formatMoney(v, { currency, market, bare: true }),
    [currency, market],
  );

  /** Returns focus to the scanner. Called after every action, deliberately. */
  const refocus = useCallback(() => {
    // A cashier who has just pressed a tender button and reaches for the
    // scanner must not have to click the box first. This is the single most
    // important interaction detail on this screen.
    requestAnimationFrame(() => scanRef.current?.focus());
  }, []);

  useEffect(() => {
    refocus();
  }, [refocus]);

  async function onScan(e: FormEvent) {
    e.preventDefault();
    const code = barcode.trim();
    if (!code || scanning) return;

    setScanning(true);
    setError(null);
    try {
      const variant = await api.get<ScannedVariant>('/catalog/scan', {
        query: {
          barcode: code,
          // Sent so a wholesale customer is charged the wholesale price. It is
          // the same id that will go on the sale.
          customer_id: customer?.id,
        },
      });

      if (!variant.is_active) {
        setError('That product is not for sale.');
        return;
      }

      setLines((current) =>
        addScanned(current, {
          variantId: variant.id,
          sku: variant.sku,
          description:
            Object.values(variant.attributes ?? {}).join(' · ') || variant.sku,
          unitPrice: variant.price,
        }),
      );
      setBarcode('');
    } catch (err) {
      // The server's own words. "That barcode is not in this catalogue" is
      // more useful than anything this screen could substitute.
      setError(messageFor(err));
    } finally {
      setScanning(false);
      refocus();
    }
  }

  function addTender(method: string, amount: string) {
    setTenders((t) => [...t, { method, amount }]);
    refocus();
  }

  /** Tenders the exact balance in one press -- the commonest action there is. */
  function tenderExact(method: string) {
    if (!owing) return;
    addTender(method, balance);
  }

  function reset() {
    setSaleId(newSaleId());
    setLines([]);
    setTenders([]);
    setInvoiceDiscount('0');
    setCustomer(null);
    setError(null);
    setCompleted(null);
    refocus();
  }

  async function pay() {
    if (lines.length === 0 || paying) return;
    setPaying(true);
    setError(null);
    try {
      const sale = await api.post<CompletedSale>(
        '/pos/sales',
        {
          invoice_uuid: saleId,
          doc_type: 'simplified',
          issued_at: new Date().toISOString(),
          invoice_discount: invoiceDiscount,
          ...(customer ? { customer_id: customer.id } : {}),
          lines: lines.map((l) => ({
            variant_id: l.variantId,
            description: l.description,
            qty: l.qty,
            unit_price: l.unitPrice,
            line_discount: l.lineDiscount,
            ...(l.promotionId ? { promotion_id: l.promotionId } : {}),
          })),
          tenders: tenders.map((t) => ({
            method: t.method,
            amount: t.amount,
            ...(t.reference ? { reference: t.reference } : {}),
          })),
        },
        // The same key for every attempt at this sale. A retry after a lost
        // response returns the original result rather than selling twice.
        { idempotencyKey: saleId },
      );
      setCompleted(sale);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError(messageFor(err));
      }
    } finally {
      setPaying(false);
    }
  }

  const counterName =
    state.kind === 'open'
      ? `${state.counter.terminal_label} · ${state.counter.store}`
      : '';

  return (
    <div className="flex h-dvh flex-col bg-ground">
      {/* ---- counter bar ------------------------------------------------ */}
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface px-3">
        <button
          type="button"
          onClick={leave}
          className="flex h-9 items-center gap-1.5 rounded-sm px-2 text-label text-muted hover:bg-surface-hover hover:text-fg"
        >
          <ArrowLeft className="size-4 rtl:rotate-180" aria-hidden="true" />
          Leave counter
        </button>
        <p className="min-w-0 flex-1 truncate text-label font-medium text-fg">
          {counterName}
        </p>
        <a
          href="/dashboard"
          className="hidden h-9 items-center rounded-sm px-2 text-label text-muted hover:bg-surface-hover hover:text-fg sm:flex"
        >
          Back office
        </a>
      </header>

      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        {/* ---- cart ----------------------------------------------------- */}
        <section
          aria-label="This sale"
          className="flex min-h-0 flex-1 flex-col border-line lg:border-e"
        >
          <form onSubmit={onScan} className="shrink-0 border-b border-line p-3">
            <div className="relative flex items-center">
              <ScanLine
                className="pointer-events-none absolute start-3 size-5 text-muted"
                aria-hidden="true"
              />
              <input
                ref={scanRef}
                value={barcode}
                onChange={(e) => setBarcode(e.target.value)}
                // A physical scanner types the code and presses Enter, so this
                // is an ordinary text input and needs no special handling.
                placeholder="Scan a barcode, or type one and press Enter"
                aria-label="Barcode"
                autoComplete="off"
                enterKeyHint="enter"
                className={cn(
                  'h-12 w-full rounded-sm border border-input bg-input-bg ps-11 pe-3',
                  'text-lede text-fg placeholder:text-disabled',
                )}
              />
              {scanning && (
                <Loader2
                  className="absolute end-3 size-4 animate-spin text-muted"
                  aria-hidden="true"
                />
              )}
            </div>
            {error && (
              <p role="alert" className="mt-2 text-body text-critical-fg">
                {error}
              </p>
            )}
          </form>

          <div className="min-h-0 flex-1 overflow-y-auto">
            {lines.length === 0 ? (
              <div className="grid h-full place-items-center px-6 text-center">
                <div>
                  <p className="text-lede font-medium text-fg">Ready</p>
                  <p className="mt-1 text-body text-muted">
                    Scan the first item to start a sale.
                  </p>
                </div>
              </div>
            ) : (
              <ul>
                {lines.map((line) => {
                  const short = exceedsStock(line);
                  return (
                    <li
                      key={line.variantId}
                      className="flex items-center gap-3 border-b border-line px-3 py-2.5"
                    >
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-body font-medium text-fg">
                          {line.description}
                        </p>
                        <p className="text-caption text-muted">
                          {line.sku} · {money(line.unitPrice)} each
                          {short && (
                            <Badge tone="caution" className="ms-2">
                              More than is in stock
                            </Badge>
                          )}
                        </p>
                      </div>

                      <div className="flex shrink-0 items-center gap-1">
                        <input
                          value={line.qty}
                          onChange={(e) =>
                            setLines((c) =>
                              setQty(c, line.variantId, e.target.value),
                            )
                          }
                          onBlur={refocus}
                          inputMode="decimal"
                          aria-label={`Quantity of ${line.description}`}
                          className={cn(
                            'num h-10 w-16 rounded-sm border border-input bg-input-bg',
                            'text-center text-body tabular-nums [direction:ltr]',
                          )}
                        />
                        <p className="num w-24 text-end text-body font-semibold tabular-nums">
                          {money(lineNet(line).toFixed(2))}
                        </p>
                        <button
                          type="button"
                          onClick={() => {
                            setLines((c) => removeLine(c, line.variantId));
                            refocus();
                          }}
                          aria-label={`Remove ${line.description}`}
                          className="grid size-10 place-items-center rounded-sm text-muted hover:bg-critical-subtle hover:text-critical-fg"
                        >
                          <Trash2 className="size-4" aria-hidden="true" />
                        </button>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </section>

        {/* ---- totals and payment --------------------------------------- */}
        <aside
          aria-label="Payment"
          className="flex shrink-0 flex-col border-t border-line bg-surface lg:w-[24rem] lg:border-t-0"
        >
          <div className="border-b border-line p-3">
            <button
              type="button"
              onClick={() => setPickingCustomer(true)}
              className={cn(
                'flex min-h-11 w-full items-center gap-2 rounded-sm border px-3',
                customer
                  ? 'border-primary bg-primary-subtle text-primary-subtle-fg'
                  : 'border-line-strong text-muted hover:bg-surface-hover',
              )}
            >
              <UserRound className="size-4 shrink-0" aria-hidden="true" />
              <span className="min-w-0 flex-1 truncate text-start text-body">
                {customer ? customer.name : 'Walk-in customer'}
              </span>
              {customer && (
                <X
                  className="size-4 shrink-0"
                  aria-hidden="true"
                  onClick={(e) => {
                    e.stopPropagation();
                    setCustomer(null);
                  }}
                />
              )}
            </button>
          </div>

          <div className="flex-1 p-3">
            <dl className="flex flex-col gap-1.5">
              <Row label="Items" value={`${totals.units} in ${totals.lineCount}`} />
              <Row label="Gross" value={money(totals.gross)} />
              <Row
                label="Discount"
                value={totals.discount === '0.00' ? '—' : `-${money(totals.discount)}`}
              />
            </dl>

            {/* The figure the customer is about to be told. Largest thing on
                the screen, and the only place the currency is spelled out. */}
            <div className="rule-total mt-3 pt-3">
              <p className="text-label text-muted">To pay</p>
              <p className="num mt-0.5 flex items-baseline gap-2 text-figure font-semibold tabular-nums tracking-tight">
                <span className="text-section font-medium text-muted">
                  {currency}
                </span>
                {money(totals.net)}
              </p>
            </div>

            {tenders.length > 0 && (
              <ul className="mt-3 flex flex-col gap-1">
                {tenders.map((t, i) => (
                  <li
                    key={`${t.method}-${i}`}
                    className="flex items-center justify-between gap-2 text-body"
                  >
                    <span className="capitalize text-muted">
                      {t.method.replace(/_/g, ' ')}
                    </span>
                    <span className="flex items-center gap-1">
                      <span className="num tabular-nums">{money(t.amount)}</span>
                      <button
                        type="button"
                        onClick={() => {
                          setTenders((c) => c.filter((_, j) => j !== i));
                          refocus();
                        }}
                        aria-label="Remove this payment"
                        className="grid size-7 place-items-center rounded-xs text-muted hover:text-critical-fg"
                      >
                        <Delete className="size-3.5" aria-hidden="true" />
                      </button>
                    </span>
                  </li>
                ))}
                <li className="mt-1 flex items-center justify-between border-t border-line pt-1.5 text-body font-medium">
                  <span>{change ? 'Change' : 'Still to pay'}</span>
                  <span
                    className={cn(
                      'num tabular-nums',
                      change && 'text-positive-fg',
                    )}
                  >
                    {money(change ? balance.slice(1) : balance)}
                  </span>
                </li>
              </ul>
            )}
          </div>

          <div className="shrink-0 border-t border-line p-3">
            <div className="grid grid-cols-2 gap-2">
              {TENDER_METHODS.map((m) => (
                <Button
                  key={m.id}
                  variant="secondary"
                  size="lg"
                  disabled={
                    lines.length === 0 ||
                    !owing ||
                    (m.id === 'customer_due' && !customer)
                  }
                  onClick={() => tenderExact(m.id)}
                  // The commonest action at a till is "the customer is paying
                  // the exact amount, by this method". One press does it.
                  title={
                    m.id === 'customer_due' && !customer
                      ? 'Choose a customer before putting a sale on account'
                      : undefined
                  }
                >
                  {m.label}
                </Button>
              ))}
            </div>

            <Button
              variant="primary"
              size="lg"
              block
              className="mt-2 h-14 text-lede"
              busy={paying}
              busyLabel="Completing the sale"
              disabled={lines.length === 0 || owing}
              onClick={() => void pay()}
            >
              {owing && lines.length > 0
                ? `${money(balance)} still to pay`
                : 'Complete sale'}
            </Button>
          </div>
        </aside>
      </div>

      {pickingCustomer && (
        <CustomerPicker
          onPick={(c) => {
            setCustomer(c);
            setPickingCustomer(false);
            refocus();
          }}
          onClose={() => {
            setPickingCustomer(false);
            refocus();
          }}
        />
      )}

      {completed && (
        <SaleComplete
          sale={completed}
          currency={currency}
          tendered={money(tenderedTotal(tenders))}
          change={change ? money(balance.slice(1)) : null}
          onNext={reset}
        />
      )}
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <dt className="text-body text-muted">{label}</dt>
      <dd className="num text-body tabular-nums">{value}</dd>
    </div>
  );
}

/**
 * The moment after the sale.
 *
 * Says the number, says the change, and gets out of the way. The one action is
 * to start the next sale, because there is a queue -- printing and emailing are
 * on the invoice screen, which is where somebody goes when a customer asks
 * afterwards.
 */
function SaleComplete({
  sale,
  currency,
  tendered,
  change,
  onNext,
}: {
  sale: CompletedSale;
  currency: string;
  tendered: string;
  change: string | null;
  onNext: () => void;
}) {
  const ref = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    ref.current?.focus();
  }, []);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Sale complete"
      className="fixed inset-0 z-50 grid place-items-center bg-[rgb(15_27_24/0.6)] p-4"
    >
      <div className="w-full max-w-sm rounded-lg bg-surface p-6 text-center shadow-overlay">
        <p className="text-label text-muted">Sale complete</p>
        {sale.human_number && (
          <p className="mt-1 text-section font-semibold text-fg">
            {sale.human_number}
          </p>
        )}

        {change && change !== '0.00' ? (
          <>
            <p className="mt-4 text-label text-muted">Change</p>
            <p className="num text-figure font-semibold tabular-nums text-positive-fg">
              <span className="text-section font-medium text-muted">
                {currency}{' '}
              </span>
              {change}
            </p>
          </>
        ) : (
          <>
            <p className="mt-4 text-label text-muted">Paid</p>
            <p className="num text-figure font-semibold tabular-nums">
              <span className="text-section font-medium text-muted">
                {currency}{' '}
              </span>
              {tendered}
            </p>
          </>
        )}

        <Button
          ref={ref}
          variant="primary"
          size="lg"
          block
          className="mt-6 h-14 text-lede"
          onClick={onNext}
        >
          Next sale
        </Button>
      </div>
    </div>
  );
}
