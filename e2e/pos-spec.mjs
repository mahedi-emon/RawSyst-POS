// The numbers the POS screen spec fixes, checked against the stylesheet.
//
// `docs/ui-ux/01-screen-specs.md` §1 has a table headed "Non-negotiables". Each
// row is a number and the reason it is that number — a pay button that cannot
// be mis-tapped, a total read across a counter, cart lines read at arm's length
// while the cashier is also handling goods.
//
// Three of them were wrong when this was written, by 4px, 4px and 3px. Nobody
// would have noticed any of them by looking, which is exactly why they belong
// in a check rather than in a review: they are the kind of requirement that is
// specified precisely, implemented approximately, and then drifts.
//
//   node e2e/pos-spec.mjs
import { readFileSync } from 'node:fs';

// Comments are stripped first. Several of these declarations carry a note
// explaining the number, and a comment sitting between the brace and the
// property is enough to make a naive match miss it — which would report the
// code as wrong when the comment is the only thing that changed.
const strip = (css) => css.replace(/\/\*[\s\S]*?\*\//g, '');

const SYSTEM = strip(readFileSync('shared/src/design-system.css', 'utf8'));
const TILL = strip(readFileSync('pos/src/styles.css', 'utf8'));

/** The value of a custom property, from wherever it is defined. */
function token(name) {
  const m = SYSTEM.match(new RegExp(`--${name}:\\s*([^;]+);`));
  return m ? m[1].trim() : null;
}

/** A declaration's value inside a named rule. */
function declaration(css, selector, property) {
  const rule = css.match(
    new RegExp(`${selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*\\{([^}]*)\\}`),
  );
  if (!rule) return null;
  const m = rule[1].match(new RegExp(`(?:^|;)\\s*${property}:\\s*([^;]+)`));
  return m ? m[1].trim() : null;
}

/** rem to px at the 16px root the till uses. */
function px(value) {
  if (!value) return null;
  const rem = value.match(/([\d.]+)rem/);
  if (rem) return Math.round(parseFloat(rem[1]) * 16);
  const p = value.match(/([\d.]+)px/);
  return p ? Math.round(parseFloat(p[1])) : null;
}

const checks = [
  {
    what: 'touch targets on the till',
    spec: 56,
    got: px(token('tap-pos')),
    why: 'fast, repeated, sometimes with gloves',
  },
  {
    what: 'the pay button',
    spec: 72,
    got: px(declaration(TILL, '.button--large', 'min-block-size')),
    why: 'cannot be mis-tapped',
  },
  {
    what: 'cart lines',
    spec: 18,
    got: px(declaration(TILL, '.cart th, .cart td', 'font-size')),
    why: 'read at arm’s length, at speed',
  },
  {
    what: 'the running total',
    spec: 40,
    // The ceiling of the clamp: what it reaches on a till-sized screen.
    got: px(
      (declaration(TILL, '.totals__grand dd', 'font-size') ?? '').split(',').pop() ?? '',
    ),
    why: 'the one number the customer will ask about',
  },
];

let wrong = 0;
console.log('UI spec §1, "Non-negotiables":\n');
for (const c of checks) {
  const ok = c.got === c.spec;
  if (!ok) wrong++;
  console.log(
    `  ${ok ? 'ok  ' : 'WRONG'}  ${c.what.padEnd(26)} spec ${String(c.spec).padStart(3)}px  ` +
      `built ${c.got === null ? '  ?' : String(c.got).padStart(3)}px   (${c.why})`,
  );
}

if (wrong > 0) {
  console.log(
    `\n${wrong} of ${checks.length} do not match. Each is a number the spec ` +
      'fixes and gives a reason for; change the spec or change the code, but ' +
      'do not let them disagree quietly.',
  );
  process.exitCode = 1;
} else {
  console.log('\nevery number the spec fixes is the number that is built');
}
