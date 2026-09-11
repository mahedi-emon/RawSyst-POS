// Which origin may serve which half of this application.
//
// The rule itself, separated from the Next middleware that applies it, so it
// can be tested as what it is: a pure decision about a hostname and a path.
// `src/middleware.ts` is a six-line wrapper around `servesPath`, and the file
// note there explains why the separation exists at all and what it does not
// protect.

/** The control plane's own pages. */
export const PLATFORM_PREFIX = '/platform';

/**
 * Paths both origins must keep serving.
 *
 * The API proxy is the important one. The console signs in through the same
 * `/api/v1/auth/login` as everybody else and must reach it on its OWN origin,
 * or the `SameSite=Strict` refresh cookie is never sent. One authentication
 * path is deliberate: a second would be a second place to get MFA, lockout and
 * rate limiting wrong.
 *
 * `/login` and the password screens are here for the same reason — the console
 * needs a sign-in page and it is the same page.
 */
const SHARED_PREFIXES = [
  '/api/',
  '/_next/',
  '/login',
  '/logout',
  '/forgot-password',
  '/reset-password',
  '/change-password',
];

/** Files a browser asks for by name rather than by route. */
const SHARED_FILES = new Set([
  '/favicon.ico',
  '/manifest.webmanifest',
  '/robots.txt',
  '/sitemap.xml',
]);

/** Whether a path belongs to the control plane. */
export function isPlatformPath(path: string): boolean {
  return path === PLATFORM_PREFIX || path.startsWith(PLATFORM_PREFIX + '/');
}

function isShared(path: string): boolean {
  if (SHARED_FILES.has(path)) return true;
  return SHARED_PREFIXES.some((p) => path === p || path.startsWith(p));
}

/**
 * Whether `host` is allowed to serve `path`.
 *
 * `consoleHost` empty means the separation is not configured — a developer's
 * machine, and every deployment that has not opted in — and everything is
 * allowed exactly as it was. Turning the split on is a deployment decision, not
 * an upgrade that changes behaviour underneath somebody.
 *
 * Comparison is case-insensitive and ignores the port: `CONSOLE.example.com`
 * and `console.example.com:3000` are the same host, and a rule that disagreed
 * with the browser about that would fail in the confusing direction.
 */
export function servesPath(
  consoleHost: string,
  host: string,
  path: string,
): boolean {
  const configured = consoleHost.trim().toLowerCase();
  if (configured === '') return true;
  if (isShared(path)) return true;

  const onConsole = normaliseHost(host) === normaliseHost(configured);
  // The console serves the control plane and nothing else; every other origin
  // serves everything else and not the control plane.
  return onConsole === isPlatformPath(path);
}

/** A hostname without its port, lower-cased. */
export function normaliseHost(host: string): string {
  const trimmed = host.trim().toLowerCase();
  // An IPv6 literal is bracketed and its colons are not a port separator.
  if (trimmed.startsWith('[')) {
    const close = trimmed.indexOf(']');
    return close === -1 ? trimmed : trimmed.slice(0, close + 1);
  }
  const colon = trimmed.indexOf(':');
  return colon === -1 ? trimmed : trimmed.slice(0, colon);
}
