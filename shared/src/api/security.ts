// The second factor (blueprint H1), the role builder (A6.2), and the caller's
// own sessions (H1).
//
// # The recovery codes come back once
//
// `completeMFA` and `regenerateRecoveryCodes` are the only calls that ever
// return them, and the server has no route that would show them again. A screen
// holding them must therefore make somebody acknowledge they have written them
// down before it lets them navigate away — see MFAPanel.

import type { Client } from './client';

// --- the second factor ----------------------------------------------------

export interface MFAStatus {
  enabled: boolean;
  enrolled_at?: string;
  /** Codes still unspent, so somebody on their ninth of ten is warned. */
  recovery_remaining: number;
}

export interface MFAEnrolment {
  /** Base32, for somebody who cannot scan and has to type it. */
  secret: string;
  /** The otpauth:// payload a QR code encodes. */
  uri: string;
}

export function mfaStatus(client: Client): Promise<{ mfa: MFAStatus }> {
  return client.send('GET', '/api/v1/auth/mfa');
}

export function beginMFA(
  client: Client,
): Promise<{ enrolment: MFAEnrolment }> {
  return client.send('POST', '/api/v1/auth/mfa/begin');
}

export function completeMFA(
  client: Client,
  code: string,
): Promise<{ recovery_codes: string[] }> {
  return client.send('POST', '/api/v1/auth/mfa/complete', { code });
}

export function disableMFA(client: Client, code: string): Promise<void> {
  return client.send('POST', '/api/v1/auth/mfa/disable', { code });
}

export function regenerateRecoveryCodes(
  client: Client,
  code: string,
): Promise<{ recovery_codes: string[] }> {
  return client.send('POST', '/api/v1/auth/mfa/recovery-codes', { code });
}

// --- the caller's own sessions --------------------------------------------

export interface ActiveSession {
  id: string;
  device_label?: string;
  ip?: string;
  user_agent?: string;
  created_at: string;
  last_seen_at?: string;
  expires_at: string;
  /** The session making the request. Labelled rather than offered for signing
   *  out, so nobody ends their own by accident. */
  current: boolean;
}

export function mySessions(
  client: Client,
): Promise<{ data: ActiveSession[] }> {
  return client.send('GET', '/api/v1/auth/sessions');
}

export function revokeMySession(
  client: Client,
  id: string,
): Promise<void> {
  return client.send('DELETE', `/api/v1/auth/sessions/${id}`);
}

// --- the role builder -----------------------------------------------------

export interface PermissionOption {
  permission: string;
  section: string;
  label: string;
  label_ar?: string;
  label_bn?: string;
  /** Shown beside the tick box for anything that moves money, reveals pay or
   *  grants further permissions. */
  caution?: string;
  /** False when the caller does not hold it themselves and therefore cannot
   *  put it into a role. Marked rather than hidden. */
  holds: boolean;
}

export interface CustomRole {
  id?: string;
  key?: string;
  name: string;
  name_ar?: string;
  description?: string;
  permissions: string[];
  /** The built-in roles, which are copied rather than edited. */
  is_system: boolean;
  cloned_from?: string;
  /** How many people hold it, so a screen can say why it cannot be deleted. */
  in_use: number;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

export function listPermissions(
  client: Client,
  companyId: string,
): Promise<{ data: PermissionOption[] }> {
  return client.send('GET', scoped('/api/v1/permissions', companyId));
}

export function readRole(
  client: Client,
  companyId: string,
  id: string,
): Promise<{ role: CustomRole }> {
  return client.send('GET', scoped(`/api/v1/roles/${id}`, companyId));
}

export function saveRole(
  client: Client,
  companyId: string,
  body: {
    name: string;
    name_ar?: string;
    description?: string;
    permissions: string[];
    cloned_from?: string;
  },
  id?: string,
): Promise<{ role: CustomRole }> {
  return id
    ? client.send('PUT', scoped(`/api/v1/roles/${id}`, companyId), body)
    : client.send('POST', scoped('/api/v1/roles', companyId), body);
}

export function removeRole(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send('DELETE', scoped(`/api/v1/roles/${id}`, companyId));
}
