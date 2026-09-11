// Which backend routes the deployed back office can actually reach.
//
// # Why this is a checked-in script rather than a grep somebody runs
//
// The figure has been measured wrongly twice, and both times the wrong figure
// was believed. Counting `shared/src`, `pos/src` and `web-next/src` together
// reported 6 unreachable routes; counting `web-next/src` alone reported 27. The
// difference is not a rounding error — `shared/src/api/*` is the FROZEN `web/`
// front end's client layer and the Tauri till's, and `web-next` imports from
// `shared` in exactly three files, all of them `@rawsyst/shared/i18n/strings`.
// A route with a client function in `shared/src/api` and no screen in
// `web-next` is unreachable in the product that ships.
//
// So the tree is fixed here, in source, and cannot drift back.
//
// # What counts as reaching a route
//
// A call through the API client, with the same METHOD and a path that matches
// the route's pattern segment by segment. The method matters: `GET
// /settlement/batches` and `POST /settlement/batches` are two routes and a
// screen that only records a deposit has not reached the one that lists them.
//
// Paths are matched in three buckets, which is what the previous hand audits
// got wrong in both directions:
//
//   * written out whole — `'/settlement/pending'`
//   * with segments interpolated — `` `/orders/${id}/documents/${kind}` ``
//   * a pattern parameter matches either a literal segment or an interpolation,
//     because `/companies/{companyID}/branches` is reached by
//     `` `/companies/${id}/branches` ``
//
// An exact-string test reports every templated path as dead. A
// segments-appear-anywhere test reports nothing as dead. Neither is useful.
//
// # The generated contract is excluded, deliberately
//
// `src/lib/api/contract.generated.ts` lists every route pattern in the product.
// Scanning it would report everything as reached, which is the third way this
// measurement has been got wrong.
//
// Run:  node scripts/reachability.mjs [--json]

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const SRC = path.resolve(here, '../src');
const ROUTER = path.resolve(here, '../../backend/internal/api/router.go');

/** The one file whose whole content is route patterns. */
const EXCLUDED = new Set(['contract.generated.ts']);

// ---------------------------------------------------------------------------
// The routes, read from the Go table rather than from the generated copy
// ---------------------------------------------------------------------------

function parseRoutes(src) {
  const chunks = src.split(/\{http\.Method([A-Za-z]+),/).slice(1);
  const routes = [];
  for (let i = 0; i < chunks.length; i += 2) {
    const method = chunks[i].toUpperCase();
    const body = chunks[i + 1];
    const pattern = body.match(/"(.*?)"/);
    if (!pattern) continue;
    const access = body.match(/Access(Public|Authenticated|Permission|SuperAdmin)/);
    if (!access) continue;
    let permission = '';
    if (access[1] === 'Permission') {
      const rest = body.slice(access.index + access[0].length);
      const p = rest.match(/"(.*?)"/);
      permission = p ? p[1] : '';
    }
    routes.push({ method, pattern: pattern[1], access: access[1], permission });
  }
  return routes;
}

// ---------------------------------------------------------------------------
// The call sites, read out of web-next/src
// ---------------------------------------------------------------------------

/**
 * How each call form is spelled, and which HTTP method it makes.
 *
 * `useApi`, `useApiList` and `useNamedList` are all GETs through React Query.
 * `download` is a GET that returns bytes. Everything else names its own verb.
 */
// A type argument, which may itself be an inline object type. `[^()]` rather
// than `[^;()]`: `api.post<{ fiscal_year: number; periods_created: number }>(`
// is an ordinary call in this codebase, and a matcher that stopped at the
// semicolon inside the type reported the accounting calendar as unreachable.
const TYPE_ARG = String.raw`(?:<[^()]*?>)?`;

const CALL_FORMS = [
  [new RegExp(String.raw`\bapi\.get\s*${TYPE_ARG}\s*\(`, 'g'), 'GET'],
  [new RegExp(String.raw`\bapi\.download\s*${TYPE_ARG}\s*\(`, 'g'), 'GET'],
  [new RegExp(String.raw`\bapi\.post\s*${TYPE_ARG}\s*\(`, 'g'), 'POST'],
  [new RegExp(String.raw`\bapi\.put\s*${TYPE_ARG}\s*\(`, 'g'), 'PUT'],
  [new RegExp(String.raw`\bapi\.patch\s*${TYPE_ARG}\s*\(`, 'g'), 'PATCH'],
  [new RegExp(String.raw`\bapi\.delete\s*${TYPE_ARG}\s*\(`, 'g'), 'DELETE'],
  [new RegExp(String.raw`\buseApi\s*${TYPE_ARG}\s*\(`, 'g'), 'GET'],
  [new RegExp(String.raw`\buseApiList\s*${TYPE_ARG}\s*\(`, 'g'), 'GET'],
  [new RegExp(String.raw`\buseNamedList\s*${TYPE_ARG}\s*\(`, 'g'), 'GET'],
];

/**
 * `<ResourceList path={scope ? '/customers' : null}>`.
 *
 * The list primitive most read-only screens are built from. It calls
 * `useApiList` internally, so the read happens one file away from where the
 * path is written, and a scanner that only looked for the hook found none of
 * them. Matched tightly — the literal has to be the value of the prop rather
 * than merely the next string in the file — because this is the one form
 * loose enough to invent reachability that is not there.
 */
const PATH_PROP = /\bpath\s*[=:]\s*\{?[^,;)}\n]{0,80}?(['"`])(\/[^'"`\n]*)\1/g;

/**
 * Every path-shaped literal, whatever it is passed to.
 *
 * Screens routinely wrap the client in a local helper — `act('/payroll/${id}/approve')`
 * calls `api.post` two lines further up — and no amount of regex follows that
 * without a type checker. Attributing a VERB to those calls is not possible
 * here; recognising that a screen was built around the path is.
 *
 * So these form a second, weaker bucket. A route matched only this way is
 * reported separately rather than counted silently, because "somebody wrote
 * this path" is a weaker claim than "this exact route is called".
 */
const ANY_PATH = /(['"`])(\/[a-z][a-z0-9\-/${}.[\]_]*)\1/gi;

/**
 * Literals that are page routes rather than API paths.
 *
 * `router.push(`/customers/${c.id}`)` opens a screen. Counting it would report
 * `GET /api/v1/customers/{customerID}` as reached by a navigation, which is the
 * easiest way for this measurement to flatter itself.
 */
const NAVIGATION = /(?:router\.(?:push|replace|prefetch)\(|href[=:]\s*\{?|redirect\(|backHref[=:]\s*\{?)\s*$/;

/**
 * An `<img src>` that names the API directly.
 *
 * A company logo is fetched by the browser rather than by the client, because
 * it is an image and the route serves it to a cookie-authenticated request.
 * There is no `api.get` to find, and the file is unmistakably reached: it is
 * on the screen.
 */
const IMG_SRC = /src=\{?\s*[`'"]\/api\/v1(\/[^`'"\n]*)[`'"]/g;

/**
 * Comments, removed before the loose scan.
 *
 * This codebase explains itself at length and names routes while doing it —
 * `client.ts` mentions `/sync/push` in a comment about idempotency keys, and
 * counting that as a call reported the offline queue as reachable from the
 * back office, which it is not. A prose mention is documentation, not a
 * screen.
 */
function withoutComments(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/[^\n]*/g, '$1');
}

/**
 * The client's own calls, which name their method as an argument.
 *
 * Sign-in, sign-out and the silent refresh are made by `RawsystClient` itself
 * rather than by a screen, because the access token lives in memory inside it.
 * They are reached — by the login screen and by every page load — and a
 * scanner that only understood `api.post(...)` reported the front door as
 * unreachable.
 */
const SEND_FORM =
  /\.send\s*(?:<[^;()]*?>)?\s*\(\s*'(GET|POST|PUT|PATCH|DELETE)'\s*,\s*'(\/[^']*)'/g;

/** And the one bare fetch: the refresh that recovers a session on page load. */
const FETCH_FORM =
  /fetch\(\s*`\$\{API_BASE\}(\/[^`]*)`[\s\S]{0,80}?method:\s*'([A-Z]+)'/g;

/**
 * The first path literal after a call's opening bracket.
 *
 * Looks past a conditional, because `useApi(scope ? '/stores' : null)` is the
 * shape most reads in this app take: nothing is fetched until a company is
 * known.
 */
function pathAfter(text, from) {
  const window = text.slice(from, from + 400);
  const literal = /(['"`])(\/[^'"`\n]*)\1/.exec(window);
  return literal ? literal[2] : null;
}

function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name.startsWith('.')) continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (/\.tsx?$/.test(entry.name) && !EXCLUDED.has(entry.name)) out.push(full);
  }
  return out;
}

/** Every call the front end makes, as {method, path, file}. */
function callSites(files) {
  const calls = [];
  const loose = [];
  for (const file of files) {
    const text = fs.readFileSync(file, 'utf8');
    const code = withoutComments(text);
    for (const match of code.matchAll(ANY_PATH)) {
      const before = code.slice(Math.max(0, match.index - 24), match.index);
      if (NAVIGATION.test(before)) continue;
      loose.push({ path: match[2], file });
    }
    for (const match of code.matchAll(IMG_SRC)) {
      calls.push({ method: 'GET', path: match[1], file });
    }
    for (const [form, method] of CALL_FORMS) {
      form.lastIndex = 0;
      for (const match of text.matchAll(form)) {
        const found = pathAfter(text, match.index + match[0].length);
        if (found) calls.push({ method, path: found, file });
      }
    }
    for (const match of text.matchAll(PATH_PROP)) {
      calls.push({ method: 'GET', path: match[2], file });
    }
    for (const match of text.matchAll(SEND_FORM)) {
      calls.push({ method: match[1], path: match[2], file });
    }
    for (const match of text.matchAll(FETCH_FORM)) {
      calls.push({ method: match[2], path: match[1], file });
    }
  }
  return { calls, loose };
}

// ---------------------------------------------------------------------------
// Matching
// ---------------------------------------------------------------------------

/**
 * A path split into segments, with anything variable reduced to a wildcard.
 *
 * `{paymentID}` in a route pattern and `${payment.id}` in a template literal
 * are the same thing: a segment whose value is decided at runtime. A query
 * string is not part of the path.
 */
function segmentsOf(raw) {
  const withoutQuery = raw.split('?')[0].replace(/\/api\/v1/, '');
  return withoutQuery
    .split('/')
    .filter(Boolean)
    .map((seg) => {
      if (/^\{.*\}$/.test(seg)) return '*';
      if (seg.includes('${')) return '*';
      return seg;
    });
}

function reaches(callSegments, routeSegments) {
  if (callSegments.length !== routeSegments.length) return false;
  // The first segment is a literal in every route this API serves — `orders`,
  // `settlement`, `healthz` — so it must be a literal on the call too. Without
  // this a one-segment interpolation like `/${kind}` matched `/healthz`,
  // `/readyz`, `/metrics` and `/live` at once, and the audit quietly reported
  // four infrastructure probes as reached from a screen.
  if (callSegments[0] !== routeSegments[0]) return false;
  return routeSegments.every(
    (seg, i) => seg === '*' || callSegments[i] === '*' || seg === callSegments[i],
  );
}

// ---------------------------------------------------------------------------
// Intentionally screenless
// ---------------------------------------------------------------------------

/**
 * Routes with no screen ON PURPOSE, each with the reason.
 *
 * This list is the whole point of the exercise: "unreachable" is only a defect
 * when a person was supposed to be able to do the thing. A health check has no
 * screen because a load balancer reads it, not a shopkeeper.
 *
 * Every entry is `METHOD /path` and a sentence. Adding one is a decision, and
 * the sentence is what makes it reviewable.
 */
const SCREENLESS = {
  'GET /healthz': 'E — a liveness probe, read by the orchestrator',
  'GET /readyz': 'E — a readiness probe, read by the orchestrator',
  'GET /metrics': 'E — Prometheus scrapes it',
  'GET /api/v1/meta/version': 'E — the build a support engineer asks for',
  'GET /api/v1/meta/ping': 'B — a terminal asking whether it can sync',
  'GET /api/v1/live': 'B — the websocket a screen opens, not a screen',
  'POST /api/v1/store-credit/expire':
    'D — enqueued daily by the worker; a person expiring credit by hand is the bug',
  'DELETE /api/v1/stock/reservations/{orderID}':
    "C — B13's second sales channel releases its own hold, over the API",
  'POST /api/v1/payment-attempts/{attemptID}/refund':
    'B — a till action, taken at the counter with the customer present',
  'POST /api/v1/eosb/accrue':
    'D — a monthly charge run by the payroll job, so the liability is never discovered at termination',

  // --- the installed terminal, not the back office -----------------------
  //
  // These are the Tauri till's, and it is a different application: it holds a
  // device secret, an offline catalogue and a signing key, and every one of
  // these routes is resolved from the DEVICE rather than from a company a
  // browser names. A back-office session has no device secret and would be
  // refused, so building a screen for one here would be building a control
  // that cannot work.
  'POST /api/v1/devices/enrol':
    'B — a machine enrolling itself with a code somebody typed into it',
  'GET /api/v1/devices/identity':
    'B — the terminal asking who it is, before it has a session',
  'GET /api/v1/catalog/scan':
    'B — the till scans against its own offline snapshot; see lib/pos/cart.ts, which records why a round trip per scan is the wrong trade at a counter',
  'GET /api/v1/pos/stock':
    'B — the on-hand figures for the till, resolved from the device rather than from a company it names',
  // `/pos/stationery` was exempted alongside this one until the back office
  // grew a receipt screen, which reads the same letterhead to print an HTML
  // copy. It is no longer screenless, so it is no longer listed — the route
  // serves a till AND a browser, and tells them apart by `actor.IsDevice`.
  'PUT /api/v1/pos/sales/{invoiceID}/signed-document':
    'B — the terminal returning what it signed; the signing key never leaves the machine',
  'POST /api/v1/sync/push':
    'B — the offline queue draining after a terminal comes back online',
  'GET /api/v1/sync/health':
    'B — the terminal asking how far behind its own queue is',

  // --- other machines ----------------------------------------------------
  'POST /api/v1/backups/{backupID}/finish':
    'D — the backup agent reporting where it put the file and what its checksum is; a person typing a checksum would be attesting to something they did not compute',
  'POST /api/v1/stock/reservations':
    "C — B13's second sales channel holds stock over the API, and releases it the same way; the release is exempted just below for the same reason",
};

// ---------------------------------------------------------------------------

const routes = parseRoutes(fs.readFileSync(ROUTER, 'utf8'));
const files = walk(SRC);
const { calls, loose } = callSites(files);

const prepared = calls.map((c) => ({ ...c, segments: segmentsOf(c.path) }));
const preparedLoose = loose.map((c) => ({ ...c, segments: segmentsOf(c.path) }));

const unreached = [];
const byPathOnly = [];
for (const route of routes) {
  const routeSegments = segmentsOf(route.pattern);
  if (prepared.some(
    (c) => c.method === route.method && reaches(c.segments, routeSegments),
  )) {
    continue;
  }
  if (preparedLoose.some((c) => reaches(c.segments, routeSegments))) {
    byPathOnly.push(route);
    continue;
  }
  unreached.push(route);
}

const key = (r) => `${r.method} ${r.pattern}`;
const intended = unreached.filter((r) => SCREENLESS[key(r)]);
const gaps = unreached.filter((r) => !SCREENLESS[key(r)]);
const staleExemptions = Object.keys(SCREENLESS).filter(
  (k) => !unreached.some((r) => key(r) === k),
);

if (process.argv.includes('--json')) {
  console.log(
    JSON.stringify(
      {
        total: routes.length,
        files: files.length,
        calls: calls.length,
        attributed: routes.length - unreached.length - byPathOnly.length,
        viaHelper: byPathOnly.map(key),
        intended,
        gaps,
        staleExemptions,
      },
      null,
      2,
    ),
  );
} else {
  console.log(`routes in the Go table        ${routes.length}`);
  console.log(`files scanned in web-next/src ${files.length}`);
  console.log(`API calls found               ${calls.length}`);
  console.log(`reached, verb attributed      ${routes.length - unreached.length - byPathOnly.length}`);
  console.log(`reached through a local helper ${byPathOnly.length}`);
  console.log(`intentionally screenless      ${intended.length}`);
  console.log(`genuine gaps                  ${gaps.length}`);

  if (intended.length) {
    console.log('\nIntentionally screenless:');
    for (const r of intended) {
      console.log(`  ${key(r).padEnd(52)} ${SCREENLESS[key(r)]}`);
    }
  }
  if (process.argv.includes('--verbose') && byPathOnly.length) {
    console.log(
      '\nReached by a path a screen writes, through a wrapper this ' +
        'scanner cannot attribute a verb to:',
    );
    for (const r of byPathOnly) console.log(`  ${key(r)}`);
  }
  if (gaps.length) {
    console.log('\nNo screen reaches these, and nothing says why:');
    for (const r of gaps) {
      console.log(`  ${key(r).padEnd(52)} ${r.access}${r.permission ? ' ' + r.permission : ''}`);
    }
  }
  if (staleExemptions.length) {
    console.log('\nExemptions that are no longer needed — a screen now reaches these:');
    for (const k of staleExemptions) console.log(`  ${k}`);
  }
}

// A gap is a failure; an exemption nobody needs any more is one too, because a
// list of reasons that has stopped being true is how the next audit is misled.
process.exitCode = gaps.length > 0 || staleExemptions.length > 0 ? 1 : 0;
