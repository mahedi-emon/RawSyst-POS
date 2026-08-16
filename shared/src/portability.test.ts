// The shared package must stay usable in a browser.
//
// It is consumed by two very different hosts: a Tauri desktop binary and a
// Next.js web app. The Tauri POS can reach a native keystore, a local SQLite
// file and an OS printer; the back office can reach none of those, and an
// import that assumed otherwise breaks the web build — usually at deploy time,
// because a bundler resolves it happily in development.
//
// So this is a structural test rather than a behavioural one. It reads the
// package's own source and asserts the boundary holds. It is cheap, it runs on
// every change, and it fails at the moment the mistake is made rather than
// three weeks later on a Friday.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

const root = fileURLToPath(new URL('.', import.meta.url));

function sourceFiles(dir = root): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) {
      out.push(...sourceFiles(path));
    } else if (/\.tsx?$/.test(entry) && !entry.endsWith('.test.ts')) {
      out.push(path);
    }
  }
  return out;
}

describe('the shared package', () => {
  const files = sourceFiles();

  it('has source to check', () => {
    // A guard on the guard: a broken glob that found nothing would make every
    // assertion below pass vacuously.
    expect(files.length).toBeGreaterThan(15);
  });

  it('imports nothing from Tauri', () => {
    // The back office has no native shell. A @tauri-apps import here compiles
    // in the POS and breaks the web build.
    const offenders = files.filter((f) =>
      readFileSync(f, 'utf8').includes('@tauri-apps'),
    );
    expect(offenders).toEqual([]);
  });

  it('reaches into neither host application', () => {
    // A relative path climbing out of the package would couple it to whichever
    // app happened to be next to it on disk.
    const offenders = files.filter((f) => {
      const src = readFileSync(f, 'utf8');
      return /from '\.\.\/\.\.\//.test(src) || src.includes("from '@rawsyst/web");
    });
    expect(offenders).toEqual([]);
  });

  it('does no arithmetic on money', () => {
    // Every figure is computed by the Go service from the journal. parseFloat
    // on an amount is how a screen starts disagreeing with the books — and
    // float64 cannot hold 0.15.
    const offenders = files.filter((f) => {
      const src = readFileSync(f, 'utf8');
      return /parseFloat\s*\(/.test(src);
    });
    expect(offenders).toEqual([]);
  });

  it('never reads a browser global at module scope', () => {
    // Next.js evaluates modules on the server during the build. A top-level
    // `window` or `localStorage` throws there, and the failure surfaces as an
    // opaque build error rather than as the line that caused it.
    const offenders: string[] = [];
    for (const file of files) {
      for (const line of readFileSync(file, 'utf8').split('\n')) {
        // Only unindented lines: anything inside a function or component body
        // runs in the browser and is fine.
        if (/^(const|let|var)\s+\w+\s*=\s*(window|document|localStorage)\b/.test(line)) {
          offenders.push(`${file}: ${line.trim()}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });
});
