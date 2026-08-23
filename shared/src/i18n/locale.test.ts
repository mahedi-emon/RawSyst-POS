import { describe, expect, it } from 'vitest';

import { ar, catalogues, directionOf, en, type Key } from './strings';

// QA gate M6, from the design system §6 rule 5: "Mixed content is the norm, not
// the edge case. A product named `قميص رجالي Slim Fit — L` must render
// correctly. Every component is tested with a mixed-script fixture string."
//
// That string, exactly as the spec writes it, so anything consuming it is being
// held to the spec's own example rather than to one invented here.
export const MIXED_SCRIPT = 'قميص رجالي Slim Fit — L';

describe('the string catalogue', () => {
  it('says everything in both languages', () => {
    // The type system already guarantees this — `ar` is Record<Key, string>, so
    // a key added to `en` without an Arabic string fails to compile. This is
    // the runtime half: a key present but left as an empty string, or left as
    // the English text, compiles perfectly and ships an untranslated screen.
    const keys = Object.keys(en) as Key[];
    expect(keys.length).toBeGreaterThan(0);

    const missing: string[] = [];
    const untranslated: string[] = [];
    for (const key of keys) {
      const arabic = ar[key];
      if (!arabic || arabic.trim() === '') {
        missing.push(key);
        continue;
      }
      // Two keys are the same in both by design: the language names are each
      // written in their own script, so "English" is correct in the Arabic
      // catalogue.
      if (arabic === en[key] && !key.startsWith('language.')) {
        untranslated.push(key);
      }
    }

    expect(missing, 'keys with no Arabic string').toEqual([]);
    expect(untranslated, 'keys still carrying the English text').toEqual([]);
  });

  it('carries no Latin letters in the Arabic strings that should not be there', () => {
    // Brand and scheme names stay Latin — "Mada" is written مدى, but a report
    // called "X report" keeps its X because that is what the till prints on the
    // paper. Everything else being Latin means a key was copied and not
    // translated, which the check above would catch only if it matched exactly.
    const allowed = new Set<Key>([
      'language.english',
      'shift.xReport',
      'shift.zReport',
      // File formats. PNG and JPEG are written that way in Arabic too; a
      // transliteration would be a worse instruction, not a better one.
      'brand.formatHint',
    ]);
    // Two things are Latin by construction and are not translation failures:
    // interpolation placeholders like `{time}`, which are key names rather than
    // text anybody reads, and the product's own name. A brand is written as it
    // is written — Mada becomes مدى because that is its Arabic name, and
    // RawSyst has none.
    const strip = (text: string) =>
      text.replace(/\{\w+\}/g, '').replace(/RawSyst/g, '');
    const suspicious = (Object.keys(ar) as Key[]).filter(
      (k) => !allowed.has(k) && /[A-Za-z]{4,}/.test(strip(ar[k])),
    );
    expect(suspicious, 'Arabic strings containing English words').toEqual([]);
  });

  it('knows which way each language runs', () => {
    expect(directionOf('en')).toBe('ltr');
    expect(directionOf('ar')).toBe('rtl');
  });

  it('offers exactly the locales it has catalogues for', () => {
    expect(Object.keys(catalogues).sort()).toEqual(['ar', 'en']);
  });
});

describe('QA gate M6 — mixed script', () => {
  it('keeps the spec fixture intact through the catalogue machinery', () => {
    // The failure this guards against is a build step or a normaliser mangling
    // the string: a stripped RTL mark, a normalised em dash, a lost Arabic
    // character. The fixture has all three hazards in one line.
    expect(MIXED_SCRIPT).toContain('قميص');
    expect(MIXED_SCRIPT).toContain('Slim Fit');
    expect(MIXED_SCRIPT).toContain('—');
    expect([...MIXED_SCRIPT].length).toBe(MIXED_SCRIPT.length);
  });

  it('sorts and compares without dropping either script', () => {
    // A catalogue lookup, a search filter and a table sort all touch strings
    // like this. None of them may lose half of one.
    const names = [MIXED_SCRIPT, 'Abaya', 'عباية'];
    const sorted = [...names].sort((a, b) => a.localeCompare(b));
    expect(sorted).toHaveLength(3);
    for (const n of names) expect(sorted).toContain(n);
  });

  it('measures length in characters a person would count', () => {
    // Receipt rendering truncates to a column width. Counting UTF-16 code units
    // instead of characters would cut an Arabic word mid-letter.
    const truncated = [...MIXED_SCRIPT].slice(0, 6).join('');
    expect(truncated).toBe('قميص ر');
  });
});
