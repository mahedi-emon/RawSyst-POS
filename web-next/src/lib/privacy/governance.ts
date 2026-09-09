// The privacy registers, and the two clocks that make them urgent.
//
// PDPL gives a data subject's request a statutory deadline and a personal-data
// breach a notification window. Both are counted by the server and read here;
// nothing in this file works out how long is left. A deadline this product
// computed itself would be a second answer to a regulatory question, free to
// disagree with the one the register was filed against.
//
// What this file DOES decide is how to describe what the server counted: when
// a number becomes a warning, whether the shop or the subject is holding
// things up, and which register entry is missing something that makes it
// unusable as a record.

/** A data subject's request. `days_left` is the server's count, always sent. */
export interface Request {
  id: string;
  request_no: string;
  kind: string;
  status: string;
  subject_type: string;
  subject_id?: string;
  subject_name: string;
  subject_contact: string;
  received_at: string;
  due_at: string;
  extended_to?: string;
  extension_reason?: string;
  days_left: number;
  closed_at?: string;
  outcome?: string;
  outcome_note?: string;
  legal_hold_applied: boolean;
  handled_by?: string;
}

/** A personal-data breach. `hours_left` runs against the notification window. */
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
  hours_left: number;
  sdaia_notified_at?: string;
  subjects_notified_at?: string;
  closed_at?: string;
  logged_by?: string;
}

export interface Consent {
  id: string;
  subject_type: string;
  subject_id: string;
  subject_name?: string;
  lawful_basis: string;
  purpose: string;
  channel: string;
  granted: boolean;
  granted_at: string;
  withdrawn_at?: string;
  proof: string;
  recorded_by?: string;
}

/** One entry in the record of processing activities. */
export interface Activity {
  id: string;
  name: string;
  purpose: string;
  lawful_basis: string;
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

export interface Retention {
  id: string;
  data_category: string;
  retain_months: number;
  action: string;
  legal_note?: string;
  is_active: boolean;
  last_run_at?: string;
}

export interface Hold {
  id: string;
  name: string;
  reason: string;
  subject_type?: string;
  subject_id?: string;
  data_category?: string;
  placed_at: string;
  released_at?: string;
  placed_by?: string;
}

/** A request is finished when it has been fulfilled or refused. */
const SETTLED_REQUEST = new Set(['fulfilled', 'refused']);

/** An incident stops running against the window once it has been notified. */
const SETTLED_INCIDENT = new Set(['notified', 'closed']);

export type Clock =
  | 'settled'
  | 'overdue'
  | 'due_soon'
  | 'running'
  | 'waiting_on_subject';

/**
 * How a request stands against its deadline.
 *
 * `waiting_on_subject` is separated from `running` because the clock does not
 * stop when the shop is waiting for the person to answer, and a queue that
 * showed those together would have somebody chasing work that is not theirs to
 * do. It is still counted, and it can still be overdue.
 */
export function requestClock(request: Request): Clock {
  if (SETTLED_REQUEST.has(request.status)) return 'settled';
  if (request.days_left < 0) return 'overdue';
  if (request.status === 'awaiting_subject') return 'waiting_on_subject';
  // A week is the point at which a request has to be picked up rather than
  // noticed: an access request needs the records gathered, and that is not a
  // morning's work.
  if (request.days_left <= 7) return 'due_soon';
  return 'running';
}

/**
 * Which date the deadline actually falls on.
 *
 * An extended request is counted against the new date, and showing the
 * original would have somebody reporting a breach of a deadline that moved.
 */
export function dueOn(request: Request): string {
  return request.extended_to || request.due_at;
}

/**
 * How an incident stands against its notification window.
 *
 * The window is far shorter than a request's, so "soon" is measured in hours.
 * Once the authority has been told, the window has been met and the incident
 * stops being a countdown even though it is still open work.
 */
export function incidentClock(incident: Incident): Clock {
  if (SETTLED_INCIDENT.has(incident.status)) return 'settled';
  if (incident.sdaia_notified_at) return 'settled';
  if (incident.hours_left < 0) return 'overdue';
  if (incident.hours_left <= 24) return 'due_soon';
  return 'running';
}

/** Anything a person has to act on, worst first. */
export interface Pressing {
  id: string;
  kind: 'request' | 'incident';
  reference: string;
  what: string;
  clock: Clock;
  /** The server's own count, in its own unit. Never converted. */
  left: number;
  unit: 'days' | 'hours';
}

const RANK: Record<string, number> = {
  overdue: 0,
  due_soon: 1,
  waiting_on_subject: 2,
  running: 3,
  settled: 4,
};

/**
 * The queue the screen opens with.
 *
 * An incident and a request are ranked together because a person opening this
 * screen wants to know what is running out, not which register it lives in.
 * Within a state the smaller count comes first, but hours and days are never
 * compared to each other -- an incident with 4 hours left and a request with 4
 * days left are both urgent, and converting either into the other's unit would
 * invent a precision the deadline does not have.
 */
export function pressing(
  requests: readonly Request[],
  incidents: readonly Incident[],
): Pressing[] {
  const out: Pressing[] = [];

  for (const r of requests) {
    const clock = requestClock(r);
    if (clock === 'settled' || clock === 'running') continue;
    out.push({
      id: r.id,
      kind: 'request',
      reference: r.request_no,
      what: r.subject_name,
      clock,
      left: r.days_left,
      unit: 'days',
    });
  }

  for (const i of incidents) {
    const clock = incidentClock(i);
    if (clock === 'settled' || clock === 'running') continue;
    out.push({
      id: i.id,
      kind: 'incident',
      reference: i.incident_no,
      what: i.title,
      clock,
      left: i.hours_left,
      unit: 'hours',
    });
  }

  return out.sort((a, b) => {
    const byState = (RANK[a.clock] ?? 9) - (RANK[b.clock] ?? 9);
    if (byState !== 0) return byState;
    // An incident before a request at the same urgency: its window is the
    // shorter one, so the same state means less time in hand.
    if (a.kind !== b.kind) return a.kind === 'incident' ? -1 : 1;
    return a.left - b.left;
  });
}

/**
 * Why a request cannot simply be actioned.
 *
 * A legal hold stops an erasure, and the request stays open while the hold
 * stands. Saying so on the row is the difference between a queue somebody can
 * work and one where the same request is picked up and put down every day.
 */
export function blockedBy(request: Request): 'legal_hold' | null {
  return request.legal_hold_applied ? 'legal_hold' : null;
}

/**
 * What a processing record is missing.
 *
 * The server refuses a cross-border entry that names no destination and no
 * safeguard, so this is the same rule stated before the refusal rather than
 * after it. An entry that passes still tells a reader nothing without a
 * retention note and an owner, so those are reported as gaps rather than
 * errors: the register is a document somebody has to be able to read.
 */
export function activityGaps(activity: Activity): string[] {
  const gaps: string[] = [];
  if (activity.cross_border) {
    if (!activity.destination_country?.trim()) gaps.push('destination_country');
    if (!activity.transfer_safeguard?.trim()) gaps.push('transfer_safeguard');
  }
  if (!activity.retention_note?.trim()) gaps.push('retention_note');
  if (!activity.owner_name?.trim()) gaps.push('owner_name');
  return gaps;
}

/** The refusal the server would give, so the form can say it first. */
export function activityRefused(activity: Activity): boolean {
  return activityGaps(activity).some(
    (g) => g === 'destination_country' || g === 'transfer_safeguard',
  );
}

/**
 * Whether a consent still stands.
 *
 * Driven live: withdrawing sets `granted` to false AND stamps `withdrawn_at`,
 * and the schema requires the two to agree, so on a healthy row either would
 * answer. Both are read anyway. The redundancy costs nothing, and the harm it
 * guards against -- marketing to somebody who said stop -- is the exact thing
 * this register exists to prevent.
 */
export function consentStands(consent: Consent): boolean {
  return consent.granted && !consent.withdrawn_at;
}

/**
 * Data categories named in the register with no retention policy behind them.
 *
 * A record of processing that says what is collected, and a retention regime
 * that never mentions it, is the gap PDPL is about: the data is kept for ever
 * by default. Categories are free text on both sides, so this compares them
 * the way a person would -- trimmed, lowercased, comma-separated.
 */
export function uncovered(
  activities: readonly Activity[],
  retentions: readonly Retention[],
): string[] {
  const covered = new Set(
    retentions
      .filter((r) => r.is_active)
      .map((r) => r.data_category.trim().toLowerCase())
      .filter(Boolean),
  );

  const named = new Set<string>();
  for (const a of activities) {
    for (const part of a.data_categories.split(',')) {
      const one = part.trim().toLowerCase();
      if (one) named.add(one);
    }
  }

  return [...named].filter((c) => !covered.has(c)).sort();
}

/**
 * Whether a hold is live.
 *
 * A released hold is stamped rather than removed, for the same reason a
 * withdrawn consent is: the register has to show that the hold once applied.
 */
export function holdStands(hold: Hold): boolean {
  return !hold.released_at;
}

// ---------------------------------------------------------------------------
// The destruction log
// ---------------------------------------------------------------------------

/**
 * One entry in the permanent record of what has been deleted, and when.
 *
 * `GET /privacy/destructions` is read-only, and that is the whole design. No
 * route creates one of these: a destruction record is written by the retention
 * worker as it disposes of data whose policy has run out, and by an erasure a
 * data-subject request produced. Nothing in the product can fabricate one, and
 * a screen offering to would be manufacturing evidence.
 *
 * So the screen is a register somebody READS, which is what PDPL asks for: the
 * proof that the retention policy above it was actually applied.
 */
export interface Destruction {
  id: string;
  /** What kind of data went — the same vocabulary the retention policies use. */
  data_category: string;
  /** The table it lived in, when the log records one. */
  entity_type?: string;
  /** `deleted` or `anonymised`. The two are not the same thing. */
  action: string;
  /** How many rows. */
  row_count: number;
  /** The policy or the request that required it. */
  reason: string;
  executed_at: string;
  /** Who or what ran it. Empty for the worker, which is nobody. */
  executed_by?: string;
}

/**
 * Whether the data is gone or merely unattributable.
 *
 * `deleted` removes the row. `anonymised` keeps it and strips the personal
 * content, which is what happens to an audit entry: the evidence that a change
 * occurred outlives the personal data inside it. A register that showed both as
 * "destroyed" would tell a regulator something untrue in one of the two cases.
 */
export function destructionIsErasure(d: Destruction): boolean {
  return d.action === 'deleted';
}

/**
 * Whether a person or a scheduled job did it.
 *
 * The retention worker names nobody, and that is correct rather than missing:
 * disposal on a schedule is the policy running, not somebody deciding. An
 * entry that DOES name somebody is the one worth reading twice, because it is a
 * person who erased something by hand.
 */
export function destroyedByHand(d: Destruction): boolean {
  return (d.executed_by ?? '').trim() !== '';
}

/** Rows for one category, newest first, as the route already returns them. */
export function destructionsFor(
  rows: readonly Destruction[],
  category: string,
): Destruction[] {
  return rows.filter((d) => d.data_category === category);
}

/**
 * Categories the retention policy covers that nothing has ever disposed of.
 *
 * The counterpart of `uncovered`: that finds data collected with no policy,
 * this finds a policy that has never been applied. Both are the same class of
 * failure — a rule that exists on paper and does nothing — and a retention
 * schedule nobody can show a disposal against is the one a regulator asks about.
 *
 * A policy written this month has legitimately disposed of nothing yet, so this
 * is reported as something to look at rather than as a fault.
 */
export function neverApplied(
  policies: readonly Retention[],
  destructions: readonly Destruction[],
): string[] {
  const seen = new Set(destructions.map((d) => d.data_category));
  return policies
    .filter((p) => !seen.has(p.data_category))
    .map((p) => p.data_category);
}
