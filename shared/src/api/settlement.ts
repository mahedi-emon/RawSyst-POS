// Card settlement (P15, blueprint C12).
//
// The shop took money on a card. Two days later the acquirer deposits it, less
// a fee. Until that deposit is recorded the takings sit in a clearing account:
// real money, on the balance sheet, that the shop cannot spend and cannot tie
// to a bank statement.
//
// Money is a STRING here as everywhere else. The net deposit is the one figure
// in this module that comes from outside the system — it is read off a bank
// statement — and putting it through a JavaScript number on the way in would
// put the rounding error into the ledger rather than into a display.

import type { Client } from './client';

/** One payment taken and not yet deposited. */
export interface PendingTender {
  tender_id: string;
  invoice_id: string;
  invoice_number: string;
  issued_at: string;
  method: string;
  reference?: string;
  amount: string;
  /** What the invoice this payment was taken against was issued in.
   *
   *  Read from the document rather than from the company, so a figure always
   *  says what it actually is. */
  currency: string;
}

/** One payment inside a recorded deposit, with the share of the fee it bore. */
export interface SettledTender {
  tender_id: string;
  invoice_id: string;
  invoice_number: string;
  method: string;
  amount: string;
  fee_amount: string;
}

/** A deposit as it appears on a bank statement. */
export interface SettlementBatch {
  id: string;
  reference: string;
  deposited_on: string;
  gross_amount: string;
  fee_amount: string;
  net_amount: string;
  tenders: SettledTender[];
  already_recorded?: boolean;
  /** The company's, not a document's: a batch is a deposit into a bank account
   *  and its gross, fee and net are figures in the books. */
  currency: string;
}

export interface SettlementBody {
  /** Assigned before the request, so a retry after a lost response returns the
   *  original deposit rather than clearing the same payments twice. */
  uuid: string;
  reference: string;
  deposited_on: string;
  net_amount: string;
  tender_ids: string[];
}

export function listPendingSettlement(
  client: Client,
  companyId: string,
): Promise<PendingTender[]> {
  return client
    .send<{ data: PendingTender[] }>(
      'GET',
      `/api/v1/settlement/pending?company_id=${companyId}`,
    )
    .then((b) => b.data ?? []);
}

export function recordSettlement(
  client: Client,
  companyId: string,
  body: SettlementBody,
): Promise<SettlementBatch> {
  return client.send<SettlementBatch>(
    'POST',
    `/api/v1/settlement/batches?company_id=${companyId}`,
    body,
  );
}

export function readSettlement(
  client: Client,
  companyId: string,
  batchId: string,
): Promise<SettlementBatch> {
  return client.send<SettlementBatch>(
    'GET',
    `/api/v1/settlement/batches/${batchId}?company_id=${companyId}`,
  );
}
