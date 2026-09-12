// The product's identity, in one place.
//
// Every screen, e-mail, document, page title and manifest that names the
// product reads it from here. The reason is not tidiness: the previous name
// was written out by hand in i18n catalogues, in three page headers, in a PWA
// manifest, in a Tauri bundle and in a dozen Go strings, and a rename had to
// find all of them. It cannot be spelled inconsistently if it is only spelled
// once.
//
// The strings a customer reads in their own language still live in the i18n
// catalogues -- a catalogue entry can say "Biz1core cannot reach the server"
// in Arabic, which a constant cannot. What lives here is the brand itself:
// the name, the tagline, the owner, and the links.

/** The product name. Exact capitalisation: a capital B, a digit one, lower-case core. */
export const PRODUCT_NAME = 'Biz1core';

/** The official tagline. Carries its full stop -- it is a sentence. */
export const PRODUCT_TAGLINE = 'One Solution for Complete Business Management.';

/**
 * The tagline without its full stop, for places that set it as a label rather
 * than a sentence: a page title, an Open Graph title, a manifest.
 */
export const PRODUCT_TAGLINE_SHORT = 'Complete Business Management';

/** What a browser tab says on a page that has no title of its own. */
export const PRODUCT_TITLE = `${PRODUCT_NAME} | ${PRODUCT_TAGLINE_SHORT}`;

/** The till. Named separately because it is a different application, not a page. */
export const POS_NAME = `${PRODUCT_NAME} POS`;

/** The operator's own workspace, which is not a tenant's. */
export const CONSOLE_NAME = `${PRODUCT_NAME} Console`;

/**
 * What the product is, in one sentence, for a store listing or a meta
 * description. Deliberately about the work rather than the technology.
 */
export const PRODUCT_DESCRIPTION =
  'Run the whole business from one place: selling, stock, buying, money and people.';

/** Who built it. Used in the About panel, the footer and the README. */
export const OWNER = {
  name: 'Mahedi Hasan Emon',
  role: 'Founder, Owner & Lead Developer',
  website: 'https://mahedihasanemon.site/',
  linkedin: 'https://www.linkedin.com/in/mahediemon/',
  github: 'https://github.com/mahedi-emon',
} as const;

/** The public repository. */
export const REPOSITORY_URL = 'https://github.com/mahedi-emon/Biz1core';

/** The year the product was first built, for a copyright line. */
export const COPYRIGHT_SINCE = 2026;

/**
 * The copyright line for a bundle, an installer or a footer.
 *
 * Takes the year rather than reading the clock, so a build is reproducible and
 * a snapshot test does not fail on New Year's Day.
 */
export function copyrightLine(year: number = COPYRIGHT_SINCE): string {
  const span = year > COPYRIGHT_SINCE ? `${COPYRIGHT_SINCE}–${year}` : `${year}`;
  return `© ${span} ${OWNER.name}`;
}
