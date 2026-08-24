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

// ---------------------------------------------------------------------------
// Onboarding with ZATCA.
//
// The private key is NOT here, and that is the design rather than an omission.
// docs/system-design/01-invoice-zatca-engine.md §7 records it as a locked rule:
// the key pair is generated on the terminal, kept in the OS keystore, and never
// leaves the device. What travels is the certificate REQUEST, which carries the
// public half and the registration details, and never the private one.
//
// Nothing here ever receives the CSID secret. The server has no field for it on
// any response, so the browser cannot be given one to mislay.

/** Which ZATCA stack a credential belongs to. Sandbox and production issue
 *  credentials that do not work against each other, so this is never guessed. */
export type ZatcaEnvironment = 'sandbox' | 'simulation' | 'production';

export type CredentialStatus =
  | 'requested'
  | 'issued'
  | 'failed'
  | 'revoked'
  | 'superseded';

/** One credential, as a screen shows it. No secret, by construction. */
export interface CredentialSummary {
  status: CredentialStatus;
  csid: string;
  issued_at: string | null;
  expires_at: string | null;
  /** What ZATCA said when it refused, verbatim. Onboarding refusals name the
   *  registration field that was wrong, and a paraphrase would discard it. */
  last_error: string;
  attempts: number;
  key_version: number;
}

export interface OnboardingStatus {
  egs_unit_id: string;
  environment: ZatcaEnvironment;
  compliance: CredentialSummary | null;
  production: CredentialSummary | null;
  /** True only for a usable PRODUCTION credential. A compliance CSID means
   *  onboarding has started, not that invoices can be reported. */
  connected: boolean;
  needs_renewal: boolean;
  /** What to do next, in words, from the server so both clients agree. */
  next_action: string;
}

export interface CsidIssued {
  status: string;
  csid: string;
  expires_at: string | null;
  request_id: number;
}

export function readOnboardingStatus(
  client: Client,
  unitId: string,
  environment: ZatcaEnvironment,
): Promise<OnboardingStatus> {
  return client.send<OnboardingStatus>(
    'GET',
    `/api/v1/einvoicing/units/${unitId}/onboarding?environment=${environment}`,
  );
}

/** Step 1: the certificate request and the one-time password from the
 *  taxpayer's own Fatoora portal, in exchange for a compliance CSID.
 *
 *  The OTP goes in the BODY, never the query string: query strings reach
 *  access logs, proxy logs and browser history. */
export function requestComplianceCsid(
  client: Client,
  unitId: string,
  body: { environment: ZatcaEnvironment; csr: string; otp: string },
): Promise<CsidIssued> {
  return client.send<CsidIssued>(
    'POST',
    `/api/v1/einvoicing/units/${unitId}/onboarding/compliance`,
    body,
  );
}

/** Step 2: promote to production. No OTP -- the compliance credential is
 *  itself the proof that one was presented. */
export function requestProductionCsid(
  client: Client,
  unitId: string,
  body: { environment: ZatcaEnvironment; csr: string },
): Promise<CsidIssued> {
  return client.send<CsidIssued>(
    'POST',
    `/api/v1/einvoicing/units/${unitId}/onboarding/production`,
    body,
  );
}
