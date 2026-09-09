// The head count, and the two things a shop is asked about it.
//
// # What the backend actually answers, and what it deliberately does not
//
// `GET /reports/workforce` returns a count, a split, a percentage, two document
// counts and a per-department breakdown. It does NOT return a Nitaqat band, and
// the route says why in as many words: the band depends on the establishment's
// activity, its size bracket and a schedule the ministry publishes and revises,
// so asserting one from a head count would be inventing a regulatory
// classification. The ministry's own portal is where a band comes from and this
// is the number a shop checks it against.
//
// Nothing here computes a band either, and nothing here recomputes the
// percentage: `saudi_share` arrives as a string and is printed as it arrived.
// A second implementation of a ratio that appears on a compliance screen is a
// second answer to a regulated question.
//
// # There is no period and no branch filter, because there is no period
//
// This is a snapshot of who is employed right now — `left_on IS NULL` — not a
// figure for a month. A date picker over a route that ignores dates would be a
// control that changes nothing, which is worse than no control: somebody would
// use it and believe the answer.

/** One department's share of the head count. */
export interface WorkforceLine {
  department: string;
  total: number;
  saudi: number;
}

/** The whole report, exactly as `GET /reports/workforce` answers it. */
export interface Workforce {
  total: number;
  saudi: number;
  non_saudi: number;
  /** A percentage to two places, as a string. Printed, never parsed. */
  saudi_share: string;
  /** Residence permits and identity documents lapsing within 60 days. */
  expiring_soon: number;
  /** And ones that already have. */
  expired: number;
  by_department: WorkforceLine[];
}

/** How urgent the document position is. Drives a word and a tone, not a colour alone. */
export type ExpiryPressure = 'expired' | 'due_soon' | 'clear';

/**
 * What the expiry counts mean for somebody reading them this morning.
 *
 * Expired outranks expiring: a lapsed permit is a person who cannot legally be
 * on shift today, and one lapsing next month is a diary entry. Reporting the
 * larger number, or the sum, would flatten the difference between those two.
 */
export function expiryPressure(w: Workforce): ExpiryPressure {
  if (w.expired > 0) return 'expired';
  if (w.expiring_soon > 0) return 'due_soon';
  return 'clear';
}

/** Non-nationals in one department. Derived, because the route sends two of three. */
export function otherThanSaudi(line: WorkforceLine): number {
  return Math.max(line.total - line.saudi, 0);
}

/**
 * Whether the department rows account for everybody.
 *
 * They should: the breakdown groups the same `left_on IS NULL` population the
 * headline counts, with a placeholder department for people who have none. If
 * they ever disagree the screen says so rather than printing two totals and
 * leaving the reader to notice — a compliance figure that quietly excludes six
 * people is the kind of error that is discovered by an inspector.
 */
export function departmentsAddUp(w: Workforce): boolean {
  const counted = w.by_department.reduce((sum, line) => sum + line.total, 0);
  return counted === w.total;
}

/**
 * Departments worth drawing, largest first.
 *
 * The route already orders by size, and this restates it rather than trusting
 * it: the table is read as a ranking, and a ranking that is only sometimes
 * sorted is worse than one that never is.
 */
export function rankedDepartments(w: Workforce): WorkforceLine[] {
  return [...w.by_department].sort(
    (a, b) => b.total - a.total || a.department.localeCompare(b.department),
  );
}

/**
 * The share as a fraction of one, for a bar.
 *
 * A bar is drawn geometry rather than a stated figure, so this is the one place
 * the percentage becomes a number — and it is derived from the two COUNTS
 * rather than by parsing `saudi_share`, so the printed percentage and the
 * printed bar cannot come from two different readings of the same string.
 * Zero people is an empty bar, not a division by zero.
 */
export function saudiFraction(w: Workforce): number {
  if (w.total <= 0) return 0;
  return Math.min(Math.max(w.saudi / w.total, 0), 1);
}
