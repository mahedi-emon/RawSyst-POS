// The catalogue, and the variant grid a shop reads it through.
//
// Read-only here. Creating products and generating grids are their own screens
// with their own permissions (`catalog.create`); this module serves UI spec §4,
// which looks at what exists.

import type { Client } from './client';

export interface Product {
  id: string;
  sku: string;
  name: string;
  tax_treatment: string;
  lifecycle: string;
  variant_count: number;
}

/** One square of the grid: the variant, plus the three facts that decide how
 *  it is drawn. All three come from the server because two of them need the
 *  whole company — quantity is summed across warehouses, and the last sale is
 *  the last one anywhere. */
export interface MatrixCell {
  id: string;
  sku: string;
  /** Unlimited custom attributes per blueprint B2, so a fixed size/colour pair
   *  would not do: `{"size":"L","colour":"Black","season":"Winter"}`. */
  attributes: Record<string, string>;
  price: string;
  is_active: boolean;

  /** Summed across the company's warehouses, as a decimal string. */
  on_hand: string;
  /** Absent when nobody has set one, which is not the same as zero: a variant
   *  with no reorder level is never "low", only in stock or out. */
  reorder_level?: string;
  /** Absent when it has never sold. */
  last_sold_at?: string;
}

export async function listProducts(
  client: Client,
  companyId: string,
  opts: { search?: string; limit?: number } = {},
): Promise<Product[]> {
  const params = new URLSearchParams({ company_id: companyId });
  if (opts.search) params.set('search', opts.search);
  params.set('limit', String(opts.limit ?? 50));

  const body = await client.send<{ data: Product[] }>(
    'GET',
    `/api/v1/catalog/products?${params.toString()}`,
  );
  return body.data ?? [];
}

/** The grid for one product. Empty for a product with no variants yet, which
 *  is a state the screen renders rather than a failure. */
export async function fetchMatrix(
  client: Client,
  productId: string,
): Promise<MatrixCell[]> {
  const body = await client.send<{ data: MatrixCell[] }>(
    'GET',
    `/api/v1/catalog/products/${productId}/matrix`,
  );
  return body.data ?? [];
}
