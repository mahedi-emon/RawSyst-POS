// How loudly the interface says a subscription is in trouble.
//
// The policy itself — which states stop writes, which reason outranks which,
// what to tell somebody to do — lives in `billing/standing.go` and is tested
// there. This file covers the only decision the client makes on its own: how
// much alarm to show.
//
// The distinction matters more than it looks. `past_due` is a shop that is
// still trading and should pay a bill; everything below it is a shop that has
// already stopped. Rendering the two the same would either panic somebody who
// is fine, or fail to tell somebody who is not.

import { describe, expect, it } from 'vitest';

import { type SubscriptionState, toneFor } from './subscription-state';

describe('how a subscription state is shown', () => {
  it('says nothing at all when the business is in good order', () => {
    // The overwhelmingly common case. A banner that appeared for a healthy
    // shop is a banner everybody learns to ignore.
    expect(toneFor('active')).toBeNull();
    expect(toneFor('trialing')).toBeNull();
  });

  it('warns, rather than alarms, about a bill that is merely late', () => {
    // Still trading. Dunning decides when a late payment becomes a
    // suspension, and until it does this is a reminder and not a wall.
    expect(toneFor('past_due')).toBe('caution');
  });

  it('is loud about every state that has already stopped the business', () => {
    const stopped: SubscriptionState[] = [
      'expired',
      'suspended',
      'cancelled',
      'deactivated',
    ];
    for (const state of stopped) {
      expect(toneFor(state), state).toBe('critical');
    }
  });

  it('separates the states that stop a business from the one that does not', () => {
    // The line this draws is the whole point of the function, so it is
    // asserted as a line rather than only case by case.
    const all: SubscriptionState[] = [
      'active',
      'trialing',
      'past_due',
      'expired',
      'suspended',
      'cancelled',
      'deactivated',
    ];
    const loud = all.filter((s) => toneFor(s) === 'critical');
    expect(loud).toEqual(['expired', 'suspended', 'cancelled', 'deactivated']);
  });
});
