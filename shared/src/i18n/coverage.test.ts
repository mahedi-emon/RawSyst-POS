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

/** Files with no translatable prose in them. */
const NO_PROSE = new Set([
  'session.tsx', // an auth context, no rendered text
  'Sparkline.tsx', // an SVG chart
  'CardTableLabels.tsx', // copies labels out of a table's own header
  'LanguageSwitch.tsx', // each language is named in its own language
]);

/**
 * Names that are written the same way in both languages.
 *
 * A card scheme and a wallet are brands. "Mada" becomes مدى because that is its
 * Arabic name; Mastercard has none, and inventing one would put a word on a
 * receipt that no cardholder recognises.
 */
const BRANDS = new Set(['Mastercard', 'Apple Pay', 'STC Pay']);

/** Prose, as opposed to an identifier, a class name or a code. */
const PROSE = /^[A-Z][A-Za-z0-9 ,.'’…\-–—()/&%:!?]{6,}$/;

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/\.tsx$/.test(entry) && !/\.test\.tsx$/.test(entry)) out.push(full);
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
    if (known.has(literal) || BRANDS.has(literal)) continue;
    // A path or an import specifier, not a sentence.
    if (literal.includes('/') && !literal.includes(' ')) continue;

    const before = text.slice(Math.max(0, match.index - 40), match.index);
    // Already inside t(...), or naming a module, or a CSS class.
    if (before.trimEnd().endsWith('t(')) continue;
    if (before.includes('import') || before.includes('className')) continue;

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
    if (!collapsed || known.has(collapsed) || BRANDS.has(collapsed)) continue;
    if (!/^[A-Z][A-Za-z0-9 ,.'’…\-–—()/&%:!?]+$/.test(collapsed)) continue;
    // Three real words or more: shorter than that is a label already keyed, an
    // abbreviation, or punctuation the regex happened to span.
    const words = collapsed.split(' ').filter((w) => /^[A-Za-z]{3,}$/.test(w));
    if (words.length < 3) continue;
    found.push(collapsed);
  }
  return [...new Set(found)];
}

describe('translation coverage', () => {
  const files = walk(sharedSrc).filter((f) => !NO_PROSE.has(f.split(/[\\/]/).pop()!));
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
