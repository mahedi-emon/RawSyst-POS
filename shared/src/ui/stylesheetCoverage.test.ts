import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, dirname, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/**
 * Every class a shared component names must exist in a stylesheet the browser
 * app loads.
 *
 * This bug has shipped three times. `box-sizing` was set only in the POS
 * stylesheet, then `.field`/`.field__input` were, then `.button` and the whole
 * sign-in screen. Each time the cause was identical: a component in `shared/`
 * is rendered by BOTH apps, but only the POS imports `pos/src/styles.css`, so
 * anything defined there alone renders bare in the browser app.
 *
 * It keeps surviving review because the POS looks perfect, and because a page
 * with no CSS still has no horizontal overflow — which is what the responsive
 * checks measure. Nothing fails; it just looks wrong, on the screens a reviewer
 * is least likely to open.
 *
 * So this is checked mechanically instead.
 */

const here = dirname(fileURLToPath(import.meta.url));
const sharedSrc = join(here, '..');
const repoRoot = join(sharedSrc, '..', '..');

/** Stylesheets the Next.js back office imports, per web/app/layout.tsx. */
const WEB_STYLESHEETS = [
  join(sharedSrc, 'design-system.css'),
  join(sharedSrc, 'dashboard', 'dashboard.css'),
  join(repoRoot, 'web', 'app', 'back-office.css'),
];

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) out.push(full);
  }
  return out;
}

/** Class names a component asks for, from className="…" and className={`…`}. */
function classesUsedIn(file: string): string[] {
  const text = readFileSync(file, 'utf8');
  const found: string[] = [];

  for (const match of text.matchAll(/className=(?:"([^"]*)"|\{`([^`]*)`\})/g)) {
    // A `${expr}` inside a template is a runtime value; drop it and keep the
    // literal parts around it.
    const blob = (match[1] ?? match[2] ?? '').replace(/\$\{[^}]*\}/g, ' ');
    for (const raw of blob.split(/\s+/)) {
      const cls = raw.trim();
      // A fragment ending in `--` is the stem of a modifier the code completes
      // at runtime (`attention__row--${severity}`). The concrete forms are
      // asserted by their own rules; the stem is not a class.
      if (cls && /^[a-zA-Z][\w-]*$/.test(cls) && !cls.endsWith('--')) found.push(cls);
    }
  }
  return found;
}

function classesDefinedIn(files: string[]): Set<string> {
  const defined = new Set<string>();
  for (const file of files) {
    const css = readFileSync(file, 'utf8');
    for (const match of css.matchAll(/\.([a-zA-Z][\w-]*)/g)) defined.add(match[1]);
  }
  return defined;
}

/**
 * Hooks that carry no rules of their own on purpose.
 *
 * Each is a second class on an element whose appearance comes from a `ds-`
 * primitive beside it — `<section className="ds-panel attention">`. They exist
 * to be targeted by descendant selectors and by tests, so an empty ruleset
 * would be noise. Anything added here should be a naming hook and nothing else.
 */
const STYLING_HOOKS = new Set([
  'attention', // beside ds-panel
  'attention__detail', // beside ds-body-sm ds-muted
  'setupw__panel', // beside ds-panel
  'rail__title', // inherits the rail's type
  'tmpl__form', // a labelled grouping, laid out by its children
]);

describe('stylesheet coverage', () => {
  const componentFiles = walk(sharedSrc);
  const webClasses = classesDefinedIn(WEB_STYLESHEETS);

  it('finds the shared components and the stylesheets', () => {
    expect(componentFiles.length).toBeGreaterThan(30);
    expect(webClasses.size).toBeGreaterThan(100);
  });

  it('defines every class shared components use in a stylesheet the web app loads', () => {
    const missing = new Map<string, string[]>();

    for (const file of componentFiles) {
      for (const cls of classesUsedIn(file)) {
        if (webClasses.has(cls) || STYLING_HOOKS.has(cls)) continue;
        const where = relative(repoRoot, file).replace(/\\/g, '/');
        const seen = missing.get(cls) ?? [];
        if (!seen.includes(where)) seen.push(where);
        missing.set(cls, seen);
      }
    }

    const report = [...missing.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([cls, files]) => `  .${cls} — used by ${files.join(', ')}`)
      .join('\n');

    expect(
      report,
      `These classes are used by shared components but defined in no stylesheet the\n` +
        `browser app imports, so they render bare there while looking correct in the\n` +
        `POS. Move the rules into shared/src/design-system.css (keeping any\n` +
        `till-specific sizing as an override in pos/src/styles.css), or add the name\n` +
        `to STYLING_HOOKS if it genuinely carries no rules:\n\n${report}\n`,
    ).toBe('');
  });
});
