// The `built` flag says a screen exists. This is what makes that true.
//
// `navigation.ts` is a client module and cannot read the app directory, so the
// flag is data somebody sets by hand — and a flag set by hand drifts. This
// reads the filesystem and fails when it has.
//
// Both directions matter. A flag set on a screen that does not exist puts a
// dead link in somebody's sidebar; a screen built and never flagged is work
// nobody can reach, which is the more expensive mistake because it is silent.

import fs from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { BUSINESS_NAV, PLATFORM_NAV } from './navigation';

const APP = path.join(process.cwd(), 'src', 'app');

/**
 * Every page route the app actually serves.
 *
 * Route groups — `(business)`, `(auth)` — are organisational and contribute
 * nothing to the URL, so they are stripped. A dynamic segment is kept as
 * written: `/products/[productId]` is not a nav destination and should not
 * match one.
 */
function builtRoutes(dir: string, prefix = ''): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      const segment = /^\(.*\)$/.test(entry.name) ? '' : `/${entry.name}`;
      out.push(...builtRoutes(path.join(dir, entry.name), prefix + segment));
    } else if (entry.name === 'page.tsx') {
      out.push(prefix === '' ? '/' : prefix);
    }
  }
  return out;
}

const ROUTES = new Set(builtRoutes(APP));
const ALL_ITEMS = [...BUSINESS_NAV, ...PLATFORM_NAV].flatMap((s) => s.items);

describe('the sidebar offers only what exists', () => {
  it('found the app directory', () => {
    // A guard on the guard: if this ever returns nothing, every assertion
    // below passes for the wrong reason.
    expect(ROUTES.size).toBeGreaterThan(10);
    expect(ROUTES.has('/dashboard')).toBe(true);
  });

  it('never marks a screen built when there is no page for it', () => {
    const missing = ALL_ITEMS.filter((i) => i.built && !ROUTES.has(i.href)).map(
      (i) => `${i.id} -> ${i.href}`,
    );
    expect(missing).toEqual([]);
  });

  it('never leaves a finished screen out of the sidebar', () => {
    // The silent failure: a screen is built, nothing links to it, and the only
    // way in is to type the URL.
    const navHrefs = new Set(ALL_ITEMS.filter((i) => i.built).map((i) => i.href));

    // Not every page is a nav destination. These are reached from elsewhere,
    // and each one names where from.
    const reachedOtherwise = new Set([
      '/', // the front door; redirects and renders nothing
      '/login',
      '/change-password', // sent here by sign-in when a password is one-time
      '/nowhere', // sent here when nothing at all is reachable
      '/products/[productId]', // a row on /products
      '/customers/[customerId]', // a row on /customers
      '/buying/orders/[poID]', // a row on /buying/orders
      '/buying/bills/[billID]', // a row on /buying/bills
      '/buying/orders/new', // an action on /buying/orders
      '/buying/requisitions/[requisitionID]', // a row on /buying/requisitions
      '/buying/requisitions/new', // an action on /buying/requisitions
      '/buying/quotes/[rfqID]', // a row on /buying/quotes
      '/buying/quotes/new', // an action on /buying/quotes and on an approved request
      '/stock/adjustments/[adjustmentID]', // a row on /stock/adjustments
      '/stock/adjustments/new', // an action on /stock/adjustments
      '/stock/counts/[countID]', // an open count, from /stock/adjustments
      '/stock/counts/new', // an action on /stock/adjustments
      '/stock/transfers/[transferID]', // a row on /stock/transfers
      '/stock/transfers/new', // an action on /stock/transfers
      '/stock/production/[productionID]', // a row on /stock/production
      '/orders/[orderID]', // a row on /orders
      '/orders/new', // an action on /orders
      '/orders/[orderID]/documents/[kind]', // printed from one order
      '/money/expenses/[expenseID]', // a row on /money/expenses
      '/money/expenses/new', // an action on /money/expenses
      '/money/reconcile/[statementID]', // a row on /money/reconcile
    ]);

    const orphans = [...ROUTES].filter(
      (r) => !navHrefs.has(r) && !reachedOtherwise.has(r),
    );
    expect(orphans).toEqual([]);
  });

  it('leaves the unbuilt architecture in place rather than deleting it', () => {
    // The map of the whole product is worth keeping: it is what says which
    // screen comes next, and it carries the permissions each will need.
    const unbuilt = ALL_ITEMS.filter((i) => !i.built);
    expect(unbuilt.length).toBeGreaterThan(20);
  });
});
