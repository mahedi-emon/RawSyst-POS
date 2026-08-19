// E-invoicing units.
//
// An EGS unit is the software unit that signs invoices and owns one invoice
// sequence. A terminal points at the unit that signs for it, and a terminal
// with no unit cannot sell: the till resolves its unit on every sale.
//
// Nothing here onboards anything. The CSID fields are read-only — the server
// has no route that writes them, because doing so needs formats that have not
// been verified against ZATCA's published standards. What this module does is
// create the unit and record the nine CSR fields it will eventually be
// certified under.

import type { Client } from './client';

/** ZATCA's three architectures (Technical Guideline V2 §3.5). Which one a unit
 *  is decides where the signing key must live, so it is chosen once. */
export type Architecture = 'centralized_server' | 'branch_server' | 'smart_pos';

export type CsidStatus =
  | 'not_started'
  | 'compliance_csid'
  | 'production_csid'
  | 'live'
  | 'revoked'
  | 'expired';

/** The nine fields Technical Guideline V2 §3.3.3 requires in a CSR. */
export interface Csr {
  common_name: string;
  egs_serial_number: string;
  organization_identifier: string;
  organization_unit: string;
  organization_name: string;
  country: string;
  invoice_type: string;
  location: string;
  industry: string;
}

export const emptyCsr: Csr = {
  common_name: '',
  egs_serial_number: '',
  organization_identifier: '',
  organization_unit: '',
  organization_name: '',
  country: '',
  invoice_type: '',
  location: '',
  industry: '',
};

export interface EgsUnit {
  id: string;
  label: string;
  architecture: Architecture;

  /** Absent on a central unit, which serves the whole business. */
  store_id?: string;
  store?: string;

  csr: Csr;

  /** Read-only. Moves when onboarding ships. */
  csid_status: CsidStatus;
  csid_serial?: string;
  csid_issued_at?: string;
  csid_expires_at?: string;

  /** How many tills sign under this unit. */
  terminals: number;
  /** How long its invoice chain is. Non-zero means it is historical evidence. */
  invoices: number;

  /** Derived by the server: all nine CSR fields present. Not a claim that the
   *  unit is certified, only that it has what onboarding will ask for. */
  csr_complete: boolean;
}

export interface EgsUnitBody {
  label: string;
  architecture: Architecture;
  store_id?: string;
  csr: Csr;
}

export function listEgsUnits(client: Client, companyId: string): Promise<EgsUnit[]> {
  return client
    .send<{ data: EgsUnit[] }>('GET', `/api/v1/einvoicing/units?company_id=${companyId}`)
    .then((b) => b.data ?? []);
}

export function createEgsUnit(
  client: Client,
  companyId: string,
  body: EgsUnitBody,
): Promise<EgsUnit> {
  return client.send<EgsUnit>(
    'POST',
    `/api/v1/einvoicing/units?company_id=${companyId}`,
    body,
  );
}

/** Corrects the name, branch and CSR details. The architecture is fixed at
 *  creation and the server ignores it here. */
export function amendEgsUnit(
  client: Client,
  companyId: string,
  unitId: string,
  body: Omit<EgsUnitBody, 'architecture'>,
): Promise<EgsUnit> {
  return client.send<EgsUnit>(
    'PUT',
    `/api/v1/einvoicing/units/${unitId}?company_id=${companyId}`,
    body,
  );
}
