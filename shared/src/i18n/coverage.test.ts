import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import { en } from './strings';

/**
 * No component may show a sentence the Arabic catalogue has never heard of.
 *
 * The catalogue reached 465 keys and was recorded as complete, and it was not:
 * 223 user-visible strings were still written into the components as English
 * literals. They survived because the earlier pass worked from the catalogue
 * outwards — it translated every key that existed, which says nothing at all
 * about text that was never keyed.
 *
 * This checks in the other direction, from the components in. It is the only
 * direction that can find the gap.
 *
 * The failure it prevents is quiet: an Arabic-speaking shop sees a screen that
 * is mostly Arabic with an English sentence in the middle of it, and nothing
 * anywhere reports a problem.
 */

const here = dirname(fileURLToPath(import.meta.url));
const sharedSrc = join(here, '..');
const repoRoot = join(sharedSrc, '..', '..');

/**
 * Every place a screen is written, not just this package.
 *
 * `shared` holds the back office and the till's building blocks; `pos/src` and
 * `web/components` hold the screens themselves. Walking shared alone left the
 * COUNTER out — the screen a cashier looks at all day — and an Arabic walk of
 * the real terminal found "Add a customer", "Finish sale", "Hold sale" and
 * "Clear" in English on it, along with half of the e-invoicing banner above.
 *
 * The catalogue is shared by all three, so there was never a reason for the
 * check to stop at a package boundary.
 */
const SCREEN_DIRS = [
  sharedSrc,
  join(repoRoot, 'pos', 'src'),
  join(repoRoot, 'web', 'components'),
  // The greenfield front end. Added the moment it had screens rather than
  // after it had dozens, because this check is the only thing that finds an
  // English sentence sitting in the middle of an Arabic screen, and the cost
  // of catching up grows with every page written.
  join(repoRoot, 'web-next', 'src'),
];

/** Files with no translatable prose in them. */
const NO_PROSE = new Set([
  'session.tsx', // an auth context, no rendered text
  'Sparkline.tsx', // an SVG chart
  'CardTableLabels.tsx', // copies labels out of a table's own header
  'LanguageSwitch.tsx', // each language is named in its own language

  // The transport, which has no locale and cannot be given one.
  //
  // Most of what it holds is not prose at all: `Content-Type`, `X-CSRF-Token`,
  // `X-Client-Kind` are header names, and `RequestFailed` is a class name.
  //
  // Three are English sentences, and each is a last resort rather than a
  // message a shop reads in the ordinary course. `Offline`'s text is replaced
  // at every display site, which switches on the exception's CLASS and calls
  // t() — see `explain` in the screens. The other two stand in for an error
  // body the server sent without a message, which the API does not do; they
  // carry `code: 'internal'`, which is what a display site would key off if a
  // server ever did.
  //
  // Translating them here would mean handing the client a t() at construction
  // and holding it for the life of the app, which goes stale the moment the
  // reader switches language — the one thing the locale provider exists to
  // avoid.
  'client.ts',

  // The portal's transport, for the same reason and with less to explain.
  //
  // What it holds is `Content-Type`, a header name, and `PortalFailed`, a class
  // name. Its one English sentence was removed rather than exempted: a failed
  // call now carries the server's own sentence or nothing, and each portal
  // screen falls back to its own translated words when the message is empty.
  'portal.ts',

  // A record, not a screen.
  //
  // `queue.ts` writes "The server refused this sale." into the local database
  // when the server refused an item and sent no reason of its own. It is the
  // stored explanation an owner reads later out of the queue, alongside the
  // server's own sentences in whatever language the server wrote them — and it
  // is the one thing here with no reader in front of it at the moment it is
  // written.
  'queue.ts',

  // The words on a receipt come from `receipt.ts`, which takes a translator.
  // These two are the defaults it falls back to when a shop has written no
  // stationery of its own, and `receipt.ts` translates them at the point of
  // printing.
  'stationery.ts',

  // `credential.ts` classifies what the Rust shell threw. Its one English
  // sentence is the no-keystore case, and the pairing screen replaces it with
  // its own words — see the `no_keystore` branch there.
  'credential.ts',

  // web-next's error classes, for exactly the reason `client.ts` is here.
  //
  // Most of what it holds is not prose: `ApiError` and `NetworkError` are class
  // names, and the error CODES (`not_found`, `period_closed`) are the server's
  // stable identifiers, which a screen branches on and never shows. Its one
  // English sentence is `NetworkError`'s message, and every display site
  // replaces it — `messageFor(e, t)` and `ErrorState` both switch on the
  // exception's CLASS and call `t()`. Translating it at construction would mean
  // holding a translator for the life of the module, which goes stale the
  // moment the reader switches language.
  'errors.ts',

  // Generated from the Go route table. Every string in it is an identifier the
  // backend defines — URL patterns, permission names, and the `Access` values
  // `Public`, `Authenticated`, `Permission`, `SuperAdmin`. Translating one
  // would stop a guard matching.
  'contract.generated.ts',
]);

/**
 * Names that are written the same way in both languages.
 *
 * A card scheme and a wallet are brands. "Mada" becomes مدى because that is its
 * Arabic name; Mastercard has none, and inventing one would put a word on a
 * receipt that no cardholder recognises.
 */
const BRANDS = new Set([
  'Mastercard',
  'Apple Pay',
  'STC Pay',
  // The product's own name, on the navigation rail's wordmark. A brand is
  // written as it is written; the Arabic catalogue check strips it for the
  // same reason.
  'RawSyst',
]);

/**
 * Shapes rather than words.
 *
 * A placeholder showing what a reference looks like — `INV-10023`,
 * `MADA-20260817-001` — is the same in Arabic, because the document it is
 * copied from is. Translating one would show a shop a format its own paperwork
 * does not use. Sample data that IS words — a street, a company name — is
 * translated and is not listed here.
 */
/** SVG path data — `d="M4 4h7v7H4z"`. Geometry, never words. */
const SVG_PATH = /^[MmZzLlHhVvCcSsQqTtAa][\dMmZzLlHhVvCcSsQqTtAa.,\-\s]*$/;

const FORMATS = new Set([
  'INV-10023',
  'MADA-20260817-001',
  // The shape of an enrolment code, shown as a placeholder in the field it is
  // typed into. Latin in both directions of script — the code itself is, and
  // the input carries dir="ltr" to say so.
  'ABCD-1234',
]);

/**
 * Words the platform chose, which a user never reads.
 *
 * A KeyboardEvent's `key` is an identifier the browser defines: "ArrowUp" is
 * spelled that way in Arabic Windows too. Translating one would stop the
 * keyboard working.
 */
const PLATFORM = new Set([
  // A KeyboardEvent's `key`, which the browser defines.
  'ArrowUp',
  'ArrowDown',
  // The two the tablist moves on, mirrored in Arabic by the component rather
  // than by renaming the key -- the browser reports 'ArrowRight' for the
  // physical right-hand key whichever way the page reads.
  'ArrowLeft',
  'ArrowRight',
  'PageUp',
  'PageDown',
  // An element's `tagName`, which the browser also defines and which it
  // reports in upper case. The counter's keyboard layer asks whether focus is
  // somewhere that takes typed characters, and these are the answers.
  'TEXTAREA',
  'SELECT',
  'INPUT',
  'BUTTON',
]);

/** Prose, as opposed to an identifier, a class name or a code. */
const PROSE = /^[A-Z][A-Za-z0-9 ,.'’“”…\-–—()/&%:!?]{6,}$/;

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/\.tsx$/.test(entry) && !/\.test\.tsx$/.test(entry)) out.push(full);
  }
  return out;
}

/**
 * The plain TypeScript half.
 *
 * `walk` collects components, and words do not only live in components.
 * `inventory/matrix.ts` held the whole vocabulary of the variant grid — "Out",
 * "Low", "Dead", and the sentences a screen reader is given in place of the
 * bare number — and none of it was ever looked at, because the file has no x on
 * the end. The grid's own header says colour is never the only signal; the
 * signal was in English on every Arabic screen.
 *
 * Only the string-literal check runs over these. A .ts file has no JSX, so the
 * other three have nothing to find in one.
 */
function walkPlain(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walkPlain(full, out);
    else if (/\.ts$/.test(entry) && !/\.test\.ts$/.test(entry) && !/\.d\.ts$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

/** Single-quoted prose literals that are not already catalogue values. */
function untranslatedIn(file: string, known: Set<string>): string[] {
  const text = readFileSync(file, 'utf8');
  const found: string[] = [];

  for (const match of text.matchAll(/'([^'\n]{7,70})'/g)) {
    const literal = match[1];
    if (!literal || literal.includes('\\')) continue;
    if (!PROSE.test(literal)) continue;
    if (known.has(literal) || BRANDS.has(literal) || FORMATS.has(literal)) continue;
    if (PLATFORM.has(literal)) continue;
    // A path or an import specifier, not a sentence.
    if (literal.includes('/') && !literal.includes(' ')) continue;
    // SVG path data, which is geometry. `M4 4h7v7H4z` is letters and numbers
    // and long enough to look like prose to a scanner, and the icon set is two
    // dozen of them. The shape is unambiguous: a path command letter followed
    // by nothing but commands, digits, separators and signs. No sentence in
    // any language is made only of `MmLlHhVvCcSsQqTtAaZz`.
    if (SVG_PATH.test(literal)) continue;

    const before = text.slice(Math.max(0, match.index - 40), match.index);
    // Already inside t(...), or naming a module, or a CSS class.
    if (before.trimEnd().endsWith('t(')) continue;
    if (before.includes('import') || before.includes('className')) continue;

    // An icon's name, which is a component identifier the icon set defines.
    // `icon: 'ShoppingCart'` resolves to a React component; translating it
    // would leave the navigation with no icons at all. The same shape as
    // `className` above, and exempted the same way.
    if (/\bicon:\s*$/.test(before)) continue;

    // A developer log. `console.error('RawSyst screen failed to render', e)` is
    // read by whoever reads stack traces, never by a shop, and the brief is
    // explicit that developer-only logs are not translated.
    if (/console\.(error|warn|info|log|debug)\(\s*$/.test(before)) continue;

    // The English half of a translated string.
    //
    // A module that cannot use a hook takes a translator and writes
    // `translate?.('key') ?? 'the English'`, so the English sentence is still
    // in the source — as the answer for a caller with no locale, which is the
    // whole point of the pattern. Looking further back finds the call it is
    // the fallback for.
    const wider = text.slice(Math.max(0, match.index - 260), match.index);
    if (/(?:translate\?\.|say)\([^)]*\)\s*\?\?[\s\S]*$/.test(wider)) continue;

    found.push(literal);
  }
  return [...new Set(found)];
}

/**
 * Prose written straight into JSX, e.g. `<p>Your logo belongs to…</p>`.
 *
 * These are not string literals, so the check above never saw them. That is how
 * 38 paragraphs — the long explanations that tell somebody what a screen is
 * for — stayed English after the literals were all translated. An Arabic RTL
 * browser run found them; this finds them without one.
 */
function untranslatedJsxTextIn(file: string, known: Set<string>): string[] {
  const text = readFileSync(file, 'utf8');
  const found: string[] = [];

  for (const match of text.matchAll(/>([^<>{}]{12,400})</g)) {
    const collapsed = match[1]!.split(/\s+/).filter(Boolean).join(' ');
    if (!collapsed || BRANDS.has(collapsed)) continue;
    // Deliberately NOT skipped when the catalogue happens to hold the same
    // words. A sentence rendered as JSX text is rendered in English whatever
    // the catalogue says — the check is whether this component calls t(), not
    // whether somebody once wrote the key. "Count denominations" and "Enter a
    // total" survived a whole pass because their keys had been added first and
    // the components still printed the literal.
    if (known.has(collapsed) && !/^[a-z]/.test(collapsed)) {
      // fall through: a catalogue value in JSX text is the defect itself
    }
    // The curly double quotes are in the class deliberately. Without them the
    // paragraph under the payment mix — which quotes the word “card” — did not
    // look like prose to this check, and stayed English on every Arabic screen.
    if (!/^[A-Z][A-Za-z0-9 ,.'’“”…\-–—()/&%:!?]+$/.test(collapsed)) continue;
    // Two real words or more. Three was the first threshold and it let
    // "Record a deposit" through — the heading of the panel where a shop
    // reconciles its card takings, on an Arabic screen, found by a browser
    // audit long after this test was recorded as complete. Short headings are
    // exactly where the eye stops, so they are the last place to relax a rule.
    const words = collapsed.split(' ').filter((w) => /^[A-Za-z]{3,}$/.test(w));
    if (words.length < 2) continue;
    found.push(collapsed);
  }
  return [...new Set(found)];
}

/**
 * Prose passed as a double-quoted JSX attribute.
 *
 * `submitLabel="Record deposit"`, `aria-label="Include this payment"`. The
 * literal check above reads single quotes only, because that is how a
 * TypeScript file writes a string — but JSX attributes are conventionally
 * double-quoted, and prettier rewrites them that way. So the text on a button
 * and every label a screen reader announces sat in a blind spot.
 *
 * aria-labels count. A blind cashier using an Arabic screen reader is a user,
 * and an English label is as wrong for them as an English heading is for
 * anybody else.
 */
function untranslatedAttributeTextIn(file: string, known: Set<string>): string[] {
  const text = readFileSync(file, 'utf8');
  const found: string[] = [];

  // Attributes that carry words a person reads or hears. Deliberately a list
  // rather than "every attribute": `className`, `id`, `type` and `href` all
  // hold strings that look like prose and are not.
  const carries =
    /\b(?:aria-label|aria-description|title|placeholder|label|submitLabel|hint|caption|heading|summary|alt)\s*=\s*"([^"\n]{7,300})"/g;

  for (const match of text.matchAll(carries)) {
    const literal = match[1];
    if (!literal) continue;
    if (!PROSE.test(literal)) continue;
    if (known.has(literal) || BRANDS.has(literal) || FORMATS.has(literal)) continue;
    if (PLATFORM.has(literal)) continue;
    found.push(literal);
  }
  return [...new Set(found)];
}

/**
 * Prose sharing a JSX text node with an expression.
 *
 * `{money(total)} not yet settled` — the words are JSX text, but the node also
 * holds `{...}`, and the paragraph check above excludes braces outright so it
 * cannot see either half. That is how "not yet settled" sat in English under
 * the cash figure on the Arabic dashboard, found by measuring the tile rather
 * than by reading anything.
 *
 * Matched from a closing brace to the next tag, which is where this shape
 * always puts the words. Two real words or more, so `{n} sales` and `{pct} of`
 * do not each need an entry — those are caught by the literal check when they
 * are written as strings, and by eye when they are not.
 */
function untranslatedBesideAnExpressionIn(
  file: string,
  known: Set<string>,
): string[] {
  const text = readFileSync(file, 'utf8');
  const found: string[] = [];

  // A generic in a type position — `): Promise<`, `function SelectInput<` —
  // ends with a `<` too, and the run before it can look like a short sentence.
  // Excluded by the words only code uses, which no interface string contains.
  //
  // `useApi<SalesDay>(scope ? '/dashboard/sales' : null, …)` is the same shape
  // and matched none of the keywords, so three more markers were added. Each
  // is something prose does not contain:
  //
  //   a straight-quoted literal — a sentence uses the typographic ’, which the
  //     catalogue's own values do throughout;
  //   `null` / `undefined` / `true` / `false`, which are not English words in
  //     any sentence a shop reads;
  //   a ternary, which is punctuation no sentence arranges that way.
  const isCode =
    /\b(function|const|let|export|import|return|async|await|interface|type|extends|Promise|Record|React|typeof|instanceof|catch|else|if|switch|case|new)\b|=>|\/\/|'[^']*'|\b(null|undefined|true|false)\b|\?[^?]*:/;

  // Both sides of the expression. `{money(x)} not yet settled` puts the words
  // after it and `The items on this {title}` puts them before, and a check
  // that read only one side would find half of them.
  const runs = [
    ...text.matchAll(/\}([^<>{}]{6,200})</g),
    ...text.matchAll(/>([^<>{}]{6,200})\{/g),
    // And BETWEEN two expressions, which is neither of the above. The
    // e-invoicing banner on the till put its whole explanation there — between
    // a translated heading and a conditional sentence — so it was the last
    // English left on the counter after everything else had been keyed.
    ...text.matchAll(/\}([^<>{}]{6,200})\{/g),
  ];

  for (const match of runs) {
    const collapsed = match[1]!.split(/\s+/).filter(Boolean).join(' ');
    if (!collapsed || isCode.test(collapsed)) continue;
    // Sentence fragments here, not whole sentences, so no leading-capital rule.
    if (!/^[A-Za-z0-9 ,.'’“”…\-–—()/&%:!?]+$/.test(collapsed)) continue;
    const words = collapsed.split(' ').filter((w) => /^[A-Za-z]{3,}$/.test(w));
    if (words.length < 2) continue;
    if (known.has(collapsed)) continue;
    // A catalogue string with the expression's place taken by a placeholder is
    // the fix for this shape, so a fragment that appears inside one is done.
    if ([...known].some((v) => v.includes(collapsed))) continue;
    found.push(collapsed);
  }
  return [...new Set(found)];
}

describe('translation coverage', () => {
  const files = SCREEN_DIRS.flatMap((d) => walk(d)).filter(
    (f) => !NO_PROSE.has(f.split(/[\\/]/).pop()!),
  );
  const plain = SCREEN_DIRS.flatMap((d) => walkPlain(d)).filter(
    (f) => !NO_PROSE.has(f.split(/[\\/]/).pop()!),
  );
  const known = new Set<string>(Object.values(en));

  it('finds the components and the catalogue', () => {
    expect(files.length).toBeGreaterThan(25);
    expect(known.size).toBeGreaterThan(400);
  });

  it('leaves no user-visible English outside the catalogue', () => {
    const offenders: string[] = [];
    for (const file of files) {
      const found = untranslatedIn(file, known);
      if (found.length === 0) continue;
      const where = relative(repoRoot, file).replace(/\\/g, '/');
      for (const literal of found) offenders.push(`  ${where}\n    "${literal}"`);
    }

    expect(
      offenders.join('\n'),
      'These strings are shown to a user but are not in the catalogue, so an\n' +
        'Arabic shop reads them in English. Add a key to `en` and `ar` in\n' +
        'shared/src/i18n/strings.ts and call t() instead — or, if it is a brand\n' +
        'name that is written the same way in both languages, add it to BRANDS:\n',
    ).toBe('');
  });

  it('leaves no untranslated words in a JSX attribute', () => {
    const offenders: string[] = [];
    for (const file of files) {
      const found = untranslatedAttributeTextIn(file, known);
      if (found.length === 0) continue;
      const where = relative(repoRoot, file).replace(/\\/g, '/');
      for (const text of found) offenders.push(`  ${where}\n    "${text}"`);
    }

    expect(
      offenders.join('\n'),
      'These words reach a user through a JSX attribute \u2014 a button label, a\n' +
        'placeholder, or the label a screen reader announces \u2014 and are not in\n' +
        'the catalogue. The literal check above reads single quotes only, so it\n' +
        'cannot see them. Replace each with a t() call:\n',
    ).toBe('');
  });

  it('leaves no user-visible English in a plain .ts module', () => {
    const offenders: string[] = [];
    for (const file of plain) {
      const found = untranslatedIn(file, known);
      if (found.length === 0) continue;
      const where = relative(repoRoot, file).replace(/\\/g, '/');
      for (const literal of found) offenders.push(`  ${where}\n    "${literal}"`);
    }

    expect(
      offenders.join('\n'),
      'These strings live in a module rather than a component, and reach a\n' +
        'user all the same. Take a translate function as an optional argument\n' +
        'the way readCell and tenderName do, so the module stays pure and the\n' +
        'words come from the catalogue:\n',
    ).toBe('');
  });

  it('leaves no untranslated words beside an expression', () => {
    const offenders: string[] = [];
    for (const file of files) {
      const found = untranslatedBesideAnExpressionIn(file, known);
      if (found.length === 0) continue;
      const where = relative(repoRoot, file).replace(/\\/g, '/');
      for (const text of found) offenders.push(`  ${where}\n    "${text}"`);
    }

    expect(
      offenders.join('\n'),
      'These words sit next to an expression in a JSX text node, so neither\n' +
        'check above can see them. Move the whole sentence into the catalogue\n' +
        'with a {placeholder} where the expression was:\n',
    ).toBe('');
  });

  it('leaves no untranslated paragraph written straight into JSX', () => {
    const offenders: string[] = [];
    for (const file of files) {
      const found = untranslatedJsxTextIn(file, known);
      if (found.length === 0) continue;
      const where = relative(repoRoot, file).replace(/\\/g, '/');
      for (const text of found) offenders.push(`  ${where}\n    "${text.slice(0, 90)}"`);
    }

    expect(
      offenders.join('\n'),
      'These paragraphs are rendered to a user and are not in the catalogue.\n' +
        'They are JSX text rather than string literals, which is why the check\n' +
        'above cannot see them. Replace the text with a t() call:\n',
    ).toBe('');
  });
});
