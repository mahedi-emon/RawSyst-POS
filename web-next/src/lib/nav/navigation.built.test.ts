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

/**
 * The portal is a different application that happens to share a build.
 *
 * `/portal/*` is signed into with a phone and a one-time code, or a supplier's
 * password. There is no staff session, no permission catalogue and no sidebar
 * — the shell is a header and a row of tabs, and the person using it is a
 * customer standing in a shop rather than somebody who works there.
 *
 * So these routes are excluded from the sidebar check rather than listed in
 * `reachedOtherwise`. That list means "a staff screen reached from another
 * staff screen", and putting a customer portal in it would make the list say
 * something untrue about who can open these.
 *
 * They are not unchecked: the portal's own tabs are asserted below, in both
 * directions, against the same filesystem read.
 */
const PORTAL = (route: string): boolean =>
  route === '/portal' || route.startsWith('/portal/');

const ALL_BUILT = builtRoutes(APP);
const ROUTES = new Set(ALL_BUILT.filter((r) => !PORTAL(r)));
const PORTAL_ROUTES = new Set(ALL_BUILT.filter(PORTAL));
const ALL_ITEMS = [...BUSINESS_NAV, ...PLATFORM_NAV].flatMap((s) => s.items);

/** The tabs the portal shell offers, read out of its layout. */
function portalTabs(): string[] {
  const layout = fs.readFileSync(
    path.join(APP, '(portal)', 'layout.tsx'),
    'utf8',
  );
  return [...layout.matchAll(/href: '(\/portal[^']*)'/g)].map(
    (m) => m[1] as string,
  );
}

describe('the portal offers only what exists', () => {
  it('found the portal', () => {
    // The same guard on the guard as below: an empty set would pass every
    // assertion here for the wrong reason.
    expect(PORTAL_ROUTES.has('/portal')).toBe(true);
    expect(PORTAL_ROUTES.has('/portal/supplier')).toBe(true);
  });

  it('never offers a tab with no page behind it', () => {
    const dead = portalTabs().filter((href) => !PORTAL_ROUTES.has(href));
    expect(dead).toEqual([]);
  });

  it('never builds a portal screen nothing reaches', () => {
    // The two sign-in pages are reached by the link a shop sends out, not by a
    // tab — a customer who is not signed in has no tabs at all.
    const entryPoints = new Set(['/portal', '/portal/supplier']);
    const tabs = new Set(portalTabs());
    const orphans = [...PORTAL_ROUTES].filter(
      (r) => !tabs.has(r) && !entryPoints.has(r),
    );
    expect(orphans).toEqual([]);
  });
});

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
      '/forgot-password', // linked from sign-in, for somebody who cannot sign in
      '/nowhere', // sent here when nothing at all is reachable
      // The three screens about the PERSON rather than the business. They are
      // in the user menu and the header, not the sidebar, and deliberately so:
      // every route behind them resolves the caller from their own token and
      // has no user parameter, so there is no permission to name — and the
      // sidebar requires one of every item it holds, precisely so that an item
      // with none cannot render for somebody holding nothing.
      '/settings/security', // the user menu
      '/notifications', // the user menu
      // Who built the product. In the user menu for the same reason as the two
      // above -- there is no permission that opens it -- and in BOTH
      // workspaces, since an operator has no tenant and this page needs none.
      '/about',
      '/search', // the search box in the header
      '/products/[productId]', // a row on /products
      // Both buttons on /products used to be dead. `POST /catalog/products`
      // was live and uncalled, so nobody could add an item to their own
      // catalogue while the screen offered to twice.
      '/products/new', // the New product button on /products, and its empty state
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
      // The receipt for one sale, printed from a row on /sales. Until it was
      // built the product could record a reprint and could not produce the
      // thing being reprinted.
      '/sales/[invoiceID]/receipt',
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
      // One partner's capital account, opened from a row on the register.
      // C3.2 asks for a statement an INVESTOR can be given access to and read
      // for themselves, so it has a URL somebody can be sent rather than being
      // a panel behind a list of everybody else's holdings.
      '/money/investors/[investorID]',
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

  it('leaves the architecture in place rather than deleting it', () => {
    // The map of the whole product is worth keeping: it is what says which
    // screen comes next, and it carries the permissions each will need. The
    // temptation this guards against is deleting an unbuilt entry to make the
    // built-check above pass.
    //
    // It used to assert that more than twenty entries were still unbuilt,
    // which was a proxy that expired by design: every module completed brings
    // the number down, and it went red at sixteen with nothing wrong. The
    // count that does NOT decay is the size of the map itself, so that is what
    // is asserted — entries move from unbuilt to built, and none disappear.
    expect(ALL_ITEMS.length).toBeGreaterThanOrEqual(82);
  });
});
