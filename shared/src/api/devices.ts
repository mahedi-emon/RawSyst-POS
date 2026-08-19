// Terminals, blueprint H3.
//
// The mirror of api/purchasing.ts and api/receivables.ts. Nothing here decides
// anything: the server owns the enrolment code, the secret, the status and the
// lifecycle rules, and this module states what happened and reads the answer.
//
// # Two calls carry no bearer token, deliberately
//
// `enrolTerminal` is how a till with no credential at all claims one — that is
// the whole problem H3 solves — and `terminalIdentity` presents the terminal's
// own secret rather than a session. Everything else needs a signed-in
// administrator with `devices.view` or `devices.manage`.

import type { Client } from './client';

export type TerminalStatus = 'pending' | 'active' | 'inactive' | 'revoked';

export interface Terminal {
  id: string;
  store_id: string;
  store: string;
  terminal_label: string;
  status: TerminalStatus;

  os?: string;
  app_version?: string;

  last_sync_at?: string;
  last_active_at?: string;
  enrolled_at?: string;

  revoked_at?: string;
  revoked_reason?: string;

  /** The e-invoicing unit this terminal signs under, and therefore which
   *  invoice sequence its sales join. Absent means the terminal cannot sell:
   *  the till resolves its unit on every sale and refuses when there is none. */
  egs_unit_id?: string;
  egs_unit?: string;

  /** Read from the EGS unit, never written here. Pairing a terminal is not
   *  onboarding it for e-invoicing; that is a separate act behind the P1
   *  verification gate. */
  csid_status?: string;
  csid_serial?: string;
  csid_expires_at?: string;

  /** A code has been issued and not yet used, so the screen can say "waiting to
   *  be paired" rather than only "pending". */
  pending_code: boolean;
  code_expires_at?: string;
}

export interface DeviceStore {
  id: string;
  code: string;
  name: string;
}

export function listTerminals(client: Client, companyId: string): Promise<Terminal[]> {
  return client
    .send<{ data: Terminal[] }>('GET', `/api/v1/devices?company_id=${companyId}`)
    .then((b) => b.data ?? []);
}

export function listDeviceStores(
  client: Client,
  companyId: string,
): Promise<DeviceStore[]> {
  return client
    .send<{ data: DeviceStore[] }>('GET', `/api/v1/devices/stores?company_id=${companyId}`)
    .then((b) => b.data ?? []);
}

/** The e-invoicing unit is required. A terminal without one pairs, reports
 *  itself healthy and is then refused by the till on its first sale, which is
 *  the state every terminal registered before this existed is still in. */
export function registerTerminal(
  client: Client,
  companyId: string,
  body: { store_id: string; terminal_label: string; egs_unit_id: string },
): Promise<Terminal> {
  return client.send<Terminal>('POST', `/api/v1/devices?company_id=${companyId}`, body);
}

/** Rename, and optionally move to another store of the SAME company.
 *
 *  Moving does not start a new ZATCA chain — 04-identity §9 keeps the chain with
 *  the device under its company's VAT registration. Moving to another company is
 *  refused by the server for exactly that reason. */
export function amendTerminal(
  client: Client,
  companyId: string,
  deviceId: string,
  body: { terminal_label: string; store_id?: string; egs_unit_id?: string },
): Promise<Terminal> {
  return client.send<Terminal>(
    'PUT',
    `/api/v1/devices/${deviceId}?company_id=${companyId}`,
    body,
  );
}

export function setTerminalActive(
  client: Client,
  companyId: string,
  deviceId: string,
  active: boolean,
): Promise<Terminal> {
  return client.send<Terminal>(
    'POST',
    `/api/v1/devices/${deviceId}/active?company_id=${companyId}`,
    { active },
  );
}

export function revokeTerminal(
  client: Client,
  companyId: string,
  deviceId: string,
  reason: string,
): Promise<Terminal> {
  return client.send<Terminal>(
    'POST',
    `/api/v1/devices/${deviceId}/revoke?company_id=${companyId}`,
    { reason },
  );
}

/** What the back office is shown once, and can never read again. */
export interface IssuedCode {
  code: string;
  expires_at: string;
  device_id: string;
  terminal_label: string;
}

export function issueEnrolmentCode(
  client: Client,
  companyId: string,
  deviceId: string,
): Promise<IssuedCode> {
  return client.send<IssuedCode>(
    'POST',
    `/api/v1/devices/${deviceId}/enrolment-code?company_id=${companyId}`,
    {},
  );
}
