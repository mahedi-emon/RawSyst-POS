// The Approval Centre (blueprint D5, F1) and the notification centre (D3).
//
// # Notifications are about the caller, so they carry no company in the path
//
// Every notification route resolves the caller from the token and scopes the
// query to them. The company still travels as a parameter because a person can
// belong to more than one and their inbox is per company, but there is no
// parameter anywhere here that names another person.

import type { Client } from './client';

export interface Decision {
  step: number;
  decision: string;
  reason?: string;
  decided_by?: string;
  decided_at: string;
}

export interface ApprovalRequest {
  id: string;
  subject: string;
  subject_id: string;
  summary: string;
  amount?: string;
  /** The currency `amount` is in. */
  currency?: string;
  status: string;
  current_step: number;
  /** So a screen can say "2 of 3" rather than leaving somebody to wonder
   *  whether their approval was the last one needed. */
  steps_total: number;
  rule_name?: string;
  requested_by?: string;
  requested_at: string;
  escalate_at?: string;
  decisions?: Decision[];
}

export interface ApprovalRule {
  id: string;
  name: string;
  is_active: boolean;
  subject: string;
  /** jsonb, as text. The engine is the only thing that interprets it, so the
   *  screen carries it unchanged rather than inventing a shape for it. */
  condition: string;
  action: string;
  steps: string;
  escalate_after_hours?: number;
  priority: number;
}

export interface Delegation {
  id: string;
  from: string;
  to: string;
  from_user_id: string;
  to_user_id: string;
  starts_on: string;
  ends_on: string;
  note?: string;
  /** Whether the cover is in force right now, so a screen does not leave
   *  somebody comparing two dates against today in their head. */
  is_live: boolean;
}

export interface Notification {
  id: string;
  kind: string;
  severity: string;
  title: string;
  body?: string;
  subject?: string;
  subject_id?: string;
  is_read: boolean;
  read_at?: string;
  created_at: string;
}

export interface NotificationPreference {
  kind: string;
  in_app: boolean;
  email: boolean;
  sms: boolean;
  push: boolean;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

// --- approvals ------------------------------------------------------------

export function listApprovals(
  client: Client,
  companyId: string,
  subject = '',
): Promise<{ data: ApprovalRequest[] }> {
  const q = subject ? `?subject=${encodeURIComponent(subject)}` : '';
  return client.send('GET', scoped('/api/v1/approvals' + q, companyId));
}

export function myApprovals(
  client: Client,
  companyId: string,
  includeSettled = false,
): Promise<{ data: ApprovalRequest[] }> {
  const q = includeSettled ? '?include_settled=true' : '';
  return client.send('GET', scoped('/api/v1/approvals/mine' + q, companyId));
}

export function readApproval(
  client: Client,
  companyId: string,
  id: string,
): Promise<ApprovalRequest> {
  return client.send('GET', scoped(`/api/v1/approvals/${id}`, companyId));
}

export function decideApproval(
  client: Client,
  companyId: string,
  id: string,
  body: { approve: boolean; reason?: string },
): Promise<ApprovalRequest> {
  return client.send(
    'POST',
    scoped(`/api/v1/approvals/${id}/decide`, companyId),
    body,
  );
}

export function escalateApprovals(
  client: Client,
  companyId: string,
): Promise<{ escalated: number }> {
  return client.send('POST', scoped('/api/v1/approvals/escalate', companyId));
}

export function listApprovalRules(
  client: Client,
  companyId: string,
): Promise<{ data: ApprovalRule[] }> {
  return client.send('GET', scoped('/api/v1/approval-rules', companyId));
}

export function saveApprovalRule(
  client: Client,
  companyId: string,
  body: {
    name: string;
    subject: string;
    action: string;
    condition?: unknown;
    steps?: unknown;
    escalate_after_hours?: number | null;
    priority?: number;
  },
): Promise<ApprovalRule> {
  return client.send('POST', scoped('/api/v1/approval-rules', companyId), body);
}

export function setRuleActive(
  client: Client,
  companyId: string,
  id: string,
  active: boolean,
): Promise<void> {
  return client.send(
    'POST',
    scoped(`/api/v1/approval-rules/${id}/active`, companyId),
    { is_active: active },
  );
}

export function listDelegations(
  client: Client,
  companyId: string,
): Promise<{ data: Delegation[] }> {
  return client.send('GET', scoped('/api/v1/approval-delegations', companyId));
}

export function delegate(
  client: Client,
  companyId: string,
  body: {
    to_user_id: string;
    starts_on: string;
    ends_on: string;
    from_user_id?: string;
    note?: string;
  },
): Promise<void> {
  return client.send(
    'POST',
    scoped('/api/v1/approval-delegations', companyId),
    body,
  );
}

// --- notifications --------------------------------------------------------

export function listNotifications(
  client: Client,
  companyId: string,
  unreadOnly = false,
): Promise<{ data: Notification[]; unread: number }> {
  const q = unreadOnly ? '?unread=true' : '';
  return client.send('GET', scoped('/api/v1/notifications' + q, companyId));
}

export function unreadCount(
  client: Client,
  companyId: string,
): Promise<{ unread: number }> {
  return client.send('GET', scoped('/api/v1/notifications/unread', companyId));
}

export function markRead(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send(
    'POST',
    scoped(`/api/v1/notifications/${id}/read`, companyId),
  );
}

export function markAllRead(
  client: Client,
  companyId: string,
): Promise<{ cleared: number }> {
  return client.send('POST', scoped('/api/v1/notifications/read', companyId));
}

export function readPreferences(
  client: Client,
  companyId: string,
): Promise<{ data: NotificationPreference[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/notifications/preferences', companyId),
  );
}

export function setPreference(
  client: Client,
  companyId: string,
  body: { kind: string; email: boolean; sms: boolean; push: boolean },
): Promise<{ data: NotificationPreference[] }> {
  return client.send(
    'PUT',
    scoped('/api/v1/notifications/preferences', companyId),
    body,
  );
}

export function announce(
  client: Client,
  companyId: string,
  body: { title: string; body?: string; severity?: string },
): Promise<void> {
  return client.send(
    'POST',
    scoped('/api/v1/notifications/announce', companyId),
    body,
  );
}
