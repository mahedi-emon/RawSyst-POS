// A payroll run, its payslips and what can be done to it.
//
// # A missing social insurance figure is named, never zeroed
//
// The run reports `gosi_unavailable` with `gosi_blocked_reason` when the rate
// has not been verified against its official source. The run is still usable —
// wages are wages — but the figure that is missing has to be SAID. Rendering
// "0.00" there would tell an owner their social insurance liability is nil for
// the month, which is a regulatory claim this product must never make.
//
// # Nothing here decides what a run may do next
//
// The status is the server's and every refusal comes from it: a draft cannot be
// paid, an approved run cannot be approved again, a wage file needs an approved
// run. `allowed` below mirrors those rules so a button that would be refused is
// not offered — it never replaces the check, and a disagreement means this is
// wrong, not the server.
//
// # The wage file is refused, and that is the correct behaviour
//
// `POST /payroll/{id}/wage-file` refuses while the Mudad layout is unverified.
// A file in the wrong layout is not partly right: it is rejected, and a
// rejected submission can freeze a company's portal access. The screen shows
// the refusal in the server's own words rather than offering a download of
// something plausible.

import { numberOf } from './staff';

/** A payroll run as read back. */
export interface PayrollRun {
  id: string;
  run_no: string;
  period: string;
  pay_date?: string;
  status: RunStatus;
  currency: string;

  gross_total: string;
  deduction_total: string;
  net_total: string;
  employer_gosi: string;

  /** The run computed everything EXCEPT social insurance. */
  gosi_unavailable?: boolean;
  /** Why, in the server's words. Shown as written. */
  gosi_blocked_reason?: string;

  note?: string;
  payslips?: Payslip[];
}

/** One person's pay for one period. */
export interface Payslip {
  id: string;
  employee_id: string;
  employee: string;

  basic: string;
  housing: string;
  transport: string;
  other_allowance: string;
  overtime: string;
  commission: string;
  bonus: string;
  gross: string;

  absence_deduction: string;
  gosi_employee: string;
  advance_recovery: string;
  other_deduction: string;
  deductions: string;

  net: string;
  gosi_employer: string;
}

/** The states a run moves through. */
export const RUN_STATUS = ['draft', 'approved', 'paid', 'cancelled'] as const;
export type RunStatus = (typeof RUN_STATUS)[number];

/** What can be done to a run in a given state. */
export type RunAction = 'approve' | 'pay' | 'wage-file' | 'cancel';

/**
 * Whether an action is available on a run.
 *
 * The server's rules, mirrored: approving is for a draft; paying and building a
 * wage file need an approved run; cancelling unwinds anything that is not
 * already cancelled or paid — a paid run is money that has left, and unwinding
 * it is a refund, not a cancellation.
 */
export function allowed(status: RunStatus, action: RunAction): boolean {
  switch (action) {
    case 'approve':
      return status === 'draft';
    case 'pay':
    case 'wage-file':
      return status === 'approved';
    case 'cancel':
      return status === 'draft' || status === 'approved';
    default:
      return false;
  }
}

/** How a run's state reads. */
export function runTone(status: RunStatus): 'neutral' | 'positive' | 'caution' {
  if (status === 'paid') return 'positive';
  if (status === 'cancelled') return 'neutral';
  if (status === 'approved') return 'caution';
  return 'neutral';
}

/**
 * The lines of one payslip, in the order a person reads them.
 *
 * Earnings first, then what came off, then what is left — the shape of every
 * payslip anybody has ever been handed. Zero rows are kept rather than dropped:
 * an employee looking for their overtime needs to see that it was nil, not
 * wonder where the row went.
 */
export interface SlipLine {
  key: keyof Payslip;
  kind: 'earning' | 'deduction' | 'total';
}

export const SLIP_LINES: SlipLine[] = [
  { key: 'basic', kind: 'earning' },
  { key: 'housing', kind: 'earning' },
  { key: 'transport', kind: 'earning' },
  { key: 'other_allowance', kind: 'earning' },
  { key: 'overtime', kind: 'earning' },
  { key: 'commission', kind: 'earning' },
  { key: 'bonus', kind: 'earning' },
  { key: 'gross', kind: 'total' },
  { key: 'absence_deduction', kind: 'deduction' },
  { key: 'gosi_employee', kind: 'deduction' },
  { key: 'advance_recovery', kind: 'deduction' },
  { key: 'other_deduction', kind: 'deduction' },
  { key: 'deductions', kind: 'total' },
  { key: 'net', kind: 'total' },
];

/**
 * What the run cost the business, which is not what it paid anybody.
 *
 * Net is what lands in people's accounts; the cost is the gross plus the
 * employer's own social insurance, which is never deducted from anybody and is
 * the figure an owner asking "what does my staff cost" means. Both are shown,
 * because confusing them understates the wage bill by the employer's share.
 *
 * Returns null when social insurance is unavailable: the cost genuinely is not
 * known, and adding a zero to the gross would state it as if it were.
 */
export function totalCost(run: PayrollRun): string | null {
  if (run.gosi_unavailable) return null;
  const gross = numberOf(run.gross_total);
  const employer = numberOf(run.employer_gosi);
  if (gross === null || employer === null) return null;
  return (gross + employer).toFixed(2);
}

/** The wage file, once a layout is verified. */
export interface WageFile {
  id: string;
  run_id: string;
  status: string;
  format_version?: number;
  employee_count: number;
  total_amount: string;
  checksum?: string;
  content?: string;
  generated_at: string;
}

/** A commission scheme in force. */
export interface CommissionRule {
  id: string;
  name: string;
  is_active: boolean;
  basis: string;
  employee_id?: string;
  store_id?: string;
  rate: string;
  tiers: string;
  effective_from: string;
  effective_to?: string;
}

/**
 * A rate as a percentage, for display only.
 *
 * The server holds and takes a fraction — `0.02` — and that is what is sent
 * back. This exists so a screen can say "2%" without anybody being tempted to
 * store the 2.
 */
export function ratePercent(rate: string): string {
  const n = numberOf(rate);
  if (n === null) return rate;
  return `${(n * 100).toFixed(2).replace(/\.?0+$/, '')}%`;
}
