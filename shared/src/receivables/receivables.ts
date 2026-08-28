// The customer screens' own decisions, separated from their rendering.
//
// Four things here decide something rather than lay something out: how a credit
// account is described to a cashier, what a receipt form should pre-fill, whether
// the allocation adds up, and how an ageing row is read. Each has a failure mode
// that costs money or embarrasses somebody in front of a customer, so each lives
// here where it can be tested.
//
// The arithmetic is in BigInt minor units throughout. float64 cannot hold 0.15,
// and a receipt form that quietly drifts by a fraction of a halala is a receipt
// form that will one day refuse a payment that exactly settles an invoice.

import type { Translate } from '../i18n/strings';

import type { AgeingRow, Customer, LedgerRow, OpenInvoice } from '../api/receivables';

/** Two decimal places, as minor units. Anything else came from a bug. */
export function minor(amount: string): bigint {
  const trimmed = (amount ?? '').trim();
  if (trimmed === '') return 0n;

  const negative = trimmed.startsWith('-');
  const digits = negative ? trimmed.slice(1) : trimmed;
  const [whole, fraction = ''] = digits.split('.');
  const padded = (fraction + '00').slice(0, 2);
  const value = BigInt(whole || '0') * 100n + BigInt(padded || '0');
  return negative ? -value : value;
}

/** Back to a string the same shape the server sends. */
export function major(units: bigint): string {
  const negative = units < 0n;
  const absolute = negative ? -units : units;
  const whole = absolute / 100n;
  const fraction = absolute % 100n;
  const text = `${whole}.${fraction.toString().padStart(2, '0')}`;
  return negative ? `-${text}` : text;
}

// --- What a credit account means -----------------------------------------

export type CreditStanding =
  | { kind: 'none'; message: string }
  | { kind: 'clear'; available: string; message: string }
  | { kind: 'near_limit'; available: string; message: string }
  | { kind: 'at_limit'; message: string };

/**
 * How to describe a customer's credit account.
 *
 * A cashier about to put a sale on account needs to know before they start, not
 * after the server refuses them in front of the customer. The server is still
 * the authority — 11-pos-and-sales.md §5 says the sale is REFUSED on breach, and
 * this changes nothing about that. It only decides what to say up front.
 *
 * "Near the limit" is drawn at a tenth remaining rather than a fixed amount,
 * because a shop with a 500 limit and a shop with a 500,000 one do not consider
 * the same number tight.
 */
export function creditStanding(
  customer: Customer,
  translate?: Translate,
): CreditStanding {
  if (!customer.credit_limit) {
    return {
      kind: 'none',
      message:
        translate?.('credit.none') ??
        'No credit account. Sales to this customer must be paid at the till.',
    };
  }

  const limit = minor(customer.credit_limit);
  const available = minor(customer.available ?? '0');

  if (available <= 0n) {
    return {
      kind: 'at_limit',
      message:
        translate?.('credit.atLimit') ??
        'At their credit limit. Nothing further can go on this account.',
    };
  }
  // A tenth of the limit, and never treat a zero limit as a division.
  if (limit > 0n && available * 10n <= limit) {
    return {
      kind: 'near_limit',
      available: major(available),
      message:
        translate?.('credit.onlyLeft', {
          available: major(available),
          limit: major(limit),
        }) ?? `Only ${major(available)} left of a ${major(limit)} limit.`,
    };
  }
  return {
    kind: 'clear',
    available: major(available),
    message:
      translate?.('credit.availableOf', {
        available: major(available),
        limit: major(limit),
      }) ?? `${major(available)} available of a ${major(limit)} limit.`,
  };
}

// --- Receipts ------------------------------------------------------------

/**
 * What to pre-fill a receipt form with, given the money received.
 *
 * OLDEST FIRST, and only as a starting point the person can change. This is the
 * opposite of the server, which refuses to guess: the server stores what was
 * decided and a wrong guess there becomes a statement the customer disputes.
 * Here nobody has decided yet, and a form that starts on the oldest invoice
 * matches what a shop does when a customer hands over a round sum.
 *
 * Whatever cannot be placed is returned as `unallocated`, so the form can say so
 * rather than silently dropping it.
 */
export function allocateOldestFirst(
  invoices: OpenInvoice[],
  received: string,
): { allocations: Record<string, string>; unallocated: string } {
  let left = minor(received);
  const allocations: Record<string, string> = {};

  const oldest = [...invoices].sort((a, b) =>
    a.due_date === b.due_date
      ? a.issue_date.localeCompare(b.issue_date)
      : a.due_date.localeCompare(b.due_date),
  );

  for (const invoice of oldest) {
    if (left <= 0n) break;
    const outstanding = minor(invoice.outstanding);
    if (outstanding <= 0n) continue;

    const take = left < outstanding ? left : outstanding;
    allocations[invoice.invoice_id] = major(take);
    left -= take;
  }

  return { allocations, unallocated: major(left) };
}

export type AllocationProblem =
  | { kind: 'nothing' }
  | { kind: 'over_invoice'; invoice: string; outstanding: string; amount: string }
  | { kind: 'ok'; total: string };

/**
 * Whether a receipt's allocation can be sent.
 *
 * The server checks all of this again and would refuse — this exists so the
 * person filling the form finds out while the numbers are in front of them
 * rather than after a round trip.
 */
export function checkAllocation(
  invoices: OpenInvoice[],
  allocations: Record<string, string>,
): AllocationProblem {
  const byId = new Map(invoices.map((i) => [i.invoice_id, i]));
  let total = 0n;

  for (const [invoiceId, raw] of Object.entries(allocations)) {
    const amount = minor(raw);
    if (amount === 0n) continue;

    const invoice = byId.get(invoiceId);
    if (!invoice) continue;

    const outstanding = minor(invoice.outstanding);
    if (amount > outstanding) {
      return {
        kind: 'over_invoice',
        invoice: invoice.human_number || invoice.invoice_id.slice(0, 8),
        outstanding: invoice.outstanding,
        amount: major(amount),
      };
    }
    total += amount;
  }

  if (total <= 0n) return { kind: 'nothing' };
  return { kind: 'ok', total: major(total) };
}

// --- Ageing --------------------------------------------------------------

/**
 * The worst bucket a customer has anything in.
 *
 * What a collections list sorts and colours by. Reading the row left to right
 * and taking the last non-zero bucket is the same question a person asks of the
 * table, which is why it is computed this way rather than from a stored flag.
 */
export function worstBucket(
  row: AgeingRow,
): 'not_due' | '0_30' | '31_60' | '61_90' | '90_plus' | 'none' {
  if (minor(row.days_90_plus) > 0n) return '90_plus';
  if (minor(row.days_61_90) > 0n) return '61_90';
  if (minor(row.days_31_60) > 0n) return '31_60';
  if (minor(row.days_0_30) > 0n) return '0_30';
  if (minor(row.not_due) > 0n) return 'not_due';
  return 'none';
}

/** How overdue is overdue enough to chase. Drives the row's emphasis only. */
export function ageingTone(
  bucket: ReturnType<typeof worstBucket>,
): 'neutral' | 'warning' | 'danger' {
  switch (bucket) {
    case '90_plus':
    case '61_90':
      return 'danger';
    case '31_60':
    case '0_30':
      return 'warning';
    default:
      return 'neutral';
  }
}

/** Column totals, so the table can foot itself without the server recomputing. */
export function ageingTotals(rows: AgeingRow[]): {
  not_due: string;
  days_0_30: string;
  days_31_60: string;
  days_61_90: string;
  days_90_plus: string;
  total: string;
} {
  const sum = (pick: (r: AgeingRow) => string) =>
    major(rows.reduce((acc, r) => acc + minor(pick(r)), 0n));

  return {
    not_due: sum((r) => r.not_due),
    days_0_30: sum((r) => r.days_0_30),
    days_31_60: sum((r) => r.days_31_60),
    days_61_90: sum((r) => r.days_61_90),
    days_90_plus: sum((r) => r.days_90_plus),
    total: sum((r) => r.total),
  };
}

/**
 * Whether this statement row can be reversed from the screen.
 *
 * Only a live payment. A sale is reversed by a credit note, a credit note is
 * not a receipt, and a reversal is itself a document — reversing that would
 * be editing history by another name. An already-reversed payment stays on
 * the statement; the control does not.
 */
export function canReversePayment(row: LedgerRow): boolean {
  return row.kind === 'receipt' && !row.reversed && Boolean(row.source_id);
}
