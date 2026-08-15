// The POS calls, against the existing contracts.
//
// Nothing here computes anything. Scanning asks the catalogue what a barcode
// is; pushing hands the queue's payloads to the sync engine. Pricing, costing,
// the invoice chain, stock and the journal all belong to the server, reached
// through the same sale service an online sale uses.

import type { Client } from './client';
import type { OfflineSalePayload } from '../offline/queue';

/** What a scan returns. Money is a string and stays one. */
export interface ScannedVariant {
  id: string;
  sku: string;
  attributes: Record<string, string>;
  price: string;
  is_active: boolean;
}

/**
 * Looks a barcode up.
 *
 * No company is named. The server resolves it from the registered device —
 * a till that could name its own company could read another company's
 * catalogue, and both belong to the same tenant so row-level security would
 * not notice.
 */
export function scan(client: Client, barcode: string): Promise<ScannedVariant> {
  return client.send<ScannedVariant>(
    'GET',
    `/api/v1/catalog/scan?barcode=${encodeURIComponent(barcode)}`,
  );
}

export interface PushItemResult {
  seq: number;
  state: 'applied' | 'duplicate' | 'failed' | 'blocked';
  error?: string;
}

export interface PushResult {
  applied: number;
  duplicates: number;
  failed: number;
  blocked: number;
  cursor: number;
  items: PushItemResult[];
}

/**
 * Pushes a batch of offline sales.
 *
 * The device is NOT sent: the server takes it from the token. A terminal that
 * could name its own device would push another till's sales onto another
 * till's ZATCA chain.
 */
export function pushBatch(
  client: Client,
  idempotencyKey: string,
  items: Array<{
    seq: number;
    entity_uuid: string;
    entity_type: string;
    payload: OfflineSalePayload;
  }>,
): Promise<PushResult> {
  return client.send<PushResult>('POST', '/api/v1/sync/push', {
    idempotency_key: idempotencyKey,
    items,
  });
}

export interface SyncHealth {
  pending: number;
  blocked: number;
  failed: number;
  gap_size: number;
}

/** What the SERVER still has outstanding for this terminal.
 *
 * Distinct from the local queue's own count: one is what this till has not
 * sent, the other is what the server received and has not settled. A cashier
 * closing up needs both, because a sale can be absent from one and stuck in
 * the other.
 */
export function syncHealth(client: Client): Promise<SyncHealth> {
  return client.send<SyncHealth>('GET', '/api/v1/sync/health');
}
