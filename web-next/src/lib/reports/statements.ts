// The four statements, and the return.
//
// Almost nothing here computes: the server adds the ledger up and says whether
// the balance sheet balances, whether the VAT return reconciles, and what is
// stopping it. A client that recomputed any of those would be a second answer
// that could disagree with the first, on the numbers a business is taxed on.
//
// What this file holds is the SHAPES, the period a screen opens on, and the one
// judgement worth encoding: whether a return is in a state anybody should be
// filing from.

import Decimal from 'decimal.js';

/** A figure that is not a figure yet reads as zero rather than as NaN. */
function decimalOf(raw: string | undefined): Decimal {
  const value = (raw ?? '').trim();
  if (value === '' || /[.+-]$/.test(value)) return new Decimal(0);
  try {
    const d = new Decimal(value);
    return d.isFinite() ? d : new Decimal(0);
  } catch {
    return new Decimal(0);
  }
}

/** One line of a statement. */
export interface StatementLine {
  account_id?: string;
  code: string;
  name: string;
  amount: string;
}

export interface TrialBalanceRow {
  account_id: string;
  code: string;
  name: string;
  type: string;
  debit: string;
  credit: string;
}

export interface TrialBalance {
  as_of: string;
  base_currency: string;
  rows: TrialBalanceRow[];
}

export interface ProfitAndLoss {
  from: string;
  to: string;
  base_currency: string;
  revenue: StatementLine[];
  revenue_total: string;
  cost_of_sales: StatementLine[];
  cost_of_sales_total: string;
  gross_profit: string;
  expenses: StatementLine[];
  expenses_total: string;
  net_profit: string;
}

export interface BalanceSheet {
  as_of: string;
  base_currency: string;
  assets: StatementLine[];
  assets_total: string;
  liabilities: StatementLine[];
  liabilities_total: string;
  equity: StatementLine[];
  equity_total: string;
  /** This year's profit, which is equity that has not been closed off yet. */
  current_earnings: string;
  equity_and_liabilities: string;
  difference: string;
  /** The server's answer, never recomputed here. */
  balanced: boolean;
}

export interface CashFlow {
  from: string;
  to: string;
  base_currency: string;
  opening: string;
  closing: string;
  in: StatementLine[];
  out: StatementLine[];
}

export interface VatSupply {
  treatment: string;
  net_amount: string;
  tax_amount: string;
  invoice_count: number;
}

export interface VatReturn {
  country: string;
  from: string;
  to: string;
  base_currency: string;
  /** What the market calls it. Not every country levies a VAT. */
  model: string;
  supplies: VatSupply[];
  total_net: string;
  output_tax_total: string;
  input_tax_total: string;
  /** Tax on bills and expenses, which should equal the Input VAT account. */
  billed_input_tax: string;
  input_difference: string;
  net_payable: string;
  ledger_output_tax: string;
  difference: string;
  /** Whether the return agrees with the ledger it was drawn from. */
  reconciled: boolean;
  /**
   * Why it cannot be filed, in the server's own words.
   *
   * This is the most important field on the whole payload. The service reports
   * an unverified form layout, a mismatch against the Input VAT account, and
   * tax held behind the three-way match — and a screen that showed the totals
   * without them would be presenting an unfiled draft as a filing.
   */
  outstanding?: string[];
  filed: boolean;
}

/**
 * Whether anybody should be filing from this.
 *
 * Three conditions and all of them the server's: it reconciles, nothing is
 * outstanding, and it has not already been filed. Never a judgement made here
 * — "never invent regulatory confirmations" is the rule, and deciding a return
 * is ready would be exactly that.
 */
export function readyToFile(vat: VatReturn): boolean {
  return vat.reconciled && (vat.outstanding?.length ?? 0) === 0 && !vat.filed;
}

/**
 * The period a financial screen opens on: this year, to today.
 *
 * Year to date rather than this month, because the question somebody opens a
 * profit and loss to answer is "how are we doing", and a month on its own
 * answers it only in December. The dates are ISO and built from the local
 * calendar date, never from a timestamp: a shop in Dhaka opening the screen at
 * nine in the morning is in a different UTC day, and a report that quietly
 * started yesterday would be wrong in a way nobody would notice.
 */
export function yearToDate(today: Date = new Date()): { from: string; to: string } {
  const year = today.getFullYear();
  const month = String(today.getMonth() + 1).padStart(2, '0');
  const day = String(today.getDate()).padStart(2, '0');
  return { from: `${year}-01-01`, to: `${year}-${month}-${day}` };
}

/**
 * What the cash position moved by over the period.
 *
 * `closing` less `opening`, both of which the server states. The in and out
 * lists carry no totals of their own and none is invented here: summing them
 * would be a second answer to a question the two balances already settle, and
 * the two could disagree the moment the server groups a line differently.
 */
export function netMovement(flow: Pick<CashFlow, 'opening' | 'closing'>): string {
  const open = decimalOf(flow.opening);
  const close = decimalOf(flow.closing);
  return close.minus(open).toDecimalPlaces(2, Decimal.ROUND_HALF_UP).toFixed(2);
}

/** The four statements a business reads, in the order they are read in. */
export const STATEMENTS = ['profit', 'balance', 'cash', 'trial'] as const;
export type StatementKind = (typeof STATEMENTS)[number];

/** Whether a statement covers a period or stands at a date. */
export function isPeriodic(kind: StatementKind): boolean {
  return kind === 'profit' || kind === 'cash';
}
