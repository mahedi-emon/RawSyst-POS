// The barcode engine and label studio (blueprint B3), global search (D7) and
// analytics (D2).
//
// # A label carries its price already worked out
//
// `Label.price` is what goes on the tag: the shelf price, VAT included, as the
// server holds it. The screen does not multiply anything — a tag is the one
// place in this product where a wrong number is physically attached to a
// garment, and arithmetic in two places is how two answers happen.

import type { Client } from './client';

export type Symbology =
  | 'code128'
  | 'ean13'
  | 'ean8'
  | 'upca'
  | 'qr'
  | 'datamatrix';

export type LabelKind = 'hang_tag' | 'thermal' | 'a4_sheet' | 'loyalty_card';

export interface BarcodeScheme {
  parts: string[];
  separator: string;
  symbology: Symbology;
  part_length: number;
  prefix?: string;
  /** The scheme applied to made-up values, so somebody changing it can see
   *  what they will get before generating a thousand of them. */
  example: string;
}

export interface LabelField {
  field: string;
  size?: number;
  height?: number;
  bold?: boolean;
  rtl?: boolean;
}

export interface LabelTemplate {
  id: string;
  name: string;
  kind: LabelKind;
  width_mm: string;
  height_mm: string;
  columns?: number;
  rows?: number;
  margin_mm: string;
  gap_mm: string;
  fields: LabelField[];
  is_default: boolean;
  /** Columns × rows — how a shopkeeper actually buys the paper. */
  per_sheet?: number;
}

export interface Label {
  variant_id: string;
  sku: string;
  barcode: string;
  symbology: string;
  /** The meaningful string. For a digit symbology this is not the barcode and
   *  is printed beside it. */
  readable?: string;
  name: string;
  name_ar?: string;
  attributes?: string;
  category?: string;
  brand?: string;
  season?: string;
  price: string;
  currency: string;
  tax_rate: string;
}

export interface LabelSheet {
  template: LabelTemplate;
  labels: Label[];
}

export interface Assignment {
  variant_id: string;
  sku: string;
  barcode: string;
  readable: string;
}

export interface SearchHit {
  kind: string;
  id: string;
  label: string;
  detail?: string;
  amount?: string;
  currency?: string;
}

export interface Movement {
  variant_id: string;
  sku: string;
  product: string;
  category?: string;
  brand?: string;
  sold_qty: string;
  revenue: string;
  profit: string;
  on_hand: string;
  velocity: string;
  days_cover?: number;
  reorder_on?: string;
  /** −1 when it has never sold at all, which is worse than old and must not
   *  sort as recent. */
  days_since_sold: number;
  currency: string;
}

export interface Forecast {
  variant_id: string;
  sku: string;
  product: string;
  window_days: number;
  sold_in_window: string;
  velocity: string;
  forecast_days: number;
  expected_demand: string;
  on_hand: string;
  shortfall: string;
  /** What the number is based on, said out loud so nobody reads it as a model
   *  that considered a season or a promotion. */
  basis: string;
}

export interface Ranked {
  id: string;
  label: string;
  revenue: string;
  cost: string;
  profit: string;
  margin_pct: string;
  units: string;
  currency: string;
}

export interface KPIs {
  from: string;
  to: string;
  revenue: string;
  gross_profit: string;
  /** Empty rather than "0.0" when there is nothing to divide by. A shop with
   *  no sales has no margin, and 0.0% reads as "we made nothing on everything
   *  we sold". */
  gross_margin_pct: string;
  orders: number;
  average_order_value: string;
  units_per_transaction: string;
  discount_ratio_pct: string;
  return_rate_pct: string;
  inventory_turnover: string;
  repeat_customer_pct: string;
  customer_lifetime_value: string;
  sales_per_store: string;
  sales_per_employee: string;
  currency: string;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

const query = (q: Record<string, string | number | undefined>) => {
  const parts = Object.entries(q)
    .filter(([, v]) => v !== undefined && v !== '')
    .map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`);
  return parts.length ? '?' + parts.join('&') : '';
};

// --- the barcode scheme ---------------------------------------------------

export function readScheme(
  client: Client,
  companyId: string,
): Promise<BarcodeScheme> {
  return client.send('GET', scoped('/api/v1/labels/scheme', companyId));
}

export function saveScheme(
  client: Client,
  companyId: string,
  body: {
    parts: string[];
    separator: string;
    symbology: Symbology;
    part_length: number;
    prefix?: string;
  },
): Promise<BarcodeScheme> {
  return client.send('PUT', scoped('/api/v1/labels/scheme', companyId), body);
}

/** B3's bulk generator. A variant that already has a code keeps it unless
 *  `overwrite` deliberately says otherwise. */
export function generateBarcodes(
  client: Client,
  companyId: string,
  body: { variant_ids?: string[]; overwrite?: boolean },
): Promise<{ count: number; assigned: Assignment[] }> {
  return client.send('POST', scoped('/api/v1/labels/barcodes', companyId), body);
}

export function setBarcode(
  client: Client,
  companyId: string,
  variantId: string,
  barcode: string,
): Promise<void> {
  return client.send(
    'PUT',
    scoped(`/api/v1/labels/barcodes/${variantId}`, companyId),
    { barcode },
  );
}

// --- templates and printing -----------------------------------------------

export function listTemplates(
  client: Client,
  companyId: string,
): Promise<{ data: LabelTemplate[] }> {
  return client.send('GET', scoped('/api/v1/labels/templates', companyId));
}

export function saveTemplate(
  client: Client,
  companyId: string,
  id: string | null,
  body: {
    name: string;
    kind: LabelKind;
    width_mm: string;
    height_mm: string;
    columns?: number | null;
    rows?: number | null;
    margin_mm?: string;
    gap_mm?: string;
    fields: LabelField[];
    is_default?: boolean;
  },
): Promise<LabelTemplate> {
  return id
    ? client.send('PUT', scoped(`/api/v1/labels/templates/${id}`, companyId), body)
    : client.send('POST', scoped('/api/v1/labels/templates', companyId), body);
}

export function deleteTemplate(
  client: Client,
  companyId: string,
  id: string,
): Promise<void> {
  return client.send(
    'DELETE',
    scoped(`/api/v1/labels/templates/${id}`, companyId),
  );
}

/** A POST although it writes nothing: a selection can name several hundred
 *  variants, and a query string that long is one a proxy will truncate. */
export function buildLabels(
  client: Client,
  companyId: string,
  body: {
    template_id?: string;
    kind?: LabelKind;
    variant_ids?: string[];
    category_id?: string;
    brand_id?: string;
    search?: string;
    copies?: number;
  },
): Promise<LabelSheet> {
  return client.send('POST', scoped('/api/v1/labels/print', companyId), body);
}

// --- search and analytics -------------------------------------------------

export function search(
  client: Client,
  companyId: string,
  term: string,
): Promise<{ data: SearchHit[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/search' + query({ q: term }), companyId),
  );
}

export function kpis(
  client: Client,
  companyId: string,
  q: { from?: string; to?: string } = {},
): Promise<KPIs> {
  return client.send(
    'GET',
    scoped('/api/v1/analytics/kpis' + query(q), companyId),
  );
}

export function movers(
  client: Client,
  companyId: string,
  days = 90,
): Promise<{ data: Movement[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/analytics/movers' + query({ days }), companyId),
  );
}

export function forecast(
  client: Client,
  companyId: string,
  q: { window_days?: number; forecast_days?: number } = {},
): Promise<{ data: Forecast[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/analytics/forecast' + query(q), companyId),
  );
}

export function profitability(
  client: Client,
  companyId: string,
  q: { by?: string; from?: string; to?: string } = {},
): Promise<{ data: Ranked[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/analytics/profitability' + query(q), companyId),
  );
}
