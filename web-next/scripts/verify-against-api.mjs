// Checks every endpoint the built screens call, against a running server.
//
// A 200 is not enough. A screen that reads `available` off a payload which has
// never carried it renders an em dash for ever and nothing reports a problem --
// so this asserts the FIELDS each screen actually reads, not only the status.
//
// Everything in web-next was first typed by reading Go source, and seven of
// those readings turned out to be wrong. This is what turns source-reading into
// knowledge: run it after changing a payload on either side.
//
//   node scripts/verify-against-api.mjs
//
// Environment, all optional:
//   RAWSYST_API_ORIGIN    default http://localhost:8080
//   RAWSYST_DEV_EMAIL     default owner@example.test  (cmd/devseed's owner)
//   RAWSYST_DEV_PASSWORD
//   RAWSYST_DEV_TENANT    the business to sign into, when the email opens
//                         more than one; the run prints the choices otherwise
//   RAWSYST_OPS_EMAIL     default ops@example.test
//                         (cmd/devseed -platform-email; a user with no tenant)
//
// Exits non-zero on the first mismatch, so it can gate a branch.

const ORIGIN = process.env.RAWSYST_API_ORIGIN ?? 'http://localhost:8080';
const API = `${ORIGIN}/api/v1`;
const EMAIL = process.env.RAWSYST_DEV_EMAIL ?? 'owner@example.test';
const PASSWORD = process.env.RAWSYST_DEV_PASSWORD ?? 'DevPassw0rd!2026';
const OPS_EMAIL = process.env.RAWSYST_OPS_EMAIL ?? 'ops@example.test';
const TENANT = process.env.RAWSYST_DEV_TENANT ?? '';

let token = '';
let failures = 0;

async function call(path) {
  const res = await fetch(API + path, {
    headers: { Authorization: `Bearer ${token}` },
  });
  const text = await res.text();
  let json = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    json = null;
  }
  return { status: res.status, json };
}

/** Checks a payload carries the fields a screen reads off it. */
function expectFields(label, obj, fields) {
  if (!obj) {
    console.log(`  x ${label}: no payload`);
    failures += 1;
    return;
  }
  const missing = fields.filter((f) => !(f in obj));
  if (missing.length) {
    console.log(`  x ${label}: missing ${missing.join(', ')}`);
    failures += 1;
  } else {
    console.log(`  ok ${label}`);
  }
}

async function check(label, path, fields, pick = (j) => j) {
  const r = await call(path);
  if (r.status !== 200) {
    console.log(`  x ${label}: HTTP ${r.status}`);
    failures += 1;
    return null;
  }
  if (fields) expectFields(label, pick(r.json), fields);
  else console.log(`  ok ${label}`);
  return r.json;
}

async function signIn(body) {
  const res = await fetch(`${API}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return res;
}

const credentials = { email: EMAIL, password: PASSWORD };
const login = await signIn(
  TENANT ? { ...credentials, tenant_id: TENANT } : credentials,
);
if (!login.ok) {
  console.error(
    `Cannot sign in as ${EMAIL}. Is the API running, and has cmd/devseed been run?`,
  );
  process.exit(1);
}
const opened = await login.json();
token = opened.access_token;

// A challenge is a 200 with no token, and it is not a server fault: this email
// opens more than one business, or the account has a second factor. Either way
// the run cannot continue, and saying which is the difference between a fix
// and an afternoon.
if (!token) {
  if (opened.tenant_choice_required) {
    // Naming the businesses rather than only counting them: the next thing
    // the developer has to do is choose one, and they cannot choose from a
    // number. Two of them may well share a name, so the id is what is shown.
    console.error(
      `Cannot verify: ${EMAIL} opens ${opened.tenants.length} businesses, so ` +
        'sign-in stops on the picker. Name one:',
    );
    for (const t of opened.tenants) {
      console.error(`  RAWSYST_DEV_TENANT=${t.tenant_id}   ${t.name}`);
    }
  } else {
    console.error(
      'Cannot verify: sign-in returned no token and no reason this script ' +
        'understands. A second factor on this account would do it.',
    );
  }
  process.exit(1);
}

const companies = await call('/companies');
if (!companies.json?.data?.length) {
  console.error('Cannot verify: /companies answered ' + companies.status + ' with no company.');
  process.exit(1);
}
const CO = companies.json.data[0].id;

console.log('\nSHELL');
await check('GET /auth/me', '/auth/me', ['user_id', 'is_super_admin', 'permissions']);
await check(
  'GET /companies',
  '/companies',
  ['id', 'legal_name', 'country', 'base_currency'],
  (j) => j.data[0],
);
await check(
  'GET /subscription/entitlements',
  '/subscription/entitlements',
  ['feature', 'allowed', 'in_plan'],
  (j) => j.data[0],
);

console.log('\nDASHBOARD');
await check('GET /dashboard/overview', `/dashboard/overview?company_id=${CO}`, [
  'date',
  'sales',
  'profit',
  'money',
  'inventory',
  'tenders',
  'attention',
  'unbuilt',
  'base_currency',
]);

console.log('\nPRODUCTS');
const products = await check(
  'GET /catalog/products',
  `/catalog/products?company_id=${CO}&limit=5`,
  ['id', 'sku', 'name', 'tax_treatment', 'lifecycle', 'variant_count'],
  (j) => j.data[0],
);
if (products?.page) {
  expectFields('  page envelope', products.page, ['cursor', 'has_more', 'limit']);
}

const productId = products?.data?.[0]?.id;
if (productId) {
  await check(
    'GET /catalog/products/{id}/matrix',
    `/catalog/products/${productId}/matrix?company_id=${CO}`,
    // The matrix screen shows stock beside the price, so it reads both.
    ['id', 'sku', 'attributes', 'price', 'is_active', 'on_hand', 'reorder_level'],
    (j) => j.data[0],
  );
}

console.log('\nSTOCK');
await check(
  'GET /stock/on-hand',
  `/stock/on-hand?company_id=${CO}&limit=5`,
  ['variant_id', 'sku', 'product', 'barcode', 'location', 'on_hand', 'reorder_level'],
  (j) => j.data[0],
);
await check(
  'GET /stock/on-hand?low=true',
  `/stock/on-hand?company_id=${CO}&low=true&limit=5`,
  null,
);
await check(
  'GET /stock/locations',
  `/stock/locations?company_id=${CO}`,
  ['id', 'code', 'name', 'is_active'],
  (j) => j.data[0],
);

console.log('\nCUSTOMERS');
const customers = await check(
  'GET /customers',
  `/customers?company_id=${CO}`,
  [
    'id',
    'code',
    'name',
    'customer_type',
    'payment_terms_days',
    'balance',
    'is_active',
    'currency',
  ],
  (j) => j.data[0],
);
const customerId = customers?.data?.[0]?.id;
if (customerId) {
  const ledger = await check(
    'GET /customers/{id}/ledger',
    `/customers/${customerId}/ledger?company_id=${CO}`,
    ['customer', 'rows', 'closing', 'base_currency'],
  );
  if (ledger?.rows?.[0]) {
    expectFields('  ledger row', ledger.rows[0], ['date', 'kind', 'balance']);
  } else {
    console.log('  -  ledger has no rows yet (the empty state is what renders)');
  }
  await check(
    'GET /customers/{id}/open-invoices',
    `/customers/${customerId}/open-invoices?company_id=${CO}`,
    null,
  );
}

console.log('\nSALES');
const day = await check(
  'GET /dashboard/sales',
  `/dashboard/sales?company_id=${CO}&limit=200`,
  [
    'date',
    'rows',
    'sales_total',
    'refund_total',
    'net_total',
    'tax_total',
    'invoice_count',
    'refund_count',
    'retail_total',
    'wholesale_total',
    'retail_count',
    'wholesale_count',
    'has_more',
    'base_currency',
  ],
);
if (day?.rows?.[0]) {
  expectFields('  sale row', day.rows[0], [
    'id',
    'doc_type',
    'state',
    'issued_at',
    'total_inclusive',
    'tax_total',
    'tenders',
    'line_count',
    'is_credit_note',
  ]);
  console.log(`  -  ${day.invoice_count} invoices, ${day.sales_total} sold`);
} else {
  console.log('  -  no sales on this day; the row shape was not exercised');
}

console.log('\nPOS');
await check(
  'GET /pos/counters',
  `/pos/counters?company_id=${CO}`,
  ['id', 'terminal_label', 'store', 'status', 'binding'],
  (j) => j.data[0],
);
const snap = await check(
  'GET /catalog/snapshot',
  `/catalog/snapshot?company_id=${CO}&limit=5`,
  // The till reads every one of these. `tax_treatment` in particular: the sale
  // route refuses a line without it and `/catalog/scan` does not return one.
  ['id', 'sku', 'barcode', 'name', 'price', 'tax_treatment', 'is_active'],
  (j) => j.items[0],
);
if (snap) console.log(`  -  ${snap.items.length} sellable lines`);

console.log('\nPLATFORM (hidden from a business owner, which is correct)');
{
  const r = await call('/platform/health');
  // 404, not 403, and deliberately so: RequireSuperAdmin says that confirming
  // a platform endpoint exists tells an attacker where to aim. The same
  // reasoning as answering 404 for another tenant's record.
  if (r.status === 404) {
    console.log('  ok GET /platform/health -> 404 for a non-operator (by design)');
  } else {
    console.log(`  x expected 404, got ${r.status}`);
    failures += 1;
  }
}

console.log('\nPLATFORM (as an operator)');
{
  const ops = await fetch(`${API}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: OPS_EMAIL, password: PASSWORD }),
  });
  if (!ops.ok) {
    // Not a failure: a database seeded before the flag existed has no
    // operator, and the business contracts above are still worth reporting.
    // Named loudly, though, because silently skipping half the product is how
    // a screen stays unverified for a month.
    console.log(`  -  no operator ${OPS_EMAIL}; platform screens NOT verified`);
    console.log('     go run ./cmd/devseed -platform-email ' + OPS_EMAIL);
  } else {
    token = (await ops.json()).access_token;

    // Zero permissions is the correct answer and the reason PLATFORM_NAV
    // names none: these routes are AccessSuperAdmin, gated on the claim
    // rather than on the catalogue. A nav that asked for a permission string
    // here would render an empty sidebar for the person who runs the service.
    const me = await check('GET /auth/me', '/auth/me', [
      'user_id',
      'is_super_admin',
      'permissions',
    ]);
    if (me && me.is_super_admin !== true) {
      console.log('  x /auth/me: is_super_admin is not true for the operator');
      failures += 1;
    }
    if (me && me.permissions.length !== 0) {
      console.log(
        `  -  operator carries ${me.permissions.length} permissions; PLATFORM_NAV assumes none are needed`,
      );
    }

    await check('GET /platform/health', '/platform/health', [
      'tenants',
      'active_tenants',
      'jobs_failed_24h',
      'jobs_dead',
      'tickets_open',
      'tenants_without_verified_backup',
      'database_ok',
      'database_latency_ms',
      'checked_at',
    ]);

    const tenants = await check(
      'GET /platform/tenants',
      '/platform/tenants',
      // `last_activity` and `backup_verified_at` are omitempty and absent on
      // a fresh seed -- which is the case the screen renders as "Never", so
      // they are deliberately NOT asserted here.
      ['id', 'name', 'plan_tier', 'status', 'market', 'companies', 'users', 'created_at'],
      (j) => j.data[0],
    );
    if (tenants?.page) {
      console.log('  -  /platform/tenants now paginates; the screen filters in the browser');
    }

    const jobs = await check('GET /platform/jobs/failed', '/platform/jobs/failed', null);
    if (jobs?.data?.[0]) {
      expectFields('  failed job', jobs.data[0], [
        'id',
        'kind',
        'status',
        'attempts',
        'failed_at',
      ]);
    } else {
      console.log('  -  nothing has failed; the row shape was not exercised');
    }

    const queue = await check('GET /platform/support', '/platform/support', null);
    if (queue?.data?.[0]) {
      expectFields('  ticket', queue.data[0], [
        'id',
        'ticket_no',
        'subject',
        'body',
        'kind',
        'priority',
        'status',
        'created_at',
      ]);
      // The screen shows the thread only after a reply BECAUSE of this. If the
      // queue ever starts carrying messages, that comment stops being true.
      if ('messages' in queue.data[0]) {
        console.log('  -  the queue now carries messages; the support screen can show threads');
      }
    } else {
      console.log('  -  no open tickets; the row shape was not exercised');
    }
  }
}

console.log(
  `\n${failures === 0 ? 'ALL SCREEN CONTRACTS VERIFIED' : `${failures} MISMATCHES`}`,
);
process.exit(failures === 0 ? 0 : 1);
