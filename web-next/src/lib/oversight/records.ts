// The three registers that answer "what happened, what do we hold, and could
// we get it back": the audit trail, the document store, and the backups.
//
// # The trail is evidence, so it is not summarised
//
// Each entry carries the raw `before` and `after` the writer recorded. The
// backend's own comment says handing them over as-is is deliberate, because
// summarising them would mean the reader sees a rendering of the record rather
// than the record. What this file does is work out WHICH fields moved, so a
// reader is not left to diff two blobs by eye -- it never rewrites either side.
//
// # The verbs are the server's vocabulary
//
// `/audit` returns the actions actually present in this tenant's trail
// alongside the rows, so the filter offers what is there. Nothing here holds a
// list of verbs: a fixed one goes stale the day a module is added, and an
// audit filter that silently stopped offering a verb would hide the entries
// somebody came to find.

/** One thing that happened. `before` and `after` are raw, and stay raw. */
export interface AuditRecord {
  occurred_at: string;
  actor?: string;
  action: string;
  entity_type: string;
  entity_id?: string;
  ip?: string;
  device?: string;
  before?: unknown;
  after?: unknown;
}

export interface Document {
  id: string;
  entity_type: string;
  entity_id: string;
  file_name: string;
  content_type: string;
  byte_size: number;
  checksum: string;
  classification: string;
  expires_on?: string;
  /** Absent when the document has no expiry at all. Absent is not zero. */
  days_to_expiry?: number;
  note?: string;
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
  at_risk: boolean;
  /** The server's sentence. Shown as written; never reworded or recomputed. */
  summary: string;
}

// --- the audit trail -------------------------------------------------------

export type Change = 'created' | 'removed' | 'changed' | 'happened';

/**
 * What kind of entry this is, from which sides of the record are present.
 *
 * An entry with only an `after` is something coming into existence; only a
 * `before` is something going out of it; both is an edit; neither is an event
 * that changed no stored row -- a sign-in, a period close. Four cases, because
 * showing all four as "changed" would make a deletion look like an amendment.
 */
export function changeKind(record: AuditRecord): Change {
  const before = record.before != null;
  const after = record.after != null;
  if (before && after) return 'changed';
  if (after) return 'created';
  if (before) return 'removed';
  return 'happened';
}

/** One field that moved, with both sides kept exactly as recorded. */
export interface FieldChange {
  field: string;
  before: unknown;
  after: unknown;
}

/**
 * Which fields differ between the two sides.
 *
 * Only for an entry that has both. The comparison is on the JSON as recorded,
 * so a value that merely re-serialised differently still reads as a change --
 * which is the safe direction for evidence: reporting a change that was only
 * cosmetic wastes a reader's time, and hiding one that was not is the failure
 * an audit trail exists to prevent.
 */
export function changedFields(record: AuditRecord): FieldChange[] {
  const before = asObject(record.before);
  const after = asObject(record.after);
  if (!before || !after) return [];

  const fields = new Set([...Object.keys(before), ...Object.keys(after)]);
  const out: FieldChange[] = [];
  for (const field of [...fields].sort()) {
    const was = before[field];
    const now = after[field];
    if (JSON.stringify(was) !== JSON.stringify(now)) {
      out.push({ field, before: was, after: now });
    }
  }
  return out;
}

function asObject(v: unknown): Record<string, unknown> | null {
  if (v === null || typeof v !== 'object' || Array.isArray(v)) return null;
  return v as Record<string, unknown>;
}

// --- documents -------------------------------------------------------------

export type Expiry = 'none' | 'expired' | 'expiring' | 'current';

/**
 * How a document stands against its own expiry date.
 *
 * `days_to_expiry` is omitted entirely for a document that never expires, and
 * `0` means it expires TODAY. Reading a missing field as zero would file every
 * permanent document as expiring this morning, which is the same shape of
 * mistake as reading an absent count as none.
 */
export function expiryState(doc: Document): Expiry {
  if (typeof doc.days_to_expiry !== 'number') return 'none';
  if (doc.days_to_expiry < 0) return 'expired';
  // A month, because a licence or a registration takes weeks to renew and
  // finding out on the last day is finding out too late.
  if (doc.days_to_expiry <= 30) return 'expiring';
  return 'current';
}

/** Documents needing attention, soonest first. Permanent ones are not listed. */
export function expiringSoon(docs: readonly Document[]): Document[] {
  return docs
    .filter((d) => {
      const state = expiryState(d);
      return state === 'expired' || state === 'expiring';
    })
    .sort((a, b) => (a.days_to_expiry ?? 0) - (b.days_to_expiry ?? 0));
}

/** A size a person reads, from a byte count. */
export function fileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB'];
  let size = bytes / 1024;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  // One decimal below 10, none above: "1.4 MB" and "812 KB" both read at a
  // glance, and "1.437 MB" does not.
  return `${size < 10 ? size.toFixed(1) : Math.round(size)} ${units[unit]}`;
}

// --- backups ---------------------------------------------------------------

export type BackupState = 'running' | 'failed' | 'unverified' | 'verified';

/**
 * What a backup actually is.
 *
 * The distinction that matters is the last two. A backup that ran and was
 * never verified is a file nobody has proved can be restored, and the backend
 * pins this with a test called "a backup that ran is not a backup that
 * restores". Showing both as "done" is how a business discovers on the worst
 * day of its life that it has been keeping unreadable files for a year.
 */
export function backupState(backup: Backup): BackupState {
  if (backup.error || backup.status === 'failed') return 'failed';
  if (!backup.finished_at) return 'running';
  // A verification that ran and failed is worse than none, and must not read
  // as verified just because the attempt is stamped.
  if (backup.verify_error) return 'failed';
  return backup.verified_at ? 'verified' : 'unverified';
}

/** How many of these could actually be restored from. */
export function restorable(backups: readonly Backup[]): number {
  return backups.filter((b) => backupState(b) === 'verified').length;
}
