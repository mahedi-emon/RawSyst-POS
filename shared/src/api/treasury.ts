// Cash and bank (blueprint C2), and the reconciliation (C11).
//
// # A balance is never sent as a number
//
// Every figure here crosses as a decimal string, like all money in this
// product. A bank balance turned into a JavaScript number to be displayed and
// back to be compared is a balance that can be out by a hundredth on a screen
// whose entire purpose is to prove two figures agree.
//
// # The interesting field is `difference`
//
// It is the closing balance the bank states, less the ledger balance, less
// everything on both exception lists. It must be nil to sign a statement off,
// and a screen that showed only the two totals would hide the one number that
// says whether they can be trusted.

import type { Client } from './client';

/** A place money sits. */
export interface MoneyAccount {
  id: string;
  /** `cash`, `petty_cash`, `bank`, `card_settlement` or `gateway`. */
  kind: string;
  name: string;
  name_ar?: string;
  currency: string;
  store?: string;
  is_active: boolean;

  account_id: string;
  account_code: string;

  bank_name?: string;
  account_number?: string;
  iban?: string;
  swift?: string;

  /** Summed from the ledger every time it is read. Never stored. */
  balance: string;
  /** Ledger lines on this account that no statement has ever seen. Only
   *  meaningful on a bank-like account, and the number a person opens this
   *  screen for. */
  unreconciled?: number;
}

/** Money moved between two of the company's own accounts. */
export interface MoneyTransfer {
  id: string;
  transfer_no: string;
  from: string;
  to: string;
  amount: string;
  currency: string;
  moved_on: string;
  reference?: string;
  note?: string;
  created_by?: string;
  created_at: string;
  already_recorded?: boolean;
}

/** One line as the bank stated it. */
export interface StatementLine {
  id: string;
  value_date: string;
  description: string;
  reference?: string;
  /** Signed from the BANK's point of view: positive is money arriving. */
  amount: string;
  matched_to?: string;
  /** `automatic` or `manual`. A rule's match is a guess; a person's is a
   *  decision, and an auditor should be able to tell them apart. */
  match_kind?: string;
  matched_by?: string;
}

/** A line in the books no statement line has claimed. */
export interface UnmatchedLedgerLine {
  id: string;
  entry_date: string;
  entry_no: string;
  memo?: string;
  amount: string;
  source_type?: string;
}

export interface BankStatement {
  id: string;
  account: string;
  currency: string;
  starts_on: string;
  ends_on: string;
  opening_balance: string;
  closing_balance: string;
  status: 'draft' | 'reconciled';
  reference?: string;
  reconciled_by?: string;
  reconciled_at?: string;

  ledger_balance: string;
  difference: string;
  reconciled: boolean;

  lines?: StatementLine[];
  /** C11's exception report, and the more useful of the two lists: the cheque
   *  that never cleared, and the payment recorded twice. */
  unmatched_in_books?: UnmatchedLedgerLine[];
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

// --- accounts -------------------------------------------------------------

export function listMoneyAccounts(
  client: Client,
  companyId: string,
  includeRetired = false,
): Promise<{ data: MoneyAccount[] }> {
  return client.send<{ data: MoneyAccount[] }>(
    'GET',
    scoped(
      '/api/v1/treasury/accounts' + (includeRetired ? '?include_retired=true' : ''),
      companyId,
    ),
  );
}

export function createMoneyAccount(
  client: Client,
  companyId: string,
  body: {
    kind: string;
    name: string;
    name_ar?: string;
    currency?: string;
    store_id?: string;
    account_id: string;
    bank_name?: string;
    account_number?: string;
    iban?: string;
    swift?: string;
  },
): Promise<MoneyAccount> {
  return client.send<MoneyAccount>(
    'POST',
    scoped('/api/v1/treasury/accounts', companyId),
    body,
  );
}

export function setMoneyAccountActive(
  client: Client,
  companyId: string,
  id: string,
  isActive: boolean,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/treasury/accounts/${id}/active`, companyId),
    { is_active: isActive },
  );
}

// --- transfers ------------------------------------------------------------

export function listMoneyTransfers(
  client: Client,
  companyId: string,
): Promise<{ data: MoneyTransfer[] }> {
  return client.send<{ data: MoneyTransfer[] }>(
    'GET',
    scoped('/api/v1/treasury/transfers', companyId),
  );
}

export function moveMoney(
  client: Client,
  companyId: string,
  body: {
    /** Assigned by the caller, so a retry after a lost response returns the
     *  original rather than banking the takings twice. */
    uuid: string;
    from_account_id: string;
    to_account_id: string;
    amount: string;
    moved_on: string;
    reference?: string;
    note?: string;
  },
): Promise<MoneyTransfer> {
  return client.send<MoneyTransfer>(
    'POST',
    scoped('/api/v1/treasury/transfers', companyId),
    body,
  );
}

// --- reconciliation -------------------------------------------------------

export function listStatements(
  client: Client,
  companyId: string,
  accountId?: string,
): Promise<{ data: BankStatement[] }> {
  const q = accountId ? `?account_id=${encodeURIComponent(accountId)}` : '';
  return client.send<{ data: BankStatement[] }>(
    'GET',
    scoped('/api/v1/treasury/statements' + q, companyId),
  );
}

export function readStatement(
  client: Client,
  companyId: string,
  id: string,
): Promise<BankStatement> {
  return client.send<BankStatement>(
    'GET',
    scoped(`/api/v1/treasury/statements/${id}`, companyId),
  );
}

export function importStatement(
  client: Client,
  companyId: string,
  body: {
    account_id: string;
    starts_on: string;
    ends_on: string;
    opening_balance: string;
    closing_balance: string;
    reference?: string;
    lines: Array<{
      value_date: string;
      description: string;
      reference?: string;
      amount: string;
    }>;
  },
): Promise<BankStatement> {
  return client.send<BankStatement>(
    'POST',
    scoped('/api/v1/treasury/statements', companyId),
    body,
  );
}

/** Points a statement line at a ledger entry, or at nothing.
 *
 *  An empty id undoes the match. One call rather than two, because pointing a
 *  line at an entry and pointing it at nothing are the same edit and a person
 *  toggles between them. */
export function matchStatementLine(
  client: Client,
  companyId: string,
  lineId: string,
  journalLineId: string,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/treasury/lines/${lineId}/match`, companyId),
    { journal_line_id: journalLineId },
  );
}

export function reconcileStatement(
  client: Client,
  companyId: string,
  id: string,
): Promise<BankStatement> {
  return client.send<BankStatement>(
    'POST',
    scoped(`/api/v1/treasury/statements/${id}/reconcile`, companyId),
  );
}
