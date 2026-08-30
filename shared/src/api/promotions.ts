// Promotions and the pricing engine (blueprint B9).
//
// # Quoting is not redeeming
//
// `quotePromotions` is read-only and is called every time a cart changes. What
// a campaign has actually cost is recorded when a sale is finalised, inside its
// transaction — a route that recorded a redemption on every quote would report
// a campaign as used forty times for one sale.

import type { Client } from './client';

/** What a promotion does. Four mechanisms; the eleven kinds B9 lists are these
 *  crossed with what they apply to and when. */
export type PromotionKind =
  | 'percentage'
  | 'amount'
  | 'buy_x_get_y'
  | 'bundle_price';

export interface Promotion {
  id: string;
  code: string;
  name: string;
  name_ar?: string;
  kind: PromotionKind;

  value?: string;
  buy_qty?: string;
  get_qty?: string;

  category_id?: string;
  brand_id?: string;
  variant_id?: string;
  customer_type?: string;
  /** What the scope amounts to, in words, computed by the server so two front
   *  ends cannot write two versions of the same sentence. */
  applies_to: string;

  starts_on?: string;
  ends_on?: string;
  store_id?: string;
  min_purchase?: string;

  coupon_code?: string;
  max_uses?: number;
  max_uses_per_customer?: number;

  is_active: boolean;
  priority: number;

  /** What the campaign has cost and brought in so far. */
  times_used: number;
  discount_given: string;
  sales_generated: string;
  currency: string;
}

/** What a promotion did to one line. */
export interface PricedLine {
  variant_id: string;
  promotion_id?: string;
  promotion?: string;
  /** What came off the whole line, not off one unit. */
  discount: string;
  line_total: string;
  /** The promotion was worth more than the floor price allowed and was
   *  clamped. B1's floor is enforced by the system, not by policy. */
  floor_applied?: boolean;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

export function listPromotions(
  client: Client,
  companyId: string,
  includeFinished = false,
): Promise<{ data: Promotion[] }> {
  return client.send<{ data: Promotion[] }>(
    'GET',
    scoped(
      '/api/v1/promotions' + (includeFinished ? '?include_finished=true' : ''),
      companyId,
    ),
  );
}

export function createPromotion(
  client: Client,
  companyId: string,
  body: Record<string, unknown>,
): Promise<Promotion> {
  return client.send<Promotion>(
    'POST',
    scoped('/api/v1/promotions', companyId),
    body,
  );
}

export function setPromotionActive(
  client: Client,
  companyId: string,
  id: string,
  isActive: boolean,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/promotions/${id}/active`, companyId),
    { is_active: isActive },
  );
}

/** Prices a cart. Read-only, and called as often as the cart changes. */
export function quotePromotions(
  client: Client,
  companyId: string,
  basket: {
    store_id?: string;
    customer_id?: string;
    customer_type?: string;
    coupon_code?: string;
    on?: string;
    lines: Array<{
      variant_id: string;
      qty: string;
      unit_price: string;
      floor_price?: string;
      category_id?: string;
      brand_id?: string;
    }>;
  },
): Promise<{ data: PricedLine[] }> {
  return client.send<{ data: PricedLine[] }>(
    'POST',
    scoped('/api/v1/promotions/quote', companyId),
    basket,
  );
}
