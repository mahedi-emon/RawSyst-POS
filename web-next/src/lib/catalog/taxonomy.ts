// Categories, brands and units — what a product hangs off.
//
// All three were tables with no route and no screen while
// `POST /catalog/products` accepted their ids, so the only way to file a
// product under a department was to already know a UUID.

/** One department in the product tree. */
export interface Category {
  id: string;
  name: string;
  name_ar?: string;
  parent_id?: string;
  /** How many ancestors it has. The list arrives parents-first, so a screen
   *  can indent on this without walking the tree itself. */
  depth: number;
  sort_order: number;
  is_active: boolean;
  /** How many products are filed under it — the answer to "may I retire
   *  this", asked at the moment somebody wants to. */
  product_count: number;
}

/** A maker. */
export interface Brand {
  id: string;
  name: string;
  name_ar?: string;
  is_active: boolean;
  product_count: number;
}

/** How a product is counted or measured. */
export interface Unit {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  /** Whether half of one may be sold. A metre of cloth may be cut; a shirt
   *  may not. */
  allows_fraction: boolean;
  is_active: boolean;
  product_count: number;
}

/** One tax treatment a product may carry in this company's country. */
export interface Treatment {
  code: string;
  /** True when choosing it obliges the product to carry an exemption reason
   *  code. */
  needs_reason: boolean;
}

/** What `GET /catalog/tax-treatments` answers. */
export interface TreatmentOptions {
  country: string;
  /** "vat" or "sales_tax". The two are not interchangeable, and words written
   *  for one are wrong in the other. */
  model: string;
  treatments: Treatment[];
}

/**
 * A category's name, prefixed so its depth is visible in a flat `<select>`.
 *
 * A native select cannot nest, and indenting with spaces is ignored by most
 * browsers. A leading rule character survives, and reads correctly in Arabic
 * because it carries no direction of its own.
 */
export function indented(category: Category): string {
  return category.depth === 0
    ? category.name
    : `${'— '.repeat(category.depth)}${category.name}`;
}
