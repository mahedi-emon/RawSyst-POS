// Loyalty, store credit, gift cards and fitting history (blueprint B16).
//
// # A gift card is never sent back with its balance guessed
//
// Every balance here is the server's sum of a ledger, and no screen computes
// one. A till that added up what it thought a card held would disagree with the
// shop's books the first time two people spent one at once.

import type { Client } from './client';

export interface Tier {
  key: string;
  name: string;
  name_ar?: string;
  min_spend: string;
  discount_percent?: string;
}

export interface LoyaltyProgram {
  is_active: boolean;
  spend_per_point: string;
  point_value: string;
  expiry_months?: number;
  tiers: Tier[];
  currency: string;
  /** Whether the company has set a scheme up at all. A company with no scheme
   *  is not a company with a scheme set to zero: points cannot be earned or
   *  spent, and the screen says so rather than showing defaults not in force. */
  exists: boolean;
  owed: string;
  points_outstanding: number;
}

export interface LoyaltyEntry {
  id: string;
  points: number;
  reason: string;
  invoice_no?: string;
  spend?: string;
  expires_on?: string;
  note?: string;
  created_by?: string;
  created_at: string;
}

export interface LoyaltyCard {
  customer_id: string;
  customer: string;
  points: number;
  worth: string;

  tier?: string;
  next_tier?: string;
  to_next_tier?: string;
  discount_percent?: string;

  lifetime_spend: string;
  visits: number;
  last_purchase?: string;
  currency: string;
  /** new, returning, vip, at_risk, wholesale, retail — derived from the
   *  invoices rather than typed by anybody. */
  segment: string;
  expiring_soon?: number;
  entries?: LoyaltyEntry[];
}

export interface WalletEntry {
  id: string;
  amount: string;
  currency: string;
  reason: string;
  customer_id?: string;
  customer?: string;
  gift_card_id?: string;
  gift_card_code?: string;
  invoice_id?: string;
  invoice_no?: string;
  expires_on?: string;
  note?: string;
  created_by?: string;
  created_at: string;
}

export interface Wallet {
  customer_id: string;
  customer?: string;
  balance: string;
  currency: string;
  entries?: WalletEntry[];
}

export interface GiftCard {
  id: string;
  code: string;
  face_value: string;
  balance: string;
  currency: string;
  expires_on?: string;
  expired?: boolean;
  is_void?: boolean;
  void_reason?: string;
  customer_id?: string;
  customer?: string;
  note?: string;
  issued_by?: string;
  issued_at: string;
  entries?: WalletEntry[];
}

export interface Size {
  id: string;
  garment: string;
  size: string;
  measurements?: Record<string, string>;
  note?: string;
  confirmed_on: string;
  recorded_by?: string;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

// --- loyalty --------------------------------------------------------------

export function readProgram(
  client: Client,
  companyId: string,
): Promise<LoyaltyProgram> {
  return client.send('GET', scoped('/api/v1/loyalty/program', companyId));
}

export function saveProgram(
  client: Client,
  companyId: string,
  body: {
    is_active: boolean;
    spend_per_point: string;
    point_value: string;
    expiry_months?: number | null;
    tiers: Tier[];
  },
): Promise<LoyaltyProgram> {
  return client.send('PUT', scoped('/api/v1/loyalty/program', companyId), body);
}

export function listMembers(
  client: Client,
  companyId: string,
): Promise<{ data: LoyaltyCard[] }> {
  return client.send('GET', scoped('/api/v1/loyalty/members', companyId));
}

export function readCard(
  client: Client,
  companyId: string,
  customerId: string,
): Promise<LoyaltyCard> {
  return client.send(
    'GET',
    scoped(`/api/v1/loyalty/members/${customerId}`, companyId),
  );
}

export function adjustPoints(
  client: Client,
  companyId: string,
  customerId: string,
  body: { points: number; note: string },
): Promise<LoyaltyCard> {
  return client.send(
    'POST',
    scoped(`/api/v1/loyalty/members/${customerId}/adjust`, companyId),
    body,
  );
}

export function expirePoints(
  client: Client,
  companyId: string,
): Promise<{ points_expired: number }> {
  return client.send('POST', scoped('/api/v1/loyalty/expire', companyId));
}

// --- store credit and gift cards ------------------------------------------

export function readWallet(
  client: Client,
  companyId: string,
  customerId: string,
): Promise<Wallet> {
  return client.send('GET', scoped(`/api/v1/wallets/${customerId}`, companyId));
}

export function giveCredit(
  client: Client,
  companyId: string,
  customerId: string,
  body: { amount: string; note: string; expires_on?: string },
): Promise<Wallet> {
  return client.send(
    'POST',
    scoped(`/api/v1/wallets/${customerId}/credit`, companyId),
    body,
  );
}

export function listGiftCards(
  client: Client,
  companyId: string,
  includeCancelled = false,
): Promise<{ data: GiftCard[] }> {
  const q = includeCancelled ? '?include_cancelled=true' : '';
  return client.send('GET', scoped('/api/v1/gift-cards' + q, companyId));
}

export function issueGiftCard(
  client: Client,
  companyId: string,
  body: {
    face_value: string;
    code?: string;
    expires_on?: string;
    customer_id?: string;
    note?: string;
    proceeds?: Array<{ role: string; amount: string }>;
  },
): Promise<GiftCard> {
  return client.send('POST', scoped('/api/v1/gift-cards', companyId), body);
}

export function readGiftCard(
  client: Client,
  companyId: string,
  id: string,
): Promise<GiftCard> {
  return client.send('GET', scoped(`/api/v1/gift-cards/${id}`, companyId));
}

/** What a till calls when a cashier types a card number. */
export function lookUpGiftCard(
  client: Client,
  companyId: string,
  code: string,
): Promise<GiftCard> {
  return client.send(
    'GET',
    scoped(`/api/v1/gift-cards/by-code/${encodeURIComponent(code)}`, companyId),
  );
}

export function voidGiftCard(
  client: Client,
  companyId: string,
  id: string,
  reason: string,
): Promise<GiftCard> {
  return client.send(
    'POST',
    scoped(`/api/v1/gift-cards/${id}/void`, companyId),
    { reason },
  );
}

export function expireStoreCredit(
  client: Client,
  companyId: string,
): Promise<{ cards: number; wallets: number; total: string }> {
  return client.send('POST', scoped('/api/v1/store-credit/expire', companyId));
}

// --- fitting history ------------------------------------------------------

export function listSizes(
  client: Client,
  companyId: string,
  customerId: string,
): Promise<{ data: Size[] }> {
  return client.send(
    'GET',
    scoped(`/api/v1/customers/${customerId}/sizes`, companyId),
  );
}

export function recordSize(
  client: Client,
  companyId: string,
  customerId: string,
  body: {
    garment: string;
    size: string;
    measurements?: Record<string, string>;
    note?: string;
  },
): Promise<{ data: Size[] }> {
  return client.send(
    'PUT',
    scoped(`/api/v1/customers/${customerId}/sizes`, companyId),
    body,
  );
}

export function forgetSize(
  client: Client,
  companyId: string,
  customerId: string,
  sizeId: string,
): Promise<{ data: Size[] }> {
  return client.send(
    'DELETE',
    scoped(`/api/v1/customers/${customerId}/sizes/${sizeId}`, companyId),
  );
}
