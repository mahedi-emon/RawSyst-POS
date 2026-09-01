// The document store (blueprint D6), the PDPL register (E4), the storefront
// disclosures Saudi e-commerce law requires (E5), and the compliance dashboard
// (E7).
//
// # A document's bytes go up as base64 and come back as a URL
//
// The upload carries the file in the JSON body, the same way the company logo
// does, because nothing in this API is multipart. The download is an ordinary
// GET the browser can follow, so a screen links to it rather than fetching the
// bytes into memory to hand them straight back.
//
// # Nothing here decides whether a shop is compliant
//
// `ComplianceReport` is a set of readings, each with its own deadline where it
// has one. There is no score and no traffic light: a single tick over nine
// unrelated legal obligations is a claim this product cannot make.

import type { Client } from './client';

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

// --- documents (D6) -------------------------------------------------------

/** E4.1's four classes, applied to a whole file. */
export type DataClass =
  | 'public'
  | 'internal'
  | 'personal'
  | 'sensitive_personal';

export interface StoredDocument {
  id: string;
  entity_type: string;
  entity_id: string;
  file_name: string;
  content_type: string;
  byte_size: number;
  checksum: string;
  classification: DataClass;
  expires_on?: string;
  /** Negative once it has lapsed, so a screen can say "expired 9 days ago"
   *  without doing date arithmetic in three languages. */
  days_to_expiry?: number;
  note?: string;
  uploaded_by?: string;
  created_at: string;
}

export function listDocuments(
  client: Client,
  companyId: string,
  query: { entity_type?: string; entity_id?: string; q?: string } = {},
): Promise<{ data: StoredDocument[] }> {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) if (v) params.set(k, v);
  const suffix = params.toString() ? `?${params.toString()}` : '';
  return client.send('GET', scoped(`/api/v1/documents${suffix}`, companyId));
}

export function expiringDocuments(
  client: Client,
  companyId: string,
  withinDays = 60,
): Promise<{ data: StoredDocument[] }> {
  return client.send(
    'GET',
    scoped(
      `/api/v1/documents?expiring=true&within_days=${withinDays}`,
      companyId,
    ),
  );
}

export function uploadDocument(
  client: Client,
  companyId: string,
  body: {
    entity_type: string;
    entity_id: string;
    file_name: string;
    /** Base64, with no `data:` prefix. */
    data: string;
    classification?: DataClass;
    expires_on?: string;
    note?: string;
  },
): Promise<{ document: StoredDocument }> {
  return client.send('POST', scoped('/api/v1/documents', companyId), body);
}

export function removeDocument(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send(
    'DELETE',
    scoped(`/api/v1/documents/${id}`, companyId),
  );
}

/** The URL a link points at. Not fetched: the browser downloads it. */
export function documentFileUrl(companyId: string, id: string): string {
  return scoped(`/api/v1/documents/${id}/file`, companyId);
}

// --- consent (E4.1) -------------------------------------------------------

export type LawfulBasis =
  | 'consent'
  | 'contract'
  | 'legal_obligation'
  | 'legitimate_interest'
  | 'vital_interest'
  | 'public_interest';

export type ConsentPurpose =
  | 'transactional'
  | 'marketing'
  | 'profiling'
  | 'loyalty'
  | 'credit_assessment';

export type ConsentChannel =
  | 'sms'
  | 'email'
  | 'whatsapp'
  | 'phone'
  | 'post'
  | 'in_app'
  | 'any';

export interface Consent {
  id: string;
  subject_type: string;
  subject_id: string;
  subject_name?: string;
  lawful_basis: LawfulBasis;
  purpose: ConsentPurpose;
  channel: ConsentChannel;
  granted: boolean;
  granted_at: string;
  withdrawn_at?: string;
  proof: string;
  recorded_by?: string;
}

export function listConsents(
  client: Client,
  companyId: string,
  query: { subject_type?: string; subject_id?: string; live?: boolean } = {},
): Promise<{ data: Consent[] }> {
  const params = new URLSearchParams();
  if (query.subject_type) params.set('subject_type', query.subject_type);
  if (query.subject_id) params.set('subject_id', query.subject_id);
  if (query.live) params.set('live', 'true');
  const suffix = params.toString() ? `?${params.toString()}` : '';
  return client.send(
    'GET',
    scoped(`/api/v1/privacy/consents${suffix}`, companyId),
  );
}

export function recordConsent(
  client: Client,
  companyId: string,
  body: {
    subject_type: string;
    subject_id: string;
    lawful_basis: LawfulBasis;
    purpose: ConsentPurpose;
    channel: ConsentChannel;
    proof: string;
  },
): Promise<{ consent: Consent }> {
  return client.send(
    'POST',
    scoped('/api/v1/privacy/consents', companyId),
    body,
  );
}

export function withdrawConsent(
  client: Client,
  companyId: string,
  id: string,
): Promise<{ consent: Consent }> {
  return client.send(
    'POST',
    scoped(`/api/v1/privacy/consents/${id}/withdraw`, companyId),
  );
}

// --- data subject requests (E4.1) -----------------------------------------

export type RequestKind =
  | 'access'
  | 'export'
  | 'correction'
  | 'deletion'
  | 'objection'
  | 'portability';

export interface SubjectRequest {
  id: string;
  request_no: string;
  kind: RequestKind;
  status: string;
  subject_type: string;
  subject_id?: string;
  subject_name: string;
  subject_contact: string;
  received_at: string;
  due_at: string;
  extended_to?: string;
  extension_reason?: string;
  /** Counts down to whichever deadline is in force, negative once passed. */
  days_left: number;
  closed_at?: string;
  outcome?: string;
  outcome_note?: string;
  legal_hold_applied: boolean;
  handled_by?: string;
}

export function listSubjectRequests(
  client: Client,
  companyId: string,
  openOnly = false,
): Promise<{ data: SubjectRequest[] }> {
  const suffix = openOnly ? '?open=true' : '';
  return client.send(
    'GET',
    scoped(`/api/v1/privacy/requests${suffix}`, companyId),
  );
}

export function openSubjectRequest(
  client: Client,
  companyId: string,
  body: {
    kind: RequestKind;
    subject_type: string;
    subject_id?: string;
    subject_name: string;
    subject_contact: string;
  },
): Promise<{ request: SubjectRequest }> {
  return client.send(
    'POST',
    scoped('/api/v1/privacy/requests', companyId),
    body,
  );
}

export function extendSubjectRequest(
  client: Client,
  companyId: string,
  id: string,
  reason: string,
): Promise<{ request: SubjectRequest }> {
  return client.send(
    'POST',
    scoped(`/api/v1/privacy/requests/${id}/extend`, companyId),
    { reason },
  );
}

export function closeSubjectRequest(
  client: Client,
  companyId: string,
  id: string,
  body: { outcome: string; note?: string },
): Promise<{ request: SubjectRequest }> {
  return client.send(
    'POST',
    scoped(`/api/v1/privacy/requests/${id}/close`, companyId),
    body,
  );
}

// --- incidents (E4.1) -----------------------------------------------------

export interface Incident {
  id: string;
  incident_no: string;
  title: string;
  severity: string;
  status: string;
  what_happened: string;
  data_categories: string;
  subjects_affected?: number;
  consequences?: string;
  containment?: string;
  discovered_at: string;
  notify_due_at: string;
  /** The countdown, in hours, negative once the window has closed. */
  hours_left: number;
  sdaia_notified_at?: string;
  subjects_notified_at?: string;
  closed_at?: string;
  logged_by?: string;
}

export function listIncidents(
  client: Client,
  companyId: string,
  openOnly = false,
): Promise<{ data: Incident[] }> {
  const suffix = openOnly ? '?open=true' : '';
  return client.send(
    'GET',
    scoped(`/api/v1/privacy/incidents${suffix}`, companyId),
  );
}

export function logIncident(
  client: Client,
  companyId: string,
  body: {
    title: string;
    what_happened: string;
    data_categories: string;
    subjects_affected?: number;
    consequences?: string;
    containment?: string;
    severity?: string;
    discovered_at?: string;
  },
): Promise<{ incident: Incident }> {
  return client.send(
    'POST',
    scoped('/api/v1/privacy/incidents', companyId),
    body,
  );
}

export function notifyIncident(
  client: Client,
  companyId: string,
  id: string,
  who: 'sdaia' | 'subjects',
): Promise<{ incident: Incident }> {
  return client.send(
    'POST',
    scoped(`/api/v1/privacy/incidents/${id}/notify`, companyId),
    { who },
  );
}

export function closeIncident(
  client: Client,
  companyId: string,
  id: string,
  containment: string,
): Promise<{ incident: Incident }> {
  return client.send(
    'POST',
    scoped(`/api/v1/privacy/incidents/${id}/close`, companyId),
    { containment },
  );
}

// --- the register ---------------------------------------------------------

export interface ProcessingActivity {
  id?: string;
  name: string;
  purpose: string;
  lawful_basis: LawfulBasis;
  data_categories: string;
  subject_categories: string;
  recipients?: string;
  cross_border: boolean;
  destination_country?: string;
  transfer_safeguard?: string;
  retention_note?: string;
  system_name?: string;
  owner_name?: string;
  reviewed_on?: string;
}

export function listActivities(
  client: Client,
  companyId: string,
): Promise<{ data: ProcessingActivity[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/privacy/activities', companyId),
  );
}

export function saveActivity(
  client: Client,
  companyId: string,
  body: ProcessingActivity,
): Promise<{ activity: ProcessingActivity }> {
  return client.send(
    'PUT',
    scoped('/api/v1/privacy/activities', companyId),
    body,
  );
}

export function removeActivity(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send(
    'DELETE',
    scoped(`/api/v1/privacy/activities/${id}`, companyId),
  );
}

export interface RetentionPolicy {
  id?: string;
  data_category: string;
  retain_months: number;
  action: 'archive' | 'anonymize' | 'destroy';
  legal_note?: string;
  is_active: boolean;
  last_run_at?: string;
}

export function listRetention(
  client: Client,
  companyId: string,
): Promise<{ data: RetentionPolicy[] }> {
  return client.send('GET', scoped('/api/v1/privacy/retention', companyId));
}

export function saveRetention(
  client: Client,
  companyId: string,
  body: RetentionPolicy,
): Promise<{ policy: RetentionPolicy }> {
  return client.send(
    'PUT',
    scoped('/api/v1/privacy/retention', companyId),
    body,
  );
}

export interface LegalHold {
  id?: string;
  name: string;
  reason: string;
  subject_type?: string;
  subject_id?: string;
  data_category?: string;
  placed_at?: string;
  released_at?: string;
  placed_by?: string;
}

export function listHolds(
  client: Client,
  companyId: string,
): Promise<{ data: LegalHold[] }> {
  return client.send('GET', scoped('/api/v1/privacy/holds', companyId));
}

export function placeHold(
  client: Client,
  companyId: string,
  body: LegalHold,
): Promise<{ hold: LegalHold }> {
  return client.send('POST', scoped('/api/v1/privacy/holds', companyId), body);
}

export function releaseHold(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send(
    'POST',
    scoped(`/api/v1/privacy/holds/${id}/release`, companyId),
  );
}

export interface Destruction {
  id: string;
  data_category: string;
  entity_type?: string;
  action: string;
  row_count: number;
  reason: string;
  executed_at: string;
  executed_by?: string;
}

export function listDestructions(
  client: Client,
  companyId: string,
): Promise<{ data: Destruction[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/privacy/destructions', companyId),
  );
}

// --- settings and disclosures ---------------------------------------------

export interface PrivacySettings {
  dpo_name?: string;
  dpo_email?: string;
  dpo_phone?: string;
  dpo_external: boolean;
  sdaia_registration_ref?: string;
  controller_registered_on?: string;
  privacy_notice_url?: string;
  /** The tenant's, not the company's: it decides which database the rows are
   *  in, and a group's companies cannot be in two places at once. */
  data_region: string;
}

export function getPrivacySettings(
  client: Client,
  companyId: string,
): Promise<{ settings: PrivacySettings }> {
  return client.send('GET', scoped('/api/v1/privacy/settings', companyId));
}

export function savePrivacySettings(
  client: Client,
  companyId: string,
  body: PrivacySettings,
): Promise<{ settings: PrivacySettings }> {
  return client.send(
    'PUT',
    scoped('/api/v1/privacy/settings', companyId),
    body,
  );
}

export interface Disclosure {
  registration_ref?: string;
  registration_channel?: string;
  verification_badge_url?: string;
  return_policy?: string;
  return_policy_ar?: string;
  delivery_terms?: string;
  delivery_terms_ar?: string;
  contact_email?: string;
  contact_phone?: string;
  support_hours?: string;
  cooling_off_days?: number;
  /** Read-only, from the company record. */
  cr_number?: string;
  vat_number?: string;
  /** The required disclosures that are not filled in. Empty means complete. */
  missing: string[];
}

export function getDisclosure(
  client: Client,
  companyId: string,
): Promise<{ disclosure: Disclosure }> {
  return client.send('GET', scoped('/api/v1/privacy/disclosure', companyId));
}

export function saveDisclosure(
  client: Client,
  companyId: string,
  body: Disclosure,
): Promise<{ disclosure: Disclosure }> {
  return client.send(
    'PUT',
    scoped('/api/v1/privacy/disclosure', companyId),
    body,
  );
}

export interface Subprocessor {
  id: string;
  name: string;
  purpose: string;
  country: string;
  data_categories: string;
  safeguard?: string;
  dpa_signed_on?: string;
  is_active: boolean;
}

export function listSubprocessors(
  client: Client,
  companyId: string,
): Promise<{ data: Subprocessor[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/privacy/subprocessors', companyId),
  );
}

// --- the compliance dashboard (E7) ----------------------------------------

export interface ComplianceReport {
  invoicing: {
    started: boolean;
    status: string;
    devices: number;
    devices_ready: number;
    pending: number;
    failed: number;
    rejected: number;
  };
  vat: {
    registered: boolean;
    vat_number?: string;
    standard_rate?: string;
    next_filing_due?: string;
    days_to_filing?: number;
    open_ended_periods: number;
  };
  privacy: {
    customers: number;
    marketing_consent: number;
    open_requests: number;
    overdue_requests: number;
    soonest_due_days?: number;
    open_incidents: number;
    incident_hours_left?: number;
    incidents_unnotified: number;
    retention_policies: number;
    retention_last_run?: string;
    processing_activities: number;
    dpo_appointed: boolean;
    legal_holds: number;
  };
  storefront: { missing: string[] };
  payroll: {
    last_run_period?: string;
    unsubmitted_runs: number;
    /** False while the wage-file rule is still unverified. The screen says so
     *  rather than showing a date this product invented. */
    deadline_known: boolean;
    next_deadline?: string;
    days_to_deadline?: number;
  };
  people: { expiring_soon: number; expired: number };
  records: {
    retention_years: number;
    oldest_invoice?: string;
    last_verified_backup?: string;
    backup_age_days?: number;
  };
  unverified_rules: number;
  blocking_rules: number;
}

export function complianceReport(
  client: Client,
  companyId: string,
): Promise<{ report: ComplianceReport }> {
  return client.send('GET', scoped('/api/v1/compliance', companyId));
}
