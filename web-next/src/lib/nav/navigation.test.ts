import { describe, expect, it } from 'vitest';

import { PERMISSIONS, ROUTE_PERMISSIONS, ROUTES } from '../api/contract.generated';
import { Grants } from '../auth/permissions';
import {
  BUSINESS_NAV,
  PLATFORM_NAV,
  findActiveItem,
  flattenNavigation,
  landingFor,
  resolveNavigation,
} from './navigation';

const everyPermission = new Grants(PERMISSIONS as unknown as string[]);

describe('navigation only names permissions the backend enforces', () => {
  it('has no invented permission anywhere in the business tree', () => {
    // The whole point of generating `PERMISSIONS` from the Go route table is
    // that a guard cannot check a string nobody enforces. This walks the
    // navigation and proves it.
    const known = new Set<string>(PERMISSIONS);
    const unknown: string[] = [];
    for (const section of BUSINESS_NAV) {
      for (const item of section.items) {
        for (const p of item.permissions) {
          if (!known.has(p)) unknown.push(`${item.id}: ${p}`);
        }
      }
    }
    expect(unknown).toEqual([]);
  });

  it('gives every business item a permission to open it', () => {
    // An item with no permission would be visible to anybody signed into the
    // workspace, which for a business screen is never right.
    const open = BUSINESS_NAV.flatMap((s) =>
      s.items.filter((i) => i.permissions.length === 0).map((i) => i.id),
    );
    expect(open).toEqual([]);
  });
});

describe('an employee sees only what they can use', () => {
  it('shows a cashier a product about ringing up sales', () => {
    // The permissions a real Cashier role holds for the counter and nothing
    // else. This is the case the whole RBAC design exists for.
    const cashier = new Grants([
      'sales.create',
      'sales.view',
      'sales.receive_payment',
      'customers.view',
    ]);

    const sections = resolveNavigation(BUSINESS_NAV, cashier);
    const items = flattenNavigation(sections).map((i) => i.id);

    expect(items).toContain('pos');
    expect(items).toContain('sales');
    expect(items).toContain('shifts');
    expect(items).toContain('customers');

    // Nothing about money, staff or buying. Not greyed out -- absent.
    expect(items).not.toContain('payroll');
    expect(items).not.toContain('journals');
    expect(items).not.toContain('suppliers');
    expect(items).not.toContain('users');
    expect(items).not.toContain('expenses');
  });

  it('drops a section entirely when nothing in it is reachable', () => {
    const cashier = new Grants(['sales.create']);
    const ids = resolveNavigation(BUSINESS_NAV, cashier).map((s) => s.id);
    expect(ids).not.toContain('staff');
    expect(ids).not.toContain('buying');
    expect(ids).not.toContain('oversight');
  });

  it('shows nothing at all to somebody holding nothing', () => {
    expect(resolveNavigation(BUSINESS_NAV, new Grants([]))).toEqual([]);
  });

  it('shows an owner the whole product', () => {
    const sections = resolveNavigation(BUSINESS_NAV, everyPermission);
    expect(sections.length).toBe(BUSINESS_NAV.length);
  });
});

describe('a module the plan does not include reads differently from one refused', () => {
  it('keeps the item but marks it outside the plan', () => {
    // Holding the permission and not the plan is a different situation from
    // not holding the permission: one is a conversation with the owner, the
    // other with sales. The item stays, greyed, saying which.
    const owner = new Grants(['payroll.view', 'hr.view', 'sales.view']);
    const withoutPayroll = new Set<string>([]);

    const sections = resolveNavigation(BUSINESS_NAV, owner, withoutPayroll);
    const payroll = flattenNavigation(sections).find((i) => i.id === 'payroll');

    expect(payroll).toBeDefined();
    expect(payroll?.includedInPlan).toBe(false);
  });

  it('treats an unknown entitlement list as everything included', () => {
    // Showing a link that turns out to be refused is a smaller failure than
    // hiding a module the business is paying for, so a failed entitlements
    // call must not silently shrink the product.
    const owner = new Grants(['payroll.view']);
    const payroll = flattenNavigation(
      resolveNavigation(BUSINESS_NAV, owner, undefined),
    ).find((i) => i.id === 'payroll');
    expect(payroll?.includedInPlan).toBe(true);
  });
});

describe('the platform workspace', () => {
  it('needs no permission, because holding the platform role is the condition', () => {
    // Every platform route is AccessSuperAdmin in the Go table. There is no
    // permission string to check, and inventing one here would be a second
    // rule that could disagree with the first.
    const operator = new Grants([], true);
    const sections = resolveNavigation(PLATFORM_NAV, operator);
    expect(sections.length).toBe(PLATFORM_NAV.length);
  });
});

describe('finding the current screen', () => {
  const sections = resolveNavigation(BUSINESS_NAV, everyPermission);

  it('prefers the longest match', () => {
    // `/stock/transfers` must select Transfers, not Stock on hand at `/stock`.
    expect(findActiveItem(sections, '/stock/transfers')?.item.id).toBe('transfers');
    expect(findActiveItem(sections, '/stock')?.item.id).toBe('on-hand');
  });

  it('matches a child route to its parent item', () => {
    expect(findActiveItem(sections, '/products/abc-123')?.item.id).toBe('products');
  });

  it('returns nothing for a route outside the tree', () => {
    expect(findActiveItem(sections, '/account')).toBeNull();
  });
});

describe('grants', () => {
  const g = new Grants(['sales.create', 'sales.view', 'inventory.view'], false, ['s1'], '50.00');

  it('answers any and all correctly', () => {
    expect(g.canAny('sales.create', 'payroll.run')).toBe(true);
    expect(g.canAll('sales.create', 'payroll.run')).toBe(false);
    expect(g.canAll('sales.create', 'sales.view')).toBe(true);
  });

  it('answers whether a module is reachable at all', () => {
    expect(g.canReachModule('sales')).toBe(true);
    expect(g.canReachModule('payroll')).toBe(false);
  });

  it('reports a branch confinement', () => {
    expect(g.isBranchConfined).toBe(true);
    expect(new Grants([]).isBranchConfined).toBe(false);
  });

  it('keeps the approval ceiling as a string', () => {
    // Never widened through a float on its way to a comparison.
    expect(g.amountLimit).toBe('50.00');
  });
});

describe('route guards may only name a permission that gates a route', () => {
  it('keeps the two kinds of permission apart', () => {
    // Verified against a live server: an Owner resolves to 109 permissions
    // where the route table names 102. The other seven are action- and
    // field-level -- sales.discount, catalog.view_cost_price and so on -- and
    // are enforced structurally rather than per route.
    expect(PERMISSIONS.length).toBeGreaterThan(ROUTE_PERMISSIONS.length);
    const routeSet = new Set<string>(ROUTE_PERMISSIONS);
    const actionOnly = PERMISSIONS.filter((p) => !routeSet.has(p));
    expect(actionOnly).toContain('sales.discount');
    expect(actionOnly).toContain('catalog.view_cost_price');
  });

  it('navigation never guards a route with a permission no route checks', () => {
    // The failure this prevents: guarding /products on catalog.view_cost_price
    // would look right, hide the screen from a cashier, and protect nothing --
    // because no route checks it, the data would still be one fetch away.
    const routeSet = new Set<string>(ROUTE_PERMISSIONS);
    const wrong: string[] = [];
    for (const section of BUSINESS_NAV) {
      for (const item of section.items) {
        for (const p of item.permissions) {
          if (!routeSet.has(p)) wrong.push(`${item.id}: ${p}`);
        }
      }
    }
    expect(wrong).toEqual([]);
  });
});

/**
 * The route each built screen reads FIRST.
 *
 * Maintained by hand, because a page route and an API route are different
 * things and nothing in the contract connects them. Add an entry when a screen
 * is built; the test below then holds it to what the API actually requires.
 */
const PRIMARY_READ: Record<string, string> = {
  dashboard: '/api/v1/dashboard/overview',
  products: '/api/v1/catalog/products',
  customers: '/api/v1/customers',
  sales: '/api/v1/dashboard/sales',
  'stock-on-hand': '/api/v1/stock/on-hand',
  movements: '/api/v1/stock/movements',
  counts: '/api/v1/stock/adjustments',
  transfers: '/api/v1/stock/transfers',
  batches: '/api/v1/stock/batches',
  locations: '/api/v1/stock/locations',
  production: '/api/v1/stock/production',
  orders: '/api/v1/orders',
  expenses: '/api/v1/expenses',
  // The chart of accounts a category posts to, which is the read this
  // screen cannot work without and the one the API gates on the
  // configuration permission rather than the reading one.
  'expense-setup': '/api/v1/expenses/accounts',
  treasury: '/api/v1/treasury/accounts',
  'money-transfers': '/api/v1/treasury/transfers',
  reconcile: '/api/v1/treasury/statements',
  suppliers: '/api/v1/purchasing/suppliers',
  'purchase-orders': '/api/v1/purchasing/orders',
  'goods-receipts': '/api/v1/purchasing/orders',
  payables: '/api/v1/purchasing/ageing',
  bills: '/api/v1/purchasing/bills',
  'supplier-payments': '/api/v1/purchasing/bills',
  requisitions: '/api/v1/purchasing/requisitions',
  rfqs: '/api/v1/purchasing/rfqs',
};

describe('a link that appears leads somewhere', () => {
  const permissionOf = new Map<string, string>();
  for (const route of ROUTES) {
    if (route.method === 'GET' && route.access === 'Permission') {
      permissionOf.set(route.pattern, route.permission);
    }
  }

  it('never shows a link on a permission the screen behind it is refused', () => {
    // The failure this catches, found live rather than by reading: the
    // dashboard was shown to anybody holding sales.view OR accounting.view OR
    // inventory.view, and GET /dashboard/overview is accounting.view alone. A
    // seeded Cashier holds two of those three and none of them that one, so
    // the link appeared and answered 403. Branch Manager and Inventory Keeper
    // had the same problem, and four buying links had it in the other
    // direction.
    //
    // Nav permissions are ANY-of, so EVERY listed permission has to be enough
    // on its own. One that is not is a link somebody sees and cannot follow.
    const wrong: string[] = [];
    for (const section of BUSINESS_NAV) {
      for (const item of section.items) {
        const route = PRIMARY_READ[item.id];
        if (!route) continue;
        const needed = permissionOf.get(route);
        expect(needed, `${route} is not a GET permission route`).toBeDefined();
        for (const granted of item.permissions) {
          if (granted !== needed) {
            wrong.push(`${item.id}: shown on ${granted}, but ${route} needs ${needed}`);
          }
        }
      }
    }
    expect(wrong).toEqual([]);
  });

  it('gives every mapped screen a permission at all', () => {
    for (const section of BUSINESS_NAV) {
      for (const item of section.items) {
        if (!PRIMARY_READ[item.id]) continue;
        expect(item.permissions.length, item.id).toBeGreaterThan(0);
      }
    }
  });
});

describe('every nav item has its own id', () => {
  it('does not repeat one inside a workspace', () => {
    // Ids key the PRIMARY_READ map above, and a repeated one makes both items
    // claim the same route -- which is how a money transfer came to be checked
    // against the STOCK transfer's permission. They are also React keys and
    // i18n key suffixes, so a collision is three bugs wearing one hat.
    for (const [workspace, sections] of [
      ['business', BUSINESS_NAV],
      ['platform', PLATFORM_NAV],
    ] as const) {
      const seen = new Set<string>();
      const repeated: string[] = [];
      for (const section of sections) {
        for (const item of section.items) {
          if (seen.has(item.id)) repeated.push(item.id);
          seen.add(item.id);
        }
      }
      expect(repeated, workspace).toEqual([]);
    }
  });
});

describe('where somebody lands after signing in', () => {
  /** The nineteen a seeded Cashier resolves to, read off a live /auth/me. */
  const CASHIER = [
    'catalog.view',
    'customers.view',
    'installment.view',
    'inventory.view',
    'label.print',
    'loyalty.view',
    'order.view',
    'portal.view',
    'promotion.view',
    'sales.create',
    'sales.discount',
    'sales.exchange',
    'sales.hold',
    'sales.receive_payment',
    'sales.refund',
    'sales.view',
    'serial.view',
    'service.view',
    'wallet.view',
  ];

  it('does not send a cashier to a screen the API refuses them', () => {
    const where = landingFor(BUSINESS_NAV, new Grants(CASHIER));
    // The old behaviour sent everybody here, and this account gets a 403 from
    // the route behind it.
    expect(where).not.toBe('/dashboard');
    expect(where).not.toBeNull();
  });

  it('sends an owner to the dashboard, which is what an owner opens for', () => {
    expect(landingFor(BUSINESS_NAV, new Grants([...PERMISSIONS]))).toBe('/dashboard');
  });

  it('says nothing rather than looping when somebody can reach nothing', () => {
    // A real state: the last role was taken away while they were signed in.
    expect(landingFor(BUSINESS_NAV, new Grants([]))).toBeNull();
  });

  it('skips a module the plan does not include', () => {
    // A link to an upsell is not somewhere to drop somebody who came to work.
    const grants = new Grants([...PERMISSIONS]);
    const withoutAnything = landingFor(BUSINESS_NAV, grants, new Set<string>());
    // Every item that names a feature is excluded, so the answer is either a
    // featureless item or the first reachable one -- never undefined.
    expect(withoutAnything).not.toBeNull();
  });
});
