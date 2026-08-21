// What the invoice screen works out for itself.
//
// Not much, deliberately. The totals are the server's and are shown as they
// arrived — a screen that re-added the lines would eventually disagree with the
// paper the customer is holding, and on a tax invoice that is not a rounding
// curiosity but a document that no longer says the same thing twice.
//
// What is here is presentation: how a document type is named, how an audit
// action reads in English, and the one arithmetic the screen genuinely needs —
// what is still unpaid, which no field on the invoice carries.

import { major, minor } from '../receivables/receivables';
import type { Invoice, InvoiceAuditEntry, InvoiceLine } from '../api/invoice';

/** How a document type is titled at the top of the page. */
export function documentTitle(docType: string): string {
  const named: Record<string, string> = {
    standard: 'Tax Invoice',
    simplified: 'Simplified Tax Invoice',
    credit_note: 'Credit Note',
    debit_note: 'Debit Note',
  };
  return named[docType] ?? docType.replace(/_/g, ' ');
}

/** True for a document that takes money back rather than in. Its figures read
 *  as negatives on screen, because that is the direction the money went. */
export function isCreditNote(invoice: Invoice): boolean {
  return invoice.doc_type === 'credit_note';
}

/**
 * What the tenders come to, and what is left owing.
 *
 * The one figure the screen computes. `total_inclusive` and each tender amount
 * are on the document; the difference between them is not, and a part-paid
 * invoice is exactly the case somebody opens this screen to check.
 *
 * BigInt minor units, never `Number(x)`: float64 cannot hold 0.15, and an
 * invoice that reported one hallala outstanding forever would be a support call
 * nobody could close.
 */
export function paymentSummary(invoice: Invoice): {
  paid: string;
  outstanding: string;
  settled: boolean;
} {
  const total = minor(invoice.total_inclusive);
  const paid = invoice.tenders.reduce((sum, t) => sum + minor(t.amount), 0n);
  const outstanding = total - paid;
  return {
    paid: major(paid),
    // Never negative on screen: over-tendering is change given, not a debt the
    // shop owes, and the till has already handled it.
    outstanding: major(outstanding > 0n ? outstanding : 0n),
    settled: outstanding <= 0n,
  };
}

/** A line's own discount and its share of any invoice-level one, together.
 *  Shown as one figure because a customer asking "why is this not 449" does
 *  not care which of the two it was. */
export function lineDiscount(line: InvoiceLine): string {
  return major(minor(line.line_discount) + minor(line.invoice_discount_alloc));
}

/** True when any line carried a discount, so the column is shown only when it
 *  has something in it. A column of dashes is a column that costs width and
 *  earns nothing. */
export function hasAnyDiscount(invoice: Invoice): boolean {
  return invoice.lines.some((l) => minor(lineDiscount(l)) !== 0n);
}

/** How a tax treatment reads. The database values are precise and are not
 *  English. */
export function taxTreatmentName(treatment: string): string {
  const named: Record<string, string> = {
    standard: 'Standard',
    zero_rated: 'Zero-rated',
    exempt: 'Exempt',
    out_of_scope: 'Out of scope',
  };
  return named[treatment] ?? treatment.replace(/_/g, ' ');
}

/** How a settlement status reads. Only shown when it is not the ordinary one:
 *  a column saying "settled" on every row tells nobody anything. */
export function settlementNote(status: string): string | null {
  const named: Record<string, string> = {
    pending: 'Awaiting settlement',
    settled: '',
    failed: 'Settlement failed',
    charged_back: 'Charged back',
  };
  const found = named[status];
  if (found === undefined) return status.replace(/_/g, ' ');
  return found === '' ? null : found;
}

/** How an audited action reads in the trail. Anything unrecognised is shown as
 *  itself rather than guessed at — an action added server-side must not be
 *  rendered as something reassuring. */
export function auditActionName(action: string): string {
  const named: Record<string, string> = {
    invoice_issued: 'Issued',
    invoice_reprinted: 'Reprinted',
    invoice_submitted: 'Submitted to ZATCA',
    invoice_credit_noted: 'Credit note raised',
    signed_document_attached: 'Signed by the terminal',
  };
  return named[action] ?? action.replace(/_/g, ' ');
}

/** The trail, newest first, as the server already orders it — restated here so
 *  a screen cannot accidentally depend on the order arriving right. */
export function orderedAudit(entries: InvoiceAuditEntry[]): InvoiceAuditEntry[] {
  return [...entries].sort((a, b) => b.occurred_at.localeCompare(a.occurred_at));
}

/** A date and time, short enough for a dense trail. Deliberately not
 *  locale-formatted: "16 Aug 14:32" reads the same in every market this product
 *  serves, where 8/16 and 16/8 do not. */
export function stamp(iso: string): string {
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  const match = /^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2}))?/.exec(iso);
  if (!match) return iso;
  const [, , m, d, hh, mm] = match;
  const month = months[Number(m) - 1] ?? m;
  const day = `${Number(d)} ${month}`;
  return hh ? `${day} ${hh}:${mm}` : day;
}

/**
 * Whether the ZATCA panel has anything real to show.
 *
 * A chain position exists as soon as a legal document is issued; the QR and the
 * stamp arrive only once the terminal has signed, which is gated while the
 * byte-level format is unverified. The panel says which of those is true rather
 * than rendering an empty code — a QR that does not scan is worse than none.
 */
export function chainStatus(invoice: Invoice): 'none' | 'positioned' | 'signed' {
  if (!invoice.zatca) return 'none';
  return invoice.zatca.qr_tlv ? 'signed' : 'positioned';
}
