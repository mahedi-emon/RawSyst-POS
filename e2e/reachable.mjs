// Screens that exist and cannot be opened.
//
// The Accounting section was built, tested, translated into three languages and
// committed — six panels covering the financial statements, the accounting
// calendar, the VAT return and the audit trail — and its entry in the back
// office's navigation never landed. Everything worked. Nothing could reach it.
//
// That is the same shape as the shift service that was mounted on no route and
// the terminal that could never sell, and the backend has a test for its
// version of it: `TestEveryServiceTheServerHoldsIsReachableFromARoute`. This is
// the front end's.
//
// # What counts as a screen
//
// A component named `*Area.tsx` in `shared/src`. That suffix is the product's
// own word for a top-level section — `DashboardArea`, `StockArea`,
// `AccountingArea` — as opposed to a `*Panel` or a `*Screen`, which are pieces
// of one. A section that nothing mounts is a section nobody can open.
//
// Reported rather than failed, like the other lints here: a section built ahead
// of its navigation entry is a legitimate half-step, and the value is that
// somebody sees it rather than that a build stops.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === '.next' || entry === 'dist') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/Area\.tsx$/.test(entry)) out.push(full);
  }
  return out;
}

// Everywhere a section could be mounted from. Both front ends, because the till
// mounts its own and a section reachable from neither is the case being hunted.
const hosts = ['web/components', 'web/app', 'pos/src'];
let mounted = '';
for (const host of hosts) {
  for (const file of walkAll(host)) {
    mounted += readFileSync(file, 'utf8');
  }
}

function walkAll(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === '.next' || entry === 'dist') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walkAll(full, out);
    else if (/\.tsx?$/.test(entry)) out.push(full);
  }
  return out;
}

const orphans = [];
for (const file of walk('shared/src')) {
  const name = file.split(/[\\/]/).pop().replace(/\.tsx$/, '');
  // Rendered, not merely imported. A section can be imported by a barrel file
  // and still be unreachable, and `<Name` is what actually puts it on a screen.
  if (!mounted.includes('<' + name)) {
    orphans.push([name, file.split('\\').join('/')]);
  }
}

if (orphans.length === 0) {
  console.log('every section can be opened from somewhere');
} else {
  console.log(`\n${orphans.length} section(s) nothing mounts:\n`);
  for (const [name, file] of orphans) {
    console.log(`  ${name.padEnd(24)} ${file}`);
  }
  console.log(
    '\nA screen that exists and cannot be opened is the shape of bug this ' +
      'lint was written for.',
  );
}
