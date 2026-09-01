// The STAFF side of the two portals (blueprint F2, F3).
//
// Inviting a supplier contact, revoking one, and answering a customer's request
// to send something back. These are ordinary back-office calls on the staff
// session — the portal's own API lives in `portal.ts` and shares nothing with
// this file but the shape of the records.

import type { Client } from './client';
import type { PortalReturnRequest } from './portal';

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

export interface SupplierContact {
  id: string;
  supplier_id: string;
  supplier_name: string;
  full_name: string;
  email: string;
  is_active: boolean;
  invited_at: string;
  last_seen_at?: string;
}

export function listSupplierContacts(
  client: Client,
  companyId: string,
): Promise<{ data: SupplierContact[] }> {
  return client.send('GET', scoped('/api/v1/portal/contacts', companyId));
}

export function inviteSupplierContact(
  client: Client,
  companyId: string,
  body: {
    supplier_id: string;
    full_name: string;
    email: string;
    password: string;
  },
): Promise<{ data: SupplierContact[] }> {
  return client.send(
    'POST',
    scoped('/api/v1/portal/contacts', companyId),
    body,
  );
}

export function revokeSupplierContact(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send(
    'DELETE',
    scoped(`/api/v1/portal/contacts/${id}`, companyId),
  );
}

export function listReturnRequests(
  client: Client,
  companyId: string,
  openOnly = true,
): Promise<{ data: PortalReturnRequest[] }> {
  const suffix = openOnly ? '?open=true' : '';
  return client.send(
    'GET',
    scoped(`/api/v1/portal/return-requests${suffix}`, companyId),
  );
}

export function decideReturnRequest(
  client: Client,
  companyId: string,
  id: string,
  body: { accept: boolean; note?: string },
): Promise<void> {
  return client.send(
    'POST',
    scoped(`/api/v1/portal/return-requests/${id}/decide`, companyId),
    body,
  );
}
