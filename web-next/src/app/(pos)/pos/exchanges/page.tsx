'use client';

// Swapping goods.
//
// # One screen, one request, because the two halves must not be separable
//
// Design 11 §7 draws this as one screen: scan the invoice, pick the lines
// coming back, pick the replacement, settle the difference. Underneath it is a
// credit note and a new invoice, and the service puts both through a single
// transaction — "a till that issued the credit note and then failed to place
// the sale would have given the goods away; one that placed the sale and failed
// to credit would have charged twice."
//
// So there is no draft, no two-step, and no way to leave half of it done.
//
// # Only the difference goes through the drawer
//
// A customer swapping a 100 item for a 150 one hands over 50. The offsetting
// 100 goes through a clearing account and never appears as a tender, because a
// drawer expected to hold cash that never moved through it shows a variance at
// close with no cause.
//
// # This screen's figures are an estimate; the server's are the answer
//
// The credit shown here is worked out pro-rata from what `returnable` reports.
// It agrees with the server for a whole line and for any line without an
// allocated discount, and where it does not, the server refuses with the exact
// amount it settles at — and that sentence is what gets shown. A till insisting
// on its own figure would be asserting what an exchange settles for.
//
// # Inside the POS, not the back office
//
// `POST /pos/exchanges` goes through `resolveTerminal` and refuses a session
// with no counter behind it: both documents join that terminal's own invoice
// chain, and one raised from a browser would break the sequence.

import { ArrowLeft, ReceiptText, Search, Trash2 } from 'lucide-react';
import { useEffect, useRef, useState, type FormEvent } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  addScanned,
  newSaleId,
  removeLine,
  setQty as setCartQty,
  type CartLine,
} from '@/lib/pos/cart';
import { useSellFrom } from '@/components/pos/location-picker';
import { Catalogue, type Sellable } from '@/lib/pos/catalogue';
import { useCounter } from '@/lib/pos/counter';
import {
  creditFor,
  overReturnable,
  readiness,
  replacementTotal,
  returningLines,
  settlementOf,
  settles,
  type ReturnableLine,
} from '@/lib/pos/exchange';
import { cn } from '@/lib/utils';

/** The tenders a counter offers, as the till offers them. */
const TENDER_METHODS = [
  { id: 'cash', labelKey: 'nx.pos.tenderCash' },
  { id: 'card', labelKey: 'nx.pos.tenderCard' },
  { id: 'bank_transfer', labelKey: 'nx.pos.tenderTransfer' },
] as const;

interface LookedUpSale {
  id: string;
  human_number?: string;
  issued_at?: string;
  total_inclusive?: string;
}

interface ExchangeResult {
  /** The note carries a number a customer can be read. */
  credit_note: { credit_note_id: string; human_number?: string; total_inclusive: string };
  /**
   * The replacement does NOT.
   *
   * Read off the live API rather than assumed: a sale response carries the
   * money split and the chain position, and the invoice number lives on the
   * invoice record the receipt screen reads. Showing a blank where one was
   * expected is how the credit note lost its own number once already.
   */
  replacement: { invoice_id: string; total_inclusive: string };
  credit_applied: string;
  difference: string;
  customer_paid: boolean;
}

function ExchangeScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const { state: counter } = useCounter();
  // One warehouse for the pair: the goods come back to the same shelf the
  // replacement leaves from, which is what a swap at a counter actually is.
  const { warehouseId } = useSellFrom();

  const [catalogue, setCatalogue] = useState<Catalogue | null>(null);
  const [reference, setReference] = useState('');
  const [sale, setSale] = useState<LookedUpSale | null>(null);
  const [returnable, setReturnable] = useState<ReturnableLine[]>([]);
  const [back, setBack] = useState<Record<string, string>>({});
  const [out, setOut] = useState<CartLine[]>([]);
  const [term, setTerm] = useState('');
  const [matches, setMatches] = useState<Sellable[]>([]);
  const [tenders, setTenders] = useState<{ method: string; amount: string }[]>([]);
  const [reason, setReason] = useState('');
  const [ids, setIds] = useState(() => ({ credit: newSaleId(), invoice: newSaleId() }));
  const [looking, setLooking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);
  const [done, setDone] = useState<ExchangeResult | null>(null);

  const scanBox = useRef<HTMLInputElement>(null);

  // Downloaded once. The same catalogue the till uses, because the replacement
  // is an ordinary sale and gets a sale's treatment.
  useEffect(() => {
    if (!scope) return;
    const controller = new AbortController();
    const next = new Catalogue();
    next
      .load(scope.company_id, controller.signal)
      .then(() => setCatalogue(next))
      .catch(() => {
        // A catalogue that failed to load leaves the search empty rather than
        // failing the screen: the half about goods coming back still works.
      });
    return () => controller.abort();
  }, [scope]);

  const money = (v: string) => formatMoney(v, { currency, market, bare: true });

  const credit = creditFor(returnable, back);
  const replacement = replacementTotal(out);
  const settlement = settlementOf(credit, replacement);
  const state = readiness(credit, replacement, reason, tenders);
  const offered = tenders.reduce(
    (sum, tender) => sum + Number.parseFloat(tender.amount || '0'),
    0,
  );

  async function lookUp(e: FormEvent) {
    e.preventDefault();
    const ref = reference.trim();
    if (!ref || looking) return;
    setLooking(true);
    setError(null);
    setSale(null);
    setReturnable([]);
    setBack({});
    try {
      const found = await api.get<LookedUpSale>('/pos/sales/lookup', {
        query: { ...scope, reference: ref },
      });
      setSale(found);
      const lines = await api.get<{ lines: ReturnableLine[] }>(
        `/pos/sales/${found.id}/returnable`,
      );
      setReturnable(lines.lines ?? []);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) setError(t('nx.exc.notFound'));
      else setError(messageFor(err, t));
    } finally {
      setLooking(false);
    }
  }

  function ring(item: Sellable) {
    setOut((current) =>
      addScanned(current, {
        variantId: item.variantId,
        sku: item.sku,
        description: item.description,
        unitPrice: item.price,
        // Required on every sale line, and the catalogue snapshot is the only
        // place the till can learn it.
        taxTreatment: item.taxTreatment,
      }),
    );
    setTerm('');
    setMatches([]);
    setError(null);
    scanBox.current?.focus();
  }

  function onScan(e: FormEvent) {
    e.preventDefault();
    const code = term.trim();
    if (!code || !catalogue) return;
    const hit = catalogue.find(code);
    if (hit) return ring(hit);
    // One match by name is unambiguous; ring it rather than making somebody
    // pick from a list of one.
    if (matches.length === 1 && matches[0]) return ring(matches[0]);
    setError(t('nx.exc.unknownCode'));
  }

  async function submit() {
    if (!state.ok || busy) return;
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    try {
      const result = await api.post<ExchangeResult>(
        '/pos/exchanges',
        {
          // Two documents, two identities. Sharing one would make the pair
          // indistinguishable to every idempotency check downstream.
          credit_note_uuid: ids.credit,
          invoice_uuid: ids.invoice,
          original_invoice_id: sale?.id,
          issued_at: new Date().toISOString(),
          ...(warehouseId ? { warehouse_id: warehouseId } : {}),
          reason: reason.trim(),
          returning: returningLines(back),
          replacement: {
            doc_type: 'simplified',
            lines: out.map((l) => ({
              variant_id: l.variantId,
              description: l.description,
              qty: l.qty,
              unit_price: l.unitPrice,
              line_discount: l.lineDiscount,
              tax_treatment: l.taxTreatment,
            })),
            // No tenders: "how an exchange settles is arithmetic the server
            // does, not a figure the till is allowed to assert."
          },
          settlement: tenders,
        },
        // One key for the PAIR. A retry that recognised the credit note and
        // went on to sell the replacement again would hand the customer two
        // jackets for one.
        { idempotencyKey: ids.credit },
      );
      setDone(result);
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFieldErrors(err.fields);
      setError(messageFor(err, t));
    } finally {
      setBusy(false);
    }
  }

  function again() {
    setIds({ credit: newSaleId(), invoice: newSaleId() });
    setReference('');
    setSale(null);
    setReturnable([]);
    setBack({});
    setOut([]);
    setTenders([]);
    setReason('');
    setDone(null);
    setError(null);
    setFieldErrors(null);
  }

  const counterName =
    counter.kind === 'open'
      ? `${counter.counter.terminal_label} · ${counter.counter.store}`
      : '';

  if (counter.kind !== 'open') {
    return (
      <Frame counterName="">
        <div className="p-6">
          <EmptyState
            icon={ReceiptText}
            title={t('nx.exc.needCounter')}
            description={t('nx.exc.needCounterDesc')}
          />
        </div>
      </Frame>
    );
  }

  if (done) {
    return (
      <Frame counterName={counterName}>
        <div className="mx-auto flex w-full max-w-md flex-col gap-4 p-6 text-center">
          <p className="text-label text-muted">{t('nx.exc.done')}</p>
          <dl className="flex flex-col gap-2 rounded-md border border-line bg-surface p-4 text-body">
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.exc.creditNote')}</dt>
              <dd className="num">{done.credit_note.human_number ?? '—'}</dd>
            </div>
            <div className="flex justify-between gap-4">
              <dt className="text-muted">{t('nx.exc.newInvoice')}</dt>
              {/* The total, not a number: a sale response has no human number
                  on it, and an empty field would look like a document that
                  failed to get one. */}
              <dd className="num">{money(done.replacement.total_inclusive)}</dd>
            </div>
          </dl>

          {/* What actually moved, stated by the server rather than computed
              here, so the receipt says what was really charged. */}
          <p className="num text-figure font-semibold tabular-nums">
            {isZero(done.difference) ? (
              <span className="text-section font-medium text-muted">
                {t('nx.exc.nothingMoved')}
              </span>
            ) : done.customer_paid ? (
              <span className="text-positive-fg">
                {t('nx.exc.tookPayment', {
                  amount: `${currency} ${money(done.difference)}`,
                })}
              </span>
            ) : (
              <span className="text-critical-fg">
                {t('nx.exc.gaveBack', {
                  amount: `${currency} ${money(done.difference).replace('-', '')}`,
                })}
              </span>
            )}
          </p>

          <Button variant="primary" size="lg" block className="mt-2" onClick={again}>
            {t('nx.exc.next')}
          </Button>
        </div>
      </Frame>
    );
  }

  return (
    <Frame counterName={counterName}>
      <div className="mx-auto flex w-full max-w-5xl flex-col gap-5 p-4">
        <div>
          <h1 className="text-page font-semibold text-fg">{t('nx.exc.title')}</h1>
          <p className="mt-1 text-body text-muted">{t('nx.exc.subtitle')}</p>
        </div>

        <FormError message={error} fields={fieldErrors} />

        <form onSubmit={lookUp}>
          <Field label={t('nx.exc.reference')} required>
            <div className="flex gap-2">
              <div className="relative flex flex-1 items-center">
                <Search
                  className="pointer-events-none absolute start-3 size-4 text-muted"
                  aria-hidden="true"
                />
                <Input
                  value={reference}
                  onChange={(e) => setReference(e.target.value)}
                  placeholder={t('nx.exc.referencePlaceholder')}
                  className="ps-9"
                  autoComplete="off"
                  spellCheck={false}
                  autoFocus
                />
              </div>
              <Button type="submit" variant="secondary" busy={looking}>
                {t('nx.exc.lookUp')}
              </Button>
            </div>
          </Field>
        </form>

        {sale && returnable.length === 0 ? (
          <EmptyState
            icon={ReceiptText}
            title={t('nx.exc.nothingLeftTitle')}
            description={t('nx.exc.nothingLeftDesc')}
          />
        ) : null}

        {sale && returnable.length > 0 ? (
          <>
            <div className="grid gap-5 lg:grid-cols-2">
              <Panel
                title={t('nx.exc.comingBack')}
                description={t('nx.exc.comingBackDesc')}
                flush
              >
                <table className="w-full text-body">
                  <caption className="sr-only">{t('nx.exc.comingBack')}</caption>
                  <thead>
                    <tr className="border-b border-line-strong bg-surface-sunken">
                      <th scope="col" className="px-3 py-2 text-start text-label text-muted">
                        {t('nx.exc.colItem')}
                      </th>
                      <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                        {t('nx.exc.colCanReturn')}
                      </th>
                      <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                        {t('nx.exc.colReturning')}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {returnable.map((l) => {
                      const entered = back[l.line_id] ?? '';
                      const over = overReturnable(l, entered);
                      return (
                        <tr key={l.line_id} className="border-b border-line last:border-b-0">
                          <td className="px-3 py-2.5">{l.description}</td>
                          <td className="num px-3 py-2.5 text-end font-medium tabular-nums">
                            {formatQuantity(l.qty_returnable, market)}
                          </td>
                          <td className="px-3 py-2.5 text-end">
                            <input
                              value={entered}
                              onChange={(e) =>
                                setBack((q) => ({ ...q, [l.line_id]: e.target.value }))
                              }
                              inputMode="decimal"
                              // Capped by what the SERVER says. How much has
                              // already gone back lives in credit notes a till
                              // that was offline never saw.
                              max={l.qty_returnable}
                              aria-invalid={over || undefined}
                              aria-label={`${l.description} — ${t('nx.exc.colReturning')}`}
                              className={cn(
                                'num h-11 w-20 rounded-sm border bg-input-bg',
                                'text-center text-body tabular-nums [direction:ltr]',
                                over ? 'border-critical' : 'border-input',
                              )}
                            />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                {returnable.some((l) => overReturnable(l, back[l.line_id] ?? '')) ? (
                  <p className="border-t border-line px-3 py-2 text-caption text-critical-fg">
                    {t('nx.exc.tooMany')}
                  </p>
                ) : null}
              </Panel>

              <Panel
                title={t('nx.exc.goingOut')}
                description={t('nx.exc.goingOutDesc')}
              >
                <form onSubmit={onScan} className="mb-3">
                  <Field label={t('nx.exc.scan')}>
                    <Input
                      ref={scanBox}
                      value={term}
                      onChange={(e) => {
                        setTerm(e.target.value);
                        // A scanner types fast and ends with Enter, so nothing
                        // is looked up until there is enough to be a name.
                        setMatches(
                          e.target.value.trim().length >= 2
                            ? (catalogue?.search(e.target.value) ?? [])
                            : [],
                        );
                      }}
                      placeholder={t('nx.exc.scanPlaceholder')}
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </Field>
                </form>

                {matches.length > 0 ? (
                  <ul className="mb-3 flex flex-col gap-1">
                    {matches.slice(0, 6).map((m) => (
                      <li key={m.variantId}>
                        <button
                          type="button"
                          onClick={() => ring(m)}
                          className={cn(
                            'flex min-h-11 w-full items-center justify-between gap-3',
                            'rounded-sm border border-line px-3 py-2 text-start text-body',
                            'hover:bg-surface-hover',
                          )}
                        >
                          <span className="min-w-0 truncate">{m.description}</span>
                          <span className="num shrink-0 tabular-nums">
                            {money(m.price)}
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                ) : null}

                {out.length === 0 ? (
                  <p className="text-caption text-subtle">{t('nx.exc.nothingOutYet')}</p>
                ) : (
                  <ul className="flex flex-col divide-y divide-line">
                    {out.map((l) => (
                      <li key={l.variantId} className="flex items-center gap-2 py-2">
                        <span className="min-w-0 flex-1 truncate">{l.description}</span>
                        <input
                          value={l.qty}
                          onChange={(e) =>
                            setOut((c) => setCartQty(c, l.variantId, e.target.value))
                          }
                          inputMode="decimal"
                          aria-label={`${l.description} — ${t('nx.exc.qty')}`}
                          className={cn(
                            'num h-11 w-16 shrink-0 rounded-sm border border-input',
                            'bg-input-bg text-center text-body tabular-nums [direction:ltr]',
                          )}
                        />
                        <span className="num w-24 shrink-0 text-end tabular-nums">
                          {money(
                            (
                              Number.parseFloat(l.qty || '0') *
                              Number.parseFloat(l.unitPrice || '0')
                            ).toFixed(2),
                          )}
                        </span>
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`${t('nx.exc.remove')} ${l.description}`}
                          onClick={() => setOut((c) => removeLine(c, l.variantId))}
                        >
                          <Trash2 aria-hidden="true" />
                        </Button>
                      </li>
                    ))}
                  </ul>
                )}
              </Panel>
            </div>

            <Field label={t('nx.exc.reason')} hint={t('nx.exc.reasonHint')} required>
              <Textarea
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                rows={2}
              />
            </Field>

            <div className="rounded-md border border-line bg-surface p-4">
              <dl className="flex flex-wrap gap-x-8 gap-y-2 text-body">
                <div>
                  <dt className="text-label text-muted">{t('nx.exc.creditTotal')}</dt>
                  <dd className="num tabular-nums">{money(credit)}</dd>
                </div>
                <div>
                  <dt className="text-label text-muted">
                    {t('nx.exc.replacementTotal')}
                  </dt>
                  <dd className="num tabular-nums">{money(replacement)}</dd>
                </div>
              </dl>

              {/* The one figure that matters, and which way it goes. */}
              <p className="mt-3 border-t border-line pt-3">
                {settlement.direction === 'even' ? (
                  <span className="text-lede text-muted">{t('nx.exc.evenSwap')}</span>
                ) : (
                  <>
                    <span className="text-label text-muted">
                      {settlement.direction === 'customer_pays'
                        ? t('nx.exc.customerPays')
                        : t('nx.exc.shopPays')}
                    </span>
                    <span
                      className={cn(
                        'num mt-0.5 flex items-baseline gap-1.5 text-display font-semibold tabular-nums',
                        settlement.direction === 'customer_pays'
                          ? 'text-fg'
                          : 'text-critical-fg',
                      )}
                    >
                      <span className="text-lede font-medium text-muted">{currency}</span>
                      {money(settlement.amount)}
                    </span>
                  </>
                )}
              </p>

              {settlement.direction !== 'even' ? (
                <div className="mt-4 border-t border-line pt-4">
                  <p className="text-label font-medium text-fg">{t('nx.exc.settle')}</p>
                  <p className="mt-0.5 text-caption text-muted">
                    {t('nx.exc.settleHint')}
                  </p>

                  <div className="mt-3 flex flex-wrap gap-2">
                    {TENDER_METHODS.map((m) => (
                      <Button
                        key={m.id}
                        size="lg"
                        // One press settles it exactly, which is the whole of
                        // what this control has to do: the amount is not the
                        // cashier's to choose.
                        onClick={() =>
                          setTenders([{ method: m.id, amount: settlement.amount }])
                        }
                      >
                        {t(m.labelKey)}
                        <span className="num ms-1.5 tabular-nums">
                          {t('nx.exc.exactly', { amount: money(settlement.amount) })}
                        </span>
                      </Button>
                    ))}
                    {tenders.length > 0 ? (
                      <Button variant="ghost" size="lg" onClick={() => setTenders([])}>
                        {t('nx.exc.remove')}
                      </Button>
                    ) : null}
                  </div>

                  {tenders.length > 0 ? (
                    <p className="mt-2 text-caption text-muted">
                      {t('nx.exc.offered')}:{' '}
                      <span className="num tabular-nums">{money(String(offered))}</span>
                    </p>
                  ) : null}

                  {!settles(tenders, settlement) ? (
                    <p className="mt-2 text-caption text-caution-fg">
                      {t('nx.exc.stillNeeded', {
                        amount: money(
                          (
                            Number.parseFloat(settlement.amount) - offered
                          ).toFixed(2),
                        ),
                      })}
                    </p>
                  ) : null}
                </div>
              ) : null}

              <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-4">
                <p className="text-caption text-muted">
                  {state.ok
                    ? null
                    : state.reason === 'nothing_back'
                      ? t('nx.exc.needBack')
                      : state.reason === 'nothing_out'
                        ? t('nx.exc.needOut')
                        : state.reason === 'no_reason'
                          ? t('nx.exc.needReason')
                          : t('nx.exc.needSettlement')}
                </p>
                <Button
                  variant="primary"
                  size="lg"
                  busy={busy}
                  busyLabel={t('nx.exc.completing')}
                  disabled={!state.ok}
                  onClick={() => void submit()}
                >
                  {t('nx.exc.complete')}
                </Button>
              </div>
            </div>
          </>
        ) : null}
      </div>
    </Frame>
  );
}

function Frame({
  counterName,
  children,
}: {
  counterName: string;
  children: React.ReactNode;
}) {
  const t = useT();
  return (
    <div className="flex min-h-dvh flex-col bg-ground">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface px-3">
        <a
          href="/pos"
          className="flex h-9 items-center gap-1.5 rounded-sm px-2 text-label text-muted hover:bg-surface-hover hover:text-fg"
        >
          <ArrowLeft className="size-4 rtl:rotate-180" aria-hidden="true" />
          {t('nx.pos.backToTill')}
        </a>
        <p className="min-w-0 flex-1 truncate text-label font-medium text-fg">
          {counterName}
        </p>
        <Badge tone="info">{t('nx.exc.title')}</Badge>
      </header>
      {children}
    </div>
  );
}

export default function ExchangesPage() {
  return (
    <RequirePermission anyOf={['sales.exchange']}>
      <ExchangeScreen />
    </RequirePermission>
  );
}
