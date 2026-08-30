// Fixed assets (blueprint C7) and investors (C3.2).
//
// # Two registers, one idea
//
// Both hold things that are neither stock nor expenses, and both have
// accounting consequences the screen must not be able to influence. A person
// says what an asset cost and how long it will last; the depreciation is
// arithmetic. A person says what they sold it for; the gain or loss is
// arithmetic. A person says how much capital went in; where it posts is fixed.
//
// Nothing in this module sends a figure the server could have worked out for
// itself, which is the same rule the till follows about the cost of a sale.

import type { Client } from './client';

/** One thing the business owns. */
export interface Asset {
  id: string;
  asset_no: string;
  name: string;
  name_ar?: string;
  category: string;

  store?: string;
  custodian?: string;
  serial_number?: string;
  warranty_until?: string;

  acquired_on: string;
  cost: string;
  residual_value: string;
  useful_life_months: number;
  currency: string;

  /** Summed from the depreciation ledger rather than stored. */
  depreciated: string;
  book_value: string;
  /** What one month costs. Shown because it is the figure a person
   *  sanity-checks the useful life against. */
  monthly_charge: string;

  depreciated_to?: string;
  /** How many months are waiting to be charged. */
  months_due: number;

  status: 'in_use' | 'disposed';
  disposed_on?: string;
  disposal_proceeds?: string;
  disposal_note?: string;
}

/** What a depreciation run did. */
export interface Charged {
  month: string;
  assets_charged: number;
  total: string;
  currency: string;
  /** Assets that had nothing to charge, and why — so a run that charged four
   *  of six is explicable without opening each one. */
  skipped?: string[];
}

/** What a disposal did. */
export interface Disposed {
  asset: string;
  book_value: string;
  proceeds: string;
  /** Proceeds less book value. Positive for a gain, negative for a loss, and
   *  derived rather than typed. */
  result: string;
  currency: string;
}

/** Somebody with money in the business. */
export interface Investor {
  id: string;
  name: string;
  name_ar?: string;
  kind: 'owner' | 'investor';
  email?: string;
  phone?: string;
  note?: string;
  is_active: boolean;

  contributed: string;
  withdrawn: string;
  net: string;
  currency: string;
  /** A share of CAPITAL CONTRIBUTED, not of ownership. The second is a legal
   *  fact that lives in a shareholders' agreement. */
  share_of_capital: string;
}

/** Money in or out. */
export interface InvestorMovement {
  id: string;
  direction: 'contribution' | 'withdrawal';
  amount: string;
  moved_on: string;
  account?: string;
  reference?: string;
  note?: string;
  currency: string;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

// --- assets ---------------------------------------------------------------

export function listAssets(
  client: Client,
  companyId: string,
  includeDisposed = false,
): Promise<{ data: Asset[] }> {
  return client.send<{ data: Asset[] }>(
    'GET',
    scoped(
      '/api/v1/assets' + (includeDisposed ? '?include_disposed=true' : ''),
      companyId,
    ),
  );
}

export function addAsset(
  client: Client,
  companyId: string,
  body: {
    name: string;
    name_ar?: string;
    category: string;
    store_id?: string;
    custodian_id?: string;
    serial_number?: string;
    warranty_until?: string;
    acquired_on: string;
    cost: string;
    residual_value?: string;
    useful_life_months: number;
  },
): Promise<Asset> {
  return client.send<Asset>('POST', scoped('/api/v1/assets', companyId), body);
}

/** Charges one month across every asset that owes it.
 *
 *  One month at a time, never "catch up to today": each month's charge belongs
 *  in that month's profit and loss. */
export function depreciate(
  client: Client,
  companyId: string,
  month: string,
): Promise<Charged> {
  return client.send<Charged>(
    'POST',
    scoped('/api/v1/assets/depreciate', companyId),
    { month },
  );
}

export function disposeAsset(
  client: Client,
  companyId: string,
  id: string,
  body: {
    /** What the business got for it. Omit or send nothing for something
     *  scrapped, which is a disposal like any other and usually a loss. */
    proceeds: string;
    money_account_id?: string;
    disposed_on: string;
    note?: string;
  },
): Promise<Disposed> {
  return client.send<Disposed>(
    'POST',
    scoped(`/api/v1/assets/${id}/dispose`, companyId),
    body,
  );
}

// --- investors ------------------------------------------------------------

export function listInvestors(
  client: Client,
  companyId: string,
): Promise<{ data: Investor[] }> {
  return client.send<{ data: Investor[] }>(
    'GET',
    scoped('/api/v1/investors', companyId),
  );
}

export function addInvestor(
  client: Client,
  companyId: string,
  body: {
    name: string;
    name_ar?: string;
    kind: string;
    email?: string;
    phone?: string;
    note?: string;
    user_id?: string;
  },
): Promise<Investor> {
  return client.send<Investor>(
    'POST',
    scoped('/api/v1/investors', companyId),
    body,
  );
}

export function recordInvestment(
  client: Client,
  companyId: string,
  body: {
    uuid: string;
    investor_id: string;
    direction: 'contribution' | 'withdrawal';
    amount: string;
    moved_on: string;
    money_account_id: string;
    reference?: string;
    note?: string;
  },
): Promise<InvestorMovement> {
  return client.send<InvestorMovement>(
    'POST',
    scoped('/api/v1/investors/movements', companyId),
    body,
  );
}

export function investorStatement(
  client: Client,
  companyId: string,
  investorId: string,
): Promise<{ data: InvestorMovement[] }> {
  return client.send<{ data: InvestorMovement[] }>(
    'GET',
    scoped(`/api/v1/investors/${investorId}/statement`, companyId),
  );
}
