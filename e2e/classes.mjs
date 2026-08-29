// Which class names the product uses and no stylesheet defines.
//
// `.dialog`, `.dialog__backdrop` and `.dialog__body` were used by the
// terminal-revoke confirmation and defined nowhere, so the one dialog in the
// product that asks somebody to type a name before doing something
// irreversible rendered as unstyled markup in the corner of the page. Nothing
// reported it: a missing rule is not a build error, not a type error, and not
// a runtime error — it is simply a screen that looks wrong to whoever opens it.
//
// This reads the class names out of the components and out of the stylesheets
// and prints the difference. It is a lint, not a test: some names are built at
// runtime and some are legitimately styled by an ancestor, so it reports rather
// than fails.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

function walk(dir, match, out = []) {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === '.next' || entry === 'dist') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, match, out);
    else if (match.test(entry)) out.push(full);
  }
  return out;
}

const roots = ['shared/src', 'pos/src', 'web/app', 'web/components'];

const defined = new Set();
for (const root of roots) {
  for (const file of walk(root, /\.css$/)) {
    const css = readFileSync(file, 'utf8');
    for (const m of css.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) defined.add(m[1]);
  }
}

const used = new Map();
for (const root of roots) {
  for (const file of walk(root, /\.tsx$/)) {
    if (/\.test\.tsx$/.test(file)) continue;
    const src = readFileSync(file, 'utf8');
    // className="a b c" and className={`a ${x} b`} alike: every bare word
    // inside a className is a candidate.
    for (const m of src.matchAll(/className=(?:"([^"]*)"|\{`([^`]*)`\})/g)) {
      const text = (m[1] ?? m[2] ?? '').replace(/\$\{[^}]*\}/g, ' ');
      for (const name of text.split(/[\s'"?:]+/)) {
        if (!/^[a-z][\w-]*$/i.test(name)) continue;
        if (!used.has(name)) used.set(name, file);
      }
    }
  }
}

const missing = [...used.entries()]
  .filter(([name]) => !defined.has(name))
  .sort();

console.log(`${used.size} class names used, ${defined.size} defined in CSS`);
if (missing.length === 0) {
  console.log('every class the components use has a rule somewhere');
} else {
  console.log(`\n${missing.length} used and never defined:\n`);
  for (const [name, where] of missing) {
    console.log(`  .${name.padEnd(28)} ${where.replace(/\\/g, '/')}`);
  }
}

/* ---------------------------------------------------------------------------
 * Prose that is not a comment
 *
 * A CSS parser has no opinion about a selector it does not recognise. It skips
 * to the next declaration block and carries on, which means a comment that
 * closes itself one line early turns the paragraph after it into a selector —
 * and takes the rule that follows with it, silently.
 *
 * That shipped here. A section heading in the back-office stylesheet ended
 * with a star and a slash before its own prose began, so eleven lines of
 * English became a selector whose block turned out to be `@media (min-width:
 * 640px)`. The pin button that expands the navigation rail had therefore never
 * appeared on any tablet or desktop, on any screen, in any language. No build
 * error, no console warning, no failing test: just a control nobody could find
 * and nobody could explain.
 *
 * The shape is unmistakable and worth checking for directly: a line beginning
 * with `*` that the parser is not currently inside a comment for.
 * ------------------------------------------------------------------------- */

const orphaned = [];
for (const root of roots) {
  for (const file of walk(root, /\.css$/)) {
    const css = readFileSync(file, 'utf8');
    let inComment = false;
    css.split('\n').forEach((line, i) => {
      const trimmed = line.trim();
      // `*` is also the universal selector, which is legitimate CSS: `*,`,
      // `*::before`, `* { box-sizing: border-box; }`. Comment prose is a star
      // followed by a SPACE and then a word — never by a comma, a brace, a
      // colon or a pseudo-element.
      const looksLikeProse =
        /^\*\s+[A-Za-z(`"']/.test(trimmed) && !/^\*\s*[,{:]/.test(trimmed);
      if (!inComment && looksLikeProse) {
        orphaned.push([file, i + 1, trimmed.slice(0, 64)]);
      }
      // Track comment state across the line, which can open and close several.
      let pos = 0;
      for (;;) {
        if (inComment) {
          const close = line.indexOf('*/', pos);
          if (close === -1) break;
          inComment = false;
          pos = close + 2;
        } else {
          const open = line.indexOf('/*', pos);
          if (open === -1) break;
          inComment = true;
          pos = open + 2;
        }
      }
    });
  }
}

if (orphaned.length === 0) {
  console.log('\nno prose is being parsed as a selector');
} else {
  console.log(`\n${orphaned.length} line(s) outside a comment that look like comment prose:\n`);
  for (const [file, line, text] of orphaned) {
    console.log(`  ${file.split(String.fromCharCode(92)).join('/')}:${line}  ${text}`);
  }
  console.log('\nEverything from there to the next { is being parsed as a selector,');
  console.log('and the rule that { belongs to is being consumed with it.');
}
