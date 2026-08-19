import { describe, expect, it } from 'vitest';

import type { Terminal } from '../api/devices';
import {
  codeGroups,
  countdown,
  describeState,
  offered,
  secondsLeft,
  terminalState,
} from './devices';

const till = (over: Partial<Terminal> = {}): Terminal => ({
  id: 'd1',
  store_id: 's1',
  store: 'Main Branch',
  terminal_label: 'Till 2',
  status: 'pending',
  pending_code: false,
  ...over,
});

describe('what state a terminal is really in', () => {
  it('separates a terminal waiting for a code from one waiting to be paired', () => {
    // The server reports both as pending. They need different actions from a
    // reader, so the screen must not.
    expect(terminalState(till())).toBe('waiting_for_code');
    expect(terminalState(till({ pending_code: true }))).toBe('waiting_to_be_paired');
  });

  it('reads the settled states straight through', () => {
    expect(terminalState(till({ status: 'active' }))).toBe('active');
    expect(terminalState(till({ status: 'inactive' }))).toBe('paused');
    expect(terminalState(till({ status: 'revoked' }))).toBe('revoked');
  });

  it('says what to do next for every state that needs something done', () => {
    for (const t of [
      till(),
      till({ pending_code: true }),
      till({ status: 'inactive', enrolled_at: 'x' }),
      till({ status: 'revoked' }),
    ]) {
      expect(describeState(t).next, JSON.stringify(t.status)).toBeTruthy();
    }
    // A working till needs nothing said about it.
    expect(describeState(till({ status: 'active' })).next).toBeUndefined();
  });

  it('does not call a revoked terminal an error', () => {
    // It is a decision somebody made, not a fault. Marked clearly, tone danger,
    // and the sentence explains it cannot be undone.
    const d = describeState(till({ status: 'revoked' }));
    expect(d.tone).toBe('danger');
    expect(d.next).toMatch(/cannot be undone/i);
  });
});

describe('which controls to offer', () => {
  it('offers nothing without the permission', () => {
    expect(offered(till({ status: 'active', enrolled_at: 'x' }), false)).toEqual({
      code: false, edit: false, pause: false, resume: false, revoke: false,
    });
  });

  it('offers nothing on a revoked terminal, because revoking is final', () => {
    expect(offered(till({ status: 'revoked' }), true).code).toBe(false);
    expect(offered(till({ status: 'revoked' }), true).revoke).toBe(false);
  });

  it('offers a code before pairing and still after, so a till can be replaced', () => {
    expect(offered(till(), true).code).toBe(true);
    expect(offered(till({ status: 'active', enrolled_at: 'x' }), true).code).toBe(true);
  });

  it('does not offer to pause a terminal that was never paired', () => {
    // There is nothing to pause, and the server refuses it.
    expect(offered(till(), true).pause).toBe(false);
    expect(offered(till(), true).resume).toBe(false);
  });

  it('offers pause and resume as opposites, never both', () => {
    const active = offered(till({ status: 'active', enrolled_at: 'x' }), true);
    expect([active.pause, active.resume]).toEqual([true, false]);

    const paused = offered(till({ status: 'inactive', enrolled_at: 'x' }), true);
    expect([paused.pause, paused.resume]).toEqual([false, true]);
  });
});

describe('the countdown on a code', () => {
  const now = Date.parse('2026-08-18T10:00:00Z');

  it('counts down in whole seconds', () => {
    expect(secondsLeft('2026-08-18T10:14:00Z', now)).toBe(14 * 60);
  });

  it('never goes negative on an expired code', () => {
    expect(secondsLeft('2026-08-18T09:59:00Z', now)).toBe(0);
  });

  it('treats an unreadable date as expired rather than as forever', () => {
    expect(secondsLeft('not a date', now)).toBe(0);
  });

  it('reads as minutes and seconds, and says so plainly at zero', () => {
    expect(countdown(905)).toBe('15:05');
    expect(countdown(59)).toBe('0:59');
    expect(countdown(0)).toBe('expired');
  });
});

describe('showing the code', () => {
  it('keeps the groups it was issued in', () => {
    // Read off one screen and typed into another; one long string is where a
    // reader loses their place.
    expect(codeGroups('K7QP-4M2X')).toEqual(['K7QP', '4M2X']);
  });

  it('survives a code with no dash rather than rendering nothing', () => {
    expect(codeGroups('K7QP4M2X')).toEqual(['K7QP4M2X']);
  });
});
