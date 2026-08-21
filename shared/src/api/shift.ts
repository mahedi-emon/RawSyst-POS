// The cash drawer, blueprint C8.
//
// The mirror of api/devices.ts: nothing here decides anything. The server owns
// the session number, the one-open-session rule, the expected figure and the
// variance, and this module states what happened and reads the answer.
//
// # The expected figure may simply be absent
//
// On a blind-close till the server omits `expected_cash` from a cashier's view
// until the count is committed. That is blueprint B7 and it is enforced there,
// not here — a cashier who could see the target can make the drawer agree with
// it, and then the variance reads zero on every shift. The field is optional in
// this type for that reason, and a screen that defaulted it to '0' would undo
// the whole control.
//
// # Nothing here is queued offline
//
// Every other POS write survives a dead network by going into the local queue.
// These do not, and must not: the session number comes from `claim_session_no`
// and a partial unique index is what makes two open sessions on one till
// impossible. A queued open would be a second session claimed against a stale
// view of the drawer, which is precisely the state the index exists to refuse.

import type { Client } from './client';

/** A till's shift. */
export interface ShiftSession {
  id: string;
  /** Sequential per device, so a cashier can say "till 2, session 47" rather
   *  than quoting a UUID. */
  session_no: number;
  device_id: string;
  store_id: string;
  state: 'open' | 'closed';

  opened_at: string;
  /** A decimal STRING, as every money value is. */
  opening_float: string;
  blind_close: boolean;
}

/** The X or Z reckoning of a session. One shape serves both, because a
 *  supervisor comparing an X against the later Z needs the same arithmetic. */
export interface ShiftReport {
  session_no: number;
  state: 'open' | 'closed';
  opened_at: string;
  closed_at?: string;

  opening_float: string;
  invoice_count: number;

  gross_sales: string;
  net_sales: string;
  tax_total: string;
  refund_total: string;

  cash_takings: string;
  non_cash_takings: string;
  cash_movements: string;

  /** Withheld on a blind close until the count is committed. See above. */
  expected_cash?: string;
  counted_cash?: string;
  variance?: string;
}

/** The reasons migration 0024 allows. A movement outside this set is refused by
 *  a check constraint, so the list is not a suggestion. */
export type CashMovementReason =
  | 'float_in'
  | 'safe_drop'
  | 'petty_cash'
  | 'supplier_paid'
  | 'correction';

export function openShift(
  client: Client,
  body: { opening_float: string; blind_close: boolean },
): Promise<ShiftSession> {
  return client.send<ShiftSession>('POST', '/api/v1/shifts', body);
}

/**
 * The till's own open session, or null when there is none.
 *
 * Takes no id: the terminal comes from the token, so this cannot be aimed at
 * another till's drawer even by a caller who knows its id. A 404 is the
 * ordinary answer at the start of a day and is reported as `null` rather than
 * thrown, because "no session yet" is a state the screen renders, not a
 * failure it apologises for.
 */
export async function currentShift(client: Client): Promise<ShiftSession | null> {
  try {
    return await client.send<ShiftSession>('GET', '/api/v1/shifts/current');
  } catch (err) {
    if (isNotFound(err)) return null;
    throw err;
  }
}

/** What the CASHIER sees. Withholds the expected figure on a blind till. */
export function peekShift(client: Client, sessionId: string): Promise<ShiftReport> {
  return client.send<ShiftReport>('GET', `/api/v1/shifts/${sessionId}`);
}

/** The supervisor's mid-shift snapshot. Closes nothing, and is the only call
 *  that reveals the expected drawer before a count is committed — which is why
 *  it is gated on report.view and a cashier is refused it. */
export function shiftXReport(client: Client, sessionId: string): Promise<ShiftReport> {
  return client.send<ShiftReport>('GET', `/api/v1/shifts/${sessionId}/x-report`);
}

/** Cash in or out of the drawer other than a sale. Signed: negative takes
 *  money out to the safe, positive puts a float back in. */
export function recordCashMovement(
  client: Client,
  sessionId: string,
  body: { amount: string; reason: CashMovementReason; note: string },
): Promise<void> {
  return client.send<void>('POST', `/api/v1/shifts/${sessionId}/cash-drop`, body);
}

/** The Z report. Closes the session, and may happen exactly once. */
export function closeShift(
  client: Client,
  sessionId: string,
  body: { counted_cash: string; note: string },
): Promise<ShiftReport> {
  return client.send<ShiftReport>('POST', `/api/v1/shifts/${sessionId}/close`, body);
}

function isNotFound(err: unknown): boolean {
  return (
    typeof err === 'object' &&
    err !== null &&
    'status' in err &&
    (err as { status: number }).status === 404
  );
}
