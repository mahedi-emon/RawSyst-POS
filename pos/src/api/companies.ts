// Which legal entities the signed-in user can work in.
//
// A terminal resolves its company from its registered device and never asks.
// A browser has no device, so an owner opening the dashboard has to be told
// which businesses are theirs — and a group can hold several, each with its own
// books, VAT registration and ZATCA sequence (F4).

import type { Client } from './client';

export interface Company {
  id: string;
  legal_name: string;
  trade_name?: string;
  country: string;
  /** The currency the books are kept in. Every figure on the dashboard is in
   *  this, and it is why the dashboard never assumes a currency of its own. */
  base_currency: string;
}

export async function listCompanies(client: Client): Promise<Company[]> {
  const body = await client.send<{ data: Company[] }>('GET', '/api/v1/companies');
  return body.data ?? [];
}
