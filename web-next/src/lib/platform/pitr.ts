// What the Point-in-Time Recovery screen reads, and the words it uses.
//
// # Why this is separate from backups.ts
//
// Because a dump and a physical copy of the cluster are not two kinds of the
// same thing, and a screen that treated them as one would invite the mistake
// this whole subsystem exists to prevent. A `pg_dump` snapshot restores the
// moment it was taken. A base backup plus the write-ahead log archive restores
// ANY moment inside a window. They have different artefacts, different
// retentions, different failure modes and different words, and keeping the
// vocabularies apart is what stops "verified" on one screen being read as a
// claim about the other.
//
// # Why the availability answer comes from the server
//
// `available` is the single most consequential boolean this product shows: it
// is the difference between "we can get back to Tuesday afternoon" and "we
// cannot". Computing it here, out of four other fields, would give the product
// two opinions about it — and the one on the screen would be the one nobody
// tested. The route answers it.

import type { Tone } from '@/components/ui/panel';
import type { Key } from '@/lib/i18n/locale';

/** The archive readout, as the agent last observed it. */
export interface ArchiveState {
  observed_at?: string;
  health?: string;
  summary?: string;

  archiving: boolean;
  wal_level?: string;
  timeline?: number;

  last_archived_segment?: string;
  last_archived_at?: string;
  archived_total: number;
  last_failed_segment?: string;
  last_failed_at?: string;
  failed_total: number;

  lag_segments: number;
  lag_seconds: number;

  current_segment?: string;
  pg_wal_bytes: number;

  archive_segments: number;
  archive_bytes: number;
  archive_gaps: number;
  oldest_archived_segment?: string;
  newest_archived_segment?: string;
  store_reachable: boolean;
  store_error?: string;

  recovery_window_start?: string;
  recovery_window_end?: string;

  /**
   * True when the reading is old enough to describe the past rather than the
   * present. A green from six hours ago describes a machine that may have
   * stopped five hours ago, and showing it without saying so would be the
   * screen's own version of the failure this subsystem is about.
   */
  stale: boolean;
}

/** One physical base backup. */
export interface BaseBackup {
  id: string;
  base_backup_id: string;

  status: string;
  started_at: string;
  completed_at?: string;

  timeline?: number;
  start_lsn?: string;
  end_lsn?: string;
  start_wal_segment?: string;
  end_wal_segment?: string;

  postgres_version?: string;
  size_bytes?: number;
  checksum?: string;

  encrypted: boolean;
  key_fingerprint?: string;

  storage?: string;
  retention_class?: string;
  app_version?: string;
  source_host?: string;

  verified_at?: string;
  error?: string;
  requested_by?: string;
}

/** What `GET /platform/pitr` answers. */
export interface PITRStatus {
  available: boolean;
  archive: ArchiveState;
  base_backups: BaseBackup[];
  retention: {
    window_days: number;
    keep_base_backups: number;
  };
}

/** A run of segments the archive does not have. */
export interface WALGap {
  timeline: number;
  from_segment: string;
  to_segment: string;
  segments_missing: number;
}

/** The span of time that can actually be recovered to. */
export interface RecoveryWindow {
  available: boolean;
  unavailable_because?: string;

  start?: string;
  end?: string;
  timeline?: number;

  earliest_base_backup?: string;
  latest_base_backup?: string;
  latest_base_backup_at?: string;
  base_backups: number;

  last_contiguous_segment?: string;
  last_archived_at?: string;

  gaps?: WALGap[];

  /**
   * True when a gap cut the window short of the newest object in the archive.
   * The difference between "we have seven days" and "we have seven days of
   * objects and two days of recovery".
   */
  truncated_by_gap?: boolean;

  read_at: string;
}

/** What `GET /platform/pitr/window` answers. */
export interface WindowAnswer {
  window: RecoveryWindow;
  /** Only present when the request named a moment. */
  recoverable?: boolean;
  because?: string;
  base_backup?: string;
}

/** One audited recovery, including the refused ones. */
export interface RecoveryRecord {
  id: string;
  kind: string;
  base_backup_id?: string;
  target_kind: string;
  target_value?: string;

  reached_lsn?: string;
  reached_at?: string;
  timeline?: number;

  status: string;
  started_at: string;
  finished_at?: string;
  requested_by?: string;
  error?: string;
}

/** One failed archive attempt, as PostgreSQL recorded it. */
export interface ArchiveFailure {
  segment?: string;
  failed_at?: string;
  reason?: string;
}

/**
 * The word an operator types to confirm a recovery.
 *
 * Here rather than in the screen because the API checks for exactly this
 * string, and a confirmation the server refuses is a button that does nothing
 * with no explanation. It is deliberately not translated: it is a token the
 * server compares, not a sentence somebody reads, and the label around it is
 * translated instead.
 */
export const CONFIRM_WORD = 'RECOVER';

// --- the vocabularies -------------------------------------------------------

/**
 * The recovery targets an operator may choose, in the order they are offered.
 *
 * `before_time` is first among the moment-based ones on purpose: somebody
 * recovering from a mistake knows when the mistake happened, and the state they
 * want is the one BEFORE it. Offering "at this moment" first invites including
 * the very transaction being recovered from.
 */
export const TARGET_KINDS = [
  'before_time',
  'time',
  'latest',
  'immediate',
  'lsn',
  'name',
] as const;

export type TargetKind = (typeof TARGET_KINDS)[number];

export const TARGET_LABEL: Record<TargetKind, Key> = {
  before_time: 'nx.pitr.target.beforeTime',
  time: 'nx.pitr.target.time',
  latest: 'nx.pitr.target.latest',
  immediate: 'nx.pitr.target.immediate',
  lsn: 'nx.pitr.target.lsn',
  name: 'nx.pitr.target.name',
};

export const TARGET_HELP: Record<TargetKind, Key> = {
  before_time: 'nx.pitr.help.beforeTime',
  time: 'nx.pitr.help.time',
  latest: 'nx.pitr.help.latest',
  immediate: 'nx.pitr.help.immediate',
  lsn: 'nx.pitr.help.lsn',
  name: 'nx.pitr.help.name',
};

/** Whether a target needs a moment typed in. */
export function needsMoment(kind: TargetKind): boolean {
  return kind === 'time' || kind === 'before_time';
}

/** Whether a target needs a position or a name typed in. */
export function needsValue(kind: TargetKind): boolean {
  return kind === 'lsn' || kind === 'name';
}

/**
 * The colour the archive health gets.
 *
 * Amber for anything not green, and red only for the states where recovery is
 * actually gone. The screen must not be able to award the comfortable colour
 * for the cheaper claim, which is the same rule the backup screen keeps.
 */
export const ARCHIVE_TONE: Record<string, Tone> = {
  green: 'positive',
  amber: 'caution',
  red: 'critical',
};

export const ARCHIVE_LABEL: Record<string, Key> = {
  green: 'nx.pitr.health.green',
  amber: 'nx.pitr.health.amber',
  red: 'nx.pitr.health.red',
};

/** The states a base backup can be in, and the word for each. */
export const BASE_LABEL: Record<string, Key> = {
  running: 'nx.pitr.base.running',
  uploading: 'nx.pitr.base.uploading',
  stored: 'nx.pitr.base.stored',
  verified: 'nx.pitr.base.verified',
  invalid: 'nx.pitr.base.invalid',
  failed: 'nx.pitr.base.failed',
  expired: 'nx.pitr.base.expired',
};

/**
 * And the tone. `stored` is amber and `verified` is green, and the gap between
 * them is the entire point: a copy that is in the bucket has had nothing proved
 * about it, and only one that has been recovered and counted gets the green.
 */
export const BASE_TONE: Record<string, Tone> = {
  running: 'info',
  uploading: 'info',
  stored: 'caution',
  verified: 'positive',
  invalid: 'critical',
  failed: 'critical',
  expired: 'neutral',
};

export const RECOVERY_STATUS_LABEL: Record<string, Key> = {
  running: 'nx.pitr.recovery.running',
  succeeded: 'nx.pitr.recovery.succeeded',
  failed: 'nx.pitr.recovery.failed',
  refused: 'nx.pitr.recovery.refused',
};

export const RECOVERY_STATUS_TONE: Record<string, Tone> = {
  running: 'info',
  succeeded: 'positive',
  failed: 'critical',
  refused: 'caution',
};

/**
 * How long the window is, in whole hours or days, for a figure.
 *
 * Returns null rather than a zero when either end is missing: a window of "0
 * days" reads as a working system with nothing in it, and an empty figure reads
 * as what it is.
 */
export function windowLength(start?: string, end?: string): number | null {
  if (!start || !end) return null;
  const from = Date.parse(start);
  const to = Date.parse(end);
  if (Number.isNaN(from) || Number.isNaN(to) || to < from) return null;
  return to - from;
}

/**
 * The value a datetime-local input wants, from a moment on the clock.
 *
 * The input has no time zone and reads whatever is typed as local time, which
 * is what an operator means when they say "the delete ran at 14:32". The
 * conversion back to an instant happens in `momentToISO`.
 */
export function nowForInput(at: Date = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return (
    `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}` +
    `T${pad(at.getHours())}:${pad(at.getMinutes())}`
  );
}

/**
 * The instant a typed local moment names, as the API wants it.
 *
 * Returns null for anything unparseable rather than a guess. A recovery target
 * that silently became the current moment would replay everything and report
 * success, which is the failure that is hardest to notice.
 */
export function momentToISO(local: string): string | null {
  const trimmed = local.trim();
  if (!trimmed) return null;
  const at = new Date(trimmed);
  if (Number.isNaN(at.getTime())) return null;
  return at.toISOString();
}
