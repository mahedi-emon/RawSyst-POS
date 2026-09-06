import { describe, expect, it } from 'vitest';

import {
  blockedBy,
  collectsCash,
  needsDriver,
  nextStates,
  type DeliveryStatus,
} from './delivery';

/**
 * These assert the screen against the constraints on `delivery`, not against
 * itself.
 *
 * Each rule below is a CHECK the database already enforces. The value of
 * having them here is that the screen asks for what the rule needs BEFORE
 * sending, so somebody gets "why it could not be delivered" as a required box
 * rather than a constraint violation naming an index. If a rule changes on the
 * server, one of these should fail rather than a dispatcher discovering it.
 */

const ALL: DeliveryStatus[] = [
  'pending',
  'assigned',
  'picked_up',
  'out_for_delivery',
  'delivered',
  'failed',
  'returned',
];

describe('the ladder a delivery climbs', () => {
  it('offers only states the column allows', () => {
    for (const from of ALL) {
      for (const to of nextStates(from)) {
        expect(ALL, `${from} offered ${to}`).toContain(to);
      }
    }
  });

  it('never offers the state it is already in', () => {
    for (const from of ALL) {
      expect(nextStates(from), `${from} offered itself`).not.toContain(from);
    }
  });

  it('ends at delivered and returned', () => {
    // Not "has no button". There is genuinely nothing after them: a delivery
    // that arrived cannot be un-arrived, and a parcel back on the shelf is
    // finished. Either would be a new delivery.
    expect(nextStates('delivered')).toEqual([]);
    expect(nextStates('returned')).toEqual([]);
  });

  it('does not end at failed', () => {
    // A failed attempt is normally retried. Treating it as an end would strand
    // every parcel nobody was in for.
    expect(nextStates('failed').length).toBeGreaterThan(0);
    expect(nextStates('failed')).toContain('assigned');
    expect(nextStates('failed')).toContain('returned');
  });

  it('offers a way out of every state that is not an end', () => {
    for (const from of ALL) {
      if (from === 'delivered' || from === 'returned') continue;
      expect(nextStates(from).length, `${from} is a dead end`).toBeGreaterThan(0);
    }
  });

  it('reaches delivered from pending, one rung at a time', () => {
    // The whole happy path, walked rather than asserted as a list -- so a rung
    // removed from the middle fails here instead of quietly shortening it.
    let at: string = 'pending';
    const walked = [at];
    for (let i = 0; i < 10 && at !== 'delivered'; i++) {
      const forward = nextStates(at).find((s) => s !== 'failed' && s !== 'returned');
      if (!forward) break;
      at = forward;
      walked.push(at);
    }
    expect(walked).toEqual([
      'pending',
      'assigned',
      'picked_up',
      'out_for_delivery',
      'delivered',
    ]);
  });

  it('says nothing about a status it does not know', () => {
    expect(nextStates('teleported')).toEqual([]);
    expect(nextStates('')).toEqual([]);
  });
});

describe('a delivery past pending is in somebody hands', () => {
  it('needs a driver everywhere the constraint needs one', () => {
    // delivery_assigned_has_a_driver exempts pending and returned and nothing
    // else.
    for (const s of ALL) {
      const exempt = s === 'pending' || s === 'returned';
      expect(needsDriver(s), `${s}`).toBe(!exempt);
    }
  });

  it('asks for nothing before a state is chosen', () => {
    expect(needsDriver('')).toBe(false);
  });
});

describe('cash on delivery is collected on arrival', () => {
  it('is asked only when the delivery carries an amount', () => {
    expect(collectsCash(false, 'delivered')).toBe(false);
  });

  it('is asked only at the point it arrives', () => {
    for (const s of ALL) {
      expect(collectsCash(true, s), `${s}`).toBe(s === 'delivered');
    }
  });
});

describe('what stops a move being saved', () => {
  const ok = { target: 'assigned', driverId: 'someone', note: '' };

  it('is nothing when the form is complete', () => {
    expect(blockedBy(ok)).toBeNull();
  });

  it('is the status when none is chosen', () => {
    expect(blockedBy({ ...ok, target: '' })).toBe('status');
    expect(blockedBy({ ...ok, target: '   ' })).toBe('status');
  });

  it('is the driver when one is needed and absent', () => {
    expect(blockedBy({ ...ok, driverId: '' })).toBe('driver_id');
    // Whitespace is not a driver. The server would take it and the constraint
    // would not.
    expect(blockedBy({ ...ok, driverId: '  ' })).toBe('driver_id');
  });

  it('is not the driver where the constraint exempts the state', () => {
    expect(blockedBy({ target: 'returned', driverId: '', note: '' })).toBeNull();
  });

  it('is the note on a failure, which cannot be chased without one', () => {
    // delivery_failure_says_why. A failed delivery with no reason is a parcel
    // nobody can follow up.
    expect(blockedBy({ target: 'failed', driverId: 'someone', note: '' })).toBe('note');
    expect(blockedBy({ target: 'failed', driverId: 'someone', note: ' ' })).toBe('note');
    expect(
      blockedBy({ target: 'failed', driverId: 'someone', note: 'nobody in' }),
    ).toBeNull();
  });

  it('does not demand a note on anything but a failure', () => {
    for (const s of ALL) {
      if (s === 'failed') continue;
      const why = blockedBy({ target: s, driverId: 'someone', note: '' });
      expect(why, `${s} demanded a note`).not.toBe('note');
    }
  });

  it('reports the driver before the note, which is the order they are fixed', () => {
    // Both missing on a failed move. Sending somebody to the note first would
    // have them write a reason and then be stopped again by the driver.
    expect(blockedBy({ target: 'failed', driverId: '', note: '' })).toBe('driver_id');
  });
});
