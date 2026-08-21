// What the wizard works out for itself.
//
// The server validates every step and is the authority — `validateStep` in
// `internal/provisioning/onboarding.go` runs the same rules and a form that
// disagreed with it would simply be wrong. What is here exists to save a round
// trip and to say which field is at fault before the Owner presses Continue.
//
// # Nothing here decides what ZATCA requires
//
// The wave and the deadline are captured, never computed. Blueprint E1.0 is
// emphatic that the software must not assume or assert a taxpayer's wave: it
// comes from ZATCA to the taxpayer directly, and a product that guessed would
// be telling a shop it is obligated on a date nobody official gave them.

import type { OnboardingProgress, OnboardingStep } from '../api/onboarding';

// --- the steps ------------------------------------------------------------

export interface StepMeta {
  key: OnboardingStep;
  title: string;
  /** One sentence on why the step exists, shown under the heading. */
  purpose: string;
  /** Optional steps can be confirmed empty. The server agrees: employees,
   *  hardware and opening balances all pass validation with nothing in them. */
  optional: boolean;
}

/** The seven steps of blueprint A5, in the order the server keeps them.
 *
 * Listed for labels only. Which step comes next is read from the server's
 * `next_step`, never computed from this array — the server owns the order and
 * a client that re-derived it would drift the day a step moved.
 */
export const STEPS: StepMeta[] = [
  {
    key: 'business_info',
    title: 'Business',
    purpose:
      'The legal identity every invoice you issue will carry. It has to match your registration exactly.',
    optional: false,
  },
  {
    key: 'stores',
    title: 'Stores',
    purpose:
      'Every sale is recorded against a store, and each store code appears in its invoice numbers.',
    optional: false,
  },
  {
    key: 'tax',
    title: 'Tax',
    purpose:
      'What your country requires, loaded from the regulatory register — and the ZATCA dates from your own notification.',
    optional: false,
  },
  {
    key: 'employees',
    title: 'People',
    purpose:
      'Who else works here. A single-person shop is a real business; you can add people at any time.',
    optional: true,
  },
  {
    key: 'hardware',
    title: 'Hardware',
    purpose:
      'Tills, scanners and printers. A terminal pairs itself later, so nothing here blocks you from trading.',
    optional: true,
  },
  {
    key: 'opening_balances',
    title: 'Opening balances',
    purpose:
      'What the business already owns and owes on the day it starts on RawSyst. A new business may have none.',
    optional: true,
  },
  {
    key: 'finished',
    title: 'Finish',
    purpose: 'Review what you entered, then create the business.',
    optional: false,
  },
];

export function stepMeta(step: OnboardingStep): StepMeta {
  return STEPS.find((s) => s.key === step) ?? STEPS[0]!;
}

/** How far along, for the progress indicator. */
export function stepNumber(step: OnboardingStep): number {
  const i = STEPS.findIndex((s) => s.key === step);
  return i < 0 ? 1 : i + 1;
}

/** Whether a step has been completed, so a finished step can be revisited. */
export function isComplete(progress: OnboardingProgress, step: OnboardingStep): boolean {
  return progress.completed_steps.includes(step);
}

/** A step is reachable if it is complete or is the one the server is on. A
 *  wizard that let somebody jump to step 6 would collect answers the server
 *  will refuse, which reads as the product losing their work. */
export function isReachable(progress: OnboardingProgress, step: OnboardingStep): boolean {
  return isComplete(progress, step) || progress.current_step === step;
}

/** The answers already given for a step, or an empty object. */
export function answersFor(
  progress: OnboardingProgress,
  step: OnboardingStep,
): Record<string, unknown> {
  const all = progress.step_data ?? {};
  const found = (all as Record<string, unknown>)[step];
  return found && typeof found === 'object' ? (found as Record<string, unknown>) : {};
}

// --- what a business looks like -------------------------------------------

export interface BusinessInfo {
  legal_name: string;
  legal_name_ar: string;
  trade_name: string;
  country: string;
  base_currency: string;
  timezone: string;
  cr_number: string;
  vat_registered: boolean;
  vat_number: string;
}

export interface StoreInfo {
  code: string;
  name: string;
}

/** The ZATCA obligation, as the taxpayer was told it. Both fields are optional
 *  because a business outside Saudi has neither, and one inside Saudi may not
 *  have been notified yet. */
export interface TaxInfo {
  zatca_wave: string;
  zatca_deadline: string;
}

export type FieldErrors = Record<string, string>;

/** Saudi VAT numbers are 15 digits beginning and ending with 3. Mirrors the
 *  database constraint so the Owner is told here rather than by a constraint
 *  violation after the company is created. */
const SAUDI_VAT = /^3[0-9]{13}3$/;

/**
 * The same rules `validateStep` applies to the business step.
 *
 * The VAT-number FORMAT check is extra, and only for Saudi: the company table
 * requires a number when registered, and the EGS unit requires it in that
 * shape. Catching it here means a shop is not told at e-invoicing setup that
 * the number they entered at step 1 was never usable.
 */
export function validateBusiness(v: BusinessInfo): FieldErrors {
  const errors: FieldErrors = {};

  if (!v.legal_name.trim()) {
    errors.legal_name = 'Enter the registered legal name of the business.';
  }
  if (v.country.trim().length !== 2) {
    errors.country = 'Choose the country the business operates in.';
  }
  if (v.base_currency.trim().length !== 3) {
    errors.base_currency = 'Choose the currency you keep your books in.';
  }
  if (v.vat_registered && !v.vat_number.trim()) {
    errors.vat_number =
      'Enter your VAT registration number. It appears on every tax invoice you issue.';
  } else if (
    v.vat_registered &&
    v.country.trim().toLowerCase() === 'sa' &&
    !SAUDI_VAT.test(v.vat_number.trim())
  ) {
    errors.vat_number = 'A Saudi VAT number is 15 digits and starts and ends with 3.';
  }

  return errors;
}

/** The same rules the server applies to the stores step. Returned per row so
 *  the message sits under the field that is wrong rather than above the list. */
export function validateStores(stores: StoreInfo[]): {
  form?: string;
  rows: Record<number, FieldErrors>;
} {
  const rows: Record<number, FieldErrors> = {};

  if (stores.length === 0) {
    return {
      form: 'Add at least one store. Every sale is recorded against a store.',
      rows,
    };
  }

  const seen = new Map<string, number>();
  stores.forEach((s, i) => {
    const row: FieldErrors = {};
    if (!s.name.trim()) row.name = 'Give this store a name.';

    const code = s.code.trim().toUpperCase();
    if (!code) {
      row.code =
        'Give this store a short code. It appears in invoice numbers, for example INV-RYD-000001.';
    } else if (seen.has(code)) {
      row.code = `Store ${seen.get(code)! + 1} already uses ${code}. Codes identify the store in every document number.`;
    } else {
      seen.set(code, i);
    }

    if (Object.keys(row).length > 0) rows[i] = row;
  });

  return { rows };
}

/** A date the taxpayer read off a ZATCA notification. Optional, and refused
 *  only when it is not a date at all — the product has no business telling a
 *  shop their deadline looks wrong. */
export function validateTax(v: TaxInfo): FieldErrors {
  const errors: FieldErrors = {};
  const deadline = v.zatca_deadline.trim();
  if (deadline && !/^\d{4}-\d{2}-\d{2}$/.test(deadline)) {
    errors.zatca_deadline = 'Enter the date as it appears on your notification, like 2026-01-01.';
  }
  return errors;
}

// --- what the tax step already knows --------------------------------------

/** What a country's tax rules mean for setup, for the copy on the Tax step.
 *
 * Only what the product already applies elsewhere: Saudi VAT is resolved from
 * the regulatory registry at the transaction date, and the ZATCA obligation is
 * a matter for the taxpayer's own notification. Nothing here is a rate this
 * module decides — the server resolves the rate when a sale is priced, and a
 * screen that hard-coded one would be a second source of truth.
 */
export interface TaxContext {
  /** True when this country's tax configuration comes from the registry rather
   *  than from anything typed here. */
  fromRegistry: boolean;
  /** Whether ZATCA e-invoicing applies, and therefore whether to ask for the
   *  wave and deadline at all. */
  zatcaApplies: boolean;
  rtl: boolean;
}

export function taxContextFor(country: string): TaxContext {
  const sa = country.trim().toLowerCase() === 'sa';
  return { fromRegistry: sa, zatcaApplies: sa, rtl: sa };
}

// --- readiness ------------------------------------------------------------

export type ReadinessState = 'incomplete' | 'ready' | 'done';

/**
 * Where setup stands, in one word.
 *
 * Deliberately about SETUP, not about ZATCA. A company's ZATCA position is
 * `zatca_status` on the company and `csid_status` on each EGS unit, both owned
 * by the server and neither of them something this wizard advances. Reporting
 * "ready" here has never meant a shop may issue a tax invoice, and the copy
 * says so.
 */
export function readiness(progress: OnboardingProgress): ReadinessState {
  if (progress.finished) return 'done';
  const required = STEPS.filter((s) => !s.optional && s.key !== 'finished');
  const outstanding = required.filter((s) => !isComplete(progress, s.key));
  return outstanding.length === 0 ? 'ready' : 'incomplete';
}

/** Which required steps are still outstanding, so the finish step can name
 *  them instead of only refusing. */
export function outstandingSteps(progress: OnboardingProgress): StepMeta[] {
  return STEPS.filter(
    (s) => !s.optional && s.key !== 'finished' && !isComplete(progress, s.key),
  );
}
