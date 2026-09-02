// Card providers (blueprint E3.3) and the machine on the counter (E3.4).
//
// # The form is drawn from the server's answer, not from a list in here
//
// `listPaymentProviders` returns every acquirer with the fields it needs, and
// the settings screen renders a box per field. That is the whole reason a shop
// can sign with any of them, paste its own credentials and start taking cards
// without a deployment — and it is why adding an acquirer is an adapter and a
// table entry rather than a change in three places.
//
// # No call here returns a stored key
//
// There is no route that would, and `Gateway` has no field for one. A screen
// learns that a key is held (`has_secret`) and can replace it; it cannot read
// it. An edit therefore leaves the secret box EMPTY rather than showing dots,
// and an empty box on save means "leave what is stored".

import type { Client } from './client';

/** One thing a provider needs, for the screen to draw a box for. */
export interface ProviderField {
  key: string;
  label: string;
  /** The half that is sealed and never read back. */
  secret: boolean;
  /** Where a shop finds this value in the acquirer's own dashboard. */
  hint?: string;
}

export interface PaymentProvider {
  key: string;
  name: string;
  fields: ProviderField[];
  methods: string[];
  docs?: string;
}

export interface Gateway {
  id: string;
  provider: string;
  label: string;
  mode: 'test' | 'live';
  settings: Record<string, string>;
  methods: string[];
  is_active: boolean;
  /** True when a key is held, without saying what it is. */
  has_secret: boolean;
  last_checked_at?: string;
  last_check_ok?: boolean;
  last_check_note?: string;
}

export interface GatewayInput {
  provider: string;
  label: string;
  mode: 'test' | 'live';
  settings: Record<string, string>;
  /** Empty means "leave the stored key alone". */
  secret: string;
  methods: string[];
  is_active: boolean;
}

export interface PaymentAttempt {
  id: string;
  method: string;
  amount: string;
  currency: string;
  status:
    | 'initiated'
    | 'authorised'
    | 'captured'
    | 'failed'
    | 'cancelled'
    | 'refunded';
  provider_ref?: string;
  provider_code?: string;
  provider_message?: string;
  redirect_url?: string;
  created_at: string;
  settled_at?: string;
}

export function listPaymentProviders(
  client: Client,
): Promise<{ providers: PaymentProvider[] }> {
  return client.send('GET', '/api/v1/payment-providers');
}

export function listGateways(
  client: Client,
  companyID: string,
): Promise<{ gateways: Gateway[] }> {
  return client.send(
    'GET',
    `/api/v1/payment-gateways?company_id=${encodeURIComponent(companyID)}`,
  );
}

export function saveGateway(
  client: Client,
  companyID: string,
  input: GatewayInput,
  gatewayID?: string,
): Promise<{ gateway: Gateway }> {
  const query = `?company_id=${encodeURIComponent(companyID)}`;
  if (gatewayID) {
    return client.send(
      'PUT',
      `/api/v1/payment-gateways/${gatewayID}${query}`,
      input,
    );
  }
  return client.send('POST', `/api/v1/payment-gateways${query}`, input);
}

export function removeGateway(
  client: Client,
  companyID: string,
  gatewayID: string,
): Promise<void> {
  return client.send(
    'DELETE',
    `/api/v1/payment-gateways/${gatewayID}?company_id=${encodeURIComponent(
      companyID,
    )}`,
  );
}

/** The Test button. Talks to the acquirer and never moves money. */
export function checkGateway(
  client: Client,
  companyID: string,
  gatewayID: string,
): Promise<{ gateway: Gateway }> {
  return client.send(
    'POST',
    `/api/v1/payment-gateways/${gatewayID}/check?company_id=${encodeURIComponent(
      companyID,
    )}`,
  );
}

export function listPaymentAttempts(
  client: Client,
  companyID: string,
): Promise<{ attempts: PaymentAttempt[] }> {
  return client.send(
    'GET',
    `/api/v1/payment-attempts?company_id=${encodeURIComponent(companyID)}`,
  );
}

export function refundAttempt(
  client: Client,
  companyID: string,
  attemptID: string,
  amount: string,
): Promise<{ attempt: PaymentAttempt }> {
  return client.send(
    'POST',
    `/api/v1/payment-attempts/${attemptID}/refund?company_id=${encodeURIComponent(
      companyID,
    )}`,
    { amount },
  );
}
