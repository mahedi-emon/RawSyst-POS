'use client';

// Taking goods back.
//
// # Why this lives inside the POS and not the back office
//
// `POST /pos/returns` goes through `resolveTerminal`, which refuses a session
// that is not bound to a counter. That is not an oversight: a credit note joins
// the terminal's own invoice chain, and one raised from a browser with no till
// behind it would break the sequence. So a refund happens where a refund
// actually happens — at a counter, with the customer in front of somebody.
//
// # The till must never work out what may go back
//
// How much of a line has already been returned lives in the credit notes
// against the invoice, which a terminal that was offline when they were raised
// has never seen. The failure mode is refunding the same jacket twice. So
// `GET /pos/sales/{id}/returnable` is the only source, and the quantity boxes
// are capped by what it says.
//
// # What the return has NOT done is shown
//
// The server reports `effects_outstanding` — the effects of a return it has not
// carried out, loyalty points and commission among them. A till that showed
// "return complete" while points were still outstanding would be lying by
// omission, so the screen says which.

import { ArrowLeft, ReceiptText, Search } from 'lucide-react';
import { useState, type FormEvent } from 'react';

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
import { newSaleId } from '@/lib/pos/cart';
import { useCounter } from '@/lib/pos/counter';
import { cn } from '@/lib/utils';
import { Decimal } from 'decimal.js';

interface ReturnableLine {
  line_id: string;
  line_no: number;
  variant_id?: string;
  description: string;
  qty_sold: string;
  qty_returned: string;
  qty_returnable: string;
  unit_price: string;
  tax_treatment: string;
  gross_returnable: string;
}

interface LookedUpSale {
  id: string;
  human_number?: string;
  issued_at?: string;
  total_inclusive?: string;
}

interface CreditNote {
  credit_note_id: string;
  human_number?: string;
  subtotal_net: string;
  tax_total: string;
  total_inclusive: string;
  zatca?: { icv: number };
  /** Effects a return has NOT carried out. Empty is the ordinary case. */
  effects_outstanding?: string[];
}

function ReturnsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const { state: counter } = useCounter();

  const [reference, setReference] = useState('');
  const [sale, setSale] = useState<LookedUpSale | null>(null);
  const [lines, setLines] = useState<ReturnableLine[]>([]);
  const [qty, setQty] = useState<Record<string, string>>({});
  const [reason, setReason] = useState('');
  const [noteId, setNoteId] = useState(() => newSaleId());
  const [looking, setLooking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);
  const [done, setDone] = useState<CreditNote | null>(null);

  const money = (v: string | null | undefined) =>
    formatMoney(v ?? null, { currency, market, bare: true });

  /** What the chosen quantities come to, at the invoice's own prices. */
  const refundTotal = lines.reduce((sum, l) => {
    const n = qty[l.line_id];
    if (!n) return sum;
    const returning = new Decimal(n || '0');
    if (returning.lessThanOrEqualTo(0)) return sum;
    // Pro-rata on the line's own returnable gross, so tax and any discount
    // already allocated to it come back in the same proportion.
    const per = new Decimal(l.gross_returnable || '0').dividedBy(
      new Decimal(l.qty_returnable || '1'),
    );
    return sum.plus(per.times(returning));
  }, new Decimal(0));

  const anythingChosen = refundTotal.greaterThan(0);

  async function lookUp(e: FormEvent) {
    e.preventDefault();
    const ref = reference.trim();
    if (!ref || looking) return;
    setLooking(true);
    setError(null);
    setSale(null);
    setLines([]);
    setQty({});
    try {
      const found = await api.get<LookedUpSale>('/pos/sales/lookup', {
        query: { ...scope, reference: ref },
      });
      setSale(found);
      const returnable = await api.get<{ lines: ReturnableLine[] }>(
        `/pos/sales/${found.id}/returnable`,
      );
      setLines(returnable.lines ?? []);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setError(t('nx.ret.notFound'));
      } else {
        setError(messageFor(err, t));
      }
    } finally {
      setLooking(false);
    }
  }

  async function submit() {
    if (!sale || !anythingChosen || busy) return;
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    try {
      const note = await api.post<CreditNote>(
        '/pos/returns',
        {
          credit_note_uuid: noteId,
          original_invoice_id: sale.id,
          issued_at: new Date().toISOString(),
          reason: reason.trim(),
          lines: Object.entries(qty)
            .filter(([, n]) => n && new Decimal(n).greaterThan(0))
            .map(([line_id, n]) => ({ line_id, qty: n })),
          // No refunds array: the money going back is settled at the drawer.
          // Sending one would claim a tender reversal the till has not made.
          refunds: [],
        },
        // The same key for every attempt, so a retry after a lost response
        // returns the original credit note rather than issuing a second.
        { idempotencyKey: noteId },
      );
      setDone(note);
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFieldErrors(err.fields);
      setError(messageFor(err, t));
    } finally {
      setBusy(false);
    }
  }

  function again() {
    setNoteId(newSaleId());
    setReference('');
    setSale(null);
    setLines([]);
    setQty({});
    setReason('');
    setDone(null);
    setError(null);
    setFieldErrors(null);
  }

  const counterName =
    counter.kind === 'open'
      ? `${counter.counter.terminal_label} · ${counter.counter.store}`
      : '';

  if (done) {
    return (
      <Frame counterName={counterName}>
        <div className="mx-auto flex w-full max-w-md flex-col gap-4 p-6 text-center">
          <p className="text-label text-muted">{t('nx.ret.done')}</p>
          {done.human_number ? (
            <p className="num text-section font-semibold text-fg">
              {done.human_number}
            </p>
          ) : null}
          <p className="num text-figure font-semibold tabular-nums text-critical-fg">
            <span className="text-section font-medium text-muted">{currency} </span>
            {money(done.total_inclusive)}
          </p>
          <dl className="flex justify-center gap-5 text-caption text-muted">
            <div>
              <dt>{t('nx.pos.net')}</dt>
              <dd className="num tabular-nums text-fg">{done.subtotal_net}</dd>
            </div>
            <div>
              <dt>{t('nx.pos.tax')}</dt>
              <dd className="num tabular-nums text-fg">{done.tax_total}</dd>
            </div>
          </dl>

          {/* What the return did NOT do. Saying nothing here would be lying by
              omission on a screen a customer is standing at. */}
          {done.effects_outstanding && done.effects_outstanding.length > 0 ? (
            <p className="rounded-sm border border-caution/25 bg-caution-subtle p-2.5 text-caption text-caution-fg">
              {t('nx.ret.outstanding', {
                items: done.effects_outstanding.join(', '),
              })}
            </p>
          ) : null}

          <Button variant="primary" size="lg" block className="mt-2" onClick={again}>
            {t('nx.ret.nextReturn')}
          </Button>
        </div>
      </Frame>
    );
  }

  return (
    <Frame counterName={counterName}>
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-5 p-4">
        <div>
          <h1 className="text-page font-semibold text-fg">{t('nx.ret.title')}</h1>
          <p className="mt-1 text-body text-muted">{t('nx.ret.subtitle')}</p>
        </div>

        <FormError message={error} fields={fieldErrors} />

        <form onSubmit={lookUp}>
          <Field label={t('nx.ret.reference')} required>
            <div className="flex gap-2">
              <div className="relative flex flex-1 items-center">
                <Search
                  className="pointer-events-none absolute start-3 size-4 text-muted"
                  aria-hidden="true"
                />
                <Input
                  value={reference}
                  onChange={(e) => setReference(e.target.value)}
                  placeholder={t('nx.ret.referencePlaceholder')}
                  className="ps-9"
                  autoComplete="off"
                  spellCheck={false}
                  autoCorrect="off"
                  autoFocus
                />
              </div>
              <Button type="submit" variant="secondary" busy={looking}>
                {t('nx.ret.lookUp')}
              </Button>
            </div>
          </Field>
        </form>

        {sale && lines.length === 0 ? (
          <EmptyState
            icon={ReceiptText}
            title={t('nx.ret.nothingLeftTitle')}
            description={t('nx.ret.nothingLeftDesc')}
          />
        ) : null}

        {sale && lines.length > 0 ? (
          <>
            <Panel
              title={t('nx.ret.whatCanGoBack')}
              description={t('nx.ret.whatCanGoBackDesc')}
              flush
            >
              <table className="w-full text-body">
                <caption className="sr-only">{t('nx.ret.caption')}</caption>
                <thead>
                  <tr className="border-b border-line-strong bg-surface-sunken">
                    <th scope="col" className="px-3 py-2 text-start text-label text-muted">
                      {t('nx.ret.colItem')}
                    </th>
                    <th scope="col" className="hidden px-3 py-2 text-end text-label text-muted sm:table-cell">
                      {t('nx.ret.colSold')}
                    </th>
                    <th scope="col" className="hidden px-3 py-2 text-end text-label text-muted sm:table-cell">
                      {t('nx.ret.colAlready')}
                    </th>
                    <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                      {t('nx.ret.colCanReturn')}
                    </th>
                    <th scope="col" className="px-3 py-2 text-end text-label text-muted">
                      {t('nx.ret.colReturning')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l) => {
                    const cap = l.qty_returnable;
                    return (
                      <tr key={l.line_id} className="border-b border-line last:border-b-0">
                        <td className="px-3 py-2.5">{l.description}</td>
                        <td className="num hidden px-3 py-2.5 text-end tabular-nums text-muted sm:table-cell">
                          {formatQuantity(l.qty_sold, market)}
                        </td>
                        <td className="num hidden px-3 py-2.5 text-end tabular-nums text-muted sm:table-cell">
                          {isZero(l.qty_returned)
                            ? '—'
                            : formatQuantity(l.qty_returned, market)}
                        </td>
                        <td className="num px-3 py-2.5 text-end font-medium tabular-nums">
                          {formatQuantity(cap, market)}
                        </td>
                        <td className="px-3 py-2.5 text-end">
                          <input
                            value={qty[l.line_id] ?? ''}
                            onChange={(e) =>
                              setQty((q) => ({ ...q, [l.line_id]: e.target.value }))
                            }
                            inputMode="decimal"
                            // Capped by what the SERVER says is returnable. The
                            // till never works this out for itself.
                            max={cap}
                            aria-label={`${l.description} — ${t('nx.ret.colReturning')}`}
                            className={cn(
                              'num h-10 w-20 rounded-sm border border-input bg-input-bg',
                              'text-center text-body tabular-nums [direction:ltr]',
                            )}
                          />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </Panel>

            <Field label={t('nx.ret.reason')} hint={t('nx.ret.reasonHint')}>
              <Textarea value={reason} onChange={(e) => setReason(e.target.value)} />
            </Field>

            <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-line bg-surface p-4">
              <div>
                <p className="text-label text-muted">{t('nx.ret.refundTotal')}</p>
                <p className="num mt-0.5 flex items-baseline gap-1.5 text-display font-semibold tabular-nums">
                  <span className="text-lede font-medium text-muted">{currency}</span>
                  {money(refundTotal.toFixed(2))}
                </p>
              </div>
              <Button
                variant="destructive"
                size="lg"
                busy={busy}
                busyLabel={t('nx.ret.completing')}
                disabled={!anythingChosen}
                onClick={() => void submit()}
              >
                {t('nx.ret.complete')}
              </Button>
            </div>

            {!anythingChosen ? (
              <p className="text-caption text-subtle">{t('nx.ret.nothingSelected')}</p>
            ) : null}
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
        <Badge tone="caution">{t('nx.ret.title')}</Badge>
      </header>
      {children}
    </div>
  );
}

export default function ReturnsPage() {
  return (
    <RequirePermission anyOf={['sales.refund']}>
      <ReturnsScreen />
    </RequirePermission>
  );
}
