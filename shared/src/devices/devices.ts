// The Devices screen's own decisions, separated from its rendering.
//
// Four things here decide something rather than lay something out: what state a
// terminal is really in, how long an enrolment code has left, which actions to
// offer, and how to present a code so somebody can read it aloud without
// mistakes. Each is a place where getting it wrong costs a shop a working till.

import type { Terminal } from '../api/devices';

/** What a terminal is doing, in the words the screen uses.
 *
 *  `pending` splits in two because the two need different actions from a
 *  reader: one is waiting for somebody to type a code that already exists, the
 *  other has no code and needs one issued. The server reports both as pending
 *  and says which through `pending_code`. */
export type TerminalState =
  | 'waiting_for_code'
  | 'waiting_to_be_paired'
  | 'active'
  | 'paused'
  | 'revoked';

export function terminalState(t: Terminal): TerminalState {
  if (t.status === 'revoked') return 'revoked';
  if (t.status === 'inactive') return 'paused';
  if (t.status === 'active') return 'active';
  return t.pending_code ? 'waiting_to_be_paired' : 'waiting_for_code';
}

export interface StateLabel {
  label: string;
  tone: 'neutral' | 'info' | 'success' | 'warning' | 'danger';
  /** One sentence saying what to do next, or nothing when there is nothing to
   *  do. A status badge that only names a state leaves a shop owner guessing. */
  next?: string;
}

export function describeState(t: Terminal): StateLabel {
  switch (terminalState(t)) {
    case 'active':
      return { label: 'Ready', tone: 'success' };
    case 'waiting_to_be_paired':
      return {
        label: 'Waiting to be paired',
        tone: 'info',
        next: 'Type the code into the terminal to finish setting it up.',
      };
    case 'waiting_for_code':
      return {
        label: 'Not set up',
        tone: 'neutral',
        next: 'Get a code, then type it into the terminal.',
      };
    case 'paused':
      return {
        label: 'Switched off',
        tone: 'warning',
        next: 'This terminal cannot sell until it is switched back on.',
      };
    default:
      return {
        label: 'Revoked',
        tone: 'danger',
        next: 'Revoking cannot be undone. Register a new terminal to replace it.',
      };
  }
}

/** Which controls to offer for a terminal.
 *
 *  Offered controls are a courtesy — the server refuses whatever the screen put
 *  on the page — but a button that always refuses teaches people to distrust
 *  the rest of them, so the ones that cannot work are not drawn. */
export interface Offered {
  code: boolean;
  edit: boolean;
  pause: boolean;
  resume: boolean;
  revoke: boolean;
}

export function offered(t: Terminal, mayManage: boolean): Offered {
  if (!mayManage || t.status === 'revoked') {
    return { code: false, edit: false, pause: false, resume: false, revoke: false };
  }
  const paired = !!t.enrolled_at;
  return {
    // Re-pairing a working till is how a shop moves it to a new machine, so the
    // control stays available after setup rather than disappearing.
    code: true,
    edit: true,
    pause: paired && t.status === 'active',
    resume: paired && t.status === 'inactive',
    revoke: true,
  };
}

/** How long a code has left, in whole seconds. Never negative. */
export function secondsLeft(expiresAt: string, now: number = Date.now()): number {
  const at = Date.parse(expiresAt);
  if (Number.isNaN(at)) return 0;
  return Math.max(0, Math.floor((at - now) / 1000));
}

/** A countdown a person reads, not a duration a machine reads. */
export function countdown(seconds: number): string {
  if (seconds <= 0) return 'expired';
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}

/** The code split into the groups it is displayed in.
 *
 *  Shown as separate blocks rather than one string because it is read off one
 *  screen and typed into another, and a reader who loses their place in
 *  "K7QP4M2X" has to start again. */
export function codeGroups(code: string): string[] {
  return code.split('-').filter(Boolean);
}
