// The staff directory, attendance and leave.
//
// # A salary that is absent is not a salary of zero
//
// `hr.view` is the directory; `hr.view_pay` is what somebody earns, and A6.2
// requires the second to be withheld from a Store Manager who holds the first
// — they roster their branch without learning what the branch is paid. The
// server enforces it by OMITTING the pay fields, not by zeroing them, and the
// distinction is the whole point: a screen that rendered `basic_salary ?? '0'`
// would show every employee earning nothing and somebody would believe it.
//
// So the fields are optional here and `maySeePay` asks whether they arrived,
// never whether they are non-zero. A person genuinely on a basic of zero — an
// entirely commission-paid salesperson — is a real case, and must read as zero
// to somebody allowed to see it and as nothing at all to somebody who is not.
//
// # An expiring permit is derived, never stored
//
// `id_expiring_soon` and `id_expired` come off the date on the server, because
// a stored flag is wrong every morning until a job runs and a residency permit
// that lapses unnoticed stops somebody working. They are read here, not
// recomputed: two answers to "is this about to expire" can disagree, and this
// side has no idea what window the alert route uses.

/**
 * A decimal string as a number, or null when it is not one.
 *
 * `Number('')` is 0, not NaN — the same trap as decimal.js reporting zero as
 * positive. Every blank field in this module would otherwise be indistinguishable
 * from a genuine nil, which is exactly the confusion the pay rules turn on.
 */
export function numberOf(raw: string | null | undefined): number | null {
  if (raw === null || raw === undefined) return null;
  const text = raw.trim();
  if (text === '') return null;
  const n = Number(text);
  return Number.isFinite(n) ? n : null;
}

/** Somebody employed. */
export interface Employee {
  id: string;
  employee_no: string;
  user_id?: string;
  full_name: string;
  name_ar?: string;
  phone?: string;
  email?: string;
  position?: string;
  department?: string;
  store_id?: string;
  store_name?: string;

  national_id?: string;
  iqama_no?: string;
  id_expires_on?: string;
  /** The server's answer, derived from the date. Never recomputed here. */
  id_expiring_soon: boolean;
  id_expired: boolean;
  gosi_number?: string;
  qiwa_contract_no?: string;
  nationality?: string;
  is_saudi: boolean;

  iban?: string;
  bank_name?: string;

  joined_on: string;
  left_on?: string;
  status: string;

  /** Absent entirely without `hr.view_pay`. Never defaulted to a number. */
  basic_salary?: string;
  housing_allowance?: string;
  transport_allowance?: string;
  other_allowance?: string;
  currency?: string;
  commission_eligible: boolean;

  notes?: string;
}

/**
 * Whether this record came back with pay on it.
 *
 * Presence, not value. `basic_salary === '0.00'` is a real answer — a
 * commission-only salesperson — and treating it as "no pay visible" would hide
 * a figure from somebody entitled to see it.
 */
export function maySeePay(e: Employee): boolean {
  return e.basic_salary !== undefined;
}

/**
 * What somebody is paid a month, before anything is added or taken off.
 *
 * Returns null rather than a zero when pay is not visible, so a caller has to
 * decide what to render instead of accidentally rendering nothing as free.
 */
export function monthlyPay(e: Employee): string | null {
  if (!maySeePay(e)) return null;
  const parts = [
    e.basic_salary,
    e.housing_allowance,
    e.transport_allowance,
    e.other_allowance,
  ];
  let total = 0;
  for (const p of parts) {
    // An absent component is nothing; a component that is present and not a
    // number is a record this cannot add up, and saying so beats guessing.
    if (p === undefined) continue;
    const n = numberOf(p);
    if (n === null) return null;
    total += n;
  }
  return total.toFixed(2);
}

/** How a document's remaining life reads on a row. */
export type DocumentState = 'expired' | 'expiring' | 'fine' | 'none';

/**
 * The state of somebody's residency permit or ID.
 *
 * Both flags are the server's. `expired` wins over `expiring` because an
 * expired permit is not a warning about the future, it is a person who cannot
 * legally work today.
 */
export function documentState(e: Employee): DocumentState {
  if (!e.id_expires_on) return 'none';
  if (e.id_expired) return 'expired';
  if (e.id_expiring_soon) return 'expiring';
  return 'fine';
}

/** A day of somebody's attendance. */
export interface AttendanceDay {
  id: string;
  employee_id: string;
  employee?: string;
  on_date: string;
  status: string;
  hours_worked: string;
  overtime_hours: string;
  late_minutes: number;
  note?: string;
}

/** What a day can be. The server takes these words. */
export const ATTENDANCE_STATUS = [
  'present',
  'absent',
  'leave',
  'holiday',
  'rest',
] as const;
export type AttendanceStatus = (typeof ATTENDANCE_STATUS)[number];

/** Time off, asked for or granted. */
export interface LeaveRequest {
  id: string;
  employee_id: string;
  employee?: string;
  kind: string;
  is_paid: boolean;
  starts_on: string;
  ends_on: string;
  days: string;
  status: string;
  reason?: string;
  decision_note?: string;
  decided_by?: string;
}

/**
 * The days between two dates, inclusive, as the leave route counts them.
 *
 * A single-day absence is one day, not zero: somebody taking Monday off is
 * away for a day even though the two dates are equal. Offered as a default the
 * person can overwrite, because whether a weekend inside a holiday counts is a
 * company's own rule and this cannot know it.
 */
export function daysBetween(from: string, to: string): string {
  const a = utcDay(from);
  const b = utcDay(to);
  if (a === null || b === null || b < a) return '';
  return String(Math.round((b - a) / 86400000) + 1);
}

/**
 * An ISO date as a UTC midnight, or null when it is not one.
 *
 * Built from the parts rather than handed to Date.parse. A bare `2026-09-15`
 * is parsed as UTC and `2026-09-15T09:00` as local, and a helper that quietly
 * depended on which one it was given would be off by a day for half the world.
 */
function utcDay(iso: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec((iso ?? '').trim());
  if (!m) return null;
  const [, y, mo, d] = m;
  const at = Date.UTC(Number(y), Number(mo) - 1, Number(d));
  return Number.isFinite(at) ? at : null;
}

/** Why a leave request cannot be sent yet. */
export type LeaveProblem = 'no_employee' | 'no_kind' | 'no_dates' | 'backwards' | 'none';

export function leaveProblem(draft: {
  employeeID: string;
  kind: string;
  from: string;
  to: string;
}): LeaveProblem {
  if (!draft.employeeID) return 'no_employee';
  if (!draft.kind.trim()) return 'no_kind';
  if (!draft.from || !draft.to) return 'no_dates';
  if (Date.parse(draft.to) < Date.parse(draft.from)) return 'backwards';
  return 'none';
}

/** Money lent against future wages. */
export interface Advance {
  id: string;
  advance_no: string;
  employee_id: string;
  employee?: string;
  amount: string;
  outstanding: string;
  installments: number;
  currency: string;
  issued_on: string;
  reason?: string;
}

/** What the business owes somebody if they left today. */
export interface EOSBPosition {
  employee_id: string;
  employee: string;
  months_of_service: string;
  accrued: string;
  currency: string;
}

/**
 * The month a payroll screen opens on: the one just finished.
 *
 * A month is paid after it is worked, so the period somebody opens payroll to
 * run is almost never the one they are standing in. Built from the local
 * calendar date rather than a timestamp — a shop in Dhaka opening this at nine
 * in the morning is on a different UTC day, and silently offering the wrong
 * month is the kind of error nobody notices until the run is approved.
 */
export function lastFullMonth(today: Date = new Date()): string {
  const d = new Date(today.getFullYear(), today.getMonth() - 1, 1);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
}

/** A month as a person reads it, from the `YYYY-MM` the server speaks. */
export function monthLabel(period: string, locale: string): string {
  const [y, m] = period.split('-').map(Number);
  if (!y || !m) return period;
  return new Date(y, m - 1, 1).toLocaleDateString(locale, {
    month: 'long',
    year: 'numeric',
  });
}
