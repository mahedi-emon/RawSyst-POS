// The counter's keyboard, and the scanner that is not a keyboard.
//
// UI spec §1 names seven shortcuts and says the counter is "fully operable with
// no mouse". None of them existed. It also makes "no field focus needed to
// scan" a non-negotiable, with the reason written next to it: requiring a click
// loses sales at a busy counter.
//
// # Why the scan field holding focus is not enough
//
// The counter's scan box carries `autoFocus`, which focuses it once, on mount.
// That is fine until the cashier touches anything else — a tender button, the
// customer link, a quantity field — at which point focus leaves and the next
// scan goes into whatever has it instead. A barcode scanner is a keyboard that
// types eight characters and presses Enter; if the cart quantity field has
// focus, the barcode lands in the quantity.
//
// So the scan is captured at the DOCUMENT, and the field is where the captured
// characters are put. A cashier can be anywhere on the screen and scan.
//
// # Telling a scanner from a person
//
// It is the same event stream, so this does not try. It asks a simpler
// question: is the keystroke going somewhere it belongs? A printable key while
// a text field has focus is somebody typing and is left alone. A printable key
// while focus is on the body, a button or a link is a scan, and goes to the
// scan box.
//
// That gets the ordinary cases right without timing heuristics, which fail on
// exactly the day the shop is busiest: a scanner interleaved with a person
// typing produces the same inter-key timings as a fast typist, and a wedge that
// guesses wrong drops a barcode or corrupts a name.

/** Where a keystroke is going. */
function isTextEntry(target: EventTarget | null): boolean {
  // Duck-typed rather than `instanceof HTMLElement`.
  //
  // The question being asked is "does this element take typed characters",
  // and every answer to it is on the element's own properties. Reaching for
  // the constructor instead binds a pure decision to a DOM being present,
  // which makes it untestable outside a browser — and a keyboard layer that
  // can only be exercised by pressing keys by hand is one nobody exercises.
  const el = target as {
    tagName?: unknown;
    type?: unknown;
    isContentEditable?: unknown;
  } | null;
  if (!el || typeof el.tagName !== 'string') return false;
  if (el.isContentEditable === true) return true;

  const tag = el.tagName.toUpperCase();
  if (tag === 'TEXTAREA' || tag === 'SELECT') return true;
  if (tag !== 'INPUT') return false;

  // A checkbox or a radio is a button that happens to be an input; typing at
  // one is not typing. An input with no `type` is a text input.
  const type = typeof el.type === 'string' ? el.type.toLowerCase() : 'text';
  return type !== 'checkbox' && type !== 'radio' && type !== 'button';
}

/** A key that produces a character, as opposed to one that does something. */
function isPrintable(e: KeyboardEvent): boolean {
  return e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey;
}

/** What the counter can be asked to do from the keyboard. */
export interface CounterActions {
  /** F2 — put the cursor in the scan box. */
  focusScan: () => void;
  /** F4 — choose or change the customer. */
  chooseCustomer: () => void;
  /** F8 — park the cart. */
  hold: () => void;
  /** F9 — bring one back. */
  resume: () => void;
  /** F12 — take the money. */
  pay: () => void;
  /** Escape — back out of whatever is open, or clear the cart. */
  cancel: () => void;
  /** A character that arrived while nothing was listening for one. */
  scanned: (char: string) => void;
}

/**
 * Decide what one keystroke means.
 *
 * Pure, so the decisions can be tested without a DOM: the caller hands it the
 * event and the actions, and it says what happened. `handled` tells the caller
 * whether to call `preventDefault` — F12 opens the developer tools and F2
 * renames a file on some platforms, and a shortcut that also does the browser's
 * thing is worse than no shortcut.
 */
export function handleCounterKey(
  e: KeyboardEvent,
  actions: CounterActions,
): { handled: boolean; what: string } {
  // A modifier the counter does not use means the keystroke belongs to the
  // browser or the operating system. Ctrl+K is the exception the spec names,
  // and it is not built yet — there is no command palette to open, and a
  // shortcut that opens nothing is worse than one that is not there.
  if (e.altKey || e.metaKey) return { handled: false, what: '' };

  switch (e.key) {
    case 'F2':
      actions.focusScan();
      return { handled: true, what: 'focusScan' };
    case 'F4':
      actions.chooseCustomer();
      return { handled: true, what: 'chooseCustomer' };
    case 'F8':
      actions.hold();
      return { handled: true, what: 'hold' };
    case 'F9':
      actions.resume();
      return { handled: true, what: 'resume' };
    case 'F12':
      actions.pay();
      return { handled: true, what: 'pay' };
    case 'Escape':
      actions.cancel();
      return { handled: true, what: 'cancel' };
  }

  if (e.ctrlKey) return { handled: false, what: '' };

  // Everything below is the scanner wedge, and it only applies when the
  // keystroke has nowhere better to go.
  if (isTextEntry(e.target)) return { handled: false, what: '' };
  if (!isPrintable(e)) return { handled: false, what: '' };

  actions.scanned(e.key);
  return { handled: true, what: 'scanned' };
}
