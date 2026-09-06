// Getting an order to the customer: the ladder, and the three rules the
// database enforces.
//
// Extracted from the screen because these are business rules rather than
// layout. Each one mirrors a CHECK constraint on `delivery`, and a screen that
// gets them wrong does not misdraw — it collects a form and hands the person a
// constraint violation naming an index. They are here so they can be asserted
// without rendering anything.
//
// # The ladder is one rung at a time
//
// pending → assigned → picked_up → out_for_delivery → delivered, with failed
// and returned as the two ways out. A dispatcher does not choose freely from
// seven states; they move a delivery forward. Offering all seven would let
// somebody mark an unassigned parcel delivered, which the constraints then
// refuse for a reason that reads as a system fault.
//
// # delivered and returned are ends
//
// Nothing follows them. A delivery that arrived cannot be un-arrived, and a
// parcel back on the shelf is finished; either would need a new delivery.
// `failed` is NOT an end — a failed attempt is normally retried, so it leads
// back to `assigned`, or on to `returned` when the shop gives up.

/** The seven states `delivery_status_valid` allows. */
export type DeliveryStatus =
  | 'pending'
  | 'assigned'
  | 'picked_up'
  | 'out_for_delivery'
  | 'delivered'
  | 'failed'
  | 'returned';

/**
 * The rungs offered from a given state.
 *
 * Empty for the two ends, which is what the screen renders as "—" rather than
 * as a disabled control: there is no next step to disable.
 */
export function nextStates(status: string): readonly DeliveryStatus[] {
  switch (status) {
    case 'pending':
      return ['assigned'];
    case 'assigned':
      return ['picked_up', 'failed'];
    case 'picked_up':
      return ['out_for_delivery', 'failed'];
    case 'out_for_delivery':
      return ['delivered', 'failed'];
    case 'failed':
      // Retried, or given up on. Both are ordinary.
      return ['returned', 'assigned'];
    default:
      return [];
  }
}

/**
 * Whether moving to this state needs a driver named.
 *
 * `delivery_assigned_has_a_driver` exempts exactly two states: pending, which
 * is before anybody has it, and returned, which is after. Everything between
 * is in somebody's hands and the constraint says so.
 */
export function needsDriver(target: string): boolean {
  return target !== '' && target !== 'pending' && target !== 'returned';
}

/**
 * Whether cash is collected at this step.
 *
 * Only on arrival, and only for a delivery that carries an amount. Asking
 * earlier collects an answer about money that has not moved; asking on a
 * delivery with no COD asks about nothing.
 */
export function collectsCash(isCOD: boolean, target: string): boolean {
  return isCOD && target === 'delivered';
}

/** What the form is missing, in the order somebody would fix it. */
export interface AdvanceDraft {
  target: string;
  driverId: string;
  note: string;
}

/**
 * The reason this move cannot be saved yet, or null.
 *
 * Returns a field name rather than a sentence: the caller owns the wording, and
 * the catalogue owns the language. `delivery_failure_says_why` is the rule
 * behind the note — a failed delivery with no reason cannot be chased, and the
 * database refuses it.
 */
export function blockedBy(draft: AdvanceDraft): 'status' | 'driver_id' | 'note' | null {
  if (draft.target.trim() === '') return 'status';
  if (needsDriver(draft.target) && draft.driverId.trim() === '') return 'driver_id';
  if (draft.target === 'failed' && draft.note.trim() === '') return 'note';
  return null;
}
