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
      const route = prefix === '' ? '/' : prefix;
      out.push(route);
      FILE_OF.set(route, path.join(dir, entry.name));
    }
  }
  return out;
}

/** Where each route's page lives, so its guard can be read. */
const FILE_OF = new Map<string, string>();

/**
 * The permissions a page's own guard accepts.
 *
 * Read out of the source rather than imported, because `RequirePermission` is
 * a React component and this is a filesystem test. Returns null for a page
 * with no guard at all, which is a different finding from a guard that
 * disagrees.
 */
function guardOf(file: string): string[] | null {
  const source = fs.readFileSync(file, 'utf8');
  const match = /<RequirePermission[^>]*anyOf=\{\[([^\]]*)\]\}/s.exec(source);
  if (!match) return null;
  return [...(match[1] ?? '').matchAll(/'([^']+)'/g)].map((m) => m[1] as string);
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
      '/buying/returns/[returnID]', // a row on /buying/returns
      '/buying/returns/new', // an action on /buying/returns
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
      '/money/journals/[journalID]', // a row on /money/journals
      '/money/journals/new', // an action on /money/journals
      '/people/employees/[employeeID]', // a row on /people/employees, and a
      // name in the expiry alert above it
      '/people/employees/new', // an action on /people/employees
      '/people/payroll/[runID]', // a row on /people/payroll, and where
      // preparing a month lands
      '/people/roles/new', // an action on /people/roles, and where Copy lands
      '/people/roles/[roleID]', // a row on /people/roles
    ]);

    const orphans = [...ROUTES].filter(
      (r) => !navHrefs.has(r) && !reachedOtherwise.has(r),
    );
    expect(orphans).toEqual([]);
  });

  it('never offers a link on a permission the screen itself refuses', () => {
    // The failure this catches, found by building the exchange screen: the
    // Returns entry was shown on `sales.refund` OR `sales.exchange`, and the
    // page behind it is guarded on `sales.refund` alone. Somebody holding only
    // `sales.exchange` -- a real seeded combination -- saw the link and got
    // "you do not have permission" for a screen the sidebar had just offered
    // them.
    //
    // `navigation.test.ts` checks the same thing against the API's own route
    // table, but only for routes it can name in PRIMARY_READ. This checks the
    // guard the page actually renders, which is the thing a person meets.
    //
    // Nav permissions are ANY-of and so are guards, so every permission that
    // SHOWS an item has to be one the guard ACCEPTS. One that is not is a link
    // somebody sees and cannot follow.
    const wrong: string[] = [];
    for (const item of ALL_ITEMS) {
      if (!item.built) continue;
      const file = FILE_OF.get(item.href);
      if (!file) continue;
      const guard = guardOf(file);
      // A platform screen is gated by holding the platform role rather than by
      // a permission string, and has no anyOf to read.
      if (guard === null) continue;
      for (const granted of item.permissions) {
        if (!guard.includes(granted)) {
          wrong.push(
            `${item.id}: shown on ${granted}, but ${item.href} accepts ${guard.join(' | ')}`,
          );
        }
      }
    }
    expect(wrong).toEqual([]);
  });

  it('leaves the unbuilt architecture in place rather than deleting it', () => {
    // The map of the whole product is worth keeping: it is what says which
    // screen comes next, and it carries the permissions each will need.
    const unbuilt = ALL_ITEMS.filter((i) => !i.built);
    expect(unbuilt.length).toBeGreaterThan(20);
  });
});
