// Giving money back.
//
// # This one needs the network, and that is a decision, not a gap
//
// Every other counter operation works offline. A return does not, because the
// question it has to answer — how much of this invoice has already been given
// back — cannot be answered on the terminal. Credit notes raised at another
// till, or at this one while it was offline, are invisible here. A till that
// guessed would refund the same jacket twice, and the second refund would be
// real money leaving a real drawer.
//
// So the till asks the server, and when it cannot reach the server it says so
// and refuses. That is the right failure: a customer told "I can't process a
// return until we're back online" has been inconvenienced, where one refunded
// twice is a loss the shop absorbs and may never notice.
//
// Blueprint C14 gives a return nine atomic effects — credit note, stock
// restoration, cost reversal, VAT reversal, tender reversal, drawer movement,
// journal entry, loyalty and commission. All nine belong to the server, and
// none of them is reimplemented here. This module builds a request and reads
// the answer.

import type { Client } from '@rawsyst/shared/api/client';

/** One line of the original sale, with what is still claimable on it. */
export interface ReturnableLine {
  line_id: string;
  line_no: number;
  variant_id?: string;
  description: string;
  qty_sold: string;
  qty_returned: string;
  qty_returnable: string;
  unit_price: string;
  tax_treatment: string;
  tax_rate: string;
  net_returnable: string;
  tax_returnable: string;
  gross_returnable: string;
}

/** What the cashier has chosen to take back. */
export interface ReturnSelection {
  lineId: string;
  qty: string;
}

export interface RefundTender {
  method: string;
  amount: string;
  reference?: string;
  /** The original tender being reversed, when there is one. A card refund
   *  should go back to the card it came from. */
  reverses_tender_id?: string;
}

/** One sale, as much of it as is needed to be sure it is the right one. */
export interface InvoiceMatch {
  id: string;
  uuid: string;
  doc_type: string;
  human_number?: string | null;
  state: string;
  issue_date: string;
  currency: string;
  total_inclusive: string;
}

/**
 * Turns what is on the receipt into the invoice it names.
 *
 * A till never learns the id the server keeps a sale under. It generates the
 * document UUID, queues the sale under it, prints a prefix of it on the
 * receipt, and the push response tells it applied or failed and nothing more.
 * Every other sales route takes sales_invoice.id — a different UUID minted
 * server-side — so sending the cashier's scan straight to one of them found
 * nothing, ever, for any sale made at any terminal.
 *
 * The server resolves the document UUID, a prefix of it, the human number, or
 * the invoice id, because a cashier holding a receipt cannot be expected to
 * know which of those they are looking at.
 */
export function lookUpSale(
  client: Client,
  reference: string,
): Promise<InvoiceMatch> {
  return client.send<InvoiceMatch>(
    'GET',
    `/api/v1/pos/sales/lookup?reference=${encodeURIComponent(reference)}`,
  );
}

/** Asks the server what is still owed back. */
export async function fetchReturnable(
  client: Client,
  invoiceId: string,
): Promise<ReturnableLine[]> {
  const body = await client.send<{ lines: ReturnableLine[] }>(
    'GET',
    `/api/v1/pos/sales/${encodeURIComponent(invoiceId)}/returnable`,
  );
  return body.lines ?? [];
}

export interface ReturnResult {
  credit_note_id: string;
  credit_note_uuid: string;
  human_number?: string;
  total_inclusive: string;
}

/**
 * Raises the credit note.
 *
 * The UUID is generated here, before the call, which is what makes a retry
 * safe: a network failure after the server committed would otherwise have the
 * cashier press refund again and give the money back twice. Same reasoning as
 * a sale's invoice UUID, same mechanism.
 *
 * No warehouse is sent. Where the stock goes back to is resolved from the
 * registered device, exactly as it is on a sale — a terminal that could name
 * its own warehouse could restore stock into another store's.
 */
export function submitReturn(
  client: Client,
  input: {
    creditNoteUuid: string;
    originalInvoiceId: string;
    reason: string;
    lines: ReturnSelection[];
    refunds: RefundTender[];
  },
): Promise<ReturnResult> {
  return client.send<ReturnResult>('POST', '/api/v1/pos/returns', {
    credit_note_uuid: input.creditNoteUuid,
    original_invoice_id: input.originalInvoiceId,
    issued_at: new Date().toISOString(),
    reason: input.reason,
    lines: input.lines.map((l) => ({ line_id: l.lineId, qty: l.qty })),
    refunds: input.refunds,
  });
}

/**
 * What the selected lines come to.
 *
 * Proportional to the quantity taken back, computed from the line's own
 * returnable amount rather than from the unit price. The two differ whenever
 * an invoice-level discount was allocated across the lines, and using the unit
 * price would refund more than the customer paid — by a little on each line,
 * and reliably in the shop's disfavour.
 *
 * This figure is for the cashier to see before they commit. The server
 * recomputes it and is the authority; a mismatch means the terminal is wrong.
 */
export function refundTotal(
  lines: ReturnableLine[],
  selection: ReturnSelection[],
): string {
  let cents = 0;
  for (const pick of selection) {
    const line = lines.find((l) => l.line_id === pick.lineId);
    if (!line) continue;

    const takingBack = Number(pick.qty);
    const available = Number(line.qty_returnable);
    if (!Number.isFinite(takingBack) || takingBack <= 0 || available <= 0) {
      continue;
    }

    const share = Math.min(takingBack, available) / available;
    cents += Math.round(toMinor(line.gross_returnable) * share);
  }
  return toMajor(cents);
}

/** Whether the cashier has asked for more than the invoice can give back. */
export function overReturned(
  lines: ReturnableLine[],
  selection: ReturnSelection[],
): ReturnSelection[] {
  return selection.filter((pick) => {
    const line = lines.find((l) => l.line_id === pick.lineId);
    if (!line) return true;
    const qty = Number(pick.qty);
    return !Number.isFinite(qty) || qty < 0 || qty > Number(line.qty_returnable);
  });
}

function toMinor(amount: string): number {
  const [whole = '0', frac = ''] = amount.replace('-', '').split('.');
  return Number(whole) * 100 + Number((frac + '00').slice(0, 2));
}

function toMajor(cents: number): string {
  return `${Math.floor(cents / 100)}.${String(cents % 100).padStart(2, '0')}`;
}
