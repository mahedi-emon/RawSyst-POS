// What the Backup & Recovery screen reads, and the words it uses.
//
// # The vocabularies live here, not in the screen
//
// A backup has thirteen phases and an operation has eleven stages. The database
// constrains both lists (migration 0135) and the agent writes them; the screen's
// only job is to print the right word. Keeping the maps here means adding a
// phase is one edit rather than a search through JSX, and means the type checker
// catches a phase the catalogue has no sentence for.
//
// # Why the tone is decided here too
//
// Because it is a claim, not a colour. `uploaded` is amber and `verified` is
// green, and the difference between them is the whole point of this subsystem:
// a run that finished is not a backup that restores. A screen that picked its
// own colours could quietly grant the green one to the cheaper claim.

import type { Tone } from '@/components/ui/panel';
import type { Key } from '@/lib/i18n/locale';

/** One backup, as the register holds it. */
export interface BackupRecord {
  id: string;
  snapshot_id?: string;

  kind: string;
  /** `server` — taken here. `upload` — carried in from another machine. */
  source: string;
  status: string;
  phase: string;

  location?: string;
  storage?: string;
  size_bytes?: number;
  checksum?: string;

  app_version?: string;
  schema_version?: number;
  retention_class?: string;
  encrypted: boolean;

  verified_at?: string;
  verify_error?: string;

  started_at: string;
  finished_at?: string;
  error?: string;

  requested_by?: string;

  /** Only present on the detail route; a history of thirty would be a megabyte. */
  manifest?: unknown;
  verify_report?: VerifyReport;
}

/** What a verification found. The document somebody reads before trusting one. */
export interface VerifyReport {
  snapshot_id?: string;
  passed?: boolean;
  restored?: boolean;
  /** False for a version 1 manifest, which carries only a partial inventory. */
  complete_inventory?: boolean;
  encrypted?: boolean;

  checked?: string[];
  findings?: string[];

  tables_restored?: number;
  schema_version_restored?: number;
  businesses_restored?: number;
  companies_restored?: number;
  rows_restored?: number;

  took?: string;
  bytes?: number;
}

/** One queued or finished operation. */
export interface BackupTask {
  id: string;
  kind: string;
  snapshot_id?: string;
  state: string;
  stage: string;
  requested_by?: string;
  requested_at: string;
  started_at?: string;
  finished_at?: string;
  error?: string;
  report?: unknown;
}

/** What the health route answers. */
export interface BackupHealth {
  health: {
    state: string;
    summary: string;
    last_verified_at?: string;
    last_verified_snapshot?: string;
    last_run_at?: string;
    last_run_status?: string;
    last_failure_at?: string;
    last_failure_reason?: string;
    hours_since_verified?: number;
    verified_count: number;
    unverified_count: number;
    failed_last_week: number;
  };
  storage: {
    configured: boolean;
    endpoint_host?: string;
    bucket?: string;
    region?: string;
    prefix?: string;
  };
  retention: { daily: number; weekly: number; monthly: number };
  production_restore_enabled: boolean;
  maintenance?: {
    active: boolean;
    reason?: string;
    allow_reads: boolean;
    started_at?: string;
    started_by?: string;
  };
  active_task?: BackupTask;
}

/**
 * The phases, in the order they happen.
 *
 * `uploaded` is the one to read carefully: the artifact is in the store and
 * NOTHING has been proved about it. It is amber for that reason and the label
 * says "Not checked" rather than anything that could be mistaken for success.
 */
export const PHASE_LABEL: Record<string, Key> = {
  pending: 'nx.pbk.phase.pending',
  creating: 'nx.pbk.phase.creating',
  uploading: 'nx.pbk.phase.uploading',
  uploaded: 'nx.pbk.phase.uploaded',
  verifying: 'nx.pbk.phase.verifying',
  verified: 'nx.pbk.phase.verified',
  invalid: 'nx.pbk.phase.invalid',
  failed: 'nx.pbk.phase.failed',
  restore_validating: 'nx.pbk.phase.restoreValidating',
  restore_ready: 'nx.pbk.phase.restoreReady',
  restoring: 'nx.pbk.phase.restoring',
  restored: 'nx.pbk.phase.restored',
  restore_failed: 'nx.pbk.phase.restoreFailed',
};

export const PHASE_TONE: Record<string, Tone> = {
  pending: 'neutral',
  creating: 'info',
  uploading: 'info',
  // Amber, not green. See the note above.
  uploaded: 'caution',
  verifying: 'info',
  verified: 'positive',
  invalid: 'critical',
  failed: 'critical',
  restore_validating: 'info',
  restore_ready: 'positive',
  restoring: 'info',
  restored: 'positive',
  restore_failed: 'critical',
};

/**
 * The stages an agent writes as it works.
 *
 * None of them is a percentage. `pg_dump` does not report progress and neither
 * does a PUT of a file to a bucket, so what there is is the name of the thing
 * currently happening — which is more useful than a bar that is guessing.
 */
export const STAGE_LABEL: Record<string, Key> = {
  pending: 'nx.pbk.stage.pending',
  preparing: 'nx.pbk.stage.preparing',
  dumping: 'nx.pbk.stage.dumping',
  inventory: 'nx.pbk.stage.inventory',
  sealing: 'nx.pbk.stage.sealing',
  uploading: 'nx.pbk.stage.uploading',
  manifest: 'nx.pbk.stage.manifest',
  verifying: 'nx.pbk.stage.verifying',
  restoring: 'nx.pbk.stage.restoring',
  checking: 'nx.pbk.stage.checking',
  cleaning_up: 'nx.pbk.stage.cleaningUp',
  done: 'nx.pbk.stage.done',
  failed: 'nx.pbk.stage.failed',
};

/**
 * What the three files are called.
 *
 * Not in the string catalogue, and deliberately: a filename is not prose. It is
 * the same in English, Arabic and Bangla because it is the same on disk, and a
 * translated one would send somebody looking for a file that does not exist.
 * The screen shows these beside the field rather than inside the sentence.
 */
export const ARTIFACT_NAMES = {
  dump: 'RawSyst_Backup_<id>.dump',
  manifest: 'RawSyst_Backup_<id>.manifest.json',
  checksum: 'RawSyst_Backup_<id>.sha256',
} as const;

/** Where the recovery sequence is written down. A path, for the same reason. */
export const RECOVERY_DOC = 'deploy/server/RECOVERY.md';
