/**
 * A type for the Next configuration, so a test can read it.
 *
 * `next.config.mjs` is JavaScript, deliberately — it is loaded by Next's own
 * loader before any TypeScript toolchain exists — and importing JavaScript from
 * a strict TypeScript project needs either `allowJs`, which would loosen the
 * whole project for one file, or this: eight lines saying what that one module
 * exports.
 *
 * The wildcard matches the specifier rather than the path, so it covers the
 * config from wherever it is imported and nothing else.
 */
declare module '*/next.config.mjs' {
  import type { NextConfig } from 'next';

  const config: NextConfig;
  export default config;
}
