// Proves the permission model with real accounts, against a running server.
//
//   node scripts/verify-rbac.mjs
//
// `verify:api` checks that every screen's payload is what the screen reads.
// This checks the other half: that the boundaries the screens are built around
// are boundaries the SERVER enforces, for accounts created the way the product
// creates them.
//
// Two things are proved.
//
// A CASHIER can reach the till and nothing else. Nineteen permissions, and the
// nine refusals that matter — including `/dashboard/overview`, which is
// `accounting.view` and which every signed-in person used to be sent to.
//
// B5.1's FOUR-WAY SPLIT in sourcing: anybody trusted with stock may ask,
// approving somebody else's request is a manager's act, running the comparison
// is the buyer's job, and awarding commits the business. The control is worth
// nothing if the person who ran the comparison also signs it off, and the
// seeded Purchase Manager role deliberately holds neither
// `purchasing.approve_request` nor `purchasing.award_rfq`.
//
// Accounts are created through `POST /people` and thrown away. It writes, so it
// is a development tool: it seeds staff, raises a requisition and awards an
// RFQ, and it says so rather than pretending to be read-only.
//
// Environment, all optional:
//   RAWSYST_API_ORIGIN    default http://localhost:8080
//   RAWSYST_DEV_EMAIL     default owner@example.test
//   RAWSYST_DEV_PASSWORD
//   RAWSYST_DEV_TENANT    when that email opens more than one business

const ORIGIN = process.env.RAWSYST_API_ORIGIN ?? 'http://localhost:8080';
const API = `${ORIGIN}/api/v1`;
const EMAIL = process.env.RAWSYST_DEV_EMAIL ?? 'owner@example.test';
const PASSWORD = process.env.RAWSYST_DEV_PASSWORD ?? 'DevPassw0rd!2026';
const TENANT = process.env.RAWSYST_DEV_TENANT ?? '';

let failures = 0;

function ok(label) {
  console.log(`  ok ${label}`);
}
function bad(label) {
  console.log(`  x  ${label}`);
  failures += 1;
}

/** Asserts a status, and says what the refusal was FOR when it is wrong. */
function expect(label, got, want) {
  if (got === want) ok(`${label} -> ${got}`);
  else bad(`${label} -> ${got}, want ${want}`);
}

async function login(body) {
  const r = await fetch(`${API}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) return null;
  return await r.json();
}

function client(token) {
  return async (method, path, body) => {
    const res = await fetch(API + path, {
      method,
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
        'Idempotency-Key': crypto.randomUUID(),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await res.text();
    let json = null;
    try {
      json = text ? JSON.parse(text) : null;
    } catch {
      json = null;
    }
    return { status: res.status, json };
  };
}

const opened = await login(
  TENANT ? { email: EMAIL, password: PASSWORD, tenant_id: TENANT } : { email: EMAIL, password: PASSWORD },
);
if (!opened?.access_token) {
  console.error(
    `Cannot sign in as ${EMAIL}. Is the API running, and does that email open exactly one business?`,
  );
  process.exit(1);
}

const owner = client(opened.access_token);
const companies = await owner('GET', '/companies');
const CO = companies.json?.data?.[0]?.id;
if (!CO) {
  console.error('Cannot verify: that account can reach no company.');
  process.exit(1);
}
const q = (p) => `${p}${p.includes('?') ? '&' : '?'}company_id=${CO}`;

const roles = (await owner('GET', q('/people/roles'))).json?.data ?? [];
const roleNamed = (re) => roles.find((r) => re.test(r.name));

/** Creates a member of staff and signs in as them with the password issued. */
async function staff(label, role) {
  if (!role) return null;
  const email = `verify-${label}-${Date.now()}@example.test`;
  const made = await owner('POST', q('/people'), {
    email,
    full_name: `verify:rbac ${label}`,
    role_id: role.id,
    company_id: CO,
  });
  if (made.status !== 201 && made.status !== 200) return null;
  const signed = await login({ email, password: made.json.data.temporary_password });
  return signed?.access_token ? { call: client(signed.access_token), email } : null;
}

// --- a cashier reaches the till and nothing else -------------------------

console.log('\nA CASHIER');
const cashierRole = roleNamed(/Cashier/);
const cashier = await staff('cashier', cashierRole);

if (!cashier) {
  console.log('  -  no Cashier role seeded; the till boundary was not exercised');
} else {
  const me = (await cashier.call('GET', '/auth/me')).json;
  console.log(`  -  resolves to ${me.permissions.length} permissions, super_admin=${me.is_super_admin}`);
  if (me.is_super_admin !== false) bad('a cashier resolved as a platform operator');

  for (const [label, path] of [
    ['GET /catalog/products', `/catalog/products?company_id=${CO}&limit=1`],
    ['GET /customers', `/customers?company_id=${CO}`],
    ['GET /catalog/snapshot', `/catalog/snapshot?company_id=${CO}&limit=1`],
    ['GET /pos/counters', `/pos/counters?company_id=${CO}`],
  ]) {
    expect(label, (await cashier.call('GET', path)).status, 200);
  }

  for (const [label, path] of [
    // The one that sent every signed-in person to a 403: the dashboard reads
    // this, and a cashier holds sales.view and inventory.view and not this.
    ['GET /dashboard/overview', `/dashboard/overview?company_id=${CO}`],
    ['GET /purchasing/suppliers', `/purchasing/suppliers?company_id=${CO}`],
    ['GET /purchasing/orders', `/purchasing/orders?company_id=${CO}`],
    ['GET /purchasing/ageing', `/purchasing/ageing?company_id=${CO}`],
    ['GET /people', `/people?company_id=${CO}`],
    ['GET /employees', `/employees?company_id=${CO}`],
    ['GET /permissions', '/permissions'],
    ['GET /reports/vat-return', `/reports/vat-return?company_id=${CO}`],
  ]) {
    expect(label, (await cashier.call('GET', path)).status, 403);
  }

  // 404 rather than 403, deliberately: confirming a platform route exists is
  // itself a leak, so it answers as though it were not there.
  expect('GET /platform/health', (await cashier.call('GET', '/platform/health')).status, 404);
}

// --- B5.1: asking, approving, comparing and awarding are four jobs -------

console.log('\nSOURCING, AND THE FOUR JOBS IT SPLITS');
const keeper = await staff('keeper', roleNamed(/Inventory/));
const buyer = await staff('buyer', roleNamed(/Purchase Manager/));

if (!keeper || !buyer) {
  console.log('  -  Inventory Keeper or Purchase Manager not seeded; the split was not exercised');
} else {
  const warehouse = (await owner('GET', q('/purchasing/warehouses'))).json?.data?.[0];
  const stock = (await owner('GET', q('/stock/on-hand?limit=1'))).json?.data?.[0];
  const suppliers = (await owner('GET', q('/purchasing/suppliers'))).json?.data ?? [];

  if (!warehouse || !stock || suppliers.length < 2) {
    console.log('  -  needs a warehouse, stock and two suppliers; the split was not exercised');
  } else {
    const line = {
      variant_id: stock.variant_id,
      description: stock.product,
      qty: '5',
    };

    // ASKING. B5 puts this in reach of any authorised staff: it carries no cost
    // and needs no buying permission.
    const asked = await keeper.call('POST', q('/purchasing/requisitions'), {
      warehouse_id: warehouse.id,
      lines: [line],
    });
    expect('inventory keeper asks for stock', asked.status, 201);
    const reqID = asked.json?.id;

    if (reqID) {
      // APPROVING. Neither the person who asked nor the buyer may decide it.
      expect(
        'keeper approves their OWN request',
        (await keeper.call('POST', q(`/purchasing/requisitions/${reqID}/decision`), {
          approve: true,
          note: 'mine',
        })).status,
        403,
      );
      expect(
        'the BUYER approves it',
        (await buyer.call('POST', q(`/purchasing/requisitions/${reqID}/decision`), {
          approve: true,
          note: 'ok',
        })).status,
        403,
      );
      expect(
        'somebody holding approve_request does',
        (await owner('POST', q(`/purchasing/requisitions/${reqID}/decision`), {
          approve: true,
          note: 'verify:rbac',
        })).status,
        200,
      );

      // COMPARING. The buyer's job, and one supplier is not a comparison.
      const one = await buyer.call('POST', q('/purchasing/rfqs'), {
        requisition_id: reqID,
        warehouse_id: warehouse.id,
        supplier_ids: [suppliers[0].id],
        lines: [line],
      });
      expect('an RFQ to a SINGLE supplier', one.status, 400);

      const rfq = await buyer.call('POST', q('/purchasing/rfqs'), {
        requisition_id: reqID,
        warehouse_id: warehouse.id,
        supplier_ids: suppliers.slice(0, 2).map((s) => s.id),
        lines: [line],
      });
      expect('the buyer raises an RFQ to two', rfq.status, 201);

      if (rfq.status === 201) {
        const rfqID = rfq.json.id;
        expect(
          'the buyer files a supplier reply',
          (await buyer.call('POST', q(`/purchasing/rfqs/${rfqID}/quotes`), {
            supplier_id: suppliers[0].id,
            received_on: new Date().toISOString().slice(0, 10),
            lines: (rfq.json.lines ?? []).map((l) => ({
              rfq_line_id: l.id,
              qty: l.qty,
              unit_cost: '12.00',
              tax_treatment: 'standard',
            })),
          })).status,
          201,
        );

        const cmp = await buyer.call('GET', q(`/purchasing/rfqs/${rfqID}/comparison`));
        expect('the buyer reads the comparison', cmp.status, 200);
        expect(
          'the keeper reads the comparison',
          (await keeper.call('GET', q(`/purchasing/rfqs/${rfqID}/comparison`))).status,
          403,
        );

        // AWARDING. The whole point: comparing does not carry choosing.
        const quoteID = cmp.json?.quotes?.[0]?.id;
        expect(
          'the buyer who COMPARED awards it',
          (await buyer.call('POST', q(`/purchasing/rfqs/${rfqID}/award`), {
            quote_id: quoteID,
            reason: 'verify:rbac',
          })).status,
          403,
        );
        expect(
          'an award with no reason',
          (await owner('POST', q(`/purchasing/rfqs/${rfqID}/award`), {
            quote_id: quoteID,
            reason: '',
          })).status,
          400,
        );
        const awarded = await owner('POST', q(`/purchasing/rfqs/${rfqID}/award`), {
          quote_id: quoteID,
          reason: 'verify:rbac — the only supplier who replied inside the window',
        });
        expect('somebody holding award_rfq does', awarded.status, 201);
        if (awarded.status === 201) {
          console.log(
            `  -  and it raised ${awarded.json?.purchase_order?.po_number ?? 'a purchase order'}`,
          );
        }
      }
    }
  }
}

console.log(
  `\n${failures === 0 ? 'EVERY BOUNDARY HELD' : `${failures} BOUNDARIES DID NOT HOLD`}`,
);
process.exit(failures === 0 ? 0 : 1);
