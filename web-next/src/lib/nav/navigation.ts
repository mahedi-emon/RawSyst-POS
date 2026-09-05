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

import type { Key } from '@rawsyst/shared/i18n/strings';

import type { Permission } from '../auth/permissions';
import type { Grants } from '../auth/permissions';

export interface NavItem {
  /** Stable id, also the i18n key suffix. */
  id: string;
  /** Catalogue key. Navigation holds keys, never prose. */
  labelKey: Key;
  href: string;
  /** Any one of these opens the item. The ACT the screen exists to perform. */
  permissions: readonly Permission[];
  /**
   * Reads the screen cannot work without, all of which are required.
   *
   * `permissions` is any-of, which is right for "who may do this" and cannot
   * express "and they also need to be able to see the list". Two screens tried
   * to say it in a comment -- "BOTH, not either" -- and then listed only the
   * read, so the link appeared for anybody who could look and refused anybody
   * who could not also act. Three seeded roles met that: an Auditor offered
   * Goods receipts, a Branch Manager and a Purchase Manager offered Supplier
   * payments.
   */
  alsoNeeds?: readonly Permission[];
  /** The plan feature this needs, when the backend gates the route group. */
  feature?: string;
  /**
   * Whether the screen behind this exists yet.
   *
   * The architecture below describes the whole product — 73 items — and the
   * screens arrive over time. Without this, a cashier's sidebar offered
   * twenty-three links and seventeen of them went to a not-found page, which
   * reads as a broken product rather than an unfinished one.
   *
   * Kept as data rather than derived, because this module is client-side and
   * cannot read the app directory. `navigation.built.test.ts` reads it instead
   * and fails when the two disagree, so the flag cannot drift.
   */
  built?: true;
  /** Catalogue key for the one-line explanation. */
  descriptionKey?: Key;
}

export interface NavSection {
  id: string;
  labelKey: Key;
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
    labelKey: 'nx.nav.biz.overview',
    icon: 'LayoutDashboard',
    items: [
      {
        id: 'dashboard',
        labelKey: 'nx.nav.biz.overview.dashboard',
        href: '/dashboard',
        built: true,
        // GET /dashboard/overview is accounting.view and nothing else. Listing
        // sales.view and inventory.view here read as generous and was not: a
        // Cashier holds both, saw the link, and got a 403 -- verified against a
        // live cashier account. Every figure on that screen is computed from
        // the journal, which is why the route asks what it asks.
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.overview.dashboard',
      },
      {
        id: 'approvals',
        labelKey: 'nx.nav.biz.overview.approvals',
        href: '/approvals',
        permissions: ['approval.view'],
        feature: 'approvals',
        descriptionKey: 'nx.navd.biz.overview.approvals',
      },
    ],
  },
  {
    id: 'selling',
    labelKey: 'nx.nav.biz.selling',
    icon: 'ShoppingCart',
    items: [
      {
        id: 'pos',
        labelKey: 'nx.nav.biz.selling.pos',
        href: '/pos',
        built: true,
        permissions: ['sales.create'],
        descriptionKey: 'nx.navd.biz.selling.pos',
      },
      {
        id: 'sales',
        labelKey: 'nx.nav.biz.selling.sales',
        href: '/sales',
        built: true,
        permissions: ['sales.view'],
        descriptionKey: 'nx.navd.biz.selling.sales',
      },
      {
        id: 'returns',
        labelKey: 'nx.nav.biz.selling.returns',
        // Inside the POS, not the back office. `POST /pos/returns` goes
        // through `resolveTerminal` and refuses a session not bound to a
        // counter -- a credit note joins that terminal's own invoice chain.
        // A link to /sales/returns would always have failed.
        href: '/pos/returns',
        built: true,
        // `sales.refund` ALONE. This listed `sales.exchange` too, and the
        // screen behind it is guarded on refund -- so somebody holding only
        // exchange saw the link and got the refusal. Exchanging is its own
        // verb with its own screen, below.
        permissions: ['sales.refund'],
        descriptionKey: 'nx.navd.biz.selling.returns',
      },
      {
        id: 'exchanges',
        labelKey: 'nx.nav.biz.selling.exchanges',
        href: '/pos/exchanges',
        built: true,
        permissions: ['sales.exchange'],
        descriptionKey: 'nx.navd.biz.selling.exchanges',
      },
      {
        id: 'orders',
        labelKey: 'nx.nav.biz.selling.orders',
        href: '/orders',
        built: true,
        permissions: ['order.view'],
        feature: 'online_orders',
        descriptionKey: 'nx.navd.biz.selling.orders',
      },
      {
        id: 'deliveries',
        labelKey: 'nx.nav.biz.selling.deliveries',
        href: '/deliveries',
        permissions: ['delivery.view'],
        feature: 'online_orders',
        descriptionKey: 'nx.navd.biz.selling.deliveries',
      },
      {
        id: 'shifts',
        labelKey: 'nx.nav.biz.selling.shifts',
        href: '/shifts',
        permissions: ['sales.receive_payment'],
        descriptionKey: 'nx.navd.biz.selling.shifts',
      },
      {
        id: 'promotions',
        labelKey: 'nx.nav.biz.selling.promotions',
        href: '/promotions',
        permissions: ['promotion.view'],
        feature: 'promotions',
        descriptionKey: 'nx.navd.biz.selling.promotions',
      },
    ],
  },
  {
    id: 'catalogue',
    labelKey: 'nx.nav.biz.catalogue',
    icon: 'Package',
    items: [
      {
        id: 'products',
        labelKey: 'nx.nav.biz.catalogue.products',
        href: '/products',
        built: true,
        permissions: ['catalog.view'],
        descriptionKey: 'nx.navd.biz.catalogue.products',
      },
      {
        id: 'labels',
        labelKey: 'nx.nav.biz.catalogue.labels',
        href: '/products/labels',
        permissions: ['label.print', 'label.manage'],
        feature: 'label_studio',
        descriptionKey: 'nx.navd.biz.catalogue.labels',
      },
    ],
  },
  {
    id: 'stock',
    labelKey: 'nx.nav.biz.stock',
    icon: 'Boxes',
    items: [
      {
        id: 'on-hand',
        labelKey: 'nx.nav.biz.stock.on-hand',
        href: '/stock',
        built: true,
        permissions: ['inventory.view'],
        descriptionKey: 'nx.navd.biz.stock.on-hand',
      },
      {
        id: 'movements',
        labelKey: 'nx.nav.biz.stock.movements',
        href: '/stock/movements',
        built: true,
        permissions: ['inventory.view'],
        descriptionKey: 'nx.navd.biz.stock.movements',
      },
      {
        id: 'counts',
        labelKey: 'nx.nav.biz.stock.counts',
        // A count IS an adjustment -- same document, kind 'count' -- so they
        // share a list, and the list route is inventory.view. Naming
        // adjust_stock here showed the link to somebody the list refuses and
        // hid it from the manager who reads it to check what was written off.
        href: '/stock/adjustments',
        built: true,
        permissions: ['inventory.view'],
        descriptionKey: 'nx.navd.biz.stock.counts',
      },
      {
        id: 'transfers',
        labelKey: 'nx.nav.biz.stock.transfers',
        href: '/stock/transfers',
        built: true,
        // The list is inventory.view. transfer_stock is the verb, and holding it
        // alone reached a link the list refuses.
        permissions: ['inventory.view'],
        descriptionKey: 'nx.navd.biz.stock.transfers',
      },
      {
        id: 'batches',
        labelKey: 'nx.nav.biz.stock.batches',
        href: '/stock/batches',
        built: true,
        permissions: ['inventory.view'],
        descriptionKey: 'nx.navd.biz.stock.batches',
      },
      {
        id: 'production',
        labelKey: 'nx.nav.biz.stock.production',
        href: '/stock/production',
        built: true,
        permissions: ['inventory.view'],
        descriptionKey: 'nx.navd.biz.stock.production',
      },
      {
        id: 'locations',
        labelKey: 'nx.nav.biz.stock.locations',
        href: '/stock/locations',
        built: true,
        permissions: ['inventory.view'],
        descriptionKey: 'nx.navd.biz.stock.locations',
      },
      {
        id: 'serials',
        labelKey: 'nx.nav.biz.stock.serials',
        href: '/stock/serials',
        permissions: ['serial.view'],
        feature: 'warranty',
        descriptionKey: 'nx.navd.biz.stock.serials',
      },
    ],
  },
  {
    id: 'buying',
    labelKey: 'nx.nav.biz.buying',
    icon: 'Truck',
    items: [
      {
        id: 'suppliers',
        labelKey: 'nx.nav.biz.buying.suppliers',
        href: '/buying/suppliers',
        built: true,
        permissions: ['purchasing.view'],
        descriptionKey: 'nx.navd.biz.buying.suppliers',
      },
      {
        id: 'purchase-orders',
        labelKey: 'nx.nav.biz.buying.purchase-orders',
        href: '/buying/orders',
        built: true,
        permissions: ['purchasing.view'],
        descriptionKey: 'nx.navd.biz.buying.purchase-orders',
      },
      {
        id: 'goods-receipts',
        labelKey: 'nx.nav.biz.buying.receipts',
        href: '/buying/receipts',
        built: true,
        // BOTH, and now said in a way the resolver can act on. The screen books
        // a delivery, which is the act, against an order it has to list first.
        permissions: ['purchasing.receive_goods'],
        alsoNeeds: ['purchasing.view'],
        descriptionKey: 'nx.navd.biz.buying.receipts',
      },
      {
        id: 'bills',
        labelKey: 'nx.nav.biz.buying.bills',
        href: '/buying/bills',
        built: true,
        permissions: ['purchasing.view'],
        descriptionKey: 'nx.navd.biz.buying.bills',
      },
      {
        id: 'purchase-returns',
        labelKey: 'nx.nav.biz.buying.returns',
        href: '/buying/returns',
        built: true,
        // Reading a claim is `purchasing.view`; raising one is its own verb,
        // checked on the action screen. Taking a delivery in is a warehouse
        // act; sending one back reduces what the business owes and produces a
        // document the supplier will argue with.
        permissions: ['purchasing.view'],
        descriptionKey: 'nx.navd.biz.buying.returns',
      },
      {
        id: 'supplier-payments',
        labelKey: 'nx.nav.biz.buying.supplier-payments',
        href: '/buying/payments',
        built: true,
        // Paying is the act; finding the bill to pay is the read it needs.
        permissions: ['purchasing.pay_supplier'],
        alsoNeeds: ['purchasing.view'],
        descriptionKey: 'nx.navd.biz.buying.supplier-payments',
      },
      {
        id: 'requisitions',
        labelKey: 'nx.nav.biz.buying.requisitions',
        href: '/buying/requisitions',
        built: true,
        // An Inventory Keeper holds purchasing.request without purchasing.view
        // -- B5 puts asking for stock in reach of any authorised staff -- and
        // saw a link to a list they cannot read. Raising a requisition is a
        // separate act and belongs where a request-only holder can reach it,
        // not behind a list route they are refused.
        permissions: ['purchasing.view'],
        descriptionKey: 'nx.navd.biz.buying.requisitions',
      },
      {
        id: 'rfqs',
        labelKey: 'nx.nav.biz.buying.rfqs',
        href: '/buying/quotes',
        built: true,
        permissions: ['purchasing.view'],
        descriptionKey: 'nx.navd.biz.buying.rfqs',
      },
      {
        id: 'payables',
        labelKey: 'nx.nav.biz.buying.payables',
        href: '/buying/ageing',
        built: true,
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.buying.payables',
      },
    ],
  },
  {
    id: 'customers',
    labelKey: 'nx.nav.biz.customers',
    icon: 'Users',
    items: [
      {
        id: 'customers',
        labelKey: 'nx.nav.biz.customers.customers',
        href: '/customers',
        built: true,
        permissions: ['customers.view'],
        descriptionKey: 'nx.navd.biz.customers.customers',
      },
      {
        id: 'receivables',
        labelKey: 'nx.nav.biz.customers.receivables',
        href: '/customers/ageing',
        built: true,
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.customers.receivables',
      },
      {
        id: 'loyalty',
        labelKey: 'nx.nav.biz.customers.loyalty',
        href: '/customers/loyalty',
        permissions: ['loyalty.view'],
        feature: 'loyalty',
        descriptionKey: 'nx.navd.biz.customers.loyalty',
      },
      {
        id: 'wallets',
        labelKey: 'nx.nav.biz.customers.wallets',
        href: '/customers/wallets',
        permissions: ['wallet.view'],
        feature: 'loyalty',
        descriptionKey: 'nx.navd.biz.customers.wallets',
      },
      {
        id: 'portal',
        labelKey: 'nx.nav.biz.customers.portal',
        href: '/customers/portal',
        permissions: ['portal.view'],
        descriptionKey: 'nx.navd.biz.customers.portal',
      },
    ],
  },
  {
    id: 'money',
    labelKey: 'nx.nav.biz.money',
    icon: 'Wallet',
    items: [
      {
        id: 'expenses',
        labelKey: 'nx.nav.biz.money.expenses',
        href: '/money/expenses',
        built: true,
        permissions: ['expense.view'],
        descriptionKey: 'nx.navd.biz.money.expenses',
      },
      {
        id: 'expense-setup',
        labelKey: 'nx.nav.biz.money.expenseSetup',
        href: '/money/expenses/setup',
        built: true,
        // The configuration permission, not the reading one. Somebody who may
        // see what was spent has no business deciding which categories reclaim
        // VAT, and `GET /expenses/accounts` -- the chart this screen picks
        // from -- is behind the same permission for the same reason.
        permissions: ['expense.manage_heads'],
        descriptionKey: 'nx.navd.biz.money.expenseSetup',
      },
      {
        id: 'treasury',
        labelKey: 'nx.nav.biz.money.treasury',
        href: '/money/accounts',
        built: true,
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.money.treasury',
      },
      {
        id: 'money-transfers',
        labelKey: 'nx.nav.biz.money.transfers',
        href: '/money/transfers',
        built: true,
        // C2 covers inter-account movement, and it is a job of its own: cash
        // to the bank is neither income nor a cost, and a screen that folded
        // it into either would teach somebody to read it wrongly.
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.money.transfers',
      },
      {
        id: 'reconcile',
        labelKey: 'nx.nav.biz.money.reconcile',
        href: '/money/reconcile',
        built: true,
        // C11's own verb. `accounting.create` covers posting an entry;
        // reconciling asserts that the books agree with an outside party,
        // which is the assertion an auditor relies on, so migration 0081 gave
        // it a permission of its own -- and deliberately withheld it from the
        // Store Manager, who "cannot see bank ledgers".
        permissions: ['accounting.reconcile'],
        descriptionKey: 'nx.navd.biz.money.reconcile',
      },
      {
        id: 'receipts',
        labelKey: 'nx.nav.biz.money.receipts',
        href: '/money/receipts',
        built: true,
        permissions: ['sales.receive_payment'],
        // The list it works from -- what each customer still owes -- is
        // `customers.view`, and the screen is useless without it.
        alsoNeeds: ['customers.view'],
        descriptionKey: 'nx.navd.biz.money.receipts',
      },
      {
        id: 'chart',
        labelKey: 'nx.nav.biz.money.chart',
        href: '/money/chart',
        built: true,
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.money.chart',
      },
      {
        id: 'journals',
        labelKey: 'nx.nav.biz.money.journals',
        href: '/money/journals',
        built: true,
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.money.journals',
      },
      {
        id: 'periods',
        labelKey: 'nx.nav.biz.money.periods',
        href: '/money/periods',
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.money.periods',
      },
      {
        id: 'installments',
        labelKey: 'nx.nav.biz.money.installments',
        href: '/money/installments',
        permissions: ['installment.view'],
        feature: 'installments',
        descriptionKey: 'nx.navd.biz.money.installments',
      },
      {
        id: 'assets',
        labelKey: 'nx.nav.biz.money.assets',
        href: '/money/assets',
        permissions: ['asset.view'],
        feature: 'assets',
        descriptionKey: 'nx.navd.biz.money.assets',
      },
      {
        id: 'investors',
        labelKey: 'nx.nav.biz.money.investors',
        href: '/money/investors',
        permissions: ['investor.view'],
        feature: 'assets',
        descriptionKey: 'nx.navd.biz.money.investors',
      },
      {
        id: 'gateways',
        labelKey: 'nx.nav.biz.money.gateways',
        href: '/money/gateways',
        permissions: ['gateway.view'],
        descriptionKey: 'nx.navd.biz.money.gateways',
      },
    ],
  },
  {
    id: 'staff',
    labelKey: 'nx.nav.biz.staff',
    icon: 'IdCard',
    items: [
      {
        id: 'employees',
        labelKey: 'nx.nav.biz.staff.employees',
        href: '/people/employees',
        built: true,
        permissions: ['hr.view'],
        feature: 'payroll',
        descriptionKey: 'nx.navd.biz.staff.employees',
      },
      {
        id: 'attendance',
        labelKey: 'nx.nav.biz.staff.attendance',
        href: '/people/attendance',
        built: true,
        permissions: ['hr.view'],
        feature: 'payroll',
        descriptionKey: 'nx.navd.biz.staff.attendance',
      },
      {
        id: 'payroll',
        labelKey: 'nx.nav.biz.staff.payroll',
        href: '/people/payroll',
        built: true,
        permissions: ['payroll.view'],
        feature: 'payroll',
        descriptionKey: 'nx.navd.biz.staff.payroll',
      },
      {
        id: 'users',
        labelKey: 'nx.nav.biz.staff.users',
        href: '/people/users',
        built: true,
        permissions: ['identity.view'],
        descriptionKey: 'nx.navd.biz.staff.users',
      },
      {
        id: 'roles',
        labelKey: 'nx.nav.biz.staff.roles',
        href: '/people/roles',
        built: true,
        // The one permission that can hand out permissions. `identity.view`
        // gets the people list and stops there: reading who works here is not
        // deciding what they may do.
        permissions: ['identity.manage_roles'],
        // And the screen cannot draw at all without reading the role list,
        // which is `identity.view`. Both, in full: somebody holding only the
        // first would see the link and collect a 403 on the first request.
        alsoNeeds: ['identity.view'],
        descriptionKey: 'nx.navd.biz.staff.roles',
      },
    ],
  },
  {
    id: 'reports',
    labelKey: 'nx.nav.biz.reports',
    icon: 'ChartNoAxesColumn',
    items: [
      {
        id: 'financials',
        labelKey: 'nx.nav.biz.reports.financials',
        href: '/reports/financials',
        built: true,
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.reports.financials',
      },
      {
        id: 'tax',
        labelKey: 'nx.nav.biz.reports.tax',
        href: '/reports/tax',
        built: true,
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.reports.tax',
      },
      {
        id: 'analytics',
        labelKey: 'nx.nav.biz.reports.analytics',
        href: '/reports/analytics',
        built: true,
        permissions: ['report.view'],
        feature: 'analytics',
        descriptionKey: 'nx.navd.biz.reports.analytics',
      },
      {
        id: 'saved',
        labelKey: 'nx.nav.biz.reports.saved',
        href: '/reports/saved',
        built: true,
        permissions: ['report.view'],
        descriptionKey: 'nx.navd.biz.reports.saved',
      },
    ],
  },
  {
    id: 'aftersales',
    labelKey: 'nx.nav.biz.aftersales',
    icon: 'Wrench',
    items: [
      {
        id: 'service-jobs',
        labelKey: 'nx.nav.biz.aftersales.service-jobs',
        href: '/aftersales/service',
        permissions: ['service.view'],
        feature: 'warranty',
        descriptionKey: 'nx.navd.biz.aftersales.service-jobs',
      },
      {
        id: 'return-requests',
        labelKey: 'nx.nav.biz.aftersales.return-requests',
        href: '/aftersales/requests',
        permissions: ['portal.view'],
        descriptionKey: 'nx.navd.biz.aftersales.return-requests',
      },
    ],
  },
  {
    id: 'oversight',
    labelKey: 'nx.nav.biz.oversight',
    icon: 'ShieldCheck',
    items: [
      {
        id: 'compliance',
        labelKey: 'nx.nav.biz.oversight.compliance',
        href: '/oversight/compliance',
        built: true,
        permissions: ['compliance.view'],
        descriptionKey: 'nx.navd.biz.oversight.compliance',
      },
      {
        id: 'audit',
        labelKey: 'nx.nav.biz.oversight.audit',
        href: '/oversight/audit',
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.oversight.audit',
      },
      {
        id: 'documents',
        labelKey: 'nx.nav.biz.oversight.documents',
        href: '/oversight/documents',
        permissions: ['document.view'],
        descriptionKey: 'nx.navd.biz.oversight.documents',
      },
      {
        id: 'privacy',
        labelKey: 'nx.nav.biz.oversight.privacy',
        href: '/oversight/privacy',
        permissions: ['privacy.view'],
        descriptionKey: 'nx.navd.biz.oversight.privacy',
      },
      {
        id: 'groups',
        labelKey: 'nx.nav.biz.oversight.groups',
        href: '/oversight/groups',
        permissions: ['group.view'],
        feature: 'consolidation',
        descriptionKey: 'nx.navd.biz.oversight.groups',
      },
      {
        id: 'backups',
        labelKey: 'nx.nav.biz.oversight.backups',
        href: '/oversight/backups',
        permissions: ['backup.view'],
        descriptionKey: 'nx.navd.biz.oversight.backups',
      },
    ],
  },
  {
    id: 'settings',
    labelKey: 'nx.nav.biz.settings',
    icon: 'Settings',
    items: [
      {
        id: 'business',
        labelKey: 'nx.nav.biz.settings.business',
        href: '/settings/business',
        permissions: ['identity.view'],
        descriptionKey: 'nx.navd.biz.settings.business',
      },
      {
        id: 'devices',
        labelKey: 'nx.nav.biz.settings.devices',
        href: '/settings/devices',
        permissions: ['devices.view'],
        descriptionKey: 'nx.navd.biz.settings.devices',
      },
      {
        // Not `tax`: the reports section already uses that id for the return,
        // and the ids key the nav's read map, so a repeat makes two screens
        // claim one route.
        id: 'tax-setup',
        labelKey: 'nx.nav.biz.settings.tax',
        href: '/settings/tax',
        built: true,
        // Reading the tax position is reading the books: the rate, the
        // deadline and the retention period all explain figures on a report.
        // Recording an exchange rate needs accounting.create, and the screen
        // hides that panel rather than gating the whole page on it.
        permissions: ['accounting.view'],
        descriptionKey: 'nx.navd.biz.settings.tax',
      },
      {
        id: 'einvoicing',
        labelKey: 'nx.nav.biz.settings.einvoicing',
        href: '/settings/einvoicing',
        built: true,
        permissions: ['einvoicing.view'],
        feature: 'einvoicing',
        descriptionKey: 'nx.navd.biz.settings.einvoicing',
      },
      {
        id: 'integrations',
        labelKey: 'nx.nav.biz.settings.integrations',
        href: '/settings/integrations',
        permissions: ['integration.view'],
        descriptionKey: 'nx.navd.biz.settings.integrations',
      },
      {
        id: 'imports',
        labelKey: 'nx.nav.biz.settings.imports',
        href: '/settings/imports',
        permissions: ['data.import'],
        descriptionKey: 'nx.navd.biz.settings.imports',
      },
      {
        id: 'subscription',
        labelKey: 'nx.nav.biz.settings.subscription',
        href: '/settings/subscription',
        permissions: ['subscription.view'],
        descriptionKey: 'nx.navd.biz.settings.subscription',
      },
      {
        id: 'support',
        labelKey: 'nx.nav.biz.settings.support',
        href: '/settings/support',
        permissions: ['support.raise'],
        descriptionKey: 'nx.navd.biz.settings.support',
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
    labelKey: 'nx.nav.plat.operations',
    icon: 'Activity',
    items: [
      {
        id: 'health',
        labelKey: 'nx.nav.plat.operations.health',
        href: '/platform',
        built: true,
        permissions: [],
        descriptionKey: 'nx.navd.plat.operations.health',
      },
      {
        id: 'jobs',
        labelKey: 'nx.nav.plat.operations.jobs',
        href: '/platform/jobs',
        built: true,
        permissions: [],
        descriptionKey: 'nx.navd.plat.operations.jobs',
      },
      {
        id: 'support',
        labelKey: 'nx.nav.plat.operations.support',
        href: '/platform/support',
        built: true,
        permissions: [],
        descriptionKey: 'nx.navd.plat.operations.support',
      },
    ],
  },
  {
    id: 'tenants',
    labelKey: 'nx.nav.plat.tenants',
    icon: 'Building2',
    items: [
      {
        id: 'tenants',
        labelKey: 'nx.nav.plat.tenants.tenants',
        href: '/platform/businesses',
        built: true,
        permissions: [],
        descriptionKey: 'nx.navd.plat.tenants.tenants',
      },
      {
        id: 'onboard',
        labelKey: 'nx.nav.plat.tenants.onboard',
        href: '/platform/businesses/new',
        permissions: [],
        descriptionKey: 'nx.navd.plat.tenants.onboard',
      },
      {
        id: 'billing',
        labelKey: 'nx.nav.plat.tenants.billing',
        href: '/platform/billing',
        permissions: [],
        descriptionKey: 'nx.navd.plat.tenants.billing',
      },
    ],
  },
  {
    id: 'regulatory',
    labelKey: 'nx.nav.plat.regulatory',
    icon: 'Scale',
    items: [
      {
        id: 'rules',
        labelKey: 'nx.nav.plat.regulatory.rules',
        href: '/platform/rules',
        permissions: [],
        descriptionKey: 'nx.navd.plat.regulatory.rules',
      },
      {
        id: 'jurisdictions',
        labelKey: 'nx.nav.plat.regulatory.jurisdictions',
        href: '/platform/jurisdictions',
        permissions: [],
        descriptionKey: 'nx.navd.plat.regulatory.jurisdictions',
      },
      {
        id: 'rates',
        labelKey: 'nx.nav.plat.regulatory.rates',
        href: '/platform/rates',
        permissions: [],
        descriptionKey: 'nx.navd.plat.regulatory.rates',
      },
      {
        id: 'subprocessors',
        labelKey: 'nx.nav.plat.regulatory.subprocessors',
        href: '/platform/subprocessors',
        permissions: [],
        descriptionKey: 'nx.navd.plat.regulatory.subprocessors',
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
      // ALL of these, not any. A screen that lists orders in order to book a
      // delivery against one is useless to somebody who cannot list them.
      if ((item.alsoNeeds ?? []).some((p) => !grants.can(p))) continue;

      const includedInPlan =
        !item.feature || features === undefined || features.has(item.feature);

      items.push({ ...item, includedInPlan });
    }
    if (items.length > 0) out.push({ ...section, items });
  }

  return out;
}

/**
 * What to actually put in the sidebar.
 *
 * `resolveNavigation` answers "what may this person reach", which is about
 * permissions and is the same answer whether or not the screen is written yet.
 * This answers "what can we offer them today", which is that less the screens
 * that do not exist.
 *
 * The two are separate because they fail differently. A permission mistake
 * shows somebody a screen they are refused; an unbuilt link shows them a
 * not-found page. The first is a security-adjacent bug and the second is an
 * unfinished product, and a sidebar should admit to neither.
 */
export function visibleNavigation(
  sections: readonly NavSection[],
  grants: Grants,
  features?: ReadonlySet<string>,
): ResolvedSection[] {
  const out: ResolvedSection[] = [];
  for (const section of resolveNavigation(sections, grants, features)) {
    const items = section.items.filter((item) => item.built);
    if (items.length > 0) out.push({ ...section, items });
  }
  return out;
}

/**
 * Where somebody should arrive after signing in.
 *
 * Not `/dashboard`, which is what every signed-in person used to get. That
 * screen reads `GET /dashboard/overview` and the route is `accounting.view`,
 * so a Cashier, a Branch Manager and an Inventory Keeper all landed on a 403 --
 * verified against a live cashier account, which resolves to nineteen
 * permissions and none of them that one.
 *
 * The first item this person can actually reach, in the order the architecture
 * already puts them: overview first, then the day's work. A cashier's first
 * reachable item is the till, which is where a cashier should start anyway.
 *
 * Null when they can reach nothing at all, which is a real state -- somebody
 * whose last role was removed -- and one the caller has to say something about
 * rather than redirect into a loop.
 */
export function landingFor(
  sections: readonly NavSection[],
  grants: Grants,
  features?: ReadonlySet<string>,
): string | null {
  const reachable = flattenNavigation(visibleNavigation(sections, grants, features));
  // A module the plan does not include is a link that leads to an upsell, not
  // to work, so it is not where anybody should be dropped.
  const usable = reachable.find((item) => item.includedInPlan);
  return usable?.href ?? reachable[0]?.href ?? null;
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
