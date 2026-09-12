// Traces the official Biz1core logo and writes every file the product needs.
//
//	node scripts/brand-assets.mjs
//
// # What this is
//
// The supplied Biz1core artwork is a raster image. This turns it into one
// scalable drawing, and then projects that ONE drawing into the twenty-odd
// files a web application, an installable PWA and a Windows installer each
// need. There is no second logo anywhere: the favicon, the installer icon, the
// rail, the sign-in page and the README all come out of the constants below.
//
// # How the wordmark became paths
//
// The artwork sets "Biz1core" in a heavy geometric sans. Poppins ExtraBold is
// the closest open face to it -- the circular o/c/e, the flat-cut z, the
// flagged 1 with no foot -- and the two vendored TTFs under
// `shared/src/brand/fonts/` are converted to OUTLINES here.
//
// Outlines rather than live text, deliberately. A logo set as text is a
// different logo on a machine that lacks the font, and it cannot be a favicon
// or a Windows icon at all. As paths it is the same drawing in a 16px browser
// tab, on a sign-in page, in an installer and on paper.
//
// Both faces are SIL Open Font License; the licences sit beside them.
//
// # The coordinate space
//
// The artwork's own: cap height 260, baseline at y = 290, the B starting at
// x = 60. Every constant below was measured against it.

import { mkdir, writeFile } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import opentype from 'opentype.js';
import sharp from 'sharp';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const fonts = join(root, 'shared', 'src', 'brand', 'fonts');

function font(file) {
  const b = readFileSync(join(fonts, file));
  return opentype.parse(b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength));
}

const WORD_FONT = font('Poppins_800ExtraBold.ttf');
const TAG_FONT = font('IBMPlexSans_500Medium.ttf');

/* -------------------------------------------------------------------------- */
/* the drawing                                                                */
/* -------------------------------------------------------------------------- */

const BASELINE = 290;
const CAP = 260;
const WORD_X = 60;
/** Closed up a little, as the artwork is. */
const TRACKING = -6;

const SIZE = CAP / ((WORD_FONT.tables.os2.sCapHeight || 700) / WORD_FONT.unitsPerEm);

/**
 * The growth leaf: a crescent sweeping from the foot of the B up past its
 * shoulder, broad at the top and tapering to a point at the tail.
 *
 * Two quadratics and a cap. The outer edge bows up and to the left; the inner
 * edge returns lower and to the right, so the thickness is greatest near the
 * top and zero at the tail -- which is what the artwork does.
 */
const LEAF = 'M20 300Q52 122 292 20C312 12 330 34 318 58Q120 150 20 300Z';

/** Three ascending bars at the foot of the B. */
const BARS = [
  { x: 66, y: 218, w: 38 },
  { x: 114, y: 186, w: 38 },
  { x: 162, y: 152, w: 38 },
];
const BAR_R = 9;

/** The ink the leaf and the bars occupy, for laying out a crop. */
const MARK_BOX = { x1: 20, y1: 12, x2: 330, y2: 300 };

/**
 * The palette, read off the artwork.
 *
 * The dark row is not the light row darkened: navy on a dark rail is invisible
 * and the blue and teal go muddy, so each is lifted to the step that carries
 * against the product's darkest surface.
 */
const PALETTE = {
  light: { word: '#16213e', one: '#1273e6', leaf: '#2bb89a', bar: '#1fa483', tag: '#3c4a63' },
  dark: { word: '#f2f5f9', one: '#5aa2ff', leaf: '#3ed0ae', bar: '#35c39c', tag: '#c3cddd' },
  mono: { word: '#000', one: '#000', leaf: '#000', bar: '#000', tag: '#000' },
  monoInv: { word: '#fff', one: '#fff', leaf: '#fff', bar: '#fff', tag: '#fff' },
};

/** The tile behind the mark on an app icon: the wordmark's own navy. */
const TILE = '#16213e';

/* -------------------------------------------------------------------------- */
/* tracing                                                                    */
/* -------------------------------------------------------------------------- */

/** Text as outlines, one entry per glyph so the "1" can take its own colour. */
function trace(text, x, y, size, f, tracking = 0) {
  const list = [];
  let cursor = x;
  let box = null;
  const scale = size / f.unitsPerEm;
  for (const ch of text) {
    const g = f.charToGlyph(ch);
    if (ch !== ' ') {
      const p = g.getPath(cursor, y, size);
      const b = p.getBoundingBox();
      list.push({ ch, d: p.toPathData(2) });
      box = box
        ? {
            x1: Math.min(box.x1, b.x1),
            y1: Math.min(box.y1, b.y1),
            x2: Math.max(box.x2, b.x2),
            y2: Math.max(box.y2, b.y2),
          }
        : { x1: b.x1, y1: b.y1, x2: b.x2, y2: b.y2 };
    }
    cursor += g.advanceWidth * scale + tracking;
  }
  return { list, box, advance: cursor - x - tracking };
}

const WORD = trace('Biz1core', WORD_X, BASELINE, SIZE, WORD_FONT, TRACKING);
const TAGLINE_TEXT = 'One Solution for Complete Business Management.';
/** Set to the wordmark's width, so the lockup is one rectangle. */
const TAG_SIZE = 62;
const TAG = trace(TAGLINE_TEXT, WORD_X + 2, 392, TAG_SIZE, TAG_FONT);

/* -------------------------------------------------------------------------- */
/* fragments                                                                  */
/* -------------------------------------------------------------------------- */

const wordSvg = (c) =>
  WORD.list.map((g) => `<path d="${g.d}" fill="${g.ch === '1' ? c.one : c.word}"/>`).join('');

const markSvg = (c) =>
  `<path d="${LEAF}" fill="${c.leaf}"/>` +
  BARS.map(
    (b) =>
      `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${BASELINE - b.y}" rx="${BAR_R}" fill="${c.bar}"/>`,
  ).join('');

const tagSvg = (c) => TAG.list.map((g) => `<path d="${g.d}" fill="${c.tag}"/>`).join('');

/** The B alone, for the compact mark. */
const B_ONLY = trace('B', WORD_X, BASELINE, SIZE, WORD_FONT).list[0];

const BANNER = (what) =>
  `<!-- Biz1core ${what}. Generated by scripts/brand-assets.mjs from the official artwork; not edited by hand. -->`;

const M = 26; // margin around the ink

/** The full lockup, optionally with the tagline. */
function lockup(c, { tagline = false, mark = true, name }) {
  const x1 = (mark ? Math.min(MARK_BOX.x1, WORD.box.x1) : WORD.box.x1) - M;
  const y1 = (mark ? Math.min(MARK_BOX.y1, WORD.box.y1) : WORD.box.y1) - M;
  const x2 = Math.max(mark ? MARK_BOX.x2 : 0, WORD.box.x2, tagline ? TAG.box.x2 : 0) + M;
  const y2 = (tagline ? TAG.box.y2 : Math.max(mark ? MARK_BOX.y2 : 0, WORD.box.y2)) + M;
  const w = Math.round(x2 - x1);
  const h = Math.round(y2 - y1);
  const label = `Biz1core${tagline ? ` — ${TAGLINE_TEXT}` : ''}`;
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${Math.round(x1)} ${Math.round(y1)} ${w} ${h}" ` +
    `width="${w}" height="${h}" role="img" aria-label="${label}">` +
    `<title>${label}</title>${BANNER(name)}` +
    wordSvg(c) +
    (mark ? markSvg(c) : '') +
    (tagline ? tagSvg(c) : '') +
    `</svg>\n`
  );
}

/**
 * The compact mark: the B of the wordmark with its leaf and bars.
 *
 * A crop of the official logo, not a separate symbol. It carries a letter of
 * the wordmark and the growth mark, so a browser tab and an installer show the
 * same identity as the sign-in page rather than an unrelated icon.
 */
function compact(c, { tile = false, inset = 0, radius = 0.22, name }) {
  const x1 = Math.min(MARK_BOX.x1, WORD_X) - 6;
  const y1 = MARK_BOX.y1 - 6;
  const x2 = MARK_BOX.x2 + 6;
  const y2 = MARK_BOX.y2 + 6;
  const side = Math.max(x2 - x1, y2 - y1);
  const dx = (side - (x2 - x1)) / 2 - x1;
  const dy = (side - (y2 - y1)) / 2 - y1;

  const inner = `<path d="${B_ONLY.d}" fill="${c.word}"/>${markSvg(c)}`;

  if (!tile) {
    return (
      `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${Math.round(side)} ${Math.round(side)}" ` +
      `width="${Math.round(side)}" height="${Math.round(side)}" role="img" aria-label="Biz1core">` +
      `<title>Biz1core</title>${BANNER(name)}` +
      `<g transform="translate(${dx.toFixed(1)} ${dy.toFixed(1)})">${inner}</g></svg>\n`
    );
  }

  const pad = side * inset;
  const t = side + pad * 2;
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${Math.round(t)} ${Math.round(t)}" ` +
    `width="${Math.round(t)}" height="${Math.round(t)}" role="img" aria-label="Biz1core">` +
    `<title>Biz1core</title>${BANNER(name)}` +
    `<rect width="${Math.round(t)}" height="${Math.round(t)}" rx="${(t * radius).toFixed(1)}" fill="${TILE}"/>` +
    `<g transform="translate(${(dx + pad).toFixed(1)} ${(dy + pad).toFixed(1)})">${inner}</g></svg>\n`
  );
}

/** A wide social card: the lockup centred on the brand navy. */
function ogImage() {
  const l = lockup(PALETTE.dark, { tagline: true, name: 'social card' });
  const vb = l.match(/viewBox="([-\d]+) ([-\d]+) (\d+) (\d+)"/);
  const [, vx, vy, vw, vh] = vb.map(Number);
  const scale = Math.min((1200 * 0.76) / vw, (630 * 0.5) / vh);
  const x = (1200 - vw * scale) / 2 - vx * scale;
  const y = (630 - vh * scale) / 2 - vy * scale;
  const body = wordSvg(PALETTE.dark) + markSvg(PALETTE.dark) + tagSvg(PALETTE.dark);
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630" width="1200" height="630" ` +
    `role="img" aria-label="Biz1core — ${TAGLINE_TEXT}">` +
    `<title>Biz1core</title>${BANNER('social card')}` +
    `<rect width="1200" height="630" fill="${TILE}"/>` +
    `<g transform="translate(${x.toFixed(1)} ${y.toFixed(1)}) scale(${scale.toFixed(4)})">${body}</g></svg>\n`
  );
}

/* -------------------------------------------------------------------------- */
/* .ico                                                                       */
/* -------------------------------------------------------------------------- */

/**
 * A Windows icon carrying PNG images.
 *
 * ICO has taken PNG payloads since Vista, and every target here -- the Tauri
 * installer, the taskbar, a browser asking for /favicon.ico -- reads them.
 */
function ico(images) {
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(images.length, 4);

  const entries = [];
  let offset = 6 + images.length * 16;
  for (const { size, data } of images) {
    const e = Buffer.alloc(16);
    e.writeUInt8(size >= 256 ? 0 : size, 0); // 256 is stored as 0: the field is one byte
    e.writeUInt8(size >= 256 ? 0 : size, 1);
    e.writeUInt8(0, 2);
    e.writeUInt8(0, 3);
    e.writeUInt16LE(1, 4);
    e.writeUInt16LE(32, 6);
    e.writeUInt32LE(data.length, 8);
    e.writeUInt32LE(offset, 12);
    entries.push(e);
    offset += data.length;
  }
  return Buffer.concat([header, ...entries, ...images.map((i) => i.data)]);
}

/* -------------------------------------------------------------------------- */
/* the generated module the components render                                 */
/* -------------------------------------------------------------------------- */

function pathsModule() {
  const j = (v) => JSON.stringify(v);
  return `// GENERATED by scripts/brand-assets.mjs. Do not edit.
//
// The official Biz1core logo, traced from the supplied artwork into outlines.
// \`Logo.tsx\` renders exactly these paths, and so do every SVG, PNG and ICO
// under \`web-next/public/brand/\` -- which is what makes the rail, the sign-in
// page, the browser tab and the Windows installer the same logo rather than
// four similar ones.
//
// To change the logo, change the constants at the top of the generator and run
// it again. Editing this file by hand puts the component and the files out of
// step, which is the one failure a single source of truth exists to prevent.

/** The artwork's coordinate space. */
export const METRICS = {
  baseline: ${BASELINE},
  capHeight: ${CAP},
  /** The full lockup's ink, before any margin. */
  logoBox: { x1: ${Math.round(Math.min(MARK_BOX.x1, WORD.box.x1))}, y1: ${Math.round(Math.min(MARK_BOX.y1, WORD.box.y1))}, x2: ${Math.round(WORD.box.x2)}, y2: ${Math.round(Math.max(MARK_BOX.y2, WORD.box.y2))} },
  /** The wordmark alone. */
  wordBox: { x1: ${Math.round(WORD.box.x1)}, y1: ${Math.round(WORD.box.y1)}, x2: ${Math.round(WORD.box.x2)}, y2: ${Math.round(WORD.box.y2)} },
  /** The leaf and the bars. */
  markBox: ${j(MARK_BOX)},
  /** The B plus the leaf and bars -- the compact mark's square crop. */
  compactBox: { x1: ${Math.min(MARK_BOX.x1, WORD_X) - 6}, y1: ${MARK_BOX.y1 - 6}, x2: ${MARK_BOX.x2 + 6}, y2: ${MARK_BOX.y2 + 6} },
  /** The tagline's ink, when it is shown. */
  taglineBox: { x1: ${Math.round(TAG.box.x1)}, y1: ${Math.round(TAG.box.y1)}, x2: ${Math.round(TAG.box.x2)}, y2: ${Math.round(TAG.box.y2)} },
} as const;

/** Each glyph of "Biz1core". \`accent\` marks the 1, which takes the blue. */
export const WORDMARK: readonly { d: string; accent?: true }[] = [
${WORD.list.map((g) => `  { d: ${j(g.d)}${g.ch === '1' ? ', accent: true' : ''} },`).join('\n')}
];

/** The B alone, for the compact mark. */
export const LETTER_B = ${j(B_ONLY.d)};

/** The growth leaf. */
export const LEAF = ${j(LEAF)};

/** The three ascending bars, and their corner radius. */
export const BARS: readonly { x: number; y: number; w: number; h: number }[] = [
${BARS.map((b) => `  { x: ${b.x}, y: ${b.y}, w: ${b.w}, h: ${BASELINE - b.y} },`).join('\n')}
];
export const BAR_RADIUS = ${BAR_R};

/** The tagline, as outlines, so it is the same on any machine. */
export const TAGLINE_PATHS: readonly string[] = [
${TAG.list.map((g) => `  ${j(g.d)},`).join('\n')}
];
`;
}

/* -------------------------------------------------------------------------- */
/* run                                                                        */
/* -------------------------------------------------------------------------- */

const written = [];
async function put(rel, contents) {
  const path = join(root, rel);
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, contents);
  const size = Buffer.isBuffer(contents) ? contents.length : Buffer.byteLength(contents);
  written.push(rel);
  console.log(`  ${rel.padEnd(58)} ${String(size).padStart(7)} B`);
}

/** Rasterises at one size. The density keeps the leaf's curve smooth. */
const png = (svg, size) =>
  sharp(Buffer.from(svg), { density: 900 }).resize(size, size).png({ compressionLevel: 9 }).toBuffer();

const BRAND = 'web-next/public/brand';

console.log('Biz1core brand assets, from the official artwork\n');

// --- what the components render -------------------------------------------
await put('shared/src/brand/logo-paths.ts', pathsModule());

// --- scalable files --------------------------------------------------------
await put(`${BRAND}/biz1core-logo.svg`, lockup(PALETTE.light, { name: 'horizontal logo, light' }));
await put(`${BRAND}/biz1core-logo-dark.svg`, lockup(PALETTE.dark, { name: 'horizontal logo, dark' }));
await put(`${BRAND}/biz1core-logo-mono.svg`, lockup(PALETTE.mono, { name: 'horizontal logo, monochrome / print-safe' }));
await put(`${BRAND}/biz1core-logo-tagline.svg`, lockup(PALETTE.light, { tagline: true, name: 'logo with tagline, light' }));
await put(`${BRAND}/biz1core-logo-tagline-dark.svg`, lockup(PALETTE.dark, { tagline: true, name: 'logo with tagline, dark' }));
await put(`${BRAND}/biz1core-wordmark.svg`, lockup(PALETTE.light, { mark: false, name: 'wordmark only, light' }));
await put(`${BRAND}/biz1core-wordmark-dark.svg`, lockup(PALETTE.dark, { mark: false, name: 'wordmark only, dark' }));
await put(`${BRAND}/biz1core-mark.svg`, compact(PALETTE.light, { name: 'compact mark, light' }));
await put(`${BRAND}/biz1core-mark-dark.svg`, compact(PALETTE.dark, { name: 'compact mark, dark' }));
await put(`${BRAND}/biz1core-mark-mono.svg`, compact(PALETTE.mono, { name: 'compact mark, monochrome' }));

const appIcon = compact(PALETTE.dark, { tile: true, inset: 0.1, radius: 0.22, name: 'app icon' });
const maskable = compact(PALETTE.dark, { tile: true, inset: 0.26, radius: 0.5, name: 'app icon, maskable safe area' });
await put(`${BRAND}/app-icon.svg`, appIcon);
await put(`${BRAND}/app-icon-maskable.svg`, maskable);
await put(`${BRAND}/og-image.svg`, ogImage());
await put(`${BRAND}/og-image.png`, await sharp(Buffer.from(ogImage()), { density: 300 }).png().toBuffer());

// --- rasters, every size a platform asks for -------------------------------
const SIZES = [16, 32, 48, 64, 72, 96, 128, 144, 152, 180, 192, 256, 384, 512];
for (const s of SIZES) await put(`web-next/public/icons/icon-${s}.png`, await png(appIcon, s));
await put('web-next/public/icons/icon-512-maskable.png', await png(maskable, 512));
await put('web-next/public/icons/icon-192-maskable.png', await png(maskable, 192));

await put(
  'web-next/public/favicon.ico',
  ico([
    { size: 16, data: await png(appIcon, 16) },
    { size: 32, data: await png(appIcon, 32) },
    { size: 48, data: await png(appIcon, 48) },
  ]),
);

// --- the till: a desktop application with an installer ---------------------
await put(
  'pos/src-tauri/icons/icon.ico',
  ico([
    { size: 16, data: await png(appIcon, 16) },
    { size: 24, data: await png(appIcon, 24) },
    { size: 32, data: await png(appIcon, 32) },
    { size: 48, data: await png(appIcon, 48) },
    { size: 64, data: await png(appIcon, 64) },
    { size: 128, data: await png(appIcon, 128) },
    { size: 256, data: await png(appIcon, 256) },
  ]),
);
for (const s of [32, 128, 256, 512]) await put(`pos/src-tauri/icons/${s}x${s}.png`, await png(appIcon, s));
await put('pos/src-tauri/icons/icon.png', await png(appIcon, 512));
await put('pos/public/favicon.ico', ico([
  { size: 16, data: await png(appIcon, 16) },
  { size: 32, data: await png(appIcon, 32) },
]));
await put('pos/public/brand/app-icon.svg', appIcon);
await put('pos/public/brand/biz1core-logo-dark.svg', lockup(PALETTE.dark, { name: 'horizontal logo, dark' }));

// --- the previous back office ----------------------------------------------
//
// `web/` is not built, not deployed and not tested -- `web-next` is the
// product. Its three icons are regenerated anyway: they are the only other
// place in the repository that could still show a different mark, and leaving
// one stale copy is how "the same logo everywhere" quietly stops being true.
await put('web/public/icons/icon-192.png', await png(appIcon, 192));
await put('web/public/icons/icon-512.png', await png(appIcon, 512));
await put('web/public/icons/icon-512-maskable.png', await png(maskable, 512));
await put('web/public/favicon.ico', ico([
  { size: 16, data: await png(appIcon, 16) },
  { size: 32, data: await png(appIcon, 32) },
]));

console.log(`\n${written.length} files written.`);
