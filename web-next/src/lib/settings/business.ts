// What a business says about itself: its identity, its branches, its
// stationery, and what it must disclose.
//
// # A field settles rather than being editable or not
//
// `GET /companies/{id}` carries `settled`: a map from field name to the sentence
// saying why that field can no longer change. Absent from the map means still
// editable. The distinction is not permanent-versus-not — it is a fact about
// what the business has already done. `base_currency` is editable on the day a
// company is created and settled the moment a journal entry exists, because
// changing it then would restate the books rather than convert them.
//
// So the screen renders a settled field fixed WITH the server's reason beside
// it, and never as a disabled box with no explanation. Naming one in a PUT is
// refused 409 `immutable` with the same sentence in `error.fields`, so a screen
// that guessed wrong would collect a refusal it could not explain.
//
// # The amendment is partial
//
// An absent field is left alone; an empty string clears it. That is what makes
// a screen with four tabbed sections safe: saving the identity section cannot
// blank the tax section merely by not mentioning it.
//
// # `missing` is the server's list, not a checklist this side invents
//
// `GET /privacy/disclosure` reports which E5 disclosures are absent, in the
// server's own field names, and the compliance dashboard reads the same list.
// Two lists would let the settings screen and the dashboard disagree about
// whether a business is ready to trade online.

/** A company, as every screen already knows it. */
export interface Company {
  id: string;
  legal_name: string;
  trade_name?: string;
  country: string;
  base_currency: string;
}

/** The business record as the settings screen reads and amends it. */
export interface Business {
  id: string;
  legal_name: string;
  legal_name_ar: string;
  trade_name: string;
  country: string;
  market: string;
  market_name: string;
  base_currency: string;
  timezone: string;

  cr_number: string;
  vat_registered: boolean;
  vat_number: string;
  zatca_wave: string;
  zatca_deadline: string;
  zatca_status: string;

  b2b_offline_policy: string;
  negative_stock_policy: string;
  costing_method: string;
  fiscal_year_start_month: number;

  match_tolerance_pct: string;
  match_tolerance_amount: string;

  wps_bank_sarie_id: string;
  wps_establishment_id: string;
  wps_bank_account: string;
  mol_establishment_id: string;

  /**
   * Field name to the sentence saying why it can no longer change.
   *
   * Absent means editable. The server's own words, shown as written — a
   * reworded explanation of why the books cannot be restated is an explanation
   * that has stopped matching the rule it describes.
   */
  settled: Record<string, string>;
}

/** A branch, with everything an invoice needs from it. */
export interface Branch {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  phone?: string;
  is_active: boolean;

  street?: string;
  building_number?: string;
  additional_number?: string;
  district?: string;
  city?: string;
  postal_code?: string;
  /** As stored. Often empty, which is not the same as absent. */
  country_code?: string;
  /**
   * What actually gets printed. Falls back to the company's country when the
   * branch has none of its own, which is why it differs from `country_code`
   * and why a screen must not infer one from the other.
   */
  effective_country_code?: string;
  /** The server's answer, never derived here. */
  can_invoice: boolean;
  /** Which address parts are missing, in the server's field names. */
  incomplete: string[];
}

/** What is printed on one kind of document. */
export interface DocumentTemplate {
  doc_type: string;
  header_text: string;
  header_text_ar: string;
  footer_text: string;
  footer_text_ar: string;
  return_policy: string;
  return_policy_ar: string;
  payment_terms: string;
  payment_terms_ar: string;
  show_logo: boolean;
  show_tax_number: boolean;
  /** Whether anybody has set this one up, as opposed to it being defaults. */
  configured: boolean;
}

/** The four documents a business puts its name on. */
export const DOC_TYPES = [
  'standard',
  'simplified',
  'credit_note',
  'debit_note',
] as const;
export type DocType = (typeof DOC_TYPES)[number];

/** What the storefront must disclose. */
export interface Disclosure {
  registration_ref?: string;
  registration_channel?: string;
  verification_badge_url?: string;

  return_policy?: string;
  return_policy_ar?: string;
  delivery_terms?: string;
  delivery_terms_ar?: string;

  contact_email?: string;
  contact_phone?: string;
  support_hours?: string;

  cooling_off_days?: number;

  /** Read-only, from the company record. */
  cr_number?: string;
  vat_number?: string;

  /** The server's own names for what is absent. Empty means complete. */
  missing: string[];
}

/** The logo on file, or the absence of one. */
export interface LogoState {
  logo: { content_type?: string; bytes?: number; updated_at?: string } | null;
}

/**
 * The disclosure fields that live on the company record rather than on the
 * disclosure itself.
 *
 * They are still typed on the settings screen — the identity form above the
 * disclosure form now owns them — so this says WHICH form, not whether they can
 * be edited at all. Before `PUT /companies/{id}` existed there was no form for
 * them anywhere, and this function said so; that is no longer true.
 */
export function isCompanyField(field: string): boolean {
  return field === 'cr_number' || field === 'vat_number';
}

/**
 * The missing disclosures, split by where somebody actually fixes each one.
 *
 * Three groups, not two. A business missing its commercial registration number
 * used to be told the box did not exist on this screen; it does now, a few
 * hundred pixels above, and pointing somewhere else would send them looking for
 * a screen that has no such field.
 *
 * - `here` — the disclosure form below.
 * - `identity` — the business form above, on this same screen.
 * - `fixed` — settled, so no form owns it and the server's reason is the answer.
 *
 * `cr_number` never settles, so it is always `identity`. `vat_number` settles
 * once e-invoicing has started, and at that point the reason is the whole
 * point: it cannot be corrected because invoices have been signed under it.
 */
export function splitMissing(
  disclosure: Disclosure,
  settled: Record<string, string> = {},
): {
  here: string[];
  identity: string[];
  fixed: string[];
} {
  const here: string[] = [];
  const identity: string[] = [];
  const fixed: string[] = [];
  for (const field of disclosure.missing ?? []) {
    if (field in settled) fixed.push(field);
    else if (isCompanyField(field)) identity.push(field);
    else here.push(field);
  }
  return { here, identity, fixed };
}

/**
 * Whether a field has settled and can no longer be amended.
 *
 * Presence in the map, never a guess from the value. A screen that decided
 * `base_currency` was fixed because a currency was set would fix it on the day
 * the company was created, when it is still perfectly changeable.
 */
export function isSettled(business: Business, field: string): boolean {
  return field in (business.settled ?? {});
}

/** The server's reason a field settled, or null while it is still editable. */
export function settledReason(
  business: Business,
  field: string,
): string | null {
  return business.settled?.[field] ?? null;
}

/**
 * The fields of an amendment worth sending.
 *
 * Only what actually changed, and never a settled field. The amendment is
 * partial — an absent field is left alone and an empty string clears it — so
 * sending the whole record back would turn "I renamed the shop" into "I also
 * confirmed every other field is exactly as I found it", which is a different
 * and much stronger claim to make on somebody's behalf.
 *
 * A settled field is NOT filtered out here, deliberately. The client's copy of
 * `settled` was true when the page loaded and the server recomputes it inside
 * the transaction that writes, so gating a request on this side would be gating
 * on information that may already be stale — and the case where the two differ
 * is exactly the case somebody needs told. A field that settled while the form
 * was open is sent, refused 409, and the reason arrives in `error.fields`, which
 * is the sentence the person actually needs. Dropping it would save everything
 * else and leave them believing the change went through.
 *
 * When the map IS current the field renders fixed rather than as an input, so
 * it never diverges from the original and never enters the diff at all.
 */
export function amendment(
  original: Business,
  draft: Partial<Business>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [field, value] of Object.entries(draft)) {
    if (field === 'settled' || field === 'id') continue;
    if (value === (original as unknown as Record<string, unknown>)[field]) continue;
    out[field] = value;
  }
  return out;
}

/**
 * The address a branch prints, in reading order.
 *
 * Empty parts are dropped rather than left as gaps, because an address with a
 * hole in it reads as a typo rather than as something unfinished — `incomplete`
 * is what says a part is missing, and it says it in the server's own words.
 */
export function addressLine(branch: Branch): string {
  return [
    branch.building_number,
    branch.street,
    branch.district,
    branch.city,
    branch.postal_code,
  ]
    .map((part) => (part ?? '').trim())
    .filter((part) => part !== '')
    .join(', ');
}

/**
 * What stops a branch issuing an invoice, if anything.
 *
 * `can_invoice` is the server's answer and is NOT "a country code is set": the
 * document layer falls back to the company's country when a branch has none of
 * its own, so a branch with an empty `country_code` invoices perfectly well.
 * Deriving this on the client would have reported every seeded branch unable to
 * invoice, including ones with a hundred invoices already behind them.
 */
export function invoiceBlock(branch: Branch): string[] {
  return branch.can_invoice ? [] : (branch.incomplete ?? []);
}

/** One accounting period. */
export interface Period {
  id: string;
  fiscal_year: number;
  period_no: number;
  starts_on: string;
  ends_on: string;
  state: string;
  /** How many entries have been posted into it. */
  entries: number;
}

/** A fiscal year and the periods in it. */
export interface FiscalYear {
  fiscal_year: number;
  periods: Period[];
}

/** The states a period moves through. */
export type PeriodState = 'open' | 'closed' | 'locked';

/**
 * Whether a period can be closed.
 *
 * Only an open one, and only once the one before it is closed — closing March
 * while February is open would leave a hole somebody can still post into, and
 * the trial balance for the closed quarter would keep moving.
 *
 * The server is the authority; this exists so the button is not offered where it
 * would be refused.
 */
export function canClose(periods: Period[], period: Period): boolean {
  if (period.state !== 'open') return false;
  const earlier = periods.filter(
    (p) =>
      p.fiscal_year < period.fiscal_year ||
      (p.fiscal_year === period.fiscal_year && p.period_no < period.period_no),
  );
  return earlier.every((p) => p.state !== 'open');
}

/**
 * Why a period cannot be closed yet.
 *
 * Named rather than left as a disabled button, because "you cannot close March"
 * and "close February first" are different sentences and only one of them tells
 * somebody what to do.
 */
export type CloseBlock = 'already_closed' | 'earlier_open' | 'none';

export function closeBlock(periods: Period[], period: Period): CloseBlock {
  if (period.state !== 'open') return 'already_closed';
  if (!canClose(periods, period)) return 'earlier_open';
  return 'none';
}

/** Every period across every year, oldest first. */
export function allPeriods(years: FiscalYear[]): Period[] {
  return years
    .flatMap((y) => y.periods)
    .sort((a, b) =>
      a.fiscal_year === b.fiscal_year
        ? a.period_no - b.period_no
        : a.fiscal_year - b.fiscal_year,
    );
}

/**
 * The period a date falls in, if any.
 *
 * Used to say which month is the current one. Compared as ISO calendar strings
 * rather than as timestamps: a period runs to the end of its last day, and a
 * shop in Dhaka opening this in the morning is on a different UTC day.
 */
export function periodOn(periods: Period[], day: string): Period | null {
  return periods.find((p) => p.starts_on <= day && day <= p.ends_on) ?? null;
}

/**
 * Whether a year already exists in the calendar.
 *
 * Opening one that does is not an error — two people can press it on the same
 * morning, and the server answers with how many periods it actually created —
 * but the screen should not invite it.
 */
export function yearExists(years: FiscalYear[], year: number): boolean {
  return years.some((y) => y.fiscal_year === year);
}

/** Today, as an ISO calendar day in the reader's own timezone. */
export function localDay(now: Date = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(
    now.getDate(),
  ).padStart(2, '0')}`;
}
