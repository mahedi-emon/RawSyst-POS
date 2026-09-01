// The subscription, its entitlements and its invoices (blueprint H5), and
// multi-company groups with consolidated statements (F4).
//
// # Entitlements gate what a screen offers, not what a person may do
//
// `Entitlement.allowed` answers a commercial question: has this client paid for
// this module. Permissions answer a different one, and both have to pass. A
// screen hidden because the plan does not include it says so; a screen hidden
// because the person lacks the permission is simply not there.
//
// # A group statement is not a report
//
// It reads every company's books at once, so it carries `group.view` rather
// than `report.view`. A consolidation at less than full ownership does not
// balance, and correctly so — the missing side is the minority interest this
// product does not compute, which is why `balanced` is reported rather than
// asserted.

import type { Client } from './client';

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

// --- the subscription (H5) ------------------------------------------------

export type PlanTier = 'starter' | 'professional' | 'business' | 'enterprise';

export interface PlanLimits {
  max_companies: number;
  max_stores: number;
  max_users: number;
  max_terminals: number;
  max_skus: number;
  max_custom_roles: number;
  max_storage_mb: number;
  sms_credits: number;
  /** What is actually used, so a screen shows 3 of 5 rather than 5. */
  companies: number;
  stores: number;
  users: number;
  terminals: number;
}

export interface Subscription {
  tier: PlanTier;
  cycle: 'monthly' | 'yearly' | 'lifetime';
  price: string;
  currency: string;
  status: string;
  started_on: string;
  trial_ends_on?: string;
  current_period_end?: string;
  cancelled_on?: string;
  grace_days: number;
  note?: string;
  outstanding: string;
  days_overdue?: number;
  limits: PlanLimits;
}

export interface Entitlement {
  feature: string;
  /** The answer after the plan and this client's exceptions. */
  allowed: boolean;
  /** What the tier alone says, so a screen can show "granted to you". */
  in_plan: boolean;
  reason?: string;
  expires_on?: string;
}

export interface SubscriptionInvoice {
  id: string;
  invoice_no: string;
  period_start: string;
  period_end: string;
  amount: string;
  currency: string;
  status: string;
  issued_on: string;
  due_on: string;
  paid_at?: string;
  payment_ref?: string;
  note?: string;
  /** Computed from the due date and the status, never stored. */
  overdue: boolean;
}

export function getSubscription(
  client: Client,
): Promise<{ subscription: Subscription }> {
  return client.send('GET', '/api/v1/subscription');
}

export function getEntitlements(
  client: Client,
): Promise<{ data: Entitlement[] }> {
  return client.send('GET', '/api/v1/subscription/entitlements');
}

export function listSubscriptionInvoices(
  client: Client,
): Promise<{ data: SubscriptionInvoice[] }> {
  return client.send('GET', '/api/v1/subscription/invoices');
}

export function listPlans(
  client: Client,
): Promise<{ plans: Record<string, string[]> }> {
  return client.send('GET', '/api/v1/plans');
}

// --- the platform's side --------------------------------------------------

export function platformSubscription(
  client: Client,
  tenantId: string,
): Promise<{
  subscription: Subscription;
  invoices: SubscriptionInvoice[];
}> {
  return client.send(
    'GET',
    `/api/v1/platform/tenants/${tenantId}/subscription`,
  );
}

export function setPlan(
  client: Client,
  tenantId: string,
  body: {
    tier: PlanTier;
    cycle: string;
    price: string;
    currency: string;
    status?: string;
    trial_ends_on?: string;
    grace_days?: number;
    note?: string;
  },
): Promise<{ subscription: Subscription }> {
  return client.send(
    'PUT',
    `/api/v1/platform/tenants/${tenantId}/subscription`,
    body,
  );
}

export function setFeature(
  client: Client,
  tenantId: string,
  body: {
    feature: string;
    enabled?: boolean;
    reason?: string;
    expires_on?: string;
    /** Drops the exception, so the plan's own answer resumes. */
    clear?: boolean;
  },
): Promise<{ subscription: Subscription }> {
  return client.send(
    'PUT',
    `/api/v1/platform/tenants/${tenantId}/features`,
    body,
  );
}

export function issueSubscriptionInvoice(
  client: Client,
  tenantId: string,
  body: {
    period_start: string;
    period_end: string;
    amount: string;
    note?: string;
  },
): Promise<{ invoice: SubscriptionInvoice }> {
  return client.send(
    'POST',
    `/api/v1/platform/tenants/${tenantId}/invoices`,
    body,
  );
}

export function settleSubscriptionInvoice(
  client: Client,
  invoiceId: string,
  body: { payment_ref?: string; void?: boolean; reason?: string },
): Promise<{ invoice: SubscriptionInvoice }> {
  return client.send(
    'POST',
    `/api/v1/platform/invoices/${invoiceId}/settle`,
    body,
  );
}

export function runDunning(
  client: Client,
): Promise<{ suspended: number }> {
  return client.send('POST', '/api/v1/platform/dunning');
}

// --- groups and consolidation (F4) ----------------------------------------

export interface GroupMember {
  company_id: string;
  name: string;
  base_currency: string;
  ownership_pct: string;
  is_parent: boolean;
  joined_on: string;
  left_on?: string;
}

export interface CompanyGroup {
  id: string;
  name: string;
  name_ar?: string;
  presentation_currency: string;
  members: GroupMember[];
}

export interface ConsolidatedLine {
  code: string;
  name: string;
  name_ar?: string;
  type: string;
  amount: string;
  by_company: Record<string, string>;
}

export interface Contribution {
  company_id: string;
  name: string;
  base_currency: string;
  ownership_pct: string;
  /** Its books are not in the group's presentation currency. Shown
   *  unconverted and flagged, rather than translated with a rate nobody
   *  chose. */
  currency_differs: boolean;
}

export interface ConsolidatedStatement {
  group_id: string;
  name: string;
  presentation_currency: string;
  from?: string;
  to: string;
  companies: Contribution[];
  /** False when a member keeps its books in another currency. */
  comparable: boolean;
  lines: ConsolidatedLine[];
  revenue?: string;
  cost_of_sales?: string;
  gross_profit?: string;
  expenses?: string;
  net_profit?: string;
  assets?: string;
  liabilities?: string;
  equity?: string;
  balanced?: boolean;
  eliminated_entries: number;
  eliminated_amount: string;
}

export interface IntercompanyEntry {
  entry_id: string;
  entry_no: string;
  entry_date: string;
  memo?: string;
  company_id: string;
  counterparty_id: string;
  kind: string;
  note?: string;
  amount: string;
  marked_by?: string;
}

export function listGroups(
  client: Client,
  companyId: string,
): Promise<{ data: CompanyGroup[] }> {
  return client.send('GET', scoped('/api/v1/groups', companyId));
}

export function saveGroup(
  client: Client,
  companyId: string,
  body: { name: string; name_ar?: string; presentation_currency: string },
  id?: string,
): Promise<{ group: CompanyGroup }> {
  return id
    ? client.send('PUT', scoped(`/api/v1/groups/${id}`, companyId), body)
    : client.send('POST', scoped('/api/v1/groups', companyId), body);
}

export function removeGroup(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send('DELETE', scoped(`/api/v1/groups/${id}`, companyId));
}

export function addGroupMember(
  client: Client,
  companyId: string,
  groupId: string,
  body: { company_id: string; ownership_pct?: string; is_parent?: boolean },
): Promise<{ group: CompanyGroup }> {
  return client.send(
    'POST',
    scoped(`/api/v1/groups/${groupId}/members`, companyId),
    body,
  );
}

export function removeGroupMember(
  client: Client,
  companyId: string,
  groupId: string,
  memberId: string,
): Promise<void> {
  return client.send(
    'DELETE',
    scoped(`/api/v1/groups/${groupId}/members/${memberId}`, companyId),
  );
}

export function groupStatement(
  client: Client,
  companyId: string,
  groupId: string,
  query: { statement: 'profit_and_loss' | 'balance_sheet'; from?: string; to?: string },
): Promise<{ statement: ConsolidatedStatement }> {
  const params = new URLSearchParams({ statement: query.statement });
  if (query.from) params.set('from', query.from);
  if (query.to) params.set('to', query.to);
  return client.send(
    'GET',
    scoped(
      `/api/v1/groups/${groupId}/statement?${params.toString()}`,
      companyId,
    ),
  );
}

export function listIntercompany(
  client: Client,
  companyId: string,
  groupId: string,
  from: string,
  to: string,
): Promise<{ data: IntercompanyEntry[] }> {
  return client.send(
    'GET',
    scoped(
      `/api/v1/groups/${groupId}/intercompany?from=${from}&to=${to}`,
      companyId,
    ),
  );
}

export function markIntercompany(
  client: Client,
  companyId: string,
  body: {
    entry_id: string;
    counterparty_id?: string;
    kind?: string;
    note?: string;
    unmark?: boolean;
  },
): Promise<void> {
  return client.send(
    'POST',
    scoped('/api/v1/groups/intercompany', companyId),
    body,
  );
}
