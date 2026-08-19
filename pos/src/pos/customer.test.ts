import { describe, expect, it } from 'vitest';

import {
  accountTender,
  creditVerdict,
  fromCache,
  mayOfferAccount,
  type CounterCustomer,
} from './customer';
import type { CachedCustomer } from '../offline/customers';

const who = (over: Partial<CounterCustomer> = {}): CounterCustomer => ({
  id: 'c1',
  code: 'NOOR',
  name: 'Al Noor Trading',
  phone: '0555000111',
  paymentTermsDays: 30,
  creditLimit: '500.00',
  balance: '0.00',
  available: '500.00',
  isActive: true,
  stale: false,
  ...over,
});

describe('whether a sale may go on account', () => {
  it('refuses when nobody has been chosen', () => {
    const v = creditVerdict(null, '100.00');
    expect(v.kind).toBe('no_customer');
    expect(v.message).toMatch(/choose a customer/i);
  });

  it('refuses a customer with no credit account, and says what to do', () => {
    // Not the same as a limit of zero. A record typed in a hurry at the counter
    // must not arrive with unlimited trust attached.
    const v = creditVerdict(who({ creditLimit: '', available: '' }), '10.00');
    expect(v.kind).toBe('no_account');
    expect(v.message).toMatch(/paid now/i);
  });

  it('refuses a retired customer', () => {
    expect(creditVerdict(who({ isActive: false }), '10.00').kind).toBe('retired');
  });

  it('allows an amount inside the limit and says what will be left', () => {
    const v = creditVerdict(who({ balance: '100.00', available: '400.00' }), '150.00');
    expect(v.kind).toBe('ok');
    expect(v.message).toContain('250.00');
  });

  it('allows an amount that exactly reaches the limit', () => {
    // The commonest case in a shop with round limits, and the one a float would
    // eventually get wrong.
    expect(creditVerdict(who({ available: '500.00' }), '500.00').kind).toBe('ok');
  });

  it('does not drift on amounts a float cannot hold', () => {
    // 0.15 has no exact float64 representation. Three of them is exactly 0.45.
    const v = creditVerdict(who({ available: '0.45' }), '0.45');
    expect(v.kind).toBe('ok');
  });

  it('refuses past the limit, and names the numbers a cashier must repeat', () => {
    const v = creditVerdict(who({ balance: '400.00', available: '100.00' }), '150.00');
    expect(v.kind).toBe('over_limit');
    expect(v.message).toContain('100.00');
    expect(v.message).toContain('500.00');
    // And offers the way out, rather than only saying no.
    expect(v.message).toMatch(/take the rest now/i);
    if (v.kind === 'over_limit') expect(v.most).toBe('100.00');
  });

  it('says plainly when there is no room at all', () => {
    const v = creditVerdict(who({ balance: '500.00', available: '0.00' }), '10.00');
    expect(v.kind).toBe('over_limit');
    expect(v.message).toMatch(/at their .* limit/i);
    if (v.kind === 'over_limit') expect(v.most).toBe('0.00');
  });

  it('judges the amount going on account, not the sale total', () => {
    // A sale of 300 settled 200 in cash and 100 on account draws down 100.
    // Checking the total would refuse a sale that is perfectly affordable.
    expect(creditVerdict(who({ available: '150.00' }), '100.00').kind).toBe('ok');
    expect(creditVerdict(who({ available: '150.00' }), '300.00').kind).toBe('over_limit');
  });
});

describe('whether to offer the account button at all', () => {
  it('offers it to a customer with an account', () => {
    expect(mayOfferAccount(who())).toBe(true);
  });

  it('does not offer it with nobody chosen', () => {
    expect(mayOfferAccount(null)).toBe(false);
  });

  it('does not offer it without a limit', () => {
    // A button that always refuses teaches a cashier to distrust the rest.
    expect(mayOfferAccount(who({ creditLimit: '' }))).toBe(false);
  });

  it('does not offer it to a retired customer', () => {
    expect(mayOfferAccount(who({ isActive: false }))).toBe(false);
  });
});

describe('what the account button fills in', () => {
  it('fills in the whole outstanding amount when there is room', () => {
    expect(accountTender(who({ available: '500.00' }), '115.00')).toBe('115.00');
  });

  it('caps at what is left on the account', () => {
    // So one press produces a part payment on account rather than a sale the
    // server refuses.
    expect(accountTender(who({ available: '60.00' }), '115.00')).toBe('60.00');
  });

  it('fills in nothing when the account is full', () => {
    expect(accountTender(who({ available: '0.00' }), '115.00')).toBe('0.00');
  });

  it('fills in nothing when there is no account', () => {
    expect(accountTender(who({ creditLimit: '' }), '115.00')).toBe('0.00');
  });

  it('fills in nothing when nobody is chosen', () => {
    expect(accountTender(null, '115.00')).toBe('0.00');
  });
});

describe('a customer read from the local cache', () => {
  const cached: CachedCustomer = {
    id: 'c9',
    code: 'CASH',
    name: 'Regular',
    nameAr: '',
    customerType: 'retail',
    phone: '',
    paymentTermsDays: 0,
    creditLimit: '200.00',
    balance: '50.00',
    available: '150.00',
    isActive: true,
    updatedAt: '2026-08-18T08:00:00Z',
  };

  it('is marked stale, so the screen can say the figures are from last sync', () => {
    expect(fromCache(cached).stale).toBe(true);
  });

  it('carries the credit figures through unchanged', () => {
    const c = fromCache(cached);
    expect(c.available).toBe('150.00');
    expect(creditVerdict(c, '150.00').kind).toBe('ok');
    expect(creditVerdict(c, '150.01').kind).toBe('over_limit');
  });
});

describe('a sale split across two presses of the account button', () => {
  // Found in a browser: the second press was offered the full limit again,
  // because the button read the server's `available` without subtracting what
  // this sale had already put on the account.
  it('does not offer headroom the first press already spent', () => {
    const c = who({ available: '300.00' });
    // 400 owed, 300 available: the first press offers 300.
    expect(accountTender(c, '400.00')).toBe('300.00');
    // 100 still owed and nothing left, so the second press offers nothing.
    expect(accountTender(c, '100.00', '300.00')).toBe('0.00');
  });

  it('offers only the remainder when the first press was partial', () => {
    const c = who({ available: '300.00' });
    expect(accountTender(c, '500.00', '100.00')).toBe('200.00');
  });

  it('never offers a negative amount', () => {
    // A limit lowered between the pull and the sale can put the balance past
    // it, which must read as nothing available rather than as credit.
    const c = who({ available: '50.00' });
    expect(accountTender(c, '100.00', '80.00')).toBe('0.00');
  });
});
