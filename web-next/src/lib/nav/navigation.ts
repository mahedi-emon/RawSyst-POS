// The information architecture.
//
// # Where this grouping comes from
//
// Not from the shape of the Go packages, and not from the old back office's 28
// sections. It follows `permission_catalogue.section` -- the grouping the
// backend already seeded, in the words a shop owner uses: selling, buying,
// money, stock, customers, staff, oversight. Those sections were written to be
// read by the person ticking permission boxes for a new employee, which makes
// them the right names for the person navigating the product too. Using any
// other grouping would mean an owner learns one vocabulary on the roles screen
// and a different one in the sidebar.
//
// # Every entry names the permission that opens it
//
// A section with no reachable item is not rendered, so an employee who can only
// ring up sales sees a product about ringing up sales. The permission strings
// are `Permission`, which is generated from the Go route table, so an entry
// naming a permission the backend does not enforce fails to compile.
//
// The `feature` field is the plan module the route group is sold under. It
// produces a different sentence -- "not included in your plan" rather than "you
// do not have permission" -- because the remedy is different: one is a call to
// the owner, the other a call to sales.

import type { Permission } from '../auth/permissions';
import type { Grants } from '../auth/permissions';

export interface NavItem {
  /** Stable id, also the i18n key suffix. */
  id: string;
  label: string;
  href: string;
  /** Any one of these opens the item. */
  permissions: readonly Permission[];
  /** The plan feature this needs, when the backend gates the route group. */
  feature?: string;
  /** Shown in the command palette and on the section index page. */
  description?: string;
}

export interface NavSection {
  id: string;
  label: string;
  /** Lucide icon name, resolved by the shell rather than imported here so this
   *  module stays free of React and can be unit-tested as data. */
  icon: string;
  items: readonly NavItem[];
}

/**
 * The business workspace.
 *
 * Ordered by how often a shop touches it, not alphabetically and not by how the
 * backend is packaged: selling first because that is what the business does all
 * day, settings last because it is done once.
 */
export const BUSINESS_NAV: readonly NavSection[] = [
  {
    id: 'overview',
    label: 'Overview',
    icon: 'LayoutDashboard',
    items: [
      {
        id: 'dashboard',
        label: 'Dashboard',
        href: '/dashboard',
        permissions: ['sales.view', 'accounting.view', 'inventory.view'],
        description: 'What the business did today, and what needs attention',
      },
      {
        id: 'approvals',
        label: 'Waiting for you',
        href: '/approvals',
        permissions: ['approval.view'],
        feature: 'approvals',
        description: 'Requests that cannot proceed until somebody decides',
      },
    ],
  },
  {
    id: 'selling',
    label: 'Selling',
    icon: 'ShoppingCart',
    items: [
      {
        id: 'pos',
        label: 'Point of sale',
        href: '/pos',
        permissions: ['sales.create'],
        description: 'Ring up a sale',
      },
      {
        id: 'sales',
        label: 'Sales',
        href: '/sales',
        permissions: ['sales.view'],
        description: 'Every invoice the shop has issued',
      },
      {
        id: 'returns',
        label: 'Returns and exchanges',
        href: '/sales/returns',
        permissions: ['sales.refund', 'sales.exchange'],
        description: 'Take goods back, and swap them',
      },
      {
        id: 'orders',
        label: 'Orders',
        href: '/orders',
        permissions: ['order.view'],
        feature: 'online_orders',
        description: 'Orders taken now and fulfilled later',
      },
      {
        id: 'deliveries',
        label: 'Deliveries',
        href: '/deliveries',
        permissions: ['delivery.view'],
        feature: 'online_orders',
        description: 'What is out for delivery, and what arrived',
      },
      {
        id: 'shifts',
        label: 'Till sessions',
        href: '/shifts',
        permissions: ['sales.receive_payment'],
        description: 'Opening float, cash drops and the close',
      },
      {
        id: 'promotions',
        label: 'Promotions',
        href: '/promotions',
        permissions: ['promotion.view'],
        feature: 'promotions',
        description: 'Discounts the till applies automatically',
      },
    ],
  },
  {
    id: 'catalogue',
    label: 'Products',
    icon: 'Package',
    items: [
      {
        id: 'products',
        label: 'Products',
        href: '/products',
        permissions: ['catalog.view'],
        description: 'What the shop sells, and what it charges',
      },
      {
        id: 'labels',
        label: 'Barcodes and labels',
        href: '/products/labels',
        permissions: ['label.print', 'label.manage'],
        feature: 'label_studio',
        description: 'Print shelf edge and product labels',
      },
    ],
  },
  {
    id: 'stock',
    label: 'Stock',
    icon: 'Boxes',
    items: [
      {
        id: 'on-hand',
        label: 'Stock on hand',
        href: '/stock',
        permissions: ['inventory.view'],
        description: 'What is where, and what it is worth',
      },
      {
        id: 'movements',
        label: 'Movements',
        href: '/stock/movements',
        permissions: ['inventory.view'],
        description: 'Every unit in and every unit out',
      },
      {
        id: 'counts',
        label: 'Counts and adjustments',
        href: '/stock/counts',
        permissions: ['inventory.adjust_stock'],
        description: 'Count the shelf, and correct the book',
      },
      {
        id: 'transfers',
        label: 'Transfers',
        href: '/stock/transfers',
        permissions: ['inventory.view', 'inventory.transfer_stock'],
        description: 'Move stock between branches',
      },
      {
        id: 'batches',
        label: 'Batches and expiry',
        href: '/stock/batches',
        permissions: ['inventory.view'],
        description: 'What expires when, and who bought a recalled batch',
      },
      {
        id: 'production',
        label: 'Production',
        href: '/stock/production',
        permissions: ['inventory.view'],
        description: 'What the shop makes from what it holds',
      },
      {
        id: 'locations',
        label: 'Locations',
        href: '/stock/locations',
        permissions: ['inventory.view'],
        description: 'Warehouses, rooms and shelves',
      },
      {
        id: 'serials',
        label: 'Serial numbers',
        href: '/stock/serials',
        permissions: ['serial.view'],
        feature: 'warranty',
        description: 'Individually tracked units',
      },
    ],
  },
  {
    id: 'buying',
    label: 'Buying',
    icon: 'Truck',
    items: [
      {
        id: 'suppliers',
        label: 'Suppliers',
        href: '/buying/suppliers',
        permissions: ['purchasing.view'],
        description: 'Who the shop buys from',
      },
      {
        id: 'purchase-orders',
        label: 'Purchase orders',
        href: '/buying/orders',
        permissions: ['purchasing.view'],
        description: 'What has been ordered, and what has arrived',
      },
      {
        id: 'receipts',
        label: 'Goods received',
        href: '/buying/receipts',
        permissions: ['purchasing.view', 'purchasing.receive_goods'],
        description: 'Book in a delivery against its order',
      },
      {
        id: 'bills',
        label: 'Bills',
        href: '/buying/bills',
        permissions: ['purchasing.view'],
        description: 'What suppliers have invoiced',
      },
      {
        id: 'supplier-payments',
        label: 'Supplier payments',
        href: '/buying/payments',
        permissions: ['purchasing.pay_supplier'],
        description: 'Pay a bill, and reverse one paid in error',
      },
      {
        id: 'requisitions',
        label: 'Requests to buy',
        href: '/buying/requisitions',
        permissions: ['purchasing.view', 'purchasing.request'],
        description: 'Ask for something before it is ordered',
      },
      {
        id: 'rfqs',
        label: 'Quotes',
        href: '/buying/quotes',
        permissions: ['purchasing.view', 'purchasing.manage_rfq'],
        description: 'Ask several suppliers, and compare',
      },
      {
        id: 'payables',
        label: 'What is owed',
        href: '/buying/ageing',
        permissions: ['accounting.view'],
        description: 'Supplier balances by age',
      },
    ],
  },
  {
    id: 'customers',
    label: 'Customers',
    icon: 'Users',
    items: [
      {
        id: 'customers',
        label: 'Customers',
        href: '/customers',
        permissions: ['customers.view'],
        description: 'Who buys, what they owe and what they bought',
      },
      {
        id: 'receivables',
        label: 'What is owed to you',
        href: '/customers/ageing',
        permissions: ['accounting.view'],
        description: 'Customer balances by age',
      },
      {
        id: 'loyalty',
        label: 'Loyalty',
        href: '/customers/loyalty',
        permissions: ['loyalty.view'],
        feature: 'loyalty',
        description: 'Points earned and points spent',
      },
      {
        id: 'wallets',
        label: 'Wallets and gift cards',
        href: '/customers/wallets',
        permissions: ['wallet.view'],
        feature: 'loyalty',
        description: 'Store credit and gift card balances',
      },
      {
        id: 'portal',
        label: 'Customer portal',
        href: '/customers/portal',
        permissions: ['portal.view'],
        description: 'What customers can see and request themselves',
      },
    ],
  },
  {
    id: 'money',
    label: 'Money',
    icon: 'Wallet',
    items: [
      {
        id: 'expenses',
        label: 'Expenses',
        href: '/money/expenses',
        permissions: ['expense.view'],
        description: 'What the business spent, and on what',
      },
      {
        id: 'treasury',
        label: 'Cash and bank',
        href: '/money/accounts',
        permissions: ['accounting.view'],
        description: 'Balances, transfers and reconciliation',
      },
      {
        id: 'receipts',
        label: 'Payments received',
        href: '/money/receipts',
        permissions: ['sales.receive_payment'],
        description: 'Settle a customer invoice',
      },
      {
        id: 'journals',
        label: 'Journals',
        href: '/money/journals',
        permissions: ['accounting.view'],
        description: 'The double-entry record, including manual entries',
      },
      {
        id: 'periods',
        label: 'Accounting periods',
        href: '/money/periods',
        permissions: ['accounting.view'],
        description: 'Close a month, and lock it',
      },
      {
        id: 'installments',
        label: 'Instalment plans',
        href: '/money/installments',
        permissions: ['installment.view'],
        feature: 'installments',
        description: 'Sales paid over time',
      },
      {
        id: 'assets',
        label: 'Fixed assets',
        href: '/money/assets',
        permissions: ['asset.view'],
        feature: 'assets',
        description: 'What the business owns, and what it has depreciated',
      },
      {
        id: 'investors',
        label: 'Investors',
        href: '/money/investors',
        permissions: ['investor.view'],
        feature: 'assets',
        description: 'Capital in, and drawings out',
      },
      {
        id: 'gateways',
        label: 'Card providers',
        href: '/money/gateways',
        permissions: ['gateway.view'],
        description: 'How card payments are taken and settled',
      },
    ],
  },
  {
    id: 'staff',
    label: 'People',
    icon: 'IdCard',
    items: [
      {
        id: 'employees',
        label: 'Employees',
        href: '/people/employees',
        permissions: ['hr.view'],
        feature: 'payroll',
        description: 'Who works here, and on what terms',
      },
      {
        id: 'attendance',
        label: 'Attendance and leave',
        href: '/people/attendance',
        permissions: ['hr.view'],
        feature: 'payroll',
        description: 'Hours worked and days off',
      },
      {
        id: 'payroll',
        label: 'Payroll',
        href: '/people/payroll',
        permissions: ['payroll.view'],
        feature: 'payroll',
        description: 'Run, approve and pay wages',
      },
      {
        id: 'users',
        label: 'Users and roles',
        href: '/people/users',
        permissions: ['identity.view'],
        description: 'Who can sign in, and what they may do',
      },
    ],
  },
  {
    id: 'reports',
    label: 'Reports',
    icon: 'ChartNoAxesColumn',
    items: [
      {
        id: 'financials',
        label: 'Financial statements',
        href: '/reports/financials',
        permissions: ['accounting.view'],
        description: 'Profit and loss, balance sheet, trial balance, cash flow',
      },
      {
        id: 'tax',
        label: 'Tax return',
        href: '/reports/tax',
        permissions: ['accounting.view'],
        description: 'The return for the period, and what it is made of',
      },
      {
        id: 'analytics',
        label: 'Trends',
        href: '/reports/analytics',
        permissions: ['report.view'],
        feature: 'analytics',
        description: 'Movers, forecast and profitability',
      },
      {
        id: 'saved',
        label: 'Saved reports',
        href: '/reports/saved',
        permissions: ['report.view'],
        description: 'Reports somebody set up to run again',
      },
    ],
  },
  {
    id: 'aftersales',
    label: 'After sales',
    icon: 'Wrench',
    items: [
      {
        id: 'service-jobs',
        label: 'Service jobs',
        href: '/aftersales/service',
        permissions: ['service.view'],
        feature: 'warranty',
        description: 'Repairs booked in, and parts used',
      },
      {
        id: 'return-requests',
        label: 'Return requests',
        href: '/aftersales/requests',
        permissions: ['portal.view'],
        description: 'What customers have asked to send back',
      },
    ],
  },
  {
    id: 'oversight',
    label: 'Oversight',
    icon: 'ShieldCheck',
    items: [
      {
        id: 'compliance',
        label: 'Compliance',
        href: '/oversight/compliance',
        permissions: ['compliance.view'],
        description: 'What is filed, what is due and what is blocked',
      },
      {
        id: 'audit',
        label: 'Audit trail',
        href: '/oversight/audit',
        permissions: ['accounting.view'],
        description: 'Who did what, and when',
      },
      {
        id: 'documents',
        label: 'Documents',
        href: '/oversight/documents',
        permissions: ['document.view'],
        description: 'Contracts, licences and their expiry dates',
      },
      {
        id: 'privacy',
        label: 'Privacy',
        href: '/oversight/privacy',
        permissions: ['privacy.view'],
        description: 'Consents, data requests, retention and legal holds',
      },
      {
        id: 'groups',
        label: 'Group companies',
        href: '/oversight/groups',
        permissions: ['group.view'],
        feature: 'consolidation',
        description: 'Consolidated statements and intercompany balances',
      },
      {
        id: 'backups',
        label: 'Backups',
        href: '/oversight/backups',
        permissions: ['backup.view'],
        description: 'What was backed up, and whether it verified',
      },
    ],
  },
  {
    id: 'settings',
    label: 'Settings',
    icon: 'Settings',
    items: [
      {
        id: 'business',
        label: 'Business details',
        href: '/settings/business',
        permissions: ['identity.view'],
        description: 'Companies, branches, logo and document templates',
      },
      {
        id: 'devices',
        label: 'Tills and devices',
        href: '/settings/devices',
        permissions: ['devices.view'],
        description: 'Which terminals may sell, and on what settings',
      },
      {
        id: 'einvoicing',
        label: 'E-invoicing',
        href: '/settings/einvoicing',
        permissions: ['einvoicing.view'],
        feature: 'einvoicing',
        description: 'ZATCA units, onboarding and credential renewal',
      },
      {
        id: 'integrations',
        label: 'Integrations',
        href: '/settings/integrations',
        permissions: ['integration.view'],
        description: 'API keys and webhooks',
      },
      {
        id: 'imports',
        label: 'Import data',
        href: '/settings/imports',
        permissions: ['data.import'],
        description: 'Bring products, customers or stock in from a file',
      },
      {
        id: 'subscription',
        label: 'Plan and billing',
        href: '/settings/subscription',
        permissions: ['subscription.view'],
        description: 'What the plan includes, and what has been invoiced',
      },
      {
        id: 'support',
        label: 'Support',
        href: '/settings/support',
        permissions: ['support.raise'],
        description: 'Ask RawSyst for help',
      },
    ],
  },
] as const;

/**
 * The platform workspace.
 *
 * A separate information architecture because it is a genuinely different job:
 * running the service, not running a shop. It is still the same website and the
 * same sign-in -- the operator does not go somewhere else, and does not pick a
 * mode.
 *
 * Every route here is `AccessSuperAdmin` in the Go table, so there are no
 * permission strings to name: holding the platform role is the whole condition.
 */
export const PLATFORM_NAV: readonly NavSection[] = [
  {
    id: 'operations',
    label: 'Operations',
    icon: 'Activity',
    items: [
      {
        id: 'health',
        label: 'Service health',
        href: '/platform',
        permissions: [],
        description: 'Load, failures and the shape of the platform',
      },
      {
        id: 'jobs',
        label: 'Failed jobs',
        href: '/platform/jobs',
        permissions: [],
        description: 'Background work that did not finish, and what to retry',
      },
      {
        id: 'support',
        label: 'Support queue',
        href: '/platform/support',
        permissions: [],
        description: 'Tickets raised by businesses, urgent first',
      },
    ],
  },
  {
    id: 'tenants',
    label: 'Businesses',
    icon: 'Building2',
    items: [
      {
        id: 'tenants',
        label: 'Businesses',
        href: '/platform/businesses',
        permissions: [],
        description: 'Every business on the platform, and whether it trades',
      },
      {
        id: 'onboard',
        label: 'Onboard a business',
        href: '/platform/businesses/new',
        permissions: [],
        description: 'Create a business and its first owner',
      },
      {
        id: 'billing',
        label: 'Subscriptions and dunning',
        href: '/platform/billing',
        permissions: [],
        description: 'Plans, limits, invoices and what is overdue',
      },
    ],
  },
  {
    id: 'regulatory',
    label: 'Regulatory',
    icon: 'Scale',
    items: [
      {
        id: 'rules',
        label: 'Rule registry',
        href: '/platform/rules',
        permissions: [],
        description: 'Dated legal values, and the evidence behind each',
      },
      {
        id: 'jurisdictions',
        label: 'Jurisdictions',
        href: '/platform/jurisdictions',
        permissions: [],
        description: 'Where the product trades, and under whose authority',
      },
      {
        id: 'rates',
        label: 'Tax schedules',
        href: '/platform/rates',
        permissions: [],
        description: 'Imported rates, and their route to being chargeable',
      },
      {
        id: 'subprocessors',
        label: 'Subprocessors',
        href: '/platform/subprocessors',
        permissions: [],
        description: 'Who processes data on the platform behalf',
      },
    ],
  },
];

export interface ResolvedItem extends NavItem {
  /** False when the permission is held but the plan does not include it. */
  includedInPlan: boolean;
}

export interface ResolvedSection extends Omit<NavSection, 'items'> {
  items: ResolvedItem[];
}

/**
 * Filters the architecture down to what this person can actually reach.
 *
 * An item with no permissions is open to anyone signed into the workspace --
 * that is how the platform entries are written, since holding the platform role
 * is their whole condition.
 *
 * `features` is the set the business plan includes, from
 * `GET /subscription/entitlements`. When it is unknown -- still loading, or the
 * call failed -- everything is treated as included: showing a link that turns
 * out to be refused is a smaller failure than hiding a module the business is
 * paying for.
 */
export function resolveNavigation(
  sections: readonly NavSection[],
  grants: Grants,
  features?: ReadonlySet<string>,
): ResolvedSection[] {
  const out: ResolvedSection[] = [];

  for (const section of sections) {
    const items: ResolvedItem[] = [];
    for (const item of section.items) {
      const permitted =
        item.permissions.length === 0 || grants.canAny(...item.permissions);
      if (!permitted) continue;

      const includedInPlan =
        !item.feature || features === undefined || features.has(item.feature);

      items.push({ ...item, includedInPlan });
    }
    if (items.length > 0) out.push({ ...section, items });
  }

  return out;
}

/** Every item in one flat list, for the command palette and route lookups. */
export function flattenNavigation(
  sections: readonly ResolvedSection[],
): ResolvedItem[] {
  return sections.flatMap((s) => s.items);
}

/**
 * The nav item a URL belongs to, for breadcrumbs and for marking the sidebar.
 *
 * Longest match wins, so `/stock/transfers` selects Transfers rather than Stock
 * on hand at `/stock`.
 */
export function findActiveItem(
  sections: readonly ResolvedSection[],
  pathname: string,
): { section: ResolvedSection; item: ResolvedItem } | null {
  let best: { section: ResolvedSection; item: ResolvedItem } | null = null;
  for (const section of sections) {
    for (const item of section.items) {
      if (pathname === item.href || pathname.startsWith(`${item.href}/`)) {
        if (!best || item.href.length > best.item.href.length) {
          best = { section, item };
        }
      }
    }
  }
  return best;
}
