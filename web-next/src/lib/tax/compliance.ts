// The tax position, the e-invoicing units and E7's compliance readings.
//
// # Every regulatory figure here is the server's
//
// The standard rate, the filing due date, the retention period, whether the
// business is registered — all of it is read off `GET /compliance`, which reads
// it from the regulatory register. Nothing on this side computes a rate, infers
// a deadline, or decides that a business is compliant. A screen that did would
// be inventing exactly the kind of confirmation this product must never invent.
//
// What IS decided here is presentation: what to show first, what counts as
// needing attention, and how to say "we do not know" without it reading as
// "nothing to do".
//
// # An unverified rule is not a fault
//
// `unverified_rules` counts legal values in the register that nobody has
// checked against a primary source yet, and `blocking_rules` counts the subset
// that stop something working. The distinction matters: twelve unverified rules
// with one blocker is a business that trades normally and has one thing to
// chase, not a business with twelve problems.

/** How the shop is getting on with e-invoicing. */
export interface InvoicingReading {
  started: boolean;
  status: string;
  devices: number;
  devices_ready: number;
  pending: number;
  failed: number;
  rejected: number;
}

/** The tax registration and what is due. */
export interface VatReading {
  registered: boolean;
  vat_number?: string;
  /** From the regulatory register. Never a constant on this side. */
  standard_rate?: string;
  next_filing_due?: string;
  days_to_filing?: number;
  /** Accounting periods still open. A period left open cannot be filed from. */
  open_ended_periods: number;
}

export interface PrivacyReading {
  customers: number;
  marketing_consent: number;
  open_requests: number;
  overdue_requests: number;
  open_incidents: number;
  incidents_unnotified: number;
  incident_hours_left?: number;
  retention_policies: number;
  retention_last_run?: string;
  processing_activities: number;
  dpo_appointed: boolean;
  legal_holds: number;
}

/** What the storefront must disclose and has not. */
export interface StorefrontReading {
  /** Field names, in the server's vocabulary. Empty means complete. */
  missing: string[];
}

export interface PayrollReading {
  last_run_period?: string;
  unsubmitted_runs: number;
  deadline_known: boolean;
}

export interface PeopleReading {
  expiring_soon: number;
  expired: number;
  /** Of those, the ones that are somebody's residency permit. */
  staff_expiring_soon: number;
  staff_expired: number;
}

export interface RecordsReading {
  retention_years: number;
  oldest_invoice?: string;
  last_verified_backup?: string;
  backup_age_days?: number;
}

export interface ComplianceReport {
  invoicing: InvoicingReading;
  vat: VatReading;
  privacy: PrivacyReading;
  storefront: StorefrontReading;
  payroll: PayrollReading;
  people: PeopleReading;
  records: RecordsReading;
  /** Legal values nobody has checked against a primary source yet. */
  unverified_rules: number;
  /** The subset of those that stop something working. */
  blocking_rules: number;
}

/** One signing unit, which in Saudi Arabia is what a till invoices through. */
export interface EGSUnit {
  id: string;
  label: string;
  architecture: string;
  store_id?: string;
  store?: string;
  csr: Record<string, string>;
  csid_status: string;
  terminals: number;
  invoices: number;
  /** Whether the nine certificate-request fields are all filled in. */
  csr_complete: boolean;
}

/** Where a unit has got to with the tax authority. */
export interface OnboardingStatus {
  egs_unit_id: string;
  environment: string;
  compliance: CertificateState | null;
  production: CertificateState | null;
  connected: boolean;
  needs_renewal: boolean;
  /** What to do next, in the server's words. Shown as written. */
  next_action: string;
}

export interface CertificateState {
  issued_at?: string;
  expires_at?: string;
  serial?: string;
  status?: string;
}

/** The three environments the tax authority runs. */
export const ZATCA_ENVIRONMENTS = ['sandbox', 'simulation', 'production'] as const;
export type ZatcaEnvironment = (typeof ZATCA_ENVIRONMENTS)[number];

/** A rate on file for one currency pair on one day. */
export interface ExchangeRate {
  id: string;
  from_currency: string;
  to_currency: string;
  rate: string;
  as_of: string;
  source?: string;
}

/** How urgent a reading is, for the eye rather than for the law. */
export type Urgency = 'critical' | 'caution' | 'settled' | 'unknown';

/**
 * How close the next filing is.
 *
 * The dates are the server's; only the banding is here. Unknown when the
 * server did not say — which happens when the filing rule for this market has
 * not been verified, and reads very differently from "nothing due".
 */
export function filingUrgency(vat: VatReading): Urgency {
  if (!vat.registered) return 'settled';
  if (vat.days_to_filing === undefined || vat.next_filing_due === undefined) {
    return 'unknown';
  }
  if (vat.days_to_filing < 0) return 'critical';
  if (vat.days_to_filing <= 14) return 'caution';
  return 'settled';
}

/**
 * Whether the e-invoicing chain is in a state that stops invoices being
 * reported.
 *
 * `failed` and `rejected` are different problems: a failure is this product's
 * or the network's and will be retried, a rejection is the authority refusing
 * the document and will not fix itself. Rejections therefore outrank failures.
 */
export function invoicingUrgency(inv: InvoicingReading): Urgency {
  if (inv.rejected > 0) return 'critical';
  if (inv.failed > 0) return 'caution';
  if (!inv.started) return 'unknown';
  if (inv.devices_ready < inv.devices) return 'caution';
  return 'settled';
}

/**
 * Whether anybody's documents need attention.
 *
 * Expired outranks expiring, because an expired residency permit is not a
 * warning about next month — it is somebody who cannot legally work today.
 */
export function documentUrgency(people: PeopleReading): Urgency {
  if (people.expired > 0) return 'critical';
  if (people.expiring_soon > 0) return 'caution';
  return 'settled';
}

/**
 * The readings that need somebody to do something, most pressing first.
 *
 * Used to lead the dashboard. A compliance screen that opened on nine equal
 * panels would have told an owner nothing, because nothing on it would be more
 * important than anything else.
 */
export interface Attention {
  key: string;
  urgency: Exclude<Urgency, 'settled'>;
}

export function needsAttention(report: ComplianceReport): Attention[] {
  const out: Attention[] = [];
  const add = (key: string, urgency: Urgency) => {
    if (urgency !== 'settled') out.push({ key, urgency });
  };

  add('invoicing', invoicingUrgency(report.invoicing));
  add('filing', filingUrgency(report.vat));
  add('documents', documentUrgency(report.people));
  if (report.blocking_rules > 0) add('rules', 'critical');
  if (report.payroll.unsubmitted_runs > 0) add('payroll', 'caution');
  if (report.privacy.overdue_requests > 0) add('privacy', 'critical');
  if (report.privacy.incidents_unnotified > 0) add('incidents', 'critical');
  if (report.storefront.missing.length > 0) add('storefront', 'caution');
  if (report.vat.open_ended_periods > 0) add('periods', 'caution');

  // Critical before caution before unknown. Stable within a band, so the order
  // above is the tie-break: it is the order a business is judged in.
  const rank = { critical: 0, caution: 1, unknown: 2 };
  return out.sort((a, b) => rank[a.urgency] - rank[b.urgency]);
}

/**
 * A rate as a percentage string, for display.
 *
 * The register speaks in percent already — "15.00" — so this only tidies the
 * trailing zeros. It does NOT convert, because a helper that multiplied by a
 * hundred would silently turn a fraction-shaped rate into 1500%.
 */
export function ratePercent(raw: string | undefined): string | null {
  if (raw === undefined) return null;
  const text = raw.trim();
  if (text === '') return null;
  const n = Number(text);
  if (!Number.isFinite(n)) return null;
  return `${text.replace(/\.?0+$/, '')}%`;
}
