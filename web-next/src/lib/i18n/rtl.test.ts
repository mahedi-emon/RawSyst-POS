import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { directionOf, LOCALES } from './locale';

/**
 * Arabic mirrors from `dir` alone.
 *
 * The stylesheets use logical properties throughout -- `ps-`, `pe-`, `ms-`,
 * `me-`, `start-`, `end-` -- so the whole product flips when `dir="rtl"` is set
 * on the root, with no second stylesheet and no per-component branch.
 *
 * That only stays true if nobody reaches for a physical property. One `ml-4`
 * looks harmless and is invisible in English; in Arabic it is a gap on the
 * wrong side of one element in a row that is otherwise correct, which is the
 * hardest kind of layout bug to see and the easiest to introduce.
 *
 * So this walks the source and refuses them. It is the mechanical half of "do
 * not solve RTL by reversing margins", and it runs in a second rather than
 * needing somebody to open the product in Arabic.
 */

const here = dirname(fileURLToPath(import.meta.url));
const srcRoot = join(here, '..', '..');

/**
 * Physical direction utilities, which do not mirror.
 *
 * `text-left` / `text-right` are included: `text-start` and `text-end` exist
 * and are what a column heading wants. The one legitimate use is a field forced
 * to `direction: ltr` -- a money input, where right IS the end in both scripts
 * -- and those carry `[direction:ltr]` on the same element, which the exemption
 * below looks for.
 */
const PHYSICAL =
  /\b(?:ml|mr|pl|pr|border-l|border-r|rounded-l|rounded-r)-[a-z0-9.[\]]+|\btext-(?:left|right)\b|\b(?:left|right)-[0-9.]+/g;

/** An `rtl:` or `ltr:` prefixed class is a deliberate, mirrored exception. */
function isPrefixed(line: string, index: number): boolean {
  const before = line.slice(Math.max(0, index - 6), index);
  return /(?:^|[\s'"`])(?:rtl|ltr):$/.test(before);
}

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === '.next') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) out.push(full);
  }
  return out;
}

describe('the product mirrors from dir alone', () => {
  it('uses no physical direction utility that would not flip in Arabic', () => {
    const offences: string[] = [];

    for (const file of walk(srcRoot)) {
      const text = readFileSync(file, 'utf8');
      const lines = text.split(/\r?\n/);

      lines.forEach((line, i) => {
        // Comments explain the rule and therefore quote the very classes it
        // forbids. Reading them as code makes the check fail on its own
        // documentation, which teaches people to delete the documentation.
        const code = line.trim();
        if (code.startsWith('//') || code.startsWith('*') || code.startsWith('/*')) {
          return;
        }

        // A line that forces its own direction is exempt: with
        // `direction: ltr` on the element, `right` is the end of the field in
        // both scripts, which is what a money input needs.
        if (line.includes('[direction:ltr]')) return;

        for (const m of line.matchAll(PHYSICAL)) {
          if (m.index !== undefined && isPrefixed(line, m.index)) continue;
          offences.push(
            `${relative(srcRoot, file)}:${i + 1}  ${m[0]}\n    ${line.trim().slice(0, 100)}`,
          );
        }
      });
    }

    expect(offences.join('\n')).toBe('');
  });
});

describe('the three languages', () => {
  it('ships English, Arabic and Bangla', () => {
    expect(LOCALES.map((l) => l.value).sort()).toEqual(['ar', 'bn', 'en']);
  });

  it('runs Arabic right to left and the other two left to right', () => {
    expect(directionOf('ar')).toBe('rtl');
    expect(directionOf('en')).toBe('ltr');
    // Bangla is left-to-right. It is grouped with Arabic often enough that
    // this is worth pinning: giving it RTL would mirror every Bangladeshi
    // shop's screens for no reason.
    expect(directionOf('bn')).toBe('ltr');
  });

  it('names each language in its own script', () => {
    // Somebody looking for Bangla is looking for বাংলা, not for the word
    // "Bengali" spelled in a script they may not read.
    expect(LOCALES.find((l) => l.value === 'ar')?.native).toBe('العربية');
    expect(LOCALES.find((l) => l.value === 'bn')?.native).toBe('বাংলা');
  });
});
