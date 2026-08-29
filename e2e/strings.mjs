// User-facing English that never reaches the catalogue.
//
// Requirement: no hardcoded user-facing strings. A source scan cannot see
// everything — text built at runtime, text in a data table — but it catches the
// two shapes that actually occur here: a bare string passed as a prop that
// renders as prose, and a template literal that stitches English round a value.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

function walk(dir, out = []) {
  for (const e of readdirSync(dir)) {
    if (e === 'node_modules' || e === '.next' || e === 'dist') continue;
    const full = join(dir, e);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/\.tsx$/.test(e) && !/\.test\.tsx$/.test(e)) out.push(full);
  }
  return out;
}

// Props whose value is shown to a person.
const PROSE = /\b(title|body|subtitle|label|backLabel|placeholder|hint|heading|caption|message|aria-label|alt|summary|name)=(?:"([^"]{4,})"|\{`([^`]{4,})`\})/g;

// Any JSX text node with two or more Latin words.
const TEXT = />\s*([A-Z][a-z]+(?: [a-zA-Z][\w'’-]*){1,})\s*</g;

let n = 0;
for (const root of ['shared/src', 'pos/src', 'web/components']) {
  for (const file of walk(root)) {
    const src = readFileSync(file, 'utf8');
    const lines = src.split('\n');
    const hits = [];
    for (const m of src.matchAll(PROSE)) {
      const text = m[2] ?? m[3] ?? '';
      if (!/[a-z]{2}.*\s/.test(text)) continue;      // one word: usually a key or id
      if (/^\$\{[^}]*\}$/.test(text)) continue;       // purely interpolated
      hits.push([src.slice(0, m.index).split('\n').length, m[0].slice(0, 90)]);
    }
    for (const m of src.matchAll(TEXT)) {
      const line = src.slice(0, m.index).split('\n').length;
      const around = lines[line - 1] ?? '';
      if (/\/\/|\/\*|\*/.test(around.trim().slice(0, 2))) continue;
      hits.push([line, m[1].slice(0, 90)]);
    }
    if (hits.length) {
      console.log(String.fromCharCode(10) + file.split(String.fromCharCode(92)).join('/'));
      for (const [l, s] of hits) { console.log(`  ${String(l).padStart(4)}  ${s}`); n++; }
    }
  }
}
console.log(`\n${n} candidates`);
