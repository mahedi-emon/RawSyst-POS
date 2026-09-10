// Every catalogue at once, and the coverage figure derived from them.
//
// # Why this is a separate module and not part of `strings.ts`
//
// Naming all three catalogues is exactly what a first load must NOT do -- see
// the note at the top of `strings.ar.ts`. Anything importing this file gets
// English, Arabic and Bangla in one chunk, which is right for the two callers
// that genuinely need to compare them and wrong for a screen.
//
// The callers are the frozen `web/` provider, which switches language without
// a bundler split, and the tests that measure coverage. `web-next` imports
// neither this file nor the two catalogues directly; it loads one on demand.

import { en, type Key, type Locale } from './strings';
import { ar } from './strings.ar';
import { bn } from './strings.bn';

export { ar, bn };

/**
 * The catalogues, by locale.
 *
 * # Why `ar` is exhaustive and `bn` is not
 *
 * `ar` is `Record<Key, string>`: a key added to `en` without an Arabic string
 * is a compile error. That is the right contract for a language the product was
 * designed in — blueprint E1.5 and G3 make Arabic a first-class mirror rather
 * than a translation layer, and a Saudi shop reading an English sentence in the
 * middle of its own invoice screen is a defect, not a gap.
 *
 * A third language cannot work that way and no real product pretends it can.
 * Twelve hundred strings do not arrive in one commit; they arrive in batches,
 * usually from somebody who is not the person adding the feature. A contract
 * that refuses to compile until every last one is done has exactly two
 * outcomes, and both are worse than a gap: the language is never added, or a
 * thousand strings get filled in with English to make the build pass — which
 * is the same gap with the evidence deleted.
 *
 * So `bn` is partial and the gap is VISIBLE: `coverageOf` measures it,
 * `locale.test.ts` holds it to a floor, and anything not yet translated falls
 * back to English rather than to a key or a blank. A Bangladeshi shop sees its
 * own language everywhere the words have been written, and English — a language
 * their accountant reads — everywhere they have not.
 *
 * As of this pass the gap is closed: every key in `en` has a Bangla string.
 * The type stays `Partial` all the same, and that is deliberate — it is what
 * lets the NEXT feature ship its strings in English and be translated after,
 * rather than blocking the feature or filling the catalogue with English to
 * get past the compiler. The floor in `locale.test.ts` is what holds the line;
 * the type is what keeps the door open.
 *
 * Adding a fourth language is now: add it to `Locale`, add a partial catalogue,
 * add it to `LOCALES`. Nothing else in the product changes.
 */
export const catalogues: Record<Locale, Partial<Record<Key, string>>> = {
  en,
  ar,
  bn,
};

/**
 * What proportion of the interface a language actually says in its own words.
 *
 * Reported rather than assumed. A number nobody measures drifts down every
 * time a feature adds a string, and the first person to notice is a shopkeeper.
 */
export function coverageOf(locale: Locale): number {
  const table = catalogues[locale];
  const keys = Object.keys(en) as Key[];
  const said = keys.filter((k) => {
    const v = table[k];
    return typeof v === 'string' && v.trim() !== '';
  }).length;
  return said / keys.length;
}
