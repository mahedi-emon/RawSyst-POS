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
// # Scanning is local
//
// The catalogue is downloaded once when the counter opens and every scan is a
// map lookup. That is not an optimisation: `GET /catalog/scan` does not return
// `tax_treatment`, and `POST /pos/sales` refuses a line without one — a till
// built on `scan` rings items up all day and fails at payment. See
// `lib/pos/catalogue.ts`.
//
// # The sale id is minted before the first line
//
// `invoice_uuid` is created when the cart starts, not when Pay is pressed. That
// is what makes a retry after a lost response safe: the same sale carries the
// same id and the server returns the original — verified against the live API,
// which answers 200 with `Idempotency-Replayed: true` and the same invoice.
//
// # What the till computes and what it does not
//
// It computes the running total, because a customer standing at a counter is
// owed a figure before being asked to pay. It does not compute tax, does not
// allocate the invoice discount across lines and does not decide the invoice
// number — the server does all three. Prices are tax-INCLUSIVE by default, so
// the total the till shows is the total the customer pays; the server returns
// the split (125 inclusive → 108.70 net + 16.30 tax) on the way back.

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
import { FormError } from '@/components/ui/form-error';
import { ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useCompany } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
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
import { Catalogue, type Sellable } from '@/lib/pos/catalogue';
import { useCounter } from '@/lib/pos/counter';
import { useShift, type ShiftReport } from '@/lib/pos/shift';
import { cn } from '@/lib/utils';

import { CloseShift } from './close-shift';
import { CustomerPicker, type PosCustomer } from './customer-picker';
import { MoveCash } from './move-cash';
import { OpenShift } from './open-shift';

interface Tender {
  method: string;
  amount: string;
  reference?: string;
}

/**
 * What the server says when a sale succeeds.
 *
 * Read off the live API, not guessed: there is no `human_number` here. The
 * invoice number is on the invoice record, which the receipt screen reads; what
 * comes back from the sale is the money split and the ZATCA chain position.
 */
interface CompletedSale {
  invoice_id: string;
  subtotal_net: string;
  tax_total: string;
  total_inclusive: string;
  discount_total: string;
  lines: {
    line_no: number;
    description: string;
    qty: string;
    unit_price: string;
    net_amount: string;
    tax_amount: string;
    tax_rate: string;
  }[];
  /** Lines the shop was short of. Empty when the sale took nothing it lacked. */
  stock_shortfalls: unknown[];
  zatca?: { icv: number; pih: string; schema_version: string };
}

/** The tenders a counter offers. Cash first: it is most of them. */
const TENDER_METHODS = [
  { id: 'cash', labelKey: 'nx.pos.tenderCash' },
  { id: 'card', labelKey: 'nx.pos.tenderCard' },
  { id: 'bank_transfer', labelKey: 'nx.pos.tenderTransfer' },
  { id: 'customer_due', labelKey: 'nx.pos.tenderAccount' },
] as const;

export function Till() {
  const t = useT();
  const { state: counterState, leave } = useCounter();
  const { currency, market, company } = useCompany();
  const shift = useShift();

  const [catalogue, setCatalogue] = useState<Catalogue | null>(null);
  const [catalogueError, setCatalogueError] = useState<unknown>(null);

  const [saleId, setSaleId] = useState(() => newSaleId());
  const [lines, setLines] = useState<CartLine[]>([]);
  const [invoiceDiscount] = useState('0');
  const [customer, setCustomer] = useState<PosCustomer | null>(null);
  const [tenders, setTenders] = useState<Tender[]>([]);
  const [term, setTerm] = useState('');
  const [matches, setMatches] = useState<Sellable[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);
  const [paying, setPaying] = useState(false);
  const [completed, setCompleted] = useState<CompletedSale | null>(null);
  const [pickingCustomer, setPickingCustomer] = useState(false);
  // Two things a counter does besides sell, both reached from its own header.
  const [closing, setClosing] = useState<ShiftReport | null | 'loading'>(null);
  const [movingCash, setMovingCash] = useState(false);
  const [closed, setClosed] = useState<ShiftReport | null>(null);

  const scanRef = useRef<HTMLInputElement>(null);

  // The catalogue is downloaded once, when the counter opens.
  useEffect(() => {
    if (!company) return;
    const controller = new AbortController();
    const next = new Catalogue();
    void next
      .load(company.id, controller.signal)
      .then(() => setCatalogue(next))
      .catch((e: unknown) => {
        if (!controller.signal.aborted) setCatalogueError(e);
      });
    return () => controller.abort();
  }, [company]);

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

  function ring(item: Sellable) {
    setLines((current) =>
      addScanned(current, {
        variantId: item.variantId,
        sku: item.sku,
        description: item.description,
        unitPrice: item.price,
        taxTreatment: item.taxTreatment,
      }),
    );
    setTerm('');
    setMatches([]);
    setError(null);
    refocus();
  }

  function onType(value: string) {
    setTerm(value);
    // A scanner types fast and ends with Enter, so nothing is looked up until
    // there is enough to be a name rather than the first characters of a
    // barcode arriving.
    setMatches(value.trim().length >= 2 ? (catalogue?.search(value) ?? []) : []);
  }

  function onScan(e: FormEvent) {
    e.preventDefault();
    const code = term.trim();
    if (!code || !catalogue) return;

    const hit = catalogue.find(code);
    if (hit) {
      ring(hit);
      return;
    }
    // One match by name is unambiguous; ring it rather than making somebody
    // pick from a list of one.
    if (matches.length === 1 && matches[0]) {
      ring(matches[0]);
      return;
    }
    setError(t('nx.pos.noMatch', { code }));
    refocus();
  }

  function addTender(method: string, amount: string) {
    setTenders((t) => [...t, { method, amount }]);
    refocus();
  }

  /** Tenders the exact balance in one press — the commonest action there is. */
  function tenderExact(method: string) {
    if (!owing) return;
    addTender(method, balance);
  }

  function reset() {
    setSaleId(newSaleId());
    setLines([]);
    setTenders([]);
    setCustomer(null);
    setError(null);
    setFieldErrors(null);
    setCompleted(null);
    refocus();
  }

  async function pay() {
    if (lines.length === 0 || paying) return;
    setPaying(true);
    setError(null);
    setFieldErrors(null);
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
            // Required by the server. It comes from the catalogue snapshot;
            // there is nowhere else the till can learn it.
            tax_treatment: l.taxTreatment,
            ...(l.promotionId ? { promotion_id: l.promotionId } : {}),
          })),
          tenders: tenders.map((tender) => ({
            method: tender.method,
            amount: tender.amount,
            ...(tender.reference ? { reference: tender.reference } : {}),
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
        // A compliance refusal names the fields that are wrong — and they are
        // usually the shop's own address, which nobody can fix at a counter.
        if (err.isComplianceRefusal && err.fields) setFieldErrors(err.fields);
      } else {
        setError(messageFor(err, t));
      }
    } finally {
      setPaying(false);
    }
  }

  const counterName =
    counterState.kind === 'open'
      ? `${counterState.counter.terminal_label} · ${counterState.counter.store}`
      : '';

  // --- gates before the till can sell --------------------------------------

  if (catalogueError) {
    return (
      <TillFrame counterName={counterName} onLeave={leave}>
        <div className="p-6">
          <ErrorState
            error={catalogueError}
            onRetry={() => window.location.reload()}
          />
        </div>
      </TillFrame>
    );
  }

  if (shift.state.kind === 'checking' || !catalogue) {
    return (
      <TillFrame counterName={counterName} onLeave={leave}>
        <div className="grid flex-1 place-items-center">
          <p className="text-body text-muted">{t('nx.pos.openingCounter')}</p>
        </div>
      </TillFrame>
    );
  }

  if (shift.state.kind === 'closed' || shift.state.kind === 'failed') {
    return (
      <TillFrame counterName={counterName} onLeave={leave}>
        <OpenShift
          currency={currency}
          busy={shift.opening}
          error={shift.state.kind === 'failed' ? shift.state.error : null}
          onOpen={(float, blind) => void shift.open(float, blind)}
        />
      </TillFrame>
    );
  }

  if (closed) {
    return (
      <TillFrame counterName={counterName} onLeave={leave}>
        <SessionClosed report={closed} onLeave={leave} />
      </TillFrame>
    );
  }

  if (closing !== null) {
    return (
      <TillFrame counterName={counterName} onLeave={leave}>
        <CloseShift
          shift={shift.state.shift}
          report={closing === 'loading' ? null : closing}
          onClosed={(final) => {
            setClosing(null);
            setClosed(final);
          }}
          onCancel={() => setClosing(null)}
        />
      </TillFrame>
    );
  }

  return (
    <TillFrame
      counterName={counterName}
      onLeave={leave}
      shiftLabel={t('nx.pos.session', { no: shift.state.shift.session_no })}
      onMoveCash={() => setMovingCash(true)}
      onClose={() => {
        // The reading is fetched, not required: a Cashier can reach
        // `GET /shifts/{id}` but the figures it carries are withheld on a
        // blind close, and the screen works either way.
        setClosing('loading');
        void shift
          .peek(shift.state.kind === 'open' ? shift.state.shift.id : '')
          .then((r) => setClosing(r))
          .catch(() => setClosing('loading'));
      }}
    >
      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        {/* ---- cart ----------------------------------------------------- */}
        <section
          aria-label={t('nx.pos.thisSale')}
          className="flex min-h-0 flex-1 flex-col border-line lg:border-e"
        >
          <form onSubmit={onScan} className="relative shrink-0 border-b border-line p-3">
            <div className="relative flex items-center">
              <ScanLine
                className="pointer-events-none absolute start-3 size-5 text-muted"
                aria-hidden="true"
              />
              <input
                ref={scanRef}
                value={term}
                onChange={(e) => onType(e.target.value)}
                placeholder={t('nx.pos.scanPlaceholder')}
                aria-label={t('nx.pos.scanLabel')}
                autoComplete="off"
                // A barcode is not a word. Spellcheck underlines every scan in
                // red and, on a phone keyboard, autocorrects one into
                // something that is not in the catalogue.
                spellCheck={false}
                autoCorrect="off"
                autoCapitalize="off"
                enterKeyHint="enter"
                className={cn(
                  'h-12 w-full rounded-sm border border-input bg-input-bg ps-11 pe-3',
                  'text-lede text-fg placeholder:text-disabled',
                )}
              />
            </div>

            {matches.length > 0 && (
              <ul className="scroll-trap absolute inset-x-3 top-16 z-20 max-h-72 overflow-y-auto rounded-sm border border-line bg-surface shadow-overlay">
                {matches.map((m) => (
                  <li key={m.variantId}>
                    <button
                      type="button"
                      onClick={() => ring(m)}
                      className="flex min-h-12 w-full items-center justify-between gap-3 border-b border-line px-3 text-start last:border-b-0 hover:bg-surface-hover"
                    >
                      <span className="min-w-0">
                        <span className="block truncate text-body text-fg">
                          {m.description}
                        </span>
                        <span className="block text-caption text-muted">{m.sku}</span>
                      </span>
                      <span className="num shrink-0 text-body tabular-nums">
                        {money(m.price)}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}

            <FormError
              message={error}
              fields={fieldErrors}
              action={
                fieldErrors ? (
                  <>
                    <p className="text-caption font-medium text-critical-fg">
                      {t('nx.pos.fixInSettings')}
                    </p>
                    <a
                      href="/settings/business"
                      className="mt-1 inline-block text-caption font-medium underline underline-offset-2"
                    >
                      {t('nx.pos.openBusinessDetails')}
                    </a>
                  </>
                ) : null
              }
              className="mt-2"
            />
          </form>

          <div className="scroll-trap min-h-0 flex-1 overflow-y-auto">
            {lines.length === 0 ? (
              <div className="grid h-full place-items-center px-6 text-center">
                <div>
                  <p className="text-lede font-medium text-fg">
                    {t('nx.pos.ready')}
                  </p>
                  <p className="mt-1 text-body text-muted">
                    {t('nx.pos.readyDesc')}
                  </p>
                  <p className="mt-3 text-caption text-subtle">
                    {catalogue.size === 1
                      ? t('nx.pos.linesOne')
                      : t('nx.pos.linesMany', { count: catalogue.size })}
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
                          {line.sku} ·{' '}
                          {t('nx.pos.each', { price: money(line.unitPrice) })}
                          {short && (
                            <Badge tone="caution" className="ms-2">
                              {t('nx.pos.moreThanStock')}
                            </Badge>
                          )}
                        </p>
                      </div>

                      <div className="flex shrink-0 items-center gap-1">
                        <input
                          value={line.qty}
                          onChange={(e) =>
                            setLines((c) => setQty(c, line.variantId, e.target.value))
                          }
                          onBlur={refocus}
                          inputMode="decimal"
                          aria-label={t('nx.pos.quantityOf', {
                            item: line.description,
                          })}
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
                          aria-label={t('nx.pos.removeItem', {
                            item: line.description,
                          })}
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
          aria-label={t('nx.pos.payment')}
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
                {customer ? customer.name : t('nx.pos.walkIn')}
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
              <Row
                label={t('nx.pos.items')}
                value={t('nx.pos.itemsCount', {
                  units: totals.units,
                  lines: totals.lineCount,
                })}
              />
              <Row label={t('nx.pos.gross')} value={money(totals.gross)} />
              <Row
                label={t('nx.pos.discount')}
                value={
                  totals.discount === '0.00' ? '—' : `-${money(totals.discount)}`
                }
              />
            </dl>

            {/* The figure the customer is about to be told. Largest thing on
                the screen, and the only place the currency is spelled out.
                Prices include tax, so this is what they pay. */}
            <div className="rule-total mt-3 pt-3">
              <p className="text-label text-muted">{t('nx.pos.toPay')}</p>
              <p className="num mt-0.5 flex items-baseline gap-2 text-figure font-semibold tabular-nums tracking-tight">
                <span className="text-section font-medium text-muted">{currency}</span>
                {money(totals.net)}
              </p>
            </div>

            {tenders.length > 0 && (
              <ul className="mt-3 flex flex-col gap-1">
                {tenders.map((tender, i) => (
                  <li
                    key={`${tender.method}-${i}`}
                    className="flex items-center justify-between gap-2 text-body"
                  >
                    <span className="capitalize text-muted">
                      {tender.method.replace(/_/g, ' ')}
                    </span>
                    <span className="flex items-center gap-1">
                      <span className="num tabular-nums">
                        {money(tender.amount)}
                      </span>
                      <button
                        type="button"
                        onClick={() => {
                          setTenders((c) => c.filter((_, j) => j !== i));
                          refocus();
                        }}
                        aria-label={t('nx.pos.removePayment')}
                        className="grid size-7 place-items-center rounded-xs text-muted hover:text-critical-fg"
                      >
                        <Delete className="size-3.5" aria-hidden="true" />
                      </button>
                    </span>
                  </li>
                ))}
                <li className="mt-1 flex items-center justify-between border-t border-line pt-1.5 text-body font-medium">
                  <span>
                    {change ? t('nx.pos.change') : t('nx.pos.stillToPay')}
                  </span>
                  <span className={cn('num tabular-nums', change && 'text-positive-fg')}>
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
                  title={
                    m.id === 'customer_due' && !customer
                      ? t('nx.pos.chooseCustomerFirst')
                      : undefined
                  }
                >
                  {t(m.labelKey)}
                </Button>
              ))}
            </div>

            <Button
              variant="primary"
              size="lg"
              block
              className="mt-2 h-14 text-lede"
              busy={paying}
              busyLabel={t('nx.pos.completing')}
              disabled={lines.length === 0 || owing}
              onClick={() => void pay()}
            >
              {owing && lines.length > 0
                ? t('nx.pos.stillToPayAmount', { amount: money(balance) })
                : t('nx.pos.completeSale')}
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

      {movingCash && shift.state.kind === 'open' && (
        <MoveCash
          onClose={() => {
            setMovingCash(false);
            refocus();
          }}
          onMove={(amount, reason, note) =>
            shift.drop(
              shift.state.kind === 'open' ? shift.state.shift.id : '',
              amount,
              reason,
              note,
            )
          }
        />
      )}

      {completed && (
        <SaleComplete
          sale={completed}
          currency={currency}
          market={market}
          change={change ? money(balance.slice(1)) : null}
          tendered={money(tenderedTotal(tenders))}
          onNext={reset}
        />
      )}
    </TillFrame>
  );
}

function TillFrame({
  counterName,
  shiftLabel,
  onLeave,
  onMoveCash,
  onClose,
  children,
}: {
  counterName: string;
  shiftLabel?: string;
  onLeave: () => void;
  onMoveCash?: () => void;
  onClose?: () => void;
  children: React.ReactNode;
}) {
  const t = useT();
  return (
    <div className="flex h-dvh flex-col bg-ground">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface px-3">
        <button
          type="button"
          onClick={onLeave}
          className="flex h-9 items-center gap-1.5 rounded-sm px-2 text-label text-muted hover:bg-surface-hover hover:text-fg"
        >
          <ArrowLeft className="size-4 rtl:rotate-180" aria-hidden="true" />
          {t('nx.pos.leaveCounter')}
        </button>
        <p className="min-w-0 flex-1 truncate text-label font-medium text-fg">
          {counterName}
          {shiftLabel && (
            <span className="ms-2 font-normal text-muted">{shiftLabel}</span>
          )}
        </p>
        <a
          href="/pos/returns"
          className="h-9 rounded-sm px-2 text-label text-muted hover:bg-surface-hover hover:text-fg"
        >
          {t('nx.pos.returns')}
        </a>
        {onMoveCash ? (
          <button
            type="button"
            onClick={onMoveCash}
            className="h-9 rounded-sm px-2 text-label text-muted hover:bg-surface-hover hover:text-fg"
          >
            {t('nx.shift.cashDrop')}
          </button>
        ) : null}
        {onClose ? (
          <button
            type="button"
            onClick={onClose}
            className="h-9 rounded-sm border border-line-strong px-2.5 text-label font-medium hover:bg-surface-hover"
          >
            {t('nx.shift.closeIt')}
          </button>
        ) : null}
        <a
          href="/dashboard"
          className="hidden h-9 items-center rounded-sm px-2 text-label text-muted hover:bg-surface-hover hover:text-fg sm:flex"
        >
          {t('nx.shell.backOffice')}
        </a>
      </header>
      {children}
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
 * Says the change, shows what the tax came to, and gets out of the way. The one
 * action is to start the next sale, because there is a queue — printing and
 * emailing live on the invoice screen, which is where somebody goes when a
 * customer asks afterwards.
 */
function SaleComplete({
  sale,
  currency,
  change,
  tendered,
  onNext,
}: {
  sale: CompletedSale;
  currency: string;
  market: string;
  change: string | null;
  tendered: string;
  onNext: () => void;
}) {
  const t = useT();
  const ref = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    ref.current?.focus();
  }, []);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t('nx.pos.saleComplete')}
      className="fixed inset-0 z-50 grid place-items-center bg-[rgb(15_27_24/0.6)] p-4"
    >
      <div className="w-full max-w-sm rounded-lg bg-surface p-6 text-center shadow-overlay">
        <p className="text-label text-muted">{t('nx.pos.saleComplete')}</p>

        {change && change !== '0.00' ? (
          <>
            <p className="mt-4 text-label text-muted">{t('nx.pos.change')}</p>
            <p className="num text-figure font-semibold tabular-nums text-positive-fg">
              <span className="text-section font-medium text-muted">{currency} </span>
              {change}
            </p>
          </>
        ) : (
          <>
            <p className="mt-4 text-label text-muted">{t('nx.pos.paid')}</p>
            <p className="num text-figure font-semibold tabular-nums">
              <span className="text-section font-medium text-muted">{currency} </span>
              {tendered}
            </p>
          </>
        )}

        {/* The split the server computed, so the cashier can answer "how much
            was the VAT?" without opening anything. */}
        <dl className="mt-4 flex justify-center gap-5 text-caption text-muted">
          <div>
            <dt>{t('nx.pos.net')}</dt>
            <dd className="num tabular-nums text-fg">{sale.subtotal_net}</dd>
          </div>
          <div>
            <dt>{t('nx.pos.tax')}</dt>
            <dd className="num tabular-nums text-fg">{sale.tax_total}</dd>
          </div>
          <div>
            <dt>{t('nx.pos.total')}</dt>
            <dd className="num tabular-nums text-fg">{sale.total_inclusive}</dd>
          </div>
        </dl>

        <Button
          ref={ref}
          variant="primary"
          size="lg"
          block
          className="mt-6 h-14 text-lede"
          onClick={onNext}
        >
          {t('nx.pos.nextSale')}
        </Button>
      </div>
    </div>
  );
}

/**
 * The Z reckoning, after the session is closed.
 *
 * Shown once and not navigable back into: a closed session is immutable, and a
 * screen that let somebody return to the count would imply otherwise.
 */
function SessionClosed({
  report,
  onLeave,
}: {
  report: ShiftReport;
  onLeave: () => void;
}) {
  const t = useT();
  const { currency, market } = useCompany();
  const money = (v: string | null | undefined) =>
    formatMoney(v ?? null, { currency, market, bare: true });

  const variance = report.variance ?? '0';
  const exact = /^[+-]?0*(\.0*)?$/.test(variance);

  return (
    <div className="mx-auto flex w-full max-w-md flex-col gap-4 p-6 text-center">
      <p className="text-label text-muted">{t('nx.shift.closed')}</p>
      <p className="text-page font-semibold text-fg">
        {t('nx.pos.session', { no: report.session_no })}
      </p>

      <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-line bg-line">
        <div className="bg-surface p-4">
          <dt className="text-label text-muted">{t('nx.shift.countedTotal')}</dt>
          <dd className="num mt-1 text-section font-semibold tabular-nums">
            {money(report.counted_cash)}
          </dd>
        </div>
        <div className="bg-surface p-4">
          <dt className="text-label text-muted">{t('nx.shift.expectedInDrawer')}</dt>
          <dd className="num mt-1 text-section font-semibold tabular-nums">
            {money(report.expected_cash)}
          </dd>
        </div>
      </dl>

      <div>
        <p className="text-label text-muted">{t('nx.shift.variance')}</p>
        <p
          className={cn(
            'num mt-1 text-figure font-semibold tabular-nums',
            exact
              ? 'text-positive-fg'
              : variance.startsWith('-')
                ? 'text-critical-fg'
                : 'text-caution-fg',
          )}
        >
          {money(variance.replace('-', ''))}
        </p>
        <p className="mt-1 text-caption text-subtle">
          {exact
            ? t('nx.shift.exact')
            : variance.startsWith('-')
              ? t('nx.shift.short')
              : t('nx.shift.over')}
        </p>
      </div>

      <Button variant="primary" size="lg" block className="mt-2" onClick={onLeave}>
        {t('nx.pos.leaveCounter')}
      </Button>
    </div>
  );
}
