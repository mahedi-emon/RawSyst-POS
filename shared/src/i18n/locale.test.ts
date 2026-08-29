import { initialLocale } from './locale';
import { afterEach, describe, expect, it } from 'vitest';

import {
  ar,
  bn,
  catalogues,
  coverageOf,
  directionOf,
  en,
  interpolate,
  plain,
  type Key,
} from './strings';

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

/**
 * Names that are written in Latin letters in every language, Bangla included.
 *
 * Stripped rather than allow-listed by key. An allow-list of thirty-one keys
 * would have said "these particular sentences may contain English", which is
 * not the rule -- the rule is that ZATCA is called ZATCA, that their portal is
 * called Fatoora, and that a file format and a worked example of an invoice
 * number are quoted as they are printed. Naming the words says that; naming
 * the keys would let a genuinely untranslated sentence hide behind one of them
 * forever.
 *
 * The Arabic check above keeps its key allow-list because its list is four
 * entries long and its strings do not carry these names -- Arabic has its own
 * word for the authority.
 */
const PROPER_NOUNS =
  /RawSyst|ZATCA|Fatoora|JPEG|JPG|PNG|SVG|YYYY|MM|DD|Wave|Manufacturer|Model|Serial|Acme|Textiles|ACME|Noor|NOOR|Trading|LLC/g;

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
    // Bangla is left-to-right. Stated rather than assumed: the product now has
    // three languages and two directions, and the one that is not Arabic being
    // silently mirrored would be a hard failure to spot on a screenshot.
    expect(directionOf('bn')).toBe('ltr');
  });

  it('offers exactly the locales it has catalogues for', () => {
    expect(Object.keys(catalogues).sort()).toEqual(['ar', 'bn', 'en']);
  });

  it('says enough of the interface in Bangla', () => {
    // A floor rather than an equality.
    //
    // `bn` is `Partial` on purpose — see the comment above `catalogues`: it is
    // what lets a feature ship its English strings and be translated after,
    // instead of blocking the feature or filling the gap with English to get
    // past the compiler. The cost of that freedom is that coverage can drift
    // down one commit at a time with nobody noticing.
    //
    // So the number is asserted. It stood at 46% when Bangla was added and is
    // complete now; the floor is set just under, which fails on a real
    // regression and does not fail on the ordinary rounding of one new key.
    expect(coverageOf('bn')).toBeGreaterThan(0.98);

    // And every Bangla string is actually Bangla. A key present but left as
    // its English text compiles, counts towards coverage, and ships an English
    // sentence to a Bangladeshi shop. Brands and placeholders are the honest
    // exceptions, for the same reason they are in Arabic.
    const strip = (text: string) =>
      text.replace(/\{\w+\}/g, '').replace(PROPER_NOUNS, '');
    const english = (Object.keys(en) as Key[]).filter((k) => {
      const t = bn[k];
      if (t === undefined) return false;
      if (SAME_IN_BOTH.has(k) || k.startsWith('language.')) return false;
      return t === en[k] || /[A-Za-z]{4,}/.test(strip(t));
    });
    expect(english, 'Bangla strings still carrying English words').toEqual([]);
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

describe('bidi isolation of interpolated values', () => {
  // The design system (§6 rule 4) calls isolation on a currency or a number not
  // optional, and gives the failure it prevents: a total placed next to Arabic
  // text can visually reorder its digits.
  //
  // `.num` carries `unicode-bidi: isolate` for amounts that get an element of
  // their own. An amount interpolated INTO a sentence has no element — the
  // whole thing is one text node — so the fence has to be in the string, which
  // is what U+2068 and U+2069 are for.
  //
  // These characters are invisible and zero-width, which is exactly why they
  // need a test: nothing about looking at the screen tells you whether they
  // are there, and everything about looking at the screen tells you when they
  // are missing and the reader is in Arabic.
  const FSI = '\u2068';
  const PDI = '\u2069';

  it('fences every substituted value', () => {
    const out = interpolate('cost of goods sold rose by {amount}.', {
      amount: 'SAR 1,250.00',
    });
    expect(out).toBe(`cost of goods sold rose by ${FSI}SAR 1,250.00${PDI}.`);
  });

  it('fences a value that is not a number', () => {
    // A customer's name, a supplier's reference and a product code are all
    // mixed-script in a shop trading in two languages. Guessing which values
    // need fencing is how the one that does gets missed.
    const out = interpolate('{customer} has settled everything.', {
      customer: 'مؤسسة النخيل Trading',
    });
    expect(out).toBe(`${FSI}مؤسسة النخيل Trading${PDI} has settled everything.`);
  });

  it('fences a number given as a number', () => {
    expect(interpolate('{n} days', { n: 30 })).toBe(`${FSI}30${PDI} days`);
  });

  it('leaves an empty value alone rather than fencing nothing', () => {
    expect(interpolate('{a}b', { a: '' })).toBe('b');
  });

  it('leaves a placeholder nobody supplied as it found it', () => {
    expect(interpolate('{a} and {b}', { a: 'x' })).toBe(`${FSI}x${PDI} and {b}`);
  });

  it('adds nothing when there is nothing to substitute', () => {
    expect(interpolate('a plain sentence')).toBe('a plain sentence');
    expect(interpolate('a plain sentence', {})).toBe('a plain sentence');
  });

  it('round-trips through plain(), which is what a test asserts on', () => {
    const out = interpolate('rose by {amount}.', { amount: 'SAR 1,250.00' });
    expect(plain(out)).toBe('rose by SAR 1,250.00.');
    // And plain() is safe on text that was never fenced.
    expect(plain('rose by SAR 1,250.00.')).toBe('rose by SAR 1,250.00.');
  });

  // The marks are zero-width, so a length a person would count is unchanged.
  it('does not change what the sentence says', () => {
    const out = interpolate('{n} units', { n: 2 });
    expect(plain(out)).toBe('2 units');
    expect(out).toContain('2 units'.slice(2));
  });
});
