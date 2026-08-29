// A screen that shows money and never says what currency it is in.
//
// The rule is per SCREEN, not per figure. Repeating "SAR" on every cell of a
// column is noise, and no commercial system does it: a ledger names its
// currency once — in a total, a column header, a document field — and the cells
// beneath are read in it. So a bare `money(x)` is not itself a defect.
//
// A FILE in which every `money()` call is bare is a different thing. Nothing on
// that screen ever says which currency it is, and the reader is left to assume
// — which works exactly as long as the product is sold into one country. This
// one is sold into three.
//
// The check found five such screens: card settlement, the expense categories,
// the customers list, the receipt form and the dashboard's sparkline label,
// which is what a screen reader is told and had no currency in it at all.
//
//   node e2e/currency.mjs
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const ROOTS = ['shared/src', 'pos/src', 'web/components'];

// Screens that render money and legitimately never name a currency, with the
// reason the reader already knows it.
const NAMED_ELSEWHERE = new Map([
  [
    'shared/src/purchasing/purchasing.ts',
    'not a screen: receiptNotice returns one sentence for the receiving page, ' +
      'which is one company and one base currency, and a code inside it would ' +
      'be the only one on the page',
  ],
  [
    'shared/src/purchasing/BillForm.tsx',
    'a form for entering amounts, not for reading them: the figures are what ' +
      'the person is typing, in the currency they are typing in, and the bill ' +
      'they produce shows the code on every screen that reads it back',
  ],
]);

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === '.next' || entry === 'dist') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) out.push(full);
  }
  return out;
}

const path = (f) => f.split(String.fromCharCode(92)).join('/');

/** Does this `money(` call pass a second argument at the top level? */
function hasCurrency(src, from) {
  let depth = 1;
  for (let i = from; i < src.length && depth > 0; i++) {
    const c = src[i];
    if (c === '(' || c === '{' || c === '[') depth++;
    else if (c === ')' || c === '}' || c === ']') depth--;
    else if (c === ',' && depth === 1) return true;
  }
  return false;
}

let calls = 0;
let bare = 0;
const silent = [];

for (const root of ROOTS) {
  for (const file of walk(root)) {
    // Comments are stripped first. The catalogue quotes `{money(...)} returned`
    // in the note explaining why that key exists, and a check that reads its
    // own bug report as three unlabelled figures is a check nobody trusts.
    const src = readFileSync(file, 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/^[^\S\n]*\/\/.*$/gm, '');
    let here = 0;
    let named = 0;
    for (const m of src.matchAll(/\bmoney\(/g)) {
      here++;
      if (hasCurrency(src, m.index + m[0].length)) named++;
    }
    if (here === 0) continue;
    calls += here;
    bare += here - named;
    if (named === 0) silent.push([path(file), here]);
  }
}

console.log(`${calls} money() call sites, ${bare} of them bare\n`);

let unexplained = 0;
for (const [file, n] of silent) {
  const why = NAMED_ELSEWHERE.get(file);
  if (why) {
    console.log(`${file}  (${n} figures, none with a currency)\n  by design: ${why}\n`);
  } else {
    unexplained++;
    console.log(`${file}  <-- ${n} figures and NOT ONE names a currency\n`);
  }
}

if (unexplained > 0) {
  console.log(
    `${unexplained} screen(s) show money and never say which.\n` +
      'Name it once — on a total, a column, a document field — or record why ' +
      'the reader already knows.',
  );
  process.exitCode = 1;
} else {
  console.log('every screen that shows money says which currency it is in');
}
