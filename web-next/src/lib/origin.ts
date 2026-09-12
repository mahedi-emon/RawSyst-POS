// Which origin may serve which half of this application.
//
// The rule itself, separated from the Next proxy that applies it, so it
// can be tested as what it is: a pure decision about a hostname and a path.
// `src/proxy.ts` is a six-line wrapper around `servesPath`, and the file note
// there explains why the separation exists at all and what it does not protect.

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
  // Who built the product. Reachable from the account menu in both
  // workspaces, so both origins have to serve it.
  '/about',
  // The brand's own files: the logo variants, and the icons the manifest
  // names. `public/` is served from the application root and therefore passes
  // through this rule, which is why `/favicon.ico` and
  // `/manifest.webmanifest` are already listed below -- the icons the manifest
  // POINTS AT were not, so an operator on the console hostname got a manifest
  // whose every icon 404'd and an install prompt with no picture.
  '/brand/',
  '/icons/',
];

/** Files a browser asks for by name rather than by route. */
const SHARED_FILES = new Set([
  '/favicon.ico',
  '/manifest.webmanifest',
  '/robots.txt',
  '/sitemap.xml',
]);

/**
 * A note on `/api/`, which is passed through here in full.
 *
 * That is deliberate and it is not a gap. This rule governs which PAGES a
 * hostname serves; the API enforces its own half in
 * `backend/internal/api/host_policy.go`, where `/api/v1/platform/*` answers only
 * on the console and the business API only on the business hostname.
 *
 * Splitting the API here as well would put the same list in two languages for
 * the two to drift apart, and would achieve nothing: this runs in the web tier,
 * and anybody calling the API directly never passes through it. The rule has to
 * live where the routes are, so it does.
 */

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

  // The root, on the console, is served rather than 404'd.
  //
  // An operator types `console.example.com` into a browser, not
  // `console.example.com/platform`. Refusing the bare hostname would make the
  // console look broken to the one person it exists for.
  //
  // The page it lands on sends them to `/platform`. That is a LOCAL navigation
  // and not a jump to the console's own address, which would arrive back here
  // and go round again — see the note in `app/page.tsx`, which is where the
  // loop would otherwise be.
  if (onConsole && path === '/') return true;
  // The console serves the control plane and nothing else; every other origin
  // serves everything else and not the control plane.
  return onConsole === isPlatformPath(path);
}

/**
 * Whether two addresses are the same origin.
 *
 * Used to answer one question: are we already on the console? Sending somebody
 * to the console's address from the console is a loop with no exit, and the
 * page it would loop on is the one an operator always starts from.
 *
 * Compares scheme, hostname and port — the whole origin — because a console on
 * a port in development is the same site as itself and a different one from the
 * business application on another port.
 *
 * An address that will not parse answers `false`, which means "go there". That
 * is the safe direction: a wrong navigation is visible and recoverable, and a
 * wrong `true` would strand an operator on the business application with no way
 * through.
 */
export function isSameOrigin(a: string, b: string): boolean {
  try {
    return new URL(a).origin === new URL(b).origin;
  } catch {
    return false;
  }
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
