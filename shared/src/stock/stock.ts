// The arithmetic and the judgements the stock screens make, away from the JSX.
//
// # Quantities are compared as strings, deliberately
//
// A quantity is a `numeric(18,4)` on the server and it can be fractional. The
// browser must not turn one into a `number` to decide whether a shelf is short:
// 0.1 + 0.2 is not 0.3 in binary floating point, and a count of 2.4 kg against
// a system figure of 2.4 kg would report a variance of a hundred-millionth.
//
// So comparison and subtraction here work on the decimal STRING, in integer
// arithmetic scaled to four places, which is what the column holds.
//
// The result is only ever a hint. Every figure that is posted is computed by
// the server from the stock it actually holds; this is what the counter sees
// before they press the button.

/** How many decimal places the schema keeps a quantity to. */
const SCALE = 4;

/** A quantity as a scaled integer, so two of them can be compared exactly.
 *
 *  Returns null for anything that is not a decimal number, which is what an
 *  in-progress typed value looks like — "", "-", "1." — and is not an error. */
export function scaled(qty: string): bigint | null {
  const s = qty.trim();
  if (!/^-?\d*(\.\d*)?$/.test(s) || s === '' || s === '-' || s === '.') {
    return null;
  }
  const negative = s.startsWith('-');
  const body = negative ? s.slice(1) : s;
  const [whole, fraction = ''] = body.split('.');
  const padded = (fraction + '0'.repeat(SCALE)).slice(0, SCALE);
  const value = BigInt(whole || '0') * BigInt(10 ** SCALE) + BigInt(padded || '0');
  return negative ? -value : value;
}

/** A scaled integer back to the shortest decimal string that means it. */
export function unscaled(value: bigint): string {
  const negative = value < 0n;
  const abs = negative ? -value : value;
  const unit = BigInt(10 ** SCALE);
  const whole = abs / unit;
  const fraction = (abs % unit).toString().padStart(SCALE, '0').replace(/0+$/, '');
  const text = fraction ? `${whole}.${fraction}` : whole.toString();
  return negative ? '-' + text : text;
}

/** What a counted figure differs from the system figure by.
 *
 *  Null when the count has not been entered, or is not a number yet — which is
 *  a blank cell rather than a variance of zero. The distinction matters: an
 *  uncounted line is silence, and the server treats it as silence too rather
 *  than writing the shelf off. */
export function variance(systemQty: string, countedQty: string): string | null {
  const system = scaled(systemQty);
  const counted = scaled(countedQty);
  if (system === null || counted === null) return null;
  return unscaled(counted - system);
}

/** Whether a quantity string means something other than nothing. */
export function isZero(qty: string): boolean {
  const v = scaled(qty);
  return v === null || v === 0n;
}

/** Whether a quantity is below zero. */
export function isNegative(qty: string): boolean {
  const v = scaled(qty);
  return v !== null && v < 0n;
}

/** The tone a variance should be shown in.
 *
 *  A shortfall is the one worth colouring: stock the shop believed it had and
 *  does not. A surplus is unexpected rather than bad, and colouring it the same
 *  red teaches people to ignore red. */
export function varianceTone(delta: string | null): 'flat' | 'short' | 'over' {
  if (delta === null) return 'flat';
  const v = scaled(delta);
  if (v === null || v === 0n) return 'flat';
  return v < 0n ? 'short' : 'over';
}

/** The steps of a transfer, in order, so a screen can show where one is. */
export const TRANSFER_STEPS = [
  'requested',
  'approved',
  'dispatched',
  'received',
] as const;

export type TransferStatus = (typeof TRANSFER_STEPS)[number] | 'cancelled';

/** How far along a transfer is, as a step index — for the progress rail.
 *
 *  A cancelled transfer returns −1 rather than a step: it did not reach one, it
 *  left the sequence, and drawing it at the step it happened to be on when
 *  somebody stopped it would say the opposite of what happened. */
export function stepOf(status: TransferStatus): number {
  if (status === 'cancelled') return -1;
  return TRANSFER_STEPS.indexOf(status);
}

/** Which step a person holding these permissions can perform next.
 *
 *  Null when there is nothing for them to do — either the transfer is finished,
 *  or the next step is somebody else's. Returning null rather than a disabled
 *  button is deliberate: a button that cannot be pressed invites a person to
 *  keep trying it, and B4 puts approval with a manager precisely so that the
 *  person who raised the transfer cannot move it on. */
export function nextStepFor(
  status: TransferStatus,
  may: { transfer: boolean; approve: boolean },
): 'approve' | 'dispatch' | 'receive' | null {
  switch (status) {
    case 'requested':
      return may.approve ? 'approve' : null;
    case 'approved':
      return may.transfer ? 'dispatch' : null;
    case 'dispatched':
      return may.transfer ? 'receive' : null;
    default:
      return null;
  }
}

/** Whether a transfer still has stock nobody has accounted for.
 *
 *  Dispatched-less-received, reported by the server. It is not an error and it
 *  is not resolved by this screen: the units are still the company's, still in
 *  the valuation, and somebody has to decide with a reason attached that they
 *  are gone — which is what a wastage voucher is for. */
export function isShort(shortBy?: string): boolean {
  return shortBy !== undefined && !isZero(shortBy);
}
