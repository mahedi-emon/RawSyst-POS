// Checks this skill against the code it describes.
//
// A design-system skill is only useful while it is true. A token renamed, a
// class deleted, a file moved — any of those turns a confident reference into a
// confident lie, and an agent reading it will write markup for a system that no
// longer exists. So every class name, custom property and repository path the
// skill states in backticks is checked against the actual stylesheets and the
// actual tree.
//
// Run from the repository root:
//
//   node .claude/skills/rawsyst-design-system/verify.mjs
//
// Exits non-zero when the skill has drifted. Fix the skill, or fix the code —
// but do not leave them disagreeing.

import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const skillDir = dirname(fileURLToPath(import.meta.url));
const root = join(skillDir, '..', '..', '..');

/** Every stylesheet either app loads. */
const SHEETS = [
  'shared/src/design-system.css',
  'shared/src/dashboard/dashboard.css',
  'web/app/back-office.css',
  'pos/src/styles.css',
];

const definedClasses = new Set();
const definedTokens = new Set();
for (const f of SHEETS) {
  const css = readFileSync(join(root, f), 'utf8');
  for (const m of css.matchAll(/\.([a-zA-Z][\w-]*)/g)) definedClasses.add(m[1]);
  // A definition, not a use: `--x:` rather than `var(--x)`.
  for (const m of css.matchAll(/(--[a-z0-9-]+)\s*:/g)) definedTokens.add(m[1]);
}

/**
 * Names that carry no rules on purpose.
 *
 * Read out of the coverage test rather than repeated here, so the two lists
 * cannot disagree — the test is the authority on what is a legitimate hook.
 */
const hooksFile = readFileSync(
  join(root, 'shared/src/ui/stylesheetCoverage.test.ts'),
  'utf8',
);
const hooksBlock = hooksFile.match(/STYLING_HOOKS = new Set\(\[([\s\S]*?)\]\)/);
for (const m of (hooksBlock?.[1] ?? '').matchAll(/'([^']+)'/g)) {
  definedClasses.add(m[1]);
}

function walk(dir, out = []) {
  for (const e of readdirSync(dir)) {
    const full = join(dir, e);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (e.endsWith('.md')) out.push(full);
  }
  return out;
}

/** File extensions and hostnames that look like classes inside a backtick. */
const NOT_A_CLASS = new Set([
  'mjs', 'css', 'tsx', 'ts', 'json', 'md', 'test', 'ds',
  'com', 'dev', 'io', 'net', 'org', 'html', 'js', 'node', 'mcp', 'claude',
]);

const badClass = [];
const badToken = [];
const badPath = [];

for (const doc of walk(skillDir)) {
  const text = readFileSync(doc, 'utf8');
  const rel = doc.slice(root.length + 1).split('\\').join('/');

  for (const m of text.matchAll(/`\.([a-zA-Z][\w-]*)`/g)) {
    if (NOT_A_CLASS.has(m[1])) continue;
    if (!definedClasses.has(m[1])) badClass.push(rel + ': .' + m[1]);
  }
  for (const m of text.matchAll(/`(--[a-z0-9-]+)`/g)) {
    if (!definedTokens.has(m[1])) badToken.push(rel + ': ' + m[1]);
  }
  for (const m of text.matchAll(/`((?:shared|web|pos|e2e|docs)\/[A-Za-z0-9._/-]+)`/g)) {
    if (!existsSync(join(root, m[1]))) badPath.push(rel + ': ' + m[1]);
  }
}

let failed = 0;
for (const [title, list, hint] of [
  ['Classes the skill names that no stylesheet defines', badClass,
    'Either the class was removed, or the skill invented it.'],
  ['Custom properties the skill names that are not defined', badToken,
    'A modifier suffix written as `--foo` reads as a token; write the full class name.'],
  ['Repository paths the skill names that do not exist', badPath,
    'A file moved. Update the reference.'],
]) {
  const uniq = [...new Set(list)].sort();
  console.log('\n' + title + ': ' + uniq.length);
  if (uniq.length) {
    failed += uniq.length;
    console.log('  ' + hint);
    for (const l of uniq) console.log('  - ' + l);
  }
}

console.log(
  '\nChecked against ' + definedClasses.size + ' classes and ' +
  definedTokens.size + ' custom properties across ' + SHEETS.length + ' stylesheets.',
);
process.exit(failed ? 1 : 0);
