// Refuses to cut a release the till should not carry.
//
// Everything here is a thing that builds perfectly, installs perfectly, and is
// wrong in a way nobody notices until it is on a shop counter: a placeholder
// icon, an installer with no publisher on it, an unsigned binary that Windows
// SmartScreen will warn a shopkeeper about.
//
// This is deliberately NOT part of `npm test`. The placeholder icon is the
// correct state of the repository today — a designed mark does not exist yet —
// and a suite that is red for a known, accepted reason stops being read. The
// gate belongs at the moment an installer is produced, which is the moment the
// placeholder stops being acceptable.
//
// Usage:
//   node scripts/release-check.mjs                 everything must be ready
//   node scripts/release-check.mjs --allow-unsigned  internal build, no cert
//
// Exit code 1 with a list of what is not ready, or 0 and silence.

import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..');

/**
 * The generated placeholder: a solid accent-green mark with a white band,
 * written by a script because `tauri-build` cannot link a Windows resource
 * without an icon present (P28).
 *
 * Identified by content rather than by filename, because the whole failure mode
 * is that the real mark is expected to arrive AT this path. A check on the name
 * would pass the moment a designer's file was dropped in — and also on the day
 * nobody dropped anything in.
 */
const PLACEHOLDER_ICON_SHA256 =
  'f55e47201fdd2620992fded08bc242435272965f4d91247c7bd451ecf93e04e3';

const allowUnsigned = process.argv.includes('--allow-unsigned');
const problems = [];

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

// --- the mark on the window and in the installer -------------------------

const iconPath = join(root, 'src-tauri', 'icons', 'icon.ico');
if (sha256(iconPath) === PLACEHOLDER_ICON_SHA256) {
  problems.push(
    'The app icon is still the generated placeholder.\n' +
      '  Put the designed mark somewhere and run:\n' +
      '    npm run icons -- path/to/mark.png\n' +
      '  It needs to be square and at least 1024x1024, with transparency.\n' +
      '  `tauri icon` writes the whole set from it, including this .ico.\n' +
      '  Then regenerate the installable web app icons from the same mark:\n' +
      '    web/public/icons/icon-192.png, icon-512.png, icon-512-maskable.png\n' +
      '  They are derived from this file, so while it is the placeholder they\n' +
      '  are too — and those are what a phone puts on its home screen.',
  );
}

// The installable app (blueprint A7) needs its icons present, or a phone
// declines to install it and says nothing useful about why.
for (const icon of ['icon-192.png', 'icon-512.png', 'icon-512-maskable.png']) {
  if (!existsSync(join(root, '..', 'web', 'public', 'icons', icon))) {
    problems.push(
      `web/public/icons/${icon} is missing. The web app manifest names it, ` +
        'and a manifest whose icons 404 is an app a phone will not install.',
    );
  }
}

// --- what the installer says it is ---------------------------------------

const config = JSON.parse(
  readFileSync(join(root, 'src-tauri', 'tauri.conf.json'), 'utf8'),
);
const bundle = config.bundle ?? {};

for (const [field, why] of [
  ['publisher', 'Add/Remove Programs shows this. Blank reads as untrustworthy.'],
  ['copyright', 'Shown in the file properties of the installed binary.'],
  ['shortDescription', 'Shown beside the entry in Add/Remove Programs.'],
]) {
  if (!bundle[field]) {
    problems.push(`bundle.${field} is not set. ${why}`);
  }
}

// The version a shop reports when something goes wrong has to be the version
// that is actually installed. Two files carry it and nothing keeps them
// together, so a release can silently ship a binary whose About box lies.
const pkg = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
if (pkg.version !== config.version) {
  problems.push(
    `package.json says version ${pkg.version} and tauri.conf.json says ` +
      `${config.version}. The About box and the installer would disagree.`,
  );
}

// --- signing --------------------------------------------------------------

// Tauri signs only when a certificate thumbprint reaches it. The thumbprint is
// deliberately absent from the committed config: it is injected at build time
// from a secret, so that a checkout of this repository cannot be used to
// produce something that appears to come from us.
const thumbprint =
  bundle.windows?.certificateThumbprint ?? process.env.RAWSYST_WINDOWS_CERT_THUMBPRINT;

if (!thumbprint) {
  const message =
    'The Windows installer will not be signed.\n' +
    '  An unsigned installer raises a SmartScreen warning that tells a\n' +
    '  shopkeeper this software is not commonly downloaded and may be unsafe.\n' +
    '  Set RAWSYST_WINDOWS_CERT_THUMBPRINT to the thumbprint of an installed\n' +
    '  code-signing certificate, or pass --allow-unsigned for an internal build.';
  if (allowUnsigned) {
    console.warn(`warning: ${message}\n`);
  } else {
    problems.push(message);
  }
}

if (thumbprint && !bundle.windows?.timestampUrl) {
  // Without a timestamp the signature dies with the certificate, and every
  // installer already in the field starts warning on the day it expires.
  problems.push(
    'A certificate is configured but bundle.windows.timestampUrl is not. ' +
      'An untimestamped signature stops being trusted when the certificate ' +
      'expires, including on copies already installed.',
  );
}

// --- verdict --------------------------------------------------------------

if (problems.length > 0) {
  console.error('This build is not ready to be released:\n');
  for (const p of problems) console.error(`  - ${p}\n`);
  process.exit(1);
}
