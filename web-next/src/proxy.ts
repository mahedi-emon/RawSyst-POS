// Which origin may serve which half of this application.
//
// # What this is for
//
// RawSyst has two completely different audiences. A shop uses the business
// application; the software owner uses the platform control plane. They are one
// Next application today, and this is what makes them two ORIGINS without
// making them two builds.
//
//	app.example.com      the business application. /platform is 404 here.
//	console.example.com  the control plane. Everything else is 404 here.
//
// # Why a separate origin rather than only a separate path
//
// Cookies. The refresh cookie is set with **no `Domain` attribute**
// (`backend/internal/api/refresh_cookie.go`), which makes it *host-only*: the
// browser sends it back to exactly the host that set it and to no sibling
// subdomain. So an operator signing in at `console.` gets a session cookie that
// is structurally incapable of reaching `app.`, and a shop's cookie cannot
// reach the console.
//
// That is the session separation the control plane needs, and it costs nothing
// because it falls out of how cookies already work here. Nothing about
// `SameSite=Strict`, the CSRF pair or the `/api/v1/*` same-origin rewrite
// changes — both origins serve the same application, so both proxy the API on
// their own origin exactly as before. No CORS is introduced and no cookie
// attribute is weakened.
//
// # What this is NOT
//
// It is not the security boundary. `RequireSuperAdmin` in the Go API answers
// **404** to any caller who is not a platform operator, and that is what
// actually protects the control plane. This middleware stops the two user
// interfaces appearing on each other's hostname; it would be worthless on its
// own and is worth having on top.
//
// Nor does it separate the JavaScript bundle. Both origins are built from one
// application, so the platform route chunks exist in the deployment either way.
// They contain interface code and no data, every byte they could fetch is
// behind the API's 404, and separating them would mean a second build and a
// shared-component extraction — a large change for no change in what anybody
// can reach. `docs/SUPERADMIN-SEPARATION-PLAN.md` sets out that trade.
//
// # Unset means unchanged
//
// With `RAWSYST_CONSOLE_HOST` empty — a developer's machine, and every
// deployment that has not opted in — this does nothing at all and both halves
// stay reachable on one origin exactly as they were. Turning the separation on
// is a deployment decision, not an upgrade that changes behaviour underneath
// somebody.

// The file is `proxy.ts` rather than `middleware.ts`: Next 16 renamed the
// convention and warns on the old name at every build. The behaviour is
// identical and the export follows the file.

import { NextResponse, type NextRequest } from 'next/server';

import { servesPath } from '@/lib/origin';

// No `runtime` export: a proxy file always runs on the Node runtime, and Next
// refuses one that says so. That is exactly what this needs — `process.env` is
// read when the server starts rather than frozen into the bundle at build time,
// so one image serves any hostname and changing the console address does not
// need a rebuild.

/**
 * The hostname the control plane answers on.
 *
 * Deliberately not `NEXT_PUBLIC_`: the business application has no reason to
 * know this value, and a `NEXT_PUBLIC_` variable is compiled into every browser
 * bundle — which would publish the console's address to every shop, defeating
 * the point of not linking to it anywhere.
 */
const CONSOLE_HOST = process.env.RAWSYST_CONSOLE_HOST ?? '';

export function proxy(request: NextRequest) {
  // The `Host` header, not `nextUrl.hostname`.
  //
  // `nextUrl` is built from the server's own origin and reports `localhost`
  // inside a container however the request was addressed, so a rule written
  // against it treats every request as the business origin — which is exactly
  // what it did until a container test showed the console 404ing its own
  // pages. The unit tests could not have caught that; they are given a
  // hostname and cannot know where a real one comes from.
  //
  // Reading the header means a caller can claim any hostname. That is
  // acceptable here and is not a hole: a spoofed `Host` reveals the control
  // plane's INTERFACE and nothing behind it, because every `/api/v1/platform`
  // call answers 404 without a platform token. See the note at the top — this
  // decides which interface a hostname shows, never what anybody may do.
  const host = request.headers.get('host') ?? request.nextUrl.hostname;

  const allowed = servesPath(CONSOLE_HOST, host, request.nextUrl.pathname);
  if (allowed) return NextResponse.next();

  // 404, never a redirect, for the same reason `RequireSuperAdmin` answers 404
  // rather than 403: a redirect to the console would publish the console's
  // address to whoever guessed the path, and telling somebody where the door is
  // undoes most of not linking to it.
  //
  // A plain response rather than a rewrite into the application's own not-found
  // page: a rewrite depends on how the framework maps an unmatched path to a
  // status, which is a detail that changes between versions.
  return new NextResponse(null, {
    status: 404,
    headers: { 'x-robots-tag': 'noindex, nofollow' },
  });
}

export const config = {
  // Everything except what Next serves for itself. The check is two string
  // comparisons, but running it on every image request would still be waste.
  matcher: ['/((?!_next/static|_next/image).*)'],
};
