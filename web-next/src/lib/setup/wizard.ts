// First-time setup, as blueprint A5 describes it.
//
// # Why a wizard rather than a list of settings screens
//
// A5's whole point is that a non-technical shop owner completes it alone. The
// settings screens exist and each is correct, and finding six of them in the
// right order is not something somebody does on their first morning with a
// product. So this is one path with one next step at a time, and the steps are
// the backend's own — `onboarding_step` is an enum in the database and the
// route refuses a step out of order.
//
// # The server holds the answers, not this browser
//
// Every step is written to `PUT /onboarding/steps/{step}` before it is
// completed, so closing the laptop between two customers loses nothing. There
// is no local draft that outlives a refresh, deliberately: a wizard that
// remembered more than the server would show somebody answers that were never
// saved.
//
// # Validation happens at completion, not at save
//
// The route says so: "A store with no name yet must be resumable rather than
// rejected — an Owner filling in a form on a phone between customers should not
// lose their work to a validation error." So saving accepts anything and the
// checks below run when somebody presses Continue.
//
// # These rules are the server's, restated
//
// Every refusal below is one `validateStep` makes. Stating them here first
// means the person learns the rule from the form rather than from a round trip;
// the server stays the authority, and a rule that changes there becomes a
// refusal here rather than a screen that quietly accepts something.

/** The seven steps of A5, in the order the enum defines. */
export const STEPS = [
  'business_info',
  'stores',
  'tax',
  'employees',
  'hardware',
  'opening_balances',
  'finished',
] as const;
export type Step = (typeof STEPS)[number];

/** Where a tenant has reached, as `GET /onboarding` answers it. */
export interface Progress {
  current_step: string;
  completed_steps: string[];
  /** An object keyed by step. Free-form by design: each step carries different answers. */
  step_data: Record<string, unknown> | null;
  finished: boolean;
  next_step?: string;
}

/**
 * The countries this release serves, and why the list is short.
 *
 * A company is only usable in a market whose tax rules are in the regulatory
 * registry: the sale service resolves a VAT rate from `country` at the date of
 * every sale and refuses when it cannot. Offering a fourth country would not
 * make a flexible product; it would make an owner who completes seven steps and
 * then discovers at the counter that no sale can be rung up.
 */
export const COUNTRIES = ['sa', 'bd', 'us'] as const;
export type Country = (typeof COUNTRIES)[number];

/**
 * The currencies the product keeps books in.
 *
 * Checked separately from the country, because a business in one country
 * legitimately keeps its books in another's currency. The set is the one the
 * rest of the product is built for: the counter's note-and-coin pad has
 * denominations for exactly these three, and a fourth would leave a cashier
 * counting a drawer with no pad to count it on.
 */
export const CURRENCIES = ['SAR', 'BDT', 'USD'] as const;
export type Currency = (typeof CURRENCIES)[number];

/** The currency a shop in this country most likely keeps its books in. */
export function likelyCurrency(country: string): Currency | '' {
  switch (country) {
    case 'sa':
      return 'SAR';
    case 'bd':
      return 'BDT';
    case 'us':
      return 'USD';
    default:
      return '';
  }
}

/**
 * Time zones offered per market, and one that is always offered.
 *
 * A list rather than the browser's guess: `Intl.DateTimeFormat().resolvedOptions()`
 * reports where the LAPTOP is, and an owner setting up their Riyadh shop from a
 * hotel in London would have every fiscal period a day out.
 */
export const TIMEZONES: Record<string, readonly string[]> = {
  sa: ['Asia/Riyadh'],
  bd: ['Asia/Dhaka'],
  us: [
    'America/New_York',
    'America/Chicago',
    'America/Denver',
    'America/Los_Angeles',
    'America/Anchorage',
    'Pacific/Honolulu',
  ],
};

export function timezonesFor(country: string): string[] {
  return [...(TIMEZONES[country] ?? []), 'UTC'];
}

// ---------------------------------------------------------------------------
// The business step
// ---------------------------------------------------------------------------

export interface BusinessAnswers {
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

export const EMPTY_BUSINESS: BusinessAnswers = {
  legal_name: '',
  legal_name_ar: '',
  trade_name: '',
  country: '',
  base_currency: '',
  timezone: '',
  cr_number: '',
  vat_registered: false,
  vat_number: '',
};

export type BusinessProblem =
  | 'no_legal_name'
  | 'no_country'
  | 'unsupported_country'
  | 'no_currency'
  | 'unsupported_currency'
  | 'no_vat_number'
  | null;

export function businessProblem(a: BusinessAnswers): BusinessProblem {
  if (a.legal_name.trim() === '') return 'no_legal_name';
  const country = a.country.trim().toLowerCase();
  if (country === '') return 'no_country';
  if (!(COUNTRIES as readonly string[]).includes(country)) return 'unsupported_country';
  const currency = a.base_currency.trim().toUpperCase();
  if (currency === '') return 'no_currency';
  if (!(CURRENCIES as readonly string[]).includes(currency)) return 'unsupported_currency';
  // Mirrors the database constraint, so the Owner meets it here rather than as
  // a constraint violation five steps later.
  if (a.vat_registered && a.vat_number.trim() === '') return 'no_vat_number';
  return null;
}

// ---------------------------------------------------------------------------
// The store step
// ---------------------------------------------------------------------------

/**
 * One branch, with the National Address ZATCA asks for on the face of every
 * invoice.
 *
 * BR-KSA-09 names all six parts. The server requires them for every market and
 * not only for Saudi Arabia, and that is the rule this form follows: a shop
 * that finishes setup without an address can take money and cannot issue a
 * compliant invoice for it, which is the worst order to discover the problem
 * in.
 */
export interface StoreAnswers {
  code: string;
  name: string;
  street: string;
  building_number: string;
  additional_number: string;
  district: string;
  city: string;
  postal_code: string;
  country_code: string;
}

export function emptyStore(countryCode: string): StoreAnswers {
  return {
    code: '',
    name: '',
    street: '',
    building_number: '',
    additional_number: '',
    district: '',
    city: '',
    postal_code: '',
    country_code: countryCode.toUpperCase(),
  };
}

const FOUR_DIGITS = /^[0-9]{4}$/;
const FIVE_DIGITS = /^[0-9]{5}$/;
const TWO_LETTERS = /^[A-Za-z]{2}$/;

/**
 * What is wrong with one branch, per field.
 *
 * Returned as a map rather than a single verdict because a form shows six
 * problems at once and fixing them one refusal at a time is how somebody
 * abandons a wizard. Each key is the field name the server uses, so the same
 * map can hold a server refusal without translating between two vocabularies.
 */
export function storeProblems(s: StoreAnswers): Record<string, string> {
  const bad: Record<string, string> = {};
  if (s.name.trim() === '') bad.name = 'no_name';
  if (s.code.trim() === '') bad.code = 'no_code';
  if (s.street.trim() === '') bad.street = 'required';
  if (s.district.trim() === '') bad.district = 'required';
  if (s.city.trim() === '') bad.city = 'required';
  if (!FOUR_DIGITS.test(s.building_number.trim())) bad.building_number = 'four_digits';
  if (!FIVE_DIGITS.test(s.postal_code.trim())) bad.postal_code = 'five_digits';
  // Optional, but wrong is worse than absent.
  const extra = s.additional_number.trim();
  if (extra !== '' && !FOUR_DIGITS.test(extra)) bad.additional_number = 'four_digits';
  const code = s.country_code.trim();
  if (code !== '' && !TWO_LETTERS.test(code)) bad.country_code = 'two_letters';
  return bad;
}

export type StoresProblem = 'none_added' | 'duplicate_code' | 'incomplete' | null;

export function storesProblem(stores: readonly StoreAnswers[]): StoresProblem {
  if (stores.length === 0) return 'none_added';
  const seen = new Set<string>();
  for (const s of stores) {
    const code = s.code.trim().toUpperCase();
    if (code !== '') {
      // Codes appear in every document number, so a duplicate corrupts the
      // numbering rather than merely confusing a list.
      if (seen.has(code)) return 'duplicate_code';
      seen.add(code);
    }
    if (Object.keys(storeProblems(s)).length > 0) return 'incomplete';
  }
  return null;
}

// ---------------------------------------------------------------------------
// Reading the progress
// ---------------------------------------------------------------------------

/** The answers already saved for one step, or null when there are none. */
export function answersFor<T>(progress: Progress | undefined, step: Step): T | null {
  const all = progress?.step_data;
  if (!all || typeof all !== 'object') return null;
  const found = (all as Record<string, unknown>)[step];
  return found === undefined || found === null ? null : (found as T);
}

export function isDone(progress: Progress | undefined, step: Step): boolean {
  return (progress?.completed_steps ?? []).includes(step);
}

/**
 * Whether this step can be opened.
 *
 * Steps are completed in order — the route refuses a jump — but a completed one
 * is still worth reading, and reading is not changing. So a step is reachable
 * when it is the current one or one already done.
 *
 * Allowing a jump forward would let opening balances be entered before the
 * chart of accounts exists, and the resulting failure would surface as a
 * confusing error rather than as a missing prerequisite.
 */
export function isReachable(progress: Progress | undefined, step: Step): boolean {
  if (!progress) return false;
  if (progress.finished) return step !== 'finished';
  return step === progress.current_step || isDone(progress, step);
}

/** How far along, for the progress read-out. `finished` is not a step to do. */
export function stepsDone(progress: Progress | undefined): number {
  const doable = STEPS.filter((s) => s !== 'finished');
  if (progress?.finished) return doable.length;
  return doable.filter((s) => isDone(progress, s)).length;
}

export const STEPS_TO_DO = STEPS.length - 1;

/**
 * Whether the company still has to be created from the answers.
 *
 * `POST /onboarding/company` is what turns the business step's scratch JSON
 * into the company that owns the books, the VAT registration and the invoice
 * sequence, and completing the step does NOT do it. Calling it twice would
 * either create a second company or be refused by the plan ceiling, so the
 * wizard asks whether one exists rather than remembering that it called it.
 */
export function needsCompany(companyCount: number): boolean {
  return companyCount === 0;
}
