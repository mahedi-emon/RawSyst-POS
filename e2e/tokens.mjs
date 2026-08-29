// Values written into a stylesheet where a design token exists for them.
//
// A design system is only a system while every screen reaches for the same
// names. The moment one stylesheet writes `#1f6feb` and another writes
// `var(--brand)`, the two drift apart on the next change and nobody finds out
// until a person opens both screens side by side.
//
// This finds the drift without a browser: literal colours, literal pixel
// spacing on the 4px scale, and literal radii, in files that have the tokens
// available to them. It reports rather than fails — a one-off value is
// sometimes right, and the comment beside it is where that gets argued.
//
//   node e2e/tokens.mjs
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const ROOTS = ['shared/src', 'pos/src', 'web/app', 'web/components'];

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === '.next' || entry === 'dist') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/\.css$/.test(entry)) out.push(full);
  }
  return out;
}

/** The token block itself defines literals; that is its job. */
function withoutTokenBlocks(css) {
  return css.replace(/:root[^{]*\{[\s\S]*?\n\}/g, '');
}

/** Comments explain literals; they are not literals. */
function withoutComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '');
}

// Colours that are legitimately literal wherever they appear.
//
// `#fff` and `#000` at full or zero alpha are not palette decisions — they are
// the ends of the scale, used for a shadow's black or an overlay's white.
// `transparent` and `currentColor` are keywords.
const ALLOWED_COLOUR = /^(#fff|#ffffff|#000|#000000|transparent|currentcolor|inherit|none)$/i;

// A data URI carries its own colours; they are the icon, not the interface.
const DATA_URI = /url\(["']?data:/;

const SPACING = new Map([
  ['4px', '--space-1'],
  ['8px', '--space-2'],
  ['12px', '--space-3'],
  ['16px', '--space-4'],
  ['24px', '--space-5'],
  ['32px', '--space-6'],
]);

const RADIUS = new Map([
  ['6px', '--radius-sm'],
  ['8px', '--radius-md'],
  ['12px', '--radius-lg'],
]);

const findings = [];

for (const root of ROOTS) {
  for (const file of walk(root)) {
    const raw = readFileSync(file, 'utf8');
    const source = withoutComments(withoutTokenBlocks(raw));
    const lines = source.split('\n');

    lines.forEach((line, i) => {
      if (DATA_URI.test(line)) return;

      // A hex colour or an rgb()/hsl() call outside the token block.
      for (const m of line.matchAll(/#[0-9a-f]{3,8}\b/gi)) {
        if (ALLOWED_COLOUR.test(m[0])) continue;
        findings.push([file, i + 1, 'colour', m[0], line.trim().slice(0, 72)]);
      }

      // Spacing and radius written as pixels where a step exists.
      const decl = /(?:padding|margin|gap|inset|border-radius)[a-z-]*:\s*([^;]+);/gi;
      for (const d of line.matchAll(decl)) {
        const property = d[0].split(':')[0].trim();
        const table = /radius/.test(property) ? RADIUS : SPACING;
        for (const px of d[1].matchAll(/\b\d+px\b/g)) {
          const token = table.get(px[0]);
          if (!token) continue;
          findings.push([file, i + 1, property, `${px[0]} → var(${token})`, line.trim().slice(0, 72)]);
        }
      }
    });
  }
}

const path = (f) => f.split(String.fromCharCode(92)).join('/');

if (findings.length === 0) {
  console.log('every stylesheet reaches for the tokens');
} else {
  const byFile = new Map();
  for (const f of findings) {
    if (!byFile.has(f[0])) byFile.set(f[0], []);
    byFile.get(f[0]).push(f);
  }
  console.log(`${findings.length} literal value(s) where a token exists:\n`);
  for (const [file, rows] of byFile) {
    console.log(path(file));
    for (const [, line, kind, what, context] of rows) {
      console.log(`  ${String(line).padStart(4)}  ${kind.padEnd(16)} ${what}`);
      console.log(`        ${context}`);
    }
    console.log('');
  }
}
