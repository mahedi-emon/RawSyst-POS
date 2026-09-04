import { describe, expect, it } from 'vitest';

import { PERMISSIONS } from '../api/contract.generated';
import { Grants } from '../auth/permissions';
import {
  BUSINESS_NAV,
  PLATFORM_NAV,
  findActiveItem,
  flattenNavigation,
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
