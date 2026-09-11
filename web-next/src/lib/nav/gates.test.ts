// A menu item must not lead somewhere the person cannot load.
//
// # The failure this catches
//
// Navigation gates on what an item is FOR. A screen usually needs something
// else first: paying a supplier is the act, finding the bill is the read. Gate
// only on the act and somebody holding just that sees the link, clicks it, and
// collects a 403 from the screen's opening request.
//
// It is not a security failure — the server refuses correctly, which is the
// whole point — but it is the security model leaking into the interface as an
// error nobody can act on. `alsoNeeds` exists for exactly this, and four items
// already carry it after three seeded roles were found in that position.
//
// A fifth, `/pos/exchanges`, was found the same way and is fixed. This test is
// what stops the sixth.
//
// # Why the pairs are written down rather than derived
//
// Deriving them would mean parsing every page for its API calls and resolving
// each against the route table — which is a script, and one that was written
// and produced five false positives before it produced this one. It matched
// `buying/orders/page.tsx` for the href `/orders`, and it could not see
// `alsoNeeds` at all.
//
// So the list below is the reviewed output of that script rather than the
// script. Each pair says: this gate implies this read. That is a claim about
// product design, and a person should write it down.

import { describe, expect, it } from 'vitest';

import type { Permission } from '@/lib/api/contract.generated';

import { BUSINESS_NAV, type NavItem, type NavSection } from './navigation';

/**
 * A permission that only makes sense alongside another.
 *
 * Left: a gate that appears in some item's `permissions`. Right: the read that
 * item's screen cannot open without. Taken from the route table — each right
 * side is the permission on the GET the screen fires first.
 */
// Typed against the generated permission union, so a permission that is
// renamed or removed in the Go route table breaks this list at compile time
// rather than quietly matching nothing.
const IMPLIES: ReadonlyArray<readonly [Permission, Permission]> = [
  // Booking a delivery against an order it has to list first.
  ['purchasing.receive_goods', 'purchasing.view'],
  // Paying a bill it has to find first.
  ['purchasing.pay_supplier', 'purchasing.view'],
  // Taking a payment against what a customer still owes.
  ['sales.receive_payment', 'customers.view'],
  // Editing roles it has to read first.
  ['identity.manage_roles', 'identity.view'],
  // Both of the exchange screen's reads — the receipt lookup and the
  // returnable lines — are sales.refund.
  ['sales.exchange', 'sales.refund'],
];

function everyItem(sections: readonly NavSection[]): NavItem[] {
  return sections.flatMap((s) => [...s.items]);
}

describe('what a menu item promises', () => {
  it('asks for the reads its screen cannot open without', () => {
    const broken: string[] = [];

    for (const item of everyItem(BUSINESS_NAV)) {
      if (!item.built) continue;
      const also = new Set(item.alsoNeeds ?? []);
      const gates = new Set<Permission>(item.permissions);

      for (const [gate, read] of IMPLIES) {
        if (!gates.has(gate)) continue;
        // Satisfied either way: named in alsoNeeds, or already one of the
        // gates in its own right.
        if (also.has(read) || gates.has(read)) continue;
        broken.push(
          `${item.href} is offered on "${gate}" but its screen opens with a ` +
            `request needing "${read}". Add it to alsoNeeds, or somebody ` +
            `holding only "${gate}" sees the link and gets a 403.`,
        );
      }
    }

    expect(broken).toEqual([]);
  });

  it('is checking gates that still exist', () => {
    // A pair naming a permission no item gates on is a pair guarding nothing,
    // and the test above would pass without ever comparing anything.
    const gates = new Set(
      everyItem(BUSINESS_NAV).flatMap((i) => [...i.permissions]),
    );
    const stale = IMPLIES.filter(([gate]) => !gates.has(gate)).map(([g]) => g);
    expect(stale, 'permissions no menu item gates on any more').toEqual([]);
  });

  it('gates every built business item on something', () => {
    // An item with no permissions is offered to everybody. That is right for
    // the platform console, where authority comes from the super admin claim
    // rather than from permissions, and wrong inside a business.
    const ungated = everyItem(BUSINESS_NAV)
      .filter((i) => i.built && i.permissions.length === 0)
      .map((i) => i.href);
    expect(ungated).toEqual([]);
  });
});
