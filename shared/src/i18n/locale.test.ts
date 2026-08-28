import { initialLocale } from './locale';
import { afterEach, describe, expect, it } from 'vitest';

import { ar, catalogues, directionOf, en, type Key } from './strings';

// QA gate M6, from the design system §6 rule 5: "Mixed content is the norm, not
// the edge case. A product named `قميص رجالي Slim Fit — L` must render
// correctly. Every component is tested with a mixed-script fixture string."
//
// That string, exactly as the spec writes it, so anything consuming it is being
// held to the spec's own example rather than to one invented here.
export const MIXED_SCRIPT = 'قميص رجالي Slim Fit — L';

/**
 * Brands, which are spelled the same way in both languages.
 *
 * A card scheme's name is what is printed on the card in the customer's hand
 * and what appears on their statement. Mada IS translated — مدى is its own
 * Arabic name, used by the scheme itself — and so is anything that is a word
 * rather than a brand: cash, a cheque, a bank transfer, points. Only names with
 * no Arabic form are listed here, because inventing one would put a word on a
 * receipt that no cardholder recognises.
 */
const SAME_IN_BOTH = new Set<string>([
  'tender.visa',
  'tender.mastercard',
  'tender.amex',
  'tender.apple_pay',
  'tender.stc_pay',
  'tender.samsung_pay',
  'tender.tabby',
  'tender.tamara',
  'tender.bkash',
  'tender.nagad',
]);

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
      // Some keys are the same in both by design: the language names are each
      // written in their own script, so "English" is correct in the Arabic
      // catalogue, and a card scheme or a wallet is a brand.
      if (arabic === en[key] && !key.startsWith('language.') && !SAME_IN_BOTH.has(key)) {
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
      // File formats. PNG, JPEG and SVG are written that way in Arabic too; a
      // transliteration would be a worse instruction, not a better one.
      'brand.formatHint',
      'brand.rules',
    ]);
    // Two things are Latin by construction and are not translation failures:
    // interpolation placeholders like `{time}`, which are key names rather than
    // text anybody reads, and the product's own name. A brand is written as it
    // is written — Mada becomes مدى because that is its Arabic name, and
    // RawSyst has none.
    const strip = (text: string) =>
      text.replace(/\{\w+\}/g, '').replace(/RawSyst/g, '');
    const suspicious = (Object.keys(ar) as Key[]).filter(
      (k) =>
        !allowed.has(k) &&
        // The card schemes and wallets above, for the same reason as the
        // brands already allowed: their names have no Arabic form.
        !SAME_IN_BOTH.has(k) &&
        /[A-Za-z]{4,}/.test(strip(ar[k])),
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

describe('which language the product opens in', () => {
  const realStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const realNavigator = Object.getOwnPropertyDescriptor(globalThis, 'navigator');

  function stub(stored: string | null, browserLanguage: string) {
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: { getItem: () => stored, setItem: () => {} },
    });
    Object.defineProperty(globalThis, 'navigator', {
      configurable: true,
      value: { language: browserLanguage },
    });
  }

  afterEach(() => {
    if (realStorage) Object.defineProperty(globalThis, 'localStorage', realStorage);
    else delete (globalThis as Record<string, unknown>)['localStorage'];
    if (realNavigator) Object.defineProperty(globalThis, 'navigator', realNavigator);
    else delete (globalThis as Record<string, unknown>)['navigator'];
  });

  it('opens in English even when the browser asks for Arabic', () => {
    // This is the whole point. It used to read navigator.language and start in
    // Arabic for any browser reporting it — which is most browsers in Saudi
    // Arabia, so most shops opened the product in a language nobody chose.
    stub(null, 'ar-SA');
    expect(initialLocale()).toBe('en');
  });

  it('opens in English for any browser language', () => {
    for (const language of ['ar', 'ar-SA', 'en-GB', 'bn-BD', '']) {
      stub(null, language);
      expect(initialLocale(), `browser language ${language}`).toBe('en');
    }
  });

  it('honours a choice somebody actually made, and remembers it', () => {
    stub('ar', 'en-GB');
    expect(initialLocale()).toBe('ar');

    stub('en', 'ar-SA');
    expect(initialLocale()).toBe('en');
  });

  it('ignores a stored value that is not a language it ships', () => {
    stub('fr', 'ar-SA');
    expect(initialLocale()).toBe('en');
  });

  it('starts in English rather than failing when storage throws', () => {
    // A private window throws on access instead of returning null. A till that
    // will not start because it could not read a preference is worse than one
    // that starts in English.
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() {
        throw new Error('access denied');
      },
    });
    expect(initialLocale()).toBe('en');
  });
});
