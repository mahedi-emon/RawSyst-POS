import { describe, expect, it } from 'vitest';

import { handleCounterKey, type CounterActions } from './keys';

/** A recording set of actions, so a test can say what was asked for. */
function spy() {
  const called: string[] = [];
  const scanned: string[] = [];
  const actions: CounterActions = {
    focusScan: () => called.push('focusScan'),
    chooseCustomer: () => called.push('chooseCustomer'),
    hold: () => called.push('hold'),
    resume: () => called.push('resume'),
    pay: () => called.push('pay'),
    cancel: () => called.push('cancel'),
    scanned: (c) => {
      called.push('scanned');
      scanned.push(c);
    },
  };
  return { actions, called, scanned };
}

/** A keystroke, with somewhere for it to have landed. */
function press(
  key: string,
  opts: {
    target?: { tagName: string; type?: string; isContentEditable?: boolean };
    ctrl?: boolean;
    alt?: boolean;
    meta?: boolean;
  } = {},
): KeyboardEvent {
  // A plain object, because `isTextEntry` duck-types. That is the point of it
  // doing so: the decision is about the element's properties, and a test
  // should be able to state those without a DOM.
  const target = opts.target
    ? {
        tagName: opts.target.tagName,
        type: opts.target.type,
        isContentEditable: opts.target.isContentEditable ?? false,
      }
    : null;
  return {
    key,
    ctrlKey: opts.ctrl ?? false,
    altKey: opts.alt ?? false,
    metaKey: opts.meta ?? false,
    target,
  } as unknown as KeyboardEvent;
}

describe('the counter keyboard', () => {
  // UI spec §1: "Fully operable with no mouse."
  it('binds the seven keys the screen spec names', () => {
    const cases: [string, string][] = [
      ['F2', 'focusScan'],
      ['F4', 'chooseCustomer'],
      ['F8', 'hold'],
      ['F9', 'resume'],
      ['F12', 'pay'],
      ['Escape', 'cancel'],
    ];
    for (const [key, action] of cases) {
      const { actions, called } = spy();
      const out = handleCounterKey(press(key), actions);
      expect(called, `${key} should ${action}`).toEqual([action]);
      expect(out.handled, `${key} must be claimed`).toBe(true);
    }
  });

  // F12 opens the developer tools and F2 renames a file on some platforms. A
  // shortcut that also does the browser's thing is worse than no shortcut.
  it('claims the keys it binds so the browser does not also act on them', () => {
    for (const key of ['F2', 'F4', 'F8', 'F9', 'F12', 'Escape']) {
      const { actions } = spy();
      expect(handleCounterKey(press(key), actions).handled, key).toBe(true);
    }
  });

  it('leaves a key it does not bind to whatever was going to handle it', () => {
    const { actions, called } = spy();
    for (const key of ['F1', 'F5', 'Tab', 'ArrowDown', 'Enter']) {
      expect(handleCounterKey(press(key), actions).handled, key).toBe(false);
    }
    expect(called).toEqual([]);
  });
});

describe('the scanner, which is a keyboard that is not a person', () => {
  // The non-negotiable from UI spec §1, with the reason beside it: requiring a
  // click loses sales at a busy counter. `autoFocus` focuses the box once; the
  // first tender button a cashier presses takes focus away for good.
  it('sends a character to the scan box when focus is nowhere that wants it', () => {
    for (const target of [
      undefined,
      { tagName: 'BUTTON' },
      { tagName: 'BODY' },
      { tagName: 'A' },
      { tagName: 'INPUT', type: 'checkbox' },
    ]) {
      const { actions, called, scanned } = spy();
      const out = handleCounterKey(press('7', { target }), actions);
      expect(called, JSON.stringify(target)).toEqual(['scanned']);
      expect(scanned).toEqual(['7']);
      expect(out.handled).toBe(true);
    }
  });

  // The failure this prevents: a barcode landing in the cart's quantity field
  // because that is what happened to have focus.
  it('leaves somebody who is typing alone', () => {
    for (const target of [
      { tagName: 'INPUT' },
      { tagName: 'INPUT', type: 'text' },
      { tagName: 'INPUT', type: 'search' },
      { tagName: 'TEXTAREA' },
      { tagName: 'SELECT' },
      { tagName: 'DIV', isContentEditable: true },
    ]) {
      const { actions, called } = spy();
      const out = handleCounterKey(press('7', { target }), actions);
      expect(called, JSON.stringify(target)).toEqual([]);
      expect(out.handled).toBe(false);
    }
  });

  it('does not treat a shortcut as a scan', () => {
    const { actions, called } = spy();
    handleCounterKey(press('k', { ctrl: true }), actions);
    handleCounterKey(press('c', { meta: true }), actions);
    handleCounterKey(press('n', { alt: true }), actions);
    expect(called).toEqual([]);
  });

  it('does not treat a key with no character as a scan', () => {
    const { actions, called } = spy();
    for (const key of ['Shift', 'Control', 'ArrowLeft', 'Home', 'Backspace']) {
      handleCounterKey(press(key), actions);
    }
    expect(called).toEqual([]);
  });

  // A barcode is characters and an Enter, and Enter is the scan form's submit.
  // Claiming it here would stop the form ever firing.
  it('leaves Enter to the form', () => {
    const { actions } = spy();
    expect(handleCounterKey(press('Enter'), actions).handled).toBe(false);
  });

  it('passes every character of a scanned code through in order', () => {
    const { actions, scanned } = spy();
    for (const c of '5901234123457') handleCounterKey(press(c), actions);
    expect(scanned.join('')).toBe('5901234123457');
  });
});
