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
