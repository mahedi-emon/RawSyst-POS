// The counters, the tags they print, and the serial numbers they sell.
//
// # A till is bound one of two ways, and the difference decides everything
//
// `session` is a counter somebody opens in a browser: it is usable the moment
// it is registered, because the authorisation is the person's. `paired` is
// registered for one machine and stays `pending` until that machine enrols with
// a code — which is the whole reason a pending till is not a broken till, and
// why the screen must not offer to "fix" it.
//
// # An enrolment code is shown once
//
// The server hashes it and the response carrying it is the only copy. A screen
// that implied it could be looked up again would leave somebody closing the
// panel expecting to find it later.
//
// # Changing the barcode scheme changes nothing already printed
//
// The route says so in as many words: "a code printed on nine hundred hang tags
// does not move because somebody edited a setting". So the scheme screen is
// about what the NEXT code will look like, and says so — an owner who thinks
// they have just renumbered their stock is an owner about to reprint a shop.

/** One counter. */
export interface Terminal {
  id: string;
  store_id: string;
  store: string;
  terminal_label: string;
  status: string;
  /** `session` or `paired`. Decides whether `pending` is a problem. */
  binding: string;
  egs_unit_id?: string;
  egs_unit?: string;
  csid_status?: string;
  /** Whether an enrolment code is outstanding. */
  pending_code: boolean;
}

/** The two ways a counter is authorised. */
export const BINDINGS = ['session', 'paired'] as const;
export type Binding = (typeof BINDINGS)[number];

/** How a terminal's state reads. */
export type TerminalState =
  | 'working'
  | 'awaiting_machine'
  | 'awaiting_registration'
  | 'suspended'
  | 'revoked';

/**
 * What a terminal is actually doing.
 *
 * `pending` means two different things and they need different words. On a
 * PAIRED till it is the normal, expected state between registering the counter
 * and the machine enrolling — nothing is wrong and somebody needs to type a
 * code into that machine. On a SESSION till it should never last, because such
 * a counter is authorised by whoever opens it.
 */
export function terminalState(t: Terminal): TerminalState {
  if (t.status === 'revoked') return 'revoked';
  if (t.status === 'disabled' || t.status === 'inactive') return 'suspended';
  if (t.status === 'active') return 'working';
  if (t.status === 'pending') {
    return t.binding === 'paired' ? 'awaiting_machine' : 'awaiting_registration';
  }
  return 'suspended';
}

/**
 * Whether issuing an enrolment code would achieve anything.
 *
 * Only a paired till needs one, and only while it is waiting. Offering it on a
 * session counter would hand somebody a code with nothing to type it into.
 */
export function needsEnrolmentCode(t: Terminal): boolean {
  return t.binding === 'paired' && terminalState(t) === 'awaiting_machine';
}

/**
 * Whether this market requires a signing unit before a till can sell.
 *
 * Saudi Arabia only today, and the server is the authority — this exists so the
 * register form can say the field is required BEFORE the refusal, rather than
 * after. A market with no e-invoicing obligation must not be shown a field it
 * has nothing to put in.
 */
export function needsSigningUnit(market: string): boolean {
  return market.toLowerCase() === 'sa';
}

/** One counter's own configuration. Every field is optional. */
export interface TerminalSettings {
  device_id: string;
  default_warehouse_id?: string;
  receipt_template?: string;
  printer_name?: string;
  scanner_prefix?: string;
  drawer_enabled?: boolean;
  require_customer?: boolean;
  max_held_carts?: number;
  default_discount_rule?: string;
}

/**
 * The fields of a settings change worth sending.
 *
 * The route takes every field as a pointer and leaves an absent one alone,
 * precisely so "a screen changing the printer should not have to restate the
 * discount rule". Sending the whole record back would undo that: two people
 * editing different settings on the same till would each overwrite the other
 * with what they happened to have loaded.
 */
export function settingsChange(
  original: TerminalSettings,
  draft: TerminalSettings,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  const before = original as unknown as Record<string, unknown>;
  const after = draft as unknown as Record<string, unknown>;
  for (const field of Object.keys(after)) {
    if (field === 'device_id') continue;
    if (after[field] === before[field]) continue;
    out[field] = after[field];
  }
  return out;
}

/** How every barcode from here on will be built. */
export interface BarcodeScheme {
  parts: string[];
  separator: string;
  symbology: string;
  part_length: number;
  /** What the next code will look like. The server builds it, not this side. */
  example: string;
}

/** The parts a code can be built from. */
export const SCHEME_PARTS = [
  'category',
  'brand',
  'colour',
  'size',
  'sequence',
] as const;

/** The barcode symbologies the printer understands. */
export const SYMBOLOGIES = ['code128', 'ean13', 'upca', 'qr'] as const;

/** A label layout. */
export interface LabelTemplate {
  id: string;
  name: string;
  kind: string;
  width_mm: string;
  height_mm: string;
  columns?: number;
  rows?: number;
  margin_mm: string;
  gap_mm: string;
  fields: LabelField[];
  is_default: boolean;
  /** columns × rows — how a shopkeeper actually buys labels, "a sheet of 24". */
  per_sheet?: number;
}

export interface LabelField {
  field: string;
  size?: number;
  height?: number;
  bold?: boolean;
  rtl?: boolean;
}

/** The two kinds of label stock. */
export const LABEL_KINDS = ['thermal', 'sheet'] as const;
export type LabelKind = (typeof LABEL_KINDS)[number];

/**
 * How many labels one sheet holds.
 *
 * `per_sheet` when the server states it; otherwise the grid, when there is one.
 * A thermal roll has neither and gets null rather than 1 — a roll does not come
 * in sheets, and saying "1 per sheet" invites somebody to work out how many
 * sheets they need.
 */
export function perSheet(template: LabelTemplate): number | null {
  if (template.per_sheet && template.per_sheet > 0) return template.per_sheet;
  if (template.columns && template.rows) return template.columns * template.rows;
  return null;
}

/** Why a print run cannot be sent yet. */
export type PrintProblem = 'no_template' | 'nothing_selected' | 'no_copies' | 'none';

/**
 * Whether a print run is ready.
 *
 * A run has to name a template and SOMETHING to print — a variant, a category,
 * a brand or a search. An empty selection would otherwise mean "every product
 * in the shop", which is a reasonable reading and a very expensive mistake.
 */
export function printProblem(draft: {
  templateID: string;
  variantIDs: string[];
  categoryID: string;
  brandID: string;
  search: string;
  copies: string;
}): PrintProblem {
  if (draft.templateID === '') return 'no_template';
  const named =
    draft.variantIDs.length > 0 ||
    draft.categoryID !== '' ||
    draft.brandID !== '' ||
    draft.search.trim() !== '';
  if (!named) return 'nothing_selected';
  const copies = Number(draft.copies.trim());
  if (draft.copies.trim() === '' || !Number.isInteger(copies) || copies < 1) {
    return 'no_copies';
  }
  return 'none';
}

/** One serialised unit, and where it has been. */
export interface Serial {
  id: string;
  serial_no: string;
  variant_id: string;
  sku?: string;
  product?: string;
  status: string;
  warehouse_id?: string;
  supplier_id?: string;
  supplier?: string;
  invoice_id?: string;
  customer_id?: string;
  customer?: string;
  sold_at?: string;
  warranty_until?: string;
  /** Derived from the date on the server, never stored and never recomputed. */
  under_warranty: boolean;
}

/** How a serial's warranty reads. */
export type WarrantyState = 'covered' | 'expired' | 'unsold' | 'none';

/**
 * What the warranty desk needs to know about one unit.
 *
 * `under_warranty` is the server's, derived from the date rather than stored —
 * "a flag would be wrong every morning until a job ran, and the warranty desk
 * is exactly where a stale answer costs the shop money". This only decides
 * which sentence to show, and never recomputes the answer.
 */
export function warrantyState(serial: Serial): WarrantyState {
  if (!serial.sold_at) return 'unsold';
  if (!serial.warranty_until) return 'none';
  return serial.under_warranty ? 'covered' : 'expired';
}
