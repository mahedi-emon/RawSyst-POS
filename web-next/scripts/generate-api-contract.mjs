// Reads the Go route table and emits the frontend's copy of it.
//
// The backend's `Server.Routes()` is the only authoritative statement of what
// this product can do and who may do it. Writing a second list by hand in
// TypeScript would produce two answers to one question, and the TypeScript one
// would be wrong first -- a route added in Go would simply be missing here, and
// a permission renamed in Go would leave a guard checking a string nobody
// enforces any more.
//
// So the list is generated, and `npm run check:contract` fails the build when
// the generated file no longer matches the Go source. That makes drift a red
// CI run rather than a permission check that silently passes.
//
// Run:  node scripts/generate-api-contract.mjs [--check]

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const ROUTER = path.resolve(here, '../../backend/internal/api/router.go');
const ENTITLEMENT = path.resolve(here, '../../backend/internal/api/entitlement.go');
const OUT = path.resolve(here, '../src/lib/api/contract.generated.ts');

/** Parse the `Route{...}` literals out of the Go route table. */
function parseRoutes(src) {
  // Entries begin `{http.MethodGet, "/api/v1/...", AccessPermission, "perm",`.
  // Splitting on the method keyword gives one chunk per entry, which survives
  // the multi-line formatting gofmt produces for the longer rows.
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

/** Parse the plan-feature gate: which module a route group is sold under. */
function parseFeatures(src) {
  const block = src.match(/featureOfRoute = map\[string\]string\{([\s\S]*?)\n\}/);
  if (!block) return {};
  const out = {};
  for (const m of block[1].matchAll(/"([^"]+)":\s*"([^"]+)"/g)) out[m[1]] = m[2];
  return out;
}

/**
 * Every permission the product uses, not only the route-gated ones.
 *
 * The route table answers "what does this URL require", and for a while that
 * was taken to be the whole list. Validating against a real server showed it is
 * not: an Owner resolves to 109 permissions where the route table names 102.
 *
 * The other seven -- `sales.discount`, `sales.hold`, `sales.void_draft`,
 * `catalog.view_cost_price`, `catalog.view_profit_margin`, `accounting.approve`,
 * `compliance.retry_submission` -- are real, seeded to roles, and returned by
 * `GET /auth/me`. They are enforced STRUCTURALLY rather than per route: cost
 * price never crosses the catalogue boundary at all, and cost documents sit
 * behind `purchasing.*`. They are exactly the right signal for an action or a
 * column in the UI, so the frontend has to know their names -- and a `Can`
 * checking `sales.discount` must not fail to compile because the route table
 * has never heard of it.
 *
 * Both sources are parsed. `ROUTE_PERMISSIONS` keeps the distinction, because a
 * permission that gates no route cannot protect a route and must never be used
 * as if it could.
 */
function parseSeededPermissions(migrationsDir) {
  const found = new Set();
  for (const file of fs.readdirSync(migrationsDir)) {
    if (!file.endsWith('.sql')) continue;
    const sql = fs.readFileSync(path.join(migrationsDir, file), 'utf8');
    // Two shapes, both two-string tuples:
    //   permission_catalogue: ('sales.view', 'selling', 'label', ...)
    //   role grants:          ('owner', 'sales.view')
    // A permission always contains a dot and a role key never does, so the
    // dotted member of the pair is the permission whichever way round it sits.
    for (const m of sql.matchAll(/\(\s*'([a-z_]+(?:\.[a-z_]+)?)'\s*,\s*'([a-z_]+(?:\.[a-z_]+)?)'/g)) {
      for (const candidate of [m[1], m[2]]) {
        if (/^[a-z_]+\.[a-z_]+$/.test(candidate)) found.add(candidate);
      }
    }
  }
  return found;
}

const routes = parseRoutes(fs.readFileSync(ROUTER, 'utf8'));
const features = parseFeatures(fs.readFileSync(ENTITLEMENT, 'utf8'));

if (routes.length === 0) {
  console.error('No routes parsed from router.go. The table shape has changed.');
  process.exit(1);
}

const routePermissions = [
  ...new Set(routes.filter((r) => r.permission).map((r) => r.permission)),
].sort();

const MIGRATIONS = path.resolve(here, '../../backend/internal/platform/db/migrations');
const seeded = parseSeededPermissions(MIGRATIONS);

// Anything seeded that names a module the routes already know. That filter is
// what keeps unrelated dotted strings in migration text -- a table name, a
// setting key -- out of the permission list.
const modulesInUse = new Set(routePermissions.map((p) => p.split('.')[0]));
const actionPermissions = [...seeded]
  .filter((p) => modulesInUse.has(p.split('.')[0]) && !routePermissions.includes(p))
  .sort();

const permissions = [...routePermissions, ...actionPermissions].sort();

// The permission catalogue groups permissions into sections written in the
// language a shop owner uses -- selling, buying, money, stock. Those sections
// are seeded in SQL and are the right grouping for navigation, so the prefix
// map below is derived from the permission names themselves and checked against
// the catalogue by the section test rather than invented here.
const modules = [...new Set(permissions.map((p) => p.split('.')[0]))].sort();

const banner = `// GENERATED by scripts/generate-api-contract.mjs -- do not edit.
//
// Sources: backend/internal/api/router.go (${routes.length} routes,
// ${routePermissions.length} route-gated permissions); the permission catalogue and
// role grants seeded in db/migrations (${actionPermissions.length} further
// action-level permissions); backend/internal/api/entitlement.go (the plan gate).
//
// Regenerate:  npm run gen:contract
// Verify:      npm run check:contract
`;

const body = `${banner}
/** Every permission the product enforces: route-gated and action-level alike. */
export const PERMISSIONS = ${JSON.stringify(permissions, null, 2)} as const;

export type Permission = (typeof PERMISSIONS)[number];

/**
 * The subset that gates a ROUTE.
 *
 * A route guard may only name one of these. The rest are action- and
 * field-level permissions enforced structurally by the backend -- they are the
 * right signal for hiding a discount button or a cost column, and the wrong
 * thing to protect a URL with, because no URL checks them.
 */
export const ROUTE_PERMISSIONS = ${JSON.stringify(routePermissions, null, 2)} as const;

export type RoutePermission = (typeof ROUTE_PERMISSIONS)[number];

/** The module half of every permission -- \`sales\` from \`sales.refund\`. */
export const PERMISSION_MODULES = ${JSON.stringify(modules, null, 2)} as const;

export type PermissionModule = (typeof PERMISSION_MODULES)[number];

/**
 * The plan feature a route group is sold under, if any.
 *
 * A caller without the feature is refused with 402 by the backend regardless of
 * permission, so navigation reads this to explain why a module is unavailable
 * rather than presenting a link that leads to a refusal.
 */
export const FEATURE_OF_ROUTE_GROUP: Readonly<Record<string, string>> =
  ${JSON.stringify(features, null, 2)};

export interface RouteSpec {
  readonly method: string;
  readonly pattern: string;
  readonly access: 'Public' | 'Authenticated' | 'Permission' | 'SuperAdmin';
  readonly permission: string;
}

/** The whole route table, for the guard tests and the API surface audit. */
export const ROUTES: readonly RouteSpec[] = ${JSON.stringify(routes, null, 1)};
`;

const check = process.argv.includes('--check');
if (check) {
  const current = fs.existsSync(OUT) ? fs.readFileSync(OUT, 'utf8') : '';
  if (current !== body) {
    console.error(
      'src/lib/api/contract.generated.ts is out of date with the Go route table.\n' +
        'Run: npm run gen:contract',
    );
    process.exit(1);
  }
  console.log(`contract up to date: ${routes.length} routes, ${permissions.length} permissions (${routePermissions.length} route-gated)`);
} else {
  fs.mkdirSync(path.dirname(OUT), { recursive: true });
  fs.writeFileSync(OUT, body);
  console.log(
    `wrote ${path.relative(process.cwd(), OUT)}: ${routes.length} routes, ` +
      `${permissions.length} permissions, ${Object.keys(features).length} gated groups`,
  );
}
