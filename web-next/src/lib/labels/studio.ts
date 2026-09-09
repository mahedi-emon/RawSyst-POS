// Designing a label, and overriding the code on one product.
//
// # The field vocabulary is the backend's, not this screen's
//
// `labels.PrintableFields` names exactly what a print run can fill: a logo, the
// product name, its Arabic name, the variant's attributes, the VAT-inclusive
// price, the barcode, and — for a loyalty card — a customer name. The server
// refuses anything else, and this offers exactly the same list, so a template
// built here is one the printer can render.
//
// That refusal is the point. A label naming a field the run cannot fill prints
// a blank line and says nothing about it: the layout is right, the price is
// absent, and nobody discovers it until nine hundred tags are on a rail.
//
// # A sheet has a grid and a roll does not
//
// `label_template_sheet_has_a_grid` refuses columns and rows on anything but an
// A4 sheet, and refuses their absence on one. Stated here so the form asks for
// them where they belong rather than teaching the rule with a constraint
// violation.
//
// # Millimetres, because that is what the box says
//
// Every label roll and every printer driver is specified in them, and 50x25 is
// a number a shopkeeper reads off the packaging. Nothing here converts.

/** One line printed on a label. */
export interface LabelField {
  field: string;
  /** Point size, for text. */
  size?: number;
  /** Height in millimetres, for the barcode. */
  height?: number;
  bold?: boolean;
  rtl?: boolean;
}

/** A layout, as `GET /labels/templates` answers it. */
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
  /** columns x rows, which is how a shopkeeper buys the paper. */
  per_sheet?: number;
}

/** One label a print run produced, as `POST /labels/print` answers it. */
export interface PreparedLabel {
  variant_id: string;
  sku: string;
  barcode: string;
  symbology: string;
  /** The meaningful string, which for a digit symbology is not the barcode. */
  readable?: string;
  name: string;
  name_ar?: string;
  attributes?: string;
  category?: string;
  brand?: string;
  season?: string;
  /** VAT-inclusive: the shelf price is the price paid. */
  price: string;
  currency: string;
  tax_rate: string;
}

export interface PreparedSheet {
  template: LabelTemplate;
  labels: PreparedLabel[];
}

/** The kinds of label the schema admits. */
export const LABEL_KINDS = ['hang_tag', 'thermal', 'a4_sheet', 'loyalty_card'] as const;
export type LabelKind = (typeof LABEL_KINDS)[number];

/**
 * What a label can carry, in the order it reads down the sticker.
 *
 * The same seven `labels.PrintableFields` lists. `customer_name` is offered
 * only on a loyalty card, because that is the only kind whose subject is a
 * person rather than a garment — see `fieldsFor`.
 */
export const PRINTABLE_FIELDS = [
  'logo',
  'name',
  'name_ar',
  'attributes',
  'price',
  'barcode',
  'customer_name',
] as const;
export type PrintableField = (typeof PRINTABLE_FIELDS)[number];

/** Which of them make sense on this kind of label. */
export function fieldsFor(kind: string): PrintableField[] {
  return PRINTABLE_FIELDS.filter((f) =>
    kind === 'loyalty_card'
      ? f !== 'attributes' && f !== 'name_ar'
      : f !== 'customer_name',
  );
}

/** Fields that take a point size rather than a height in millimetres. */
export function isTextField(field: string): boolean {
  return field !== 'logo' && field !== 'barcode';
}

/** A layout being edited, before it becomes a request. */
export interface TemplateDraft {
  id?: string;
  name: string;
  kind: string;
  width_mm: string;
  height_mm: string;
  columns: string;
  rows: string;
  margin_mm: string;
  gap_mm: string;
  is_default: boolean;
  fields: LabelField[];
}

export type TemplateProblem =
  | 'no_name'
  | 'unknown_kind'
  | 'bad_width'
  | 'bad_height'
  | 'needs_grid'
  | 'no_fields'
  | 'unprintable_field'
  | null;

/** A positive decimal, as a millimetre measurement is. */
function positiveNumber(value: string): boolean {
  const text = value.trim();
  if (!/^\d+(\.\d+)?$/.test(text)) return false;
  return Number(text) > 0;
}

/**
 * What is still wrong with the layout, in the order somebody fixes it.
 *
 * Every one of these is a rule the server applies. Stating them here first
 * means the person learns the rule from the form rather than from a refusal;
 * the server stays the authority, so a rule that changes there becomes a
 * refusal here rather than a screen that quietly accepts something it should
 * not.
 */
export function templateProblem(draft: TemplateDraft): TemplateProblem {
  if (draft.name.trim() === '') return 'no_name';
  if (!(LABEL_KINDS as readonly string[]).includes(draft.kind)) return 'unknown_kind';
  if (!positiveNumber(draft.width_mm)) return 'bad_width';
  if (!positiveNumber(draft.height_mm)) return 'bad_height';
  if (draft.kind === 'a4_sheet') {
    const across = Number(draft.columns);
    const down = Number(draft.rows);
    if (!Number.isInteger(across) || across <= 0) return 'needs_grid';
    if (!Number.isInteger(down) || down <= 0) return 'needs_grid';
  }
  if (draft.fields.length === 0) return 'no_fields';
  if (
    draft.fields.some(
      (f) => !(PRINTABLE_FIELDS as readonly string[]).includes(f.field),
    )
  ) {
    return 'unprintable_field';
  }
  return null;
}

/**
 * The body the save route takes.
 *
 * A grid is sent only for an A4 sheet: the service nulls it for every other
 * kind, and sending one anyway would be a request that says something the
 * database refuses.
 */
export function templateBody(draft: TemplateDraft): Record<string, unknown> {
  const body: Record<string, unknown> = {
    name: draft.name.trim(),
    kind: draft.kind,
    width_mm: draft.width_mm.trim(),
    height_mm: draft.height_mm.trim(),
    margin_mm: draft.margin_mm.trim() || '0',
    gap_mm: draft.gap_mm.trim() || '0',
    is_default: draft.is_default,
    fields: draft.fields,
  };
  if (draft.kind === 'a4_sheet') {
    body.columns = Number(draft.columns);
    body.rows = Number(draft.rows);
  }
  return body;
}

/** An existing layout, as the editor holds it. */
export function draftOf(template: LabelTemplate): TemplateDraft {
  return {
    id: template.id,
    name: template.name,
    kind: template.kind,
    width_mm: template.width_mm,
    height_mm: template.height_mm,
    columns: template.columns ? String(template.columns) : '',
    rows: template.rows ? String(template.rows) : '',
    margin_mm: template.margin_mm,
    gap_mm: template.gap_mm,
    is_default: template.is_default,
    // Copied rather than shared: the editor mutates its own list, and a
    // reference into the cached response would change the table behind it.
    fields: template.fields.map((f) => ({ ...f })),
  };
}

/**
 * A blank layout of the given kind, with sizes the hardware actually comes in.
 *
 * Not a guess about what the shop wants — a guess about what the printer is.
 * 50x25 is what a thermal roll comes in and a sheet of 24 is what the box of
 * A4 labels says on it, so somebody starting a new layout does not have to
 * measure a sticker before they can save one.
 */
export function blankDraft(kind: LabelKind): TemplateDraft {
  const shape: Record<LabelKind, Partial<TemplateDraft>> = {
    thermal: { width_mm: '50', height_mm: '25', margin_mm: '1', gap_mm: '0' },
    hang_tag: { width_mm: '50', height_mm: '80', margin_mm: '3', gap_mm: '0' },
    a4_sheet: {
      width_mm: '63.5',
      height_mm: '33.9',
      columns: '3',
      rows: '8',
      margin_mm: '8',
      gap_mm: '2.5',
    },
    loyalty_card: {
      width_mm: '85.6',
      height_mm: '53.98',
      margin_mm: '4',
      gap_mm: '0',
    },
  };
  return {
    name: '',
    kind,
    width_mm: '',
    height_mm: '',
    columns: '',
    rows: '',
    margin_mm: '0',
    gap_mm: '0',
    is_default: false,
    fields:
      kind === 'loyalty_card'
        ? [
            { field: 'customer_name', size: 11, bold: true },
            { field: 'barcode', height: 12 },
          ]
        : [
            { field: 'name', size: 8 },
            { field: 'price', size: 10, bold: true },
            { field: 'barcode', height: 10 },
          ],
    ...shape[kind],
  };
}

/** How many labels one sheet holds. Null for a roll, which does not come in sheets. */
export function perSheet(t: {
  columns?: number | null;
  rows?: number | null;
}): number | null {
  if (!t.columns || !t.rows) return null;
  return t.columns * t.rows;
}

// ---------------------------------------------------------------------------
// Overriding one product's barcode
// ---------------------------------------------------------------------------

export type BarcodeProblem = 'empty' | 'unchanged' | 'taken_here' | null;

/**
 * What is wrong with a hand-assigned barcode, as far as this screen can tell.
 *
 * `taken_here` is a HINT, not the rule: it only sees the labels currently on
 * screen, and the shop has thousands. `variant_barcode_uq` is what actually
 * makes a code unique and the server's refusal is the authority — this exists
 * so an obvious clash is caught before somebody presses the button, not so the
 * check can be moved off the server.
 *
 * `unchanged` is not an error the server raises: it recognises the no-op and
 * records nothing. Saying so here stops somebody pressing a button that will
 * appear to do nothing.
 */
export function barcodeProblem(
  next: string,
  current: string,
  othersOnScreen: readonly { variant_id: string; barcode: string }[],
  variantID: string,
): BarcodeProblem {
  const code = next.trim();
  if (code === '') return 'empty';
  if (code === current.trim()) return 'unchanged';
  if (
    othersOnScreen.some((l) => l.variant_id !== variantID && l.barcode.trim() === code)
  ) {
    return 'taken_here';
  }
  return null;
}
