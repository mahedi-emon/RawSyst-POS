// Whether this sale may go on this customer's account.
//
// The till's answer, not the authority. 11-pos-and-sales.md §5 is explicit that
// `customer_due` "is refused when it would breach the customer's credit limit
// (B16)", and the server enforces that under a row lock on the customer — two
// tills that each checked the same headroom and both passed is exactly how a
// limit of 1,000 quietly becomes 2,000, and only a lock prevents it.
//
// What lives here is the decision about what to OFFER. A cashier must not put a
// sale through, hand over the goods, and only then discover the account was
// full; and offline they must not be able to start one they have no way to
// check. So this refuses early, in words a cashier can repeat to the customer.
//
// # Arithmetic in minor units
//
// float64 cannot hold 0.15, and a credit check that drifted by a fraction of a
// halala would eventually refuse a sale that exactly reaches the limit — the
// commonest case in a shop that sets round limits.

import type { Translate } from '@rawsyst/shared/i18n/strings';

import { minor, major } from '@rawsyst/shared/receivables/receivables';
import type { CachedCustomer } from '../offline/customers';

/** A customer as the till holds them, from either the server or the cache. */
export interface CounterCustomer {
  id: string;
  code: string;
  name: string;
  phone: string;
  paymentTermsDays: number;
  creditLimit: string;
  balance: string;
  available: string;
  isActive: boolean;
  /** True when this came from the local cache rather than from the server, so
   *  the screen can say the figures are as at the last sync rather than now. */
  stale: boolean;
}

export function fromCache(c: CachedCustomer): CounterCustomer {
  return {
    id: c.id,
    code: c.code,
    name: c.name,
    phone: c.phone,
    paymentTermsDays: c.paymentTermsDays,
    creditLimit: c.creditLimit,
    balance: c.balance,
    available: c.available,
    isActive: c.isActive,
    stale: true,
  };
}

export type CreditVerdict =
  /** No customer chosen. Nothing can go on account. */
  | { kind: 'no_customer'; message: string }
  /** Chosen, but they have no credit account at all. */
  | { kind: 'no_account'; message: string }
  /** Chosen and retired. */
  | { kind: 'retired'; message: string }
  /** Room for this amount. */
  | { kind: 'ok'; available: string; message: string }
  /** Not enough room. `most` is what WOULD fit, so the cashier can offer it. */
  | { kind: 'over_limit'; available: string; most: string; message: string };

/**
 * Whether `amount` may go on this customer's account.
 *
 * `amount` is what would be put on account by this sale — not the sale total.
 * A sale half in cash and half on account only draws down the half on account,
 * and checking the total would refuse sales that are perfectly affordable.
 */
export function creditVerdict(
  customer: CounterCustomer | null,
  amount: string,
  // The reader's language. A parameter, not a hook, so this stays a pure
  // function the tests call directly; English when it is absent.
  translate?: Translate,
): CreditVerdict {
  if (!customer) {
    return {
      kind: 'no_customer',
      message:
        translate?.('till.chooseCustomer') ??
        'Choose a customer before putting a sale on account.',
    };
  }
  if (!customer.isActive) {
    return {
      kind: 'retired',
      message:
        translate?.('till.retiredAccount', { customer: customer.name }) ??
        `${customer.name} is no longer an active account.`,
    };
  }
  if (!customer.creditLimit) {
    return {
      kind: 'no_account',
      message:
        translate?.('till.noCreditAccount', { customer: customer.name }) ??
        `${customer.name} has no credit account, so this sale has to be paid ` +
          'now. An owner can set a credit limit.',
    };
  }

  const available = minor(customer.available || '0');
  const wanted = minor(amount);

  if (wanted > available) {
    return {
      kind: 'over_limit',
      available: major(available),
      most: major(available > 0n ? available : 0n),
      message:
        available > 0n
          ? (translate?.('till.partialCredit', {
              customer: customer.name,
              available: major(available),
              limit: customer.creditLimit,
              wanted: major(wanted),
            }) ??
            `${customer.name} has ${major(available)} left of a ` +
              `${customer.creditLimit} limit, which is less than ${major(wanted)}. ` +
              `Put ${major(available)} on account and take the rest now.`)
          : (translate?.('till.atLimitFull', {
              customer: customer.name,
              limit: customer.creditLimit,
            }) ??
            `${customer.name} is at their ${customer.creditLimit} limit. ` +
              'Nothing further can go on this account until they pay.'),
    };
  }

  return {
    kind: 'ok',
    available: major(available),
    message:
      translate?.('till.willBeLeft', {
        left: major(available - wanted),
        limit: customer.creditLimit,
      }) ??
      `${major(available - wanted)} will be left of a ${customer.creditLimit} limit.`,
  };
}

/** Whether the "On account" tender button should be offered at all. */
export function mayOfferAccount(customer: CounterCustomer | null): boolean {
  return !!customer && customer.isActive && !!customer.creditLimit;
}

/**
 * The most that may go on account right now, as a tender amount.
 *
 * Capped at what is outstanding on the sale AND at what is left on the account,
 * because a tender button that filled in more than either would produce a sale
 * the server refuses twice over.
 *
 * `alreadyOnAccount` is what THIS sale has put there so far. Without it the
 * button reads a headroom that a tender already taken has spent: a cashier
 * splitting a sale across two presses would be offered the full limit both
 * times, and the second press would quietly build a sale the server then
 * refuses at the counter with the customer waiting.
 */
export function accountTender(
  customer: CounterCustomer | null,
  owed: string,
  alreadyOnAccount = '0.00',
): string {
  if (!mayOfferAccount(customer)) return '0.00';

  const left = minor(customer!.available || '0') - minor(alreadyOnAccount);
  if (left <= 0n) return '0.00';

  const wanted = minor(owed);
  return major(wanted < left ? wanted : left);
}
