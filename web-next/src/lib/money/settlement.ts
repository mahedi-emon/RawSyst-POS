// Card connections, the attempts made through them, and the money arriving in
// the bank days later.
//
// # Three answers, not two
//
// `last_check_ok` is a nullable boolean, and all three values mean something
// different: absent is "never tried", true is "answered", false is "did not
// answer". Reading absent as false would tell a shopkeeper their card machine
// is broken when nobody has ever asked it a question.
//
// # A live connection is switched on last
//
// The server refuses to activate a live connection that has never answered a
// check, so the order is configure, check, then switch on. The screen shows
// that as a sequence rather than three independent controls, because a toggle
// that refuses is a worse way to learn a rule than a step that is not offered
// yet.
//
// # The key is never here
//
// A gateway carries `has_secret`, not the secret. Nothing in this file, and
// nothing on the screen, can show a key back — which is also why an empty
// secret box on an edit means "leave what is there".

export interface Gateway {
  id: string;
  provider: string;
  label: string;
  mode: string;
  settings: Record<string, string>;
  methods: string[];
  is_active: boolean;
  /** Whether a key is stored. The key itself is never sent. */
  has_secret: boolean;
  last_checked_at?: string;
  /** Absent, true and false are three different answers. */
  last_check_ok?: boolean;
  last_check_note?: string;
}

/** One acquirer the product can talk to, and the fields it needs. */
export interface Provider {
  key: string;
  name: string;
  fields: ProviderField[];
  methods: string[];
}

export interface ProviderField {
  key: string;
  label: string;
  secret: boolean;
  hint?: string;
}

export interface Attempt {
  id: string;
  method: string;
  amount: string;
  currency: string;
  status: string;
  provider_ref?: string;
  provider_code?: string;
  provider_message?: string;
  redirect_url?: string;
  created_at: string;
  settled_at?: string;
}

/** A card payment taken at the till, not yet matched to a bank deposit. */
export interface PendingTender {
  tender_id: string;
  invoice_id: string;
  invoice_number: string;
  issued_at: string;
  method: string;
  reference?: string;
  amount: string;
  currency: string;
  already_recorded?: boolean;
}

export type Health = 'never_checked' | 'answering' | 'not_answering';

/**
 * What is known about whether this connection works.
 *
 * The three-way read is the point. `last_check_ok` is absent until somebody
 * checks, and a screen using `!gateway.last_check_ok` would report a brand new
 * connection as broken.
 */
export function health(gateway: Gateway): Health {
  if (typeof gateway.last_check_ok !== 'boolean') return 'never_checked';
  return gateway.last_check_ok ? 'answering' : 'not_answering';
}

export type Step = 'check' | 'activate' | 'ready' | 'testing';

/**
 * The one thing to do next with this connection.
 *
 * A live connection cannot be switched on until it has answered, and the
 * server refuses the attempt. Offering an activate control that refuses
 * teaches the rule by failure; naming the check as the next step teaches it
 * before anything is lost.
 *
 * A test-mode connection is never "ready" — it takes no real money — so it is
 * reported as testing however healthy it is.
 */
export function nextStep(gateway: Gateway): Step {
  if (gateway.mode !== 'live') return 'testing';
  if (health(gateway) !== 'answering') return 'check';
  return gateway.is_active ? 'ready' : 'activate';
}

/**
 * Whether a shop can actually take a card right now.
 *
 * Active AND live AND answering. Any two of the three is a connection that
 * will fail at the counter with a customer waiting, which is the specific
 * moment this screen exists to prevent.
 */
export function takingCards(gateways: readonly Gateway[]): boolean {
  return gateways.some(
    (g) => g.is_active && g.mode === 'live' && health(g) === 'answering',
  );
}

/** The fields a provider needs that are not the sealed half. */
export function publicFields(provider: Provider): ProviderField[] {
  return provider.fields.filter((f) => !f.secret);
}

/** The sealed field, if this provider has one. */
export function secretField(provider: Provider): ProviderField | null {
  return provider.fields.find((f) => f.secret) ?? null;
}

/**
 * What is still missing before this can be saved.
 *
 * The same rule the server applies, stated before the refusal rather than
 * after it. The secret is deliberately not required on an edit: a screen that
 * cannot read a key back cannot send it again, so a blank box means "leave
 * what is there".
 */
export function missingFields(
  provider: Provider,
  settings: Record<string, string>,
  secret: string,
  isNew: boolean,
): string[] {
  const missing = publicFields(provider)
    .filter((f) => !(settings[f.key] ?? '').trim())
    .map((f) => f.key);

  const sealed = secretField(provider);
  if (sealed && isNew && !secret.trim()) missing.push(sealed.key);
  return missing;
}

/**
 * What a deposit is worth once the acquirer has taken its cut.
 *
 * Money stays a string all the way through. The subtraction happens in minor
 * units as integers, because a float here is how a deposit reconciles to
 * 0.30000000000000004 short and somebody spends an afternoon on it.
 */
export function netOf(gross: string, fee: string): string {
  const g = minorUnits(gross);
  const f = minorUnits(fee);
  if (g === null || f === null) return '';
  const net = g - f;
  const sign = net < 0 ? '-' : '';
  const abs = Math.abs(net);
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`;
}

/** A decimal string as an integer number of minor units, or null if it is not one. */
function minorUnits(value: string): number | null {
  const text = (value ?? '').trim();
  // Deliberately strict. `Number('')` is 0, and an empty fee silently becoming
  // zero is a deposit that reconciles against the wrong figure.
  if (!/^-?\d+(\.\d{1,2})?$/.test(text)) return null;
  const negative = text.startsWith('-');
  const [whole, fraction = ''] = text.replace('-', '').split('.');
  const cents = Number(whole) * 100 + Number(fraction.padEnd(2, '0'));
  return negative ? -cents : cents;
}

/**
 * Adds up what is being banked.
 *
 * Returns a string, and returns '' rather than 0 if any amount is unreadable:
 * a total that silently dropped a line is worse than no total.
 */
export function totalOf(tenders: readonly PendingTender[]): string {
  let cents = 0;
  for (const t of tenders) {
    const value = minorUnits(t.amount);
    if (value === null) return '';
    cents += value;
  }
  const sign = cents < 0 ? '-' : '';
  const abs = Math.abs(cents);
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`;
}

/**
 * Tenders that can still be banked.
 *
 * `already_recorded` is omitted rather than false on a fresh one, so this reads
 * the flag as truthy-only. Offering one that has already been settled would
 * bank the same money twice.
 */
export function bankable(tenders: readonly PendingTender[]): PendingTender[] {
  return tenders.filter((t) => !t.already_recorded);
}

// ---------------------------------------------------------------------------
// Deposits already matched
// ---------------------------------------------------------------------------

/** One deposit, as `GET /settlement/batches` answers it. */
export interface ListedBatch {
  id: string;
  reference: string;
  deposited_on: string;
  gross_amount: string;
  fee_amount: string;
  net_amount: string;
  currency: string;
  tender_count: number;
  /** False for a deposit whose journal entry is missing, which must not read as reconciled. */
  posted: boolean;
}

/** One payment inside a deposit, with the share of the fee it carried. */
export interface SettledTender {
  tender_id: string;
  invoice_id: string;
  invoice_number: string;
  method: string;
  amount: string;
  fee_amount: string;
}

/** A deposit and the sales it covered, as `GET /settlement/batches/{id}` answers it. */
export interface Batch extends Omit<ListedBatch, 'tender_count' | 'posted'> {
  tenders: SettledTender[];
  already_recorded?: boolean;
}

/**
 * What is wrong with the deposit being entered, in the order somebody fixes it.
 *
 * Every one of these is a rule the SERVER applies. Saying it here first means
 * a person learns it before pressing a button, not from a refusal — and the
 * server stays the authority, so a rule that changes there is a refusal here
 * rather than a screen quietly accepting something it should not.
 */
export type DepositProblem =
  | 'nothing_selected'
  | 'no_reference'
  | 'no_date'
  | 'no_amount'
  | 'not_a_number'
  | 'net_above_gross'
  | 'net_not_positive'
  | null;

export interface DepositDraft {
  reference: string;
  depositedOn: string;
  netAmount: string;
  selected: readonly PendingTender[];
}

export function depositProblem(draft: DepositDraft): DepositProblem {
  if (draft.selected.length === 0) return 'nothing_selected';
  if (draft.reference.trim() === '') return 'no_reference';
  if (draft.depositedOn.trim() === '') return 'no_date';
  if (draft.netAmount.trim() === '') return 'no_amount';

  const gross = totalOf(draft.selected);
  const grossCents = minorUnits(gross);
  const netCents = minorUnits(draft.netAmount.trim());
  if (grossCents === null || netCents === null) return 'not_a_number';

  // "A deposit of nothing is not a deposit. Money taken back by the acquirer is
  // a chargeback, which is recorded on its own."
  if (netCents <= 0) return 'net_not_positive';
  // "An acquirer paying more than was taken is a separate event, not a fee."
  if (netCents > grossCents) return 'net_above_gross';
  return null;
}

/**
 * The fee this deposit implies: what was taken, less what arrived.
 *
 * Not derived from a configured rate anywhere in this product. A rate would be
 * a forecast, and a forecast posted into the ledger disagrees with the bank the
 * first time the contract or the scheme mix says otherwise. Shown before the
 * deposit is recorded so somebody can check the figure against the statement
 * in front of them.
 */
export function impliedFee(draft: DepositDraft): string {
  return netOf(totalOf(draft.selected), draft.netAmount.trim());
}

/**
 * The fee as a fraction of the gross, for a sanity read.
 *
 * Returns null when there is nothing to divide by, or when either figure is
 * unreadable. Two per cent is an ordinary card fee and twenty is a typing
 * error, and the difference is worth seeing before it reaches the ledger.
 */
export function feeShare(draft: DepositDraft): number | null {
  const gross = minorUnits(totalOf(draft.selected));
  const net = minorUnits(draft.netAmount.trim());
  if (gross === null || net === null || gross <= 0) return null;
  return (gross - net) / gross;
}

/** Above this a fee is worth questioning out loud before it is posted. */
export const FEE_WORTH_QUESTIONING = 0.1;
