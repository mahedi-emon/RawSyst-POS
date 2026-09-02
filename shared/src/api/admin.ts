// Webhooks and API keys (blueprint H6), migration and export (H7), backups
// (H4), support (H10), and the Super Admin control plane (H8).
//
// # `Minted` is the only type here that carries a secret
//
// It is returned once, by the route that creates a key, and by nothing else.
// The type is separate from `APIKey` so that a screen holding a list cannot
// accidentally be handed one that has a secret on it — the shape says which is
// which.

import type { Client } from './client';

export interface WebhookEndpoint {
  id: string;
  name: string;
  url: string;
  is_active: boolean;
  events: string[];
  created_at: string;
  created_by?: string;
  /** How it has been behaving. A list without these is a list of URLs somebody
   *  has to test one at a time to find the broken one. */
  queued: number;
  failed: number;
  last_delivered_at?: string;
  last_error?: string;
}

export interface WebhookDelivery {
  id: string;
  event: string;
  status: string;
  attempts: number;
  response_status?: number;
  last_error?: string;
  next_attempt_at?: string;
  delivered_at?: string;
  created_at: string;
}

export interface APIKey {
  id: string;
  name: string;
  key_prefix: string;
  permissions: string[];
  last_used_at?: string;
  expires_at?: string;
  revoked_at?: string;
  created_at: string;
  created_by?: string;
  /** Not revoked and not expired, folded into one answer so a screen does not
   *  leave somebody comparing a date against today. */
  is_live: boolean;
}

/** A key at the one moment it can be read. */
export interface Minted extends APIKey {
  secret: string;
}

export interface ImportShape {
  kind: string;
  label: string;
  required: string[];
  optional: string[];
}

export interface ExportKind {
  kind: string;
  label: string;
  filename: string;
}

export interface ImportRow {
  row_no: number;
  raw: Record<string, string>;
  status: string;
  error?: string;
}

export interface ImportBatch {
  id: string;
  kind: string;
  filename?: string;
  status: string;
  mapping: Record<string, string>;
  total_rows: number;
  valid_rows: number;
  error_rows: number;
  imported_rows: number;
  created_by?: string;
  created_at: string;
  committed_at?: string;
  rows?: ImportRow[];
}

export interface Backup {
  id: string;
  kind: string;
  status: string;
  location?: string;
  size_bytes?: number;
  checksum?: string;
  verified_at?: string;
  verify_error?: string;
  started_at: string;
  finished_at?: string;
  error?: string;
  requested_by?: string;
}

export interface BackupHealth {
  last_verified_at?: string;
  last_run_at?: string;
  last_status?: string;
  hours_since_verified?: number;
  recent_failures: number;
  /** The judgement, made once on the server so every screen agrees. */
  at_risk: boolean;
  summary: string;
}

export interface TicketMessage {
  id: string;
  body: string;
  from_platform: boolean;
  author?: string;
  created_at: string;
}

export interface Ticket {
  id: string;
  ticket_no: string;
  subject: string;
  body: string;
  kind: string;
  priority: string;
  status: string;
  raised_by?: string;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
  tenant?: string;
  messages?: TicketMessage[];
}

export interface PlatformHealth {
  database_ok: boolean;
  database_latency_ms: number;
  tenants: number;
  active_tenants: number;
  companies: number;
  users: number;
  active_users_30d: number;
  terminals: number;
  jobs_queued: number;
  jobs_running: number;
  jobs_failed_24h: number;
  jobs_dead: number;
  submissions_pending: number;
  submissions_failed: number;
  tenants_with_verified_backup: number;
  tenants_without_verified_backup: number;
  sync_failures_24h: number;
  tickets_open: number;
  tickets_waiting_on_support: number;
  checked_at: string;
}

export interface PlatformTenant {
  id: string;
  name: string;
  plan_tier?: string;
  status?: string;
  /** The market this account was sold into: `sa`, `bd` or `us`. */
  market?: string;
  companies: number;
  users: number;
  created_at: string;
  last_activity?: string;
  backup_verified_at?: string;
}

export interface FailedJob {
  id: string;
  tenant_id?: string;
  tenant?: string;
  kind: string;
  status: string;
  attempts: number;
  last_error?: string;
  failed_at: string;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

// --- webhooks -------------------------------------------------------------

export function listWebhooks(
  client: Client,
  companyId: string,
): Promise<{ data: WebhookEndpoint[]; events: string[] }> {
  return client.send('GET', scoped('/api/v1/webhooks', companyId));
}

export function createWebhook(
  client: Client,
  companyId: string,
  body: { name: string; url: string; events: string[] },
): Promise<WebhookEndpoint> {
  return client.send('POST', scoped('/api/v1/webhooks', companyId), body);
}

export function setWebhookActive(
  client: Client,
  companyId: string,
  id: string,
  active: boolean,
): Promise<void> {
  return client.send(
    'POST',
    scoped(`/api/v1/webhooks/${id}/active`, companyId),
    { is_active: active },
  );
}

export function webhookDeliveries(
  client: Client,
  companyId: string,
  id: string,
): Promise<{ data: WebhookDelivery[] }> {
  return client.send(
    'GET',
    scoped(`/api/v1/webhooks/${id}/deliveries`, companyId),
  );
}

// --- API keys -------------------------------------------------------------

export function listAPIKeys(
  client: Client,
  companyId: string,
  includeRevoked = false,
): Promise<{ data: APIKey[]; grantable: string[] }> {
  const q = includeRevoked ? '?include_revoked=true' : '';
  return client.send('GET', scoped('/api/v1/api-keys' + q, companyId));
}

export function mintAPIKey(
  client: Client,
  companyId: string,
  body: { name: string; permissions: string[]; expires_on?: string },
): Promise<Minted> {
  return client.send('POST', scoped('/api/v1/api-keys', companyId), body);
}

export function revokeAPIKey(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send('DELETE', scoped(`/api/v1/api-keys/${id}`, companyId));
}

// --- import and export ----------------------------------------------------

export function importShapes(
  client: Client,
  companyId: string,
): Promise<{ data: ImportShape[]; exports: ExportKind[] }> {
  return client.send('GET', scoped('/api/v1/imports/shapes', companyId));
}

export function listImports(
  client: Client,
  companyId: string,
): Promise<{ data: ImportBatch[] }> {
  return client.send('GET', scoped('/api/v1/imports', companyId));
}

export function uploadImport(
  client: Client,
  companyId: string,
  body: {
    kind: string;
    filename?: string;
    mapping: Record<string, string>;
    csv: string;
  },
): Promise<ImportBatch> {
  return client.send('POST', scoped('/api/v1/imports', companyId), body);
}

export function readImport(
  client: Client,
  companyId: string,
  id: string,
): Promise<ImportBatch> {
  return client.send('GET', scoped(`/api/v1/imports/${id}`, companyId));
}

export function validateImport(
  client: Client,
  companyId: string,
  id: string,
): Promise<ImportBatch> {
  return client.send(
    'POST',
    scoped(`/api/v1/imports/${id}/validate`, companyId),
  );
}

export function commitImport(
  client: Client,
  companyId: string,
  id: string,
): Promise<ImportBatch> {
  return client.send('POST', scoped(`/api/v1/imports/${id}/commit`, companyId));
}

export function cancelImport(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send('POST', scoped(`/api/v1/imports/${id}/cancel`, companyId));
}

/** Where a download points. A URL rather than a fetch, because the browser
 *  saving a file is the browser's job and streaming it through JavaScript
 *  would hold the whole export in memory to hand it straight back. */
export function exportURL(companyId: string, kind: string): string {
  return `/api/v1/exports/${encodeURIComponent(kind)}?company_id=${companyId}`;
}

// --- backups --------------------------------------------------------------

export function backupHealth(
  client: Client,
  companyId: string,
): Promise<BackupHealth> {
  return client.send('GET', scoped('/api/v1/backups/health', companyId));
}

export function listBackups(
  client: Client,
  companyId: string,
): Promise<{ data: Backup[] }> {
  return client.send('GET', scoped('/api/v1/backups', companyId));
}

export function startBackup(
  client: Client,
  companyId: string,
): Promise<Backup> {
  return client.send('POST', scoped('/api/v1/backups', companyId));
}

export function verifyBackup(
  client: Client,
  companyId: string,
  id: string,
  error = '',
): Promise<Backup> {
  return client.send(
    'POST',
    scoped(`/api/v1/backups/${id}/verify`, companyId),
    { error },
  );
}

// --- support --------------------------------------------------------------

export function listTickets(
  client: Client,
  companyId: string,
  includeClosed = false,
): Promise<{ data: Ticket[] }> {
  const q = includeClosed ? '?include_closed=true' : '';
  return client.send('GET', scoped('/api/v1/support/tickets' + q, companyId));
}

export function raiseTicket(
  client: Client,
  companyId: string,
  body: { subject: string; body: string; kind?: string; priority?: string },
): Promise<Ticket> {
  return client.send('POST', scoped('/api/v1/support/tickets', companyId), body);
}

export function readTicket(
  client: Client,
  companyId: string,
  id: string,
): Promise<Ticket> {
  return client.send('GET', scoped(`/api/v1/support/tickets/${id}`, companyId));
}

export function replyToTicket(
  client: Client,
  companyId: string,
  id: string,
  body: string,
): Promise<Ticket> {
  return client.send(
    'POST',
    scoped(`/api/v1/support/tickets/${id}/reply`, companyId),
    { body },
  );
}

export function closeTicket(
  client: Client,
  companyId: string,
  id: string,
): Promise<Ticket> {
  return client.send(
    'POST',
    scoped(`/api/v1/support/tickets/${id}/close`, companyId),
  );
}

// --- the control plane ----------------------------------------------------

export function platformHealth(client: Client): Promise<PlatformHealth> {
  return client.send('GET', '/api/v1/platform/health');
}

export function platformTenants(
  client: Client,
): Promise<{ data: PlatformTenant[] }> {
  return client.send('GET', '/api/v1/platform/tenants');
}

/** What the platform operator answers to provision a client. */
export interface NewBusiness {
  name: string;
  market: string;
  plan_tier: string;
  data_region: string;
  owner_name: string;
  owner_email: string;
}

/**
 * What comes back once, and only once.
 *
 * The temporary password is not stored anywhere in readable form, so this
 * response is the only time it exists outside the owner's head. The screen has
 * to treat it accordingly: shown until dismissed, never refetched, and not put
 * anywhere a later render could recover it from.
 */
export interface ProvisionedBusiness {
  tenant_id: string;
  owner_user_id: string;
  owner_email: string;
  temporary_password: string;
  detail: string;
}

export function createBusiness(
  client: Client,
  body: NewBusiness,
): Promise<ProvisionedBusiness> {
  return client.send('POST', '/api/v1/platform/tenants', body);
}

export function failedJobs(client: Client): Promise<{ data: FailedJob[] }> {
  return client.send('GET', '/api/v1/platform/jobs/failed');
}

export function retryJob(client: Client, id: string): Promise<void> {
  return client.send('POST', `/api/v1/platform/jobs/${id}/retry`);
}

export function supportQueue(
  client: Client,
  includeClosed = false,
): Promise<{ data: Ticket[] }> {
  const q = includeClosed ? '?include_closed=true' : '';
  return client.send('GET', '/api/v1/platform/support' + q);
}

export function answerTicket(
  client: Client,
  id: string,
  body: { body: string; status?: string },
): Promise<Ticket> {
  return client.send('POST', `/api/v1/platform/support/${id}/reply`, body);
}
