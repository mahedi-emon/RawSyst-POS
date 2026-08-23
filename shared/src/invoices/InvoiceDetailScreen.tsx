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
import {
  fetchTemplates,
  logoObjectURL,
  type DocType,
  type DocumentTemplate,
} from '../api/branding';
import { InvoiceState, invoiceStateHint } from '../dashboard/InvoiceState';
import { money } from '../ui/format';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
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
  const t = useT();
  const { client, can } = useAuth();
  const [load, setLoad] = useState<Load>({ state: 'loading' });
  const [logo, setLogo] = useState<string | null>(null);
  const [seller, setSeller] = useState<string | null>(null);
  // The shop's stationery (I2 / P35). All four types are fetched together and
  // the right one is picked at render, so this does not have to wait for the
  // invoice to know its own document type. Empty while it loads and after a
  // failure: a document reads correctly without it, so a failure here is silent
  // rather than a panel somebody has to dismiss.
  const [templates, setTemplates] = useState<DocumentTemplate[]>([]);
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
        message: explain(err, t),
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

    fetchTemplates(client, companyId)
      .then((all) => {
        if (!cancelled) setTemplates(all);
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
                ? t('inv.noPermissionView')
                : t('inv.notFound')}
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
              {load.offline ? t('inv.unreachable') : t('inv.unreadable')}
            </p>
            <p className="ds-state__body">{load.message}</p>
            <button className="ds-btn ds-btn--secondary" onClick={() => void reload()}>
              {t('common.tryAgain')}
            </button>
          </div>
        </div>
      </Frame>
    );
  }

  const { invoice } = load;
  const credit = isCreditNote(invoice);
  const template =
    templates.find((t) => t.doc_type === (invoice.doc_type as DocType)) ?? null;
  const payment = paymentSummary(invoice);
  const showDiscount = hasAnyDiscount(invoice);
  const mayRefund = can('sales.refund');

  async function reprint() {
    setProblem(null);
    setNotice(null);
    setReprinting(true);
    try {
      await reprintInvoice(client, invoiceId);
      setNotice(t('inv.reprintRecorded'));
      // Reloaded so the trail below shows what just happened rather than
      // making the reader take the notice on trust.
      await reload();
    } catch (err) {
      setProblem(explain(err, t));
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
            {reprinting ? 'Recording…' : t('inv.reprint')}
          </button>
          {!credit && mayRefund && onIssueCreditNote && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => onIssueCreditNote(invoice)}
            >
              {t('inv.issueCreditNote')}
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
            {/* The client's own mark, unless this document type is set to go
                without one. A shop with a logo that wants a plain credit note
                should not have to delete the logo to get one. */}
            {logo && template?.show_logo !== false && (
              <img className="inv__logo" src={logo} alt="" />
            )}
            {seller && <p className="inv__sellername">{seller}</p>}
            {/* What the shop wrote for the head of this document type. */}
            {template?.header_text && (
              <p className="inv__block">{template.header_text}</p>
            )}
            {template?.header_text_ar && (
              <p className="inv__block" lang="ar" dir="rtl">
                {template.header_text_ar}
              </p>
            )}
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

        <p className="ds-caption inv__statehint">{invoiceStateHint(invoice.state, t)}</p>

        <dl className="inv__parties">
          <div>
            <dt className="ds-caption">{t('inv.billedTo')}</dt>
            <dd>
              {invoice.customer ? (
                invoice.customer.name
              ) : (
                // Not a gap. A shop does not ask a name to sell a bottle of
                // water, and a simplified invoice does not require one.
                <span className="ds-subtle">{t('inv.walkIn')}</span>
              )}
            </dd>
          </div>
          <div>
            <dt className="ds-caption">{t('common.currency')}</dt>
            <dd className="num">{invoice.currency}</dd>
          </div>
          {invoice.parent_invoice_id && (
            <div>
              <dt className="ds-caption">{t('inv.corrects')}</dt>
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
                <th scope="col">{t('common.description')}</th>
                <th scope="col" className="num">{t('common.qty')}</th>
                <th scope="col" className="num">{t('common.unit')}</th>
                {showDiscount && <th scope="col" className="num">{t('common.discount')}</th>}
                <th scope="col">{t('inv.tax')}</th>
                <th scope="col" className="num">{t('common.amount')}</th>
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
          <Total label={t('common.subtotal')} value={invoice.subtotal_net} currency={invoice.currency} />
          {money(invoice.discount_total) !== '0.00' && (
            <Total
              label={t('common.discount')}
              value={`-${invoice.discount_total}`}
              currency={invoice.currency}
            />
          )}
          <Total label={t('common.vat')} value={invoice.tax_total} currency={invoice.currency} />
          <Total
            label={t('common.total')}
            value={credit ? `-${invoice.total_inclusive}` : invoice.total_inclusive}
            currency={invoice.currency}
            grand
          />
        </div>

        <section className="inv__tenders" aria-label={t('inv.payment')}>
          <h2 className="ds-h3">{credit ? 'Refunded by' : 'Paid by'}</h2>
          {invoice.tenders.length === 0 ? (
            <p className="ds-subtle ds-body-sm">{t('inv.nothingRecorded')}</p>
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
              <span>{t('inv.stillOutstanding')}</span>
              <strong className="num">
                {money(payment.outstanding, { currency: invoice.currency })}
              </strong>
            </p>
          )}
        </section>

        {/* --- what the shop writes at the foot of this document ---------- */}
        {/*
          I2's remaining blocks. Presentation only: none of this can reach a
          figure, a party or a date, which is why a template may be changed
          after a document has been issued without touching what it recorded.
          A reprint carries today's stationery, and that is a copy on new
          letterhead rather than a reissue of the record.
        */}
        {template && hasFooterBlocks(template) && (
          <footer className="inv__foot">
            {(template.payment_terms || template.payment_terms_ar) && (
              <div className="inv__blockgroup">
                <h2 className="ds-caption">{t('inv.payment')}</h2>
                {template.payment_terms && (
                  <p className="inv__block">{template.payment_terms}</p>
                )}
                {template.payment_terms_ar && (
                  <p className="inv__block" lang="ar" dir="rtl">
                    {template.payment_terms_ar}
                  </p>
                )}
              </div>
            )}

            {(template.return_policy || template.return_policy_ar) && (
              <div className="inv__blockgroup">
                <h2 className="ds-caption">{t('inv.returns')}</h2>
                {template.return_policy && (
                  <p className="inv__block">{template.return_policy}</p>
                )}
                {template.return_policy_ar && (
                  <p className="inv__block" lang="ar" dir="rtl">
                    {template.return_policy_ar}
                  </p>
                )}
              </div>
            )}

            {(template.footer_text || template.footer_text_ar) && (
              <div className="inv__blockgroup inv__blockgroup--closing">
                {template.footer_text && (
                  <p className="inv__block">{template.footer_text}</p>
                )}
                {template.footer_text_ar && (
                  <p className="inv__block" lang="ar" dir="rtl">
                    {template.footer_text_ar}
                  </p>
                )}
              </div>
            )}
          </footer>
        )}
      </article>

      {/* --- what cannot be done, and what can --------------------------- */}
      {/*
        Spec §5, close to verbatim. The teaching moment: a user who came here
        looking for Edit or Delete is told the rule, the reason, and the correct
        alternative, rather than hunting for a button that was never going to
        exist.
      */}
      <section className="inv__immutable" aria-label={t('inv.cannotBeChanged')}>
        <h2 className="ds-h3">{t('inv.cannotEditDelete')}</h2>
        <p className="ds-body-sm">{t('inv.immutable')}<strong>{t('inv.creditNote')}</strong>.
        </p>
        {credit ? (
          <p className="ds-caption">{t('inv.isCreditNote')}</p>
        ) : mayRefund && onIssueCreditNote ? (
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => onIssueCreditNote(invoice)}
          >
            {t('inv.issueCreditNote')}
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
  const t = useT();
  const status = chainStatus(invoice);

  return (
    <section className="ds-panel inv__panel" aria-label={t('egs.einvoicing')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('egs.einvoicing')}</h2>
        <InvoiceState state={invoice.state} />
      </div>
      <div className="ds-panel__body">
        {status === 'none' ? (
          <p className="ds-body-sm ds-muted">{t('inv.noChainPosition')}</p>
        ) : (
          <>
            <dl className="inv__facts">
              <div>
                <dt className="ds-caption">{t('inv.icv')}</dt>
                <dd className="num">{invoice.zatca!.icv}</dd>
              </div>
              <div>
                <dt className="ds-caption">{t('inv.schema')}</dt>
                <dd>{invoice.zatca!.schema_version}</dd>
              </div>
            </dl>

            <p className="ds-caption inv__hashlabel">{t('inv.pih')}</p>
            <p className="inv__hash num">{invoice.zatca!.pih}</p>
            <p className="ds-caption inv__hashlabel">{t('inv.thisHash')}</p>
            <p className="inv__hash num">{invoice.zatca!.invoice_hash}</p>

            {status === 'signed' ? (
              <p className="ds-body-sm">{t('inv.terminalSigned')}</p>
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
  const t = useT();
  const entries = orderedAudit(invoice.audit);

  return (
    <section className="ds-panel inv__panel" aria-label={t('common.history')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('common.history')}</h2>
      </div>
      <div className="ds-panel__body">
        {entries.length === 0 ? (
          <p className="ds-body-sm ds-muted">
            {t('inv.nothingHappened')}
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

/** Whether a template has anything to print at the foot of a document. An
 *  empty footer element would draw a rule under nothing. */
function hasFooterBlocks(t: DocumentTemplate): boolean {
  return Boolean(
    t.footer_text ||
      t.footer_text_ar ||
      t.return_policy ||
      t.return_policy_ar ||
      t.payment_terms ||
      t.payment_terms_ar,
  );
}

function explain(err: unknown, t: (key: Key) => string): string {
  if (err instanceof Offline) {
    return (
      t('inv.offlineOnServer') +
      'connection is down. Nothing about it has changed.'
    );
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) return t('common.loginNotAllowed');
    return err.message;
  }
  return err instanceof Error ? err.message : t('common.somethingWrong');
}
