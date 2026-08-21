// Invoice detail, UI spec §5 — "most-viewed record".
//
// Laid out as a DOCUMENT rather than as a dashboard of cards, because that is
// what it is and what the person reading it has in their hand. A shop owner
// opens this with a customer on the phone quoting a number; the number, the
// date, the lines and the total have to be findable without reading a legend.
//
// # No edit button, no delete button, no void
//
// Spec §5 is explicit, and equally explicit about how the absence is handled:
// where a user expects one, explain. A disabled button teaches nothing and a
// missing button reads as a bug. The panel names the rule, names the reason,
// and offers the thing that IS correct — a credit note. That is the difference
// between a user trusting the system and a user trying to work around it.
//
// Reprint is offered and is logged. Reprinting is not reissuing: no new
// document, no new number, no new chain position.
//
// # Colour is information here, and only information
//
// The design system's rule. The document is neutral; the only colour on the
// page is the state badge, the credit-note direction, and the one panel that
// explains an immutability rule. A screen where everything is emphasised has
// nothing emphasised.

import { useCallback, useEffect, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import { fetchInvoice, reprintInvoice, type Invoice } from '../api/invoice';
import { listCompanies } from '../api/companies';
import { logoObjectURL } from '../api/branding';
import { InvoiceState, invoiceStateHint } from '../dashboard/InvoiceState';
import { money } from '../ui/format';
import {
  auditActionName,
  chainStatus,
  documentTitle,
  hasAnyDiscount,
  isCreditNote,
  lineDiscount,
  orderedAudit,
  paymentSummary,
  settlementNote,
  stamp,
  taxTreatmentName,
} from './invoice';

type Load =
  | { state: 'loading' }
  | { state: 'ready'; invoice: Invoice }
  | { state: 'missing' }
  | { state: 'denied' }
  | { state: 'failed'; message: string; offline: boolean };

export function InvoiceDetailScreen({
  invoiceId,
  companyId,
  onBack,
  onOpenInvoice,
  onIssueCreditNote,
}: {
  invoiceId: string;
  companyId?: string;
  onBack: () => void;
  /** Opens another invoice — the one a credit note corrects. */
  onOpenInvoice?: (id: string) => void;
  /** Absent when this login cannot refund, which is how the action stays
   *  honest: the button is not offered to somebody the server would refuse. */
  onIssueCreditNote?: (invoice: Invoice) => void;
}) {
  const { client, can } = useAuth();
  const [load, setLoad] = useState<Load>({ state: 'loading' });
  const [logo, setLogo] = useState<string | null>(null);
  const [seller, setSeller] = useState<string | null>(null);
  const [reprinting, setReprinting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  const reload = useCallback(async () => {
    setLoad({ state: 'loading' });
    try {
      setLoad({ state: 'ready', invoice: await fetchInvoice(client, invoiceId) });
    } catch (err) {
      if (err instanceof RequestFailed) {
        // 404 rather than 403 for another tenant's invoice: a 403 would confirm
        // the record exists, which leaks across the boundary.
        if (err.status === 404) return setLoad({ state: 'missing' });
        if (err.status === 403) return setLoad({ state: 'denied' });
      }
      setLoad({
        state: 'failed',
        message: explain(err),
        offline: err instanceof Offline,
      });
    }
  }, [client, invoiceId]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // The seller's own name and mark, for the head of the document. I2 stores the
  // logo; this is the first surface that can carry one. Neither is essential —
  // the document reads correctly without them — so a failure here is silent
  // rather than a panel the reader has to dismiss.
  useEffect(() => {
    if (!companyId) return;
    let cancelled = false;
    let url: string | null = null;

    listCompanies(client)
      .then((found) => {
        if (cancelled) return;
        const mine = found.find((c) => c.id === companyId);
        setSeller(mine ? mine.trade_name || mine.legal_name : null);
      })
      .catch(() => undefined);

    logoObjectURL(client, companyId)
      .then((made) => {
        url = made;
        if (cancelled) URL.revokeObjectURL(made);
        else setLogo(made);
      })
      .catch(() => undefined);

    return () => {
      cancelled = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [client, companyId]);

  if (load.state === 'loading') {
    return (
      <Frame onBack={onBack}>
        <div className="ds-panel">
          <div className="ds-panel__body">
            <div className="ds-skeleton" style={{ blockSize: 320 }} />
          </div>
        </div>
      </Frame>
    );
  }

  if (load.state === 'missing' || load.state === 'denied') {
    return (
      <Frame onBack={onBack}>
        <div className="ds-panel">
          <div className="ds-state">
            <p className="ds-state__title">
              {load.state === 'denied'
                ? 'This account cannot view invoices'
                : 'That invoice was not found'}
            </p>
            <p className="ds-state__body">
              {load.state === 'denied'
                ? 'Looking up a sale needs permission to view sales. An owner can change that under Settings > People.'
                : 'It may belong to another business, or the reference may be wrong. Check the number and try again.'}
            </p>
          </div>
        </div>
      </Frame>
    );
  }

  if (load.state === 'failed') {
    return (
      <Frame onBack={onBack}>
        <div className="ds-panel">
          <div className="ds-state">
            <p className="ds-state__title">
              {load.offline ? 'This invoice could not be reached' : 'This invoice could not be read'}
            </p>
            <p className="ds-state__body">{load.message}</p>
            <button className="ds-btn ds-btn--secondary" onClick={() => void reload()}>
              Try again
            </button>
          </div>
        </div>
      </Frame>
    );
  }

  const { invoice } = load;
  const credit = isCreditNote(invoice);
  const payment = paymentSummary(invoice);
  const showDiscount = hasAnyDiscount(invoice);
  const mayRefund = can('sales.refund');

  async function reprint() {
    setProblem(null);
    setNotice(null);
    setReprinting(true);
    try {
      await reprintInvoice(client, invoiceId);
      setNotice('A reprint has been recorded against this invoice.');
      // Reloaded so the trail below shows what just happened rather than
      // making the reader take the notice on trust.
      await reload();
    } catch (err) {
      setProblem(explain(err));
    } finally {
      setReprinting(false);
    }
  }

  return (
    <Frame
      onBack={onBack}
      actions={
        <>
          <button
            className="ds-btn ds-btn--secondary"
            disabled={reprinting}
            onClick={() => void reprint()}
          >
            {reprinting ? 'Recording…' : 'Reprint'}
          </button>
          {!credit && mayRefund && onIssueCreditNote && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => onIssueCreditNote(invoice)}
            >
              Issue credit note
            </button>
          )}
        </>
      }
    >
      {notice && (
        <p className="inv__notice" role="status">
          {notice}
        </p>
      )}
      {problem && (
        <p className="inv__problem" role="alert">
          {problem}
        </p>
      )}

      {/* --- the document ------------------------------------------------ */}
      <article className="ds-panel inv__doc" aria-label={documentTitle(invoice.doc_type)}>
        <header className="inv__head">
          <div className="inv__seller">
            {logo && <img className="inv__logo" src={logo} alt="" />}
            {seller && <p className="inv__sellername">{seller}</p>}
          </div>

          <div className="inv__ident">
            <h1 className="ds-h2 inv__title">{documentTitle(invoice.doc_type)}</h1>
            <p className="inv__number num">
              {invoice.human_number ?? invoice.uuid.slice(0, 8)}
            </p>
            <p className="ds-caption">{invoice.issue_date}</p>
            <InvoiceState state={invoice.state} />
          </div>
        </header>

        <p className="ds-caption inv__statehint">{invoiceStateHint(invoice.state)}</p>

        <dl className="inv__parties">
          <div>
            <dt className="ds-caption">Billed to</dt>
            <dd>
              {invoice.customer ? (
                invoice.customer.name
              ) : (
                // Not a gap. A shop does not ask a name to sell a bottle of
                // water, and a simplified invoice does not require one.
                <span className="ds-subtle">Walk-in customer</span>
              )}
            </dd>
          </div>
          <div>
            <dt className="ds-caption">Currency</dt>
            <dd className="num">{invoice.currency}</dd>
          </div>
          {invoice.parent_invoice_id && (
            <div>
              <dt className="ds-caption">Corrects</dt>
              <dd>
                {onOpenInvoice ? (
                  <button
                    className="inv__link"
                    onClick={() => onOpenInvoice(invoice.parent_invoice_id!)}
                  >
                    {invoice.parent_invoice_id.slice(0, 8)}
                  </button>
                ) : (
                  <span className="num">{invoice.parent_invoice_id.slice(0, 8)}</span>
                )}
              </dd>
            </div>
          )}
        </dl>

        <div className="ds-scroll-x">
          <table className="ds-table inv__lines">
            <caption className="ds-visually-hidden">
              The items on this {documentTitle(invoice.doc_type).toLowerCase()}
            </caption>
            <thead>
              <tr>
                <th scope="col" className="inv__lineno">#</th>
                <th scope="col">Description</th>
                <th scope="col" className="num">Qty</th>
                <th scope="col" className="num">Unit</th>
                {showDiscount && <th scope="col" className="num">Discount</th>}
                <th scope="col">Tax</th>
                <th scope="col" className="num">Amount</th>
              </tr>
            </thead>
            <tbody>
              {invoice.lines.map((line) => (
                <tr key={line.line_no}>
                  <td className="inv__lineno num">{line.line_no}</td>
                  <td>
                    <span className="inv__desc">{line.description}</span>
                    {/* Arabic renders alongside rather than instead: a Saudi
                        tax invoice carries both, and the reader may want
                        either. */}
                    {line.description_ar && (
                      <span className="inv__descar" lang="ar" dir="rtl">
                        {line.description_ar}
                      </span>
                    )}
                  </td>
                  <td className="num">{line.qty}</td>
                  <td className="num">{money(line.unit_price)}</td>
                  {showDiscount && (
                    <td className="num">
                      {money(lineDiscount(line)) === '0.00' ? (
                        <span className="ds-subtle">—</span>
                      ) : (
                        money(lineDiscount(line))
                      )}
                    </td>
                  )}
                  <td>
                    <span className="inv__tax">{taxTreatmentName(line.tax_treatment)}</span>
                    {line.tax_treatment === 'standard' && (
                      <span className="ds-caption num">{line.tax_rate}%</span>
                    )}
                  </td>
                  <td className="num">{money(line.gross_amount)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* The totals block, ruled and right-aligned as on paper. The grand
            total is the one number the customer will ask about, so it is the
            only figure here given weight. */}
        <div className="inv__totals">
          <Total label="Subtotal" value={invoice.subtotal_net} currency={invoice.currency} />
          {money(invoice.discount_total) !== '0.00' && (
            <Total
              label="Discount"
              value={`-${invoice.discount_total}`}
              currency={invoice.currency}
            />
          )}
          <Total label="VAT" value={invoice.tax_total} currency={invoice.currency} />
          <Total
            label="Total"
            value={credit ? `-${invoice.total_inclusive}` : invoice.total_inclusive}
            currency={invoice.currency}
            grand
          />
        </div>

        <section className="inv__tenders" aria-label="Payment">
          <h2 className="ds-h3">{credit ? 'Refunded by' : 'Paid by'}</h2>
          {invoice.tenders.length === 0 ? (
            <p className="ds-subtle ds-body-sm">Nothing recorded against this document.</p>
          ) : (
            <ul className="inv__tenderlist">
              {invoice.tenders.map((t) => {
                const note = settlementNote(t.settlement_status);
                return (
                  <li key={t.tender_no}>
                    <span className="inv__tendername">{tenderLabel(t.method)}</span>
                    <span className="num">{money(t.amount, { currency: invoice.currency })}</span>
                    {t.reference && <span className="ds-caption">{t.reference}</span>}
                    {note && <span className="ds-caption inv__pending">{note}</span>}
                  </li>
                );
              })}
            </ul>
          )}

          {/* Shown only when it says something. An invoice settled in full does
              not need a line telling the reader it owes nothing. */}
          {!payment.settled && (
            <p className="inv__outstanding">
              <span>Still outstanding</span>
              <strong className="num">
                {money(payment.outstanding, { currency: invoice.currency })}
              </strong>
            </p>
          )}
        </section>
      </article>

      {/* --- what cannot be done, and what can --------------------------- */}
      {/*
        Spec §5, close to verbatim. The teaching moment: a user who came here
        looking for Edit or Delete is told the rule, the reason, and the correct
        alternative, rather than hunting for a button that was never going to
        exist.
      */}
      <section className="inv__immutable" aria-label="Why this cannot be changed">
        <h2 className="ds-h3">This invoice cannot be edited or deleted.</h2>
        <p className="ds-body-sm">
          Finalized tax invoices are immutable under ZATCA rules. To correct it,
          issue a <strong>Credit Note</strong>.
        </p>
        {credit ? (
          <p className="ds-caption">
            This document is itself a credit note. Correcting one means issuing
            a fresh invoice, not amending this.
          </p>
        ) : mayRefund && onIssueCreditNote ? (
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => onIssueCreditNote(invoice)}
          >
            Issue credit note
          </button>
        ) : (
          <p className="ds-caption">
            Issuing one needs permission to refund sales. An owner can change
            that under Settings &gt; People.
          </p>
        )}
      </section>

      <div className="inv__side">
        <ZatcaPanel invoice={invoice} />
        <AuditPanel invoice={invoice} />
      </div>
    </Frame>
  );
}

// --- the ZATCA panel ------------------------------------------------------

/** State, ICV, QR and chain position, as spec §5 names them.
 *
 * It must never overstate. While the byte-level format is an unverified release
 * blocker no invoice has been signed or reported, so the panel says which of
 * those is true rather than rendering an empty QR — a code that does not scan
 * is worse than no code. */
function ZatcaPanel({ invoice }: { invoice: Invoice }) {
  const status = chainStatus(invoice);

  return (
    <section className="ds-panel inv__panel" aria-label="E-invoicing">
      <div className="ds-panel__head">
        <h2 className="ds-h3">E-invoicing</h2>
        <InvoiceState state={invoice.state} />
      </div>
      <div className="ds-panel__body">
        {status === 'none' ? (
          <p className="ds-body-sm ds-muted">
            This document holds no chain position. A counter is consumed only
            when a legal document is issued.
          </p>
        ) : (
          <>
            <dl className="inv__facts">
              <div>
                <dt className="ds-caption">Invoice counter (ICV)</dt>
                <dd className="num">{invoice.zatca!.icv}</dd>
              </div>
              <div>
                <dt className="ds-caption">Schema</dt>
                <dd>{invoice.zatca!.schema_version}</dd>
              </div>
            </dl>

            <p className="ds-caption inv__hashlabel">Previous invoice hash (PIH)</p>
            <p className="inv__hash num">{invoice.zatca!.pih}</p>
            <p className="ds-caption inv__hashlabel">This invoice&rsquo;s hash</p>
            <p className="inv__hash num">{invoice.zatca!.invoice_hash}</p>

            {status === 'signed' ? (
              <p className="ds-body-sm">
                The terminal has signed this document and returned its QR
                payload.
              </p>
            ) : (
              // Honest, and specific about which half is missing.
              <p className="ds-body-sm ds-muted inv__unsigned">
                Position {invoice.zatca!.icv} on this terminal&rsquo;s chain is
                recorded and cannot move. The QR code and stamp are produced by
                the terminal when signing is enabled; this document has none
                yet.
              </p>
            )}

            {invoice.zatca!.reject_reason && (
              <p className="inv__reject" role="alert">
                {invoice.zatca!.reject_reason}
              </p>
            )}
          </>
        )}
      </div>
    </section>
  );
}

// --- the audit trail ------------------------------------------------------

/** What has happened to this document. The first question in a dispute is who
 *  reprinted it and when, and this is the only place that answers it. */
function AuditPanel({ invoice }: { invoice: Invoice }) {
  const entries = orderedAudit(invoice.audit);

  return (
    <section className="ds-panel inv__panel" aria-label="History">
      <div className="ds-panel__head">
        <h2 className="ds-h3">History</h2>
      </div>
      <div className="ds-panel__body">
        {entries.length === 0 ? (
          <p className="ds-body-sm ds-muted">
            Nothing has happened to this document since it was issued.
          </p>
        ) : (
          <ol className="inv__trail">
            {entries.map((e, i) => (
              <li key={`${e.occurred_at}-${i}`}>
                <span className="inv__trailwhen num">{stamp(e.occurred_at)}</span>
                <span className="inv__trailwhat">{auditActionName(e.action)}</span>
                <span className="ds-caption">
                  {e.actor_label ?? 'System'}
                  {e.device_label ? ` · ${e.device_label}` : ''}
                </span>
              </li>
            ))}
          </ol>
        )}
      </div>
    </section>
  );
}

// --- furniture ------------------------------------------------------------

function Frame({
  onBack,
  actions,
  children,
}: {
  onBack: () => void;
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <main className="inv">
      <div className="inv__bar">
        <button className="ds-btn ds-btn--quiet" onClick={onBack}>
          &larr; Back
        </button>
        <div className="inv__baractions">{actions}</div>
      </div>
      {children}
    </main>
  );
}

function Total({
  label,
  value,
  currency,
  grand,
}: {
  label: string;
  value: string;
  currency: string;
  grand?: boolean;
}) {
  return (
    <div className={`inv__total${grand ? ' inv__total--grand' : ''}`}>
      <span>{label}</span>
      <span className="num">{money(value, { currency })}</span>
    </div>
  );
}

/** Payment methods, as a shop says them. Kept here rather than imported from
 *  the till's own list because that one lives in the POS package. */
function tenderLabel(method: string): string {
  const named: Record<string, string> = {
    cash: 'Cash',
    mada: 'Mada',
    visa: 'Visa',
    mastercard: 'Mastercard',
    amex: 'Amex',
    apple_pay: 'Apple Pay',
    stc_pay: 'STC Pay',
    bank_transfer: 'Bank transfer',
    store_credit: 'Store credit',
    customer_due: 'On account',
    tabby: 'Tabby',
    tamara: 'Tamara',
  };
  return named[method] ?? method.replace(/_/g, ' ');
}

function explain(err: unknown): string {
  if (err instanceof Offline) {
    return (
      'This invoice lives on the server and cannot be read while the ' +
      'connection is down. Nothing about it has changed.'
    );
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) return 'This login is not allowed to do that.';
    return err.message;
  }
  return err instanceof Error ? err.message : 'Something went wrong.';
}
