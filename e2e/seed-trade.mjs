// Fill the dev shop with enough trade to LOOK at a dense screen.
//
// The audit and the layout probe walk every section, and until now every one of
// them was empty: a dev database has one company, one store, four products and
// no history. So every judgement about the dense screens — is this table
// legible, does a long supplier name crush the column beside it, does a row of
// figures line up — was being made against an empty state.
//
// Everything here goes through the real HTTP API with a real owner's token, so
// what lands in the database is data the product could actually have produced.
// Nothing is inserted behind the business rules' back.
//
// It is idempotent in the way that matters: run it twice and you get twice the
// trade, not an error. Nothing here is asserted on, so it is a tool rather than
// a test — the same standing as shots.mjs.
//
//   RS_PASSWORD=... node e2e/seed-trade.mjs

const API = process.env.RS_API ?? 'http://localhost:8080';
const EMAIL = process.env.RS_EMAIL ?? 'owner@example.test';
const PASSWORD = process.env.RS_PASSWORD;

if (!PASSWORD) {
  console.error('RS_PASSWORD not set');
  process.exit(2);
}

let token = '';
let company = '';

/** One request. Returns the parsed body and the status, and never throws. */
async function call(method, path, body) {
  const url = API + path + (company ? (path.includes('?') ? '&' : '?') + `company_id=${company}` : '');
  const res = await fetch(url, {
    method,
    headers: {
      'content-type': 'application/json',
      ...(token ? { authorization: `Bearer ${token}` } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let json = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    /* a non-JSON body is reported as text */
  }
  return { status: res.status, json, text };
}

/** A request that is expected to work, reported loudly when it does not. */
async function must(what, method, path, body) {
  const r = await call(method, path, body);
  if (r.status >= 400) {
    console.error(`  ✗ ${what}: ${r.status} ${r.text.slice(0, 220)}`);
    return null;
  }
  console.log(`  ✓ ${what}`);
  return r.json?.data ?? r.json;
}

/** A date `n` days before today, as the API wants it. */
function daysAgo(n) {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return d.toISOString().slice(0, 10);
}

/** A short, unique suffix, so a second run does not collide on a code. */
const tag = Math.random().toString(36).slice(2, 6).toUpperCase();

async function main() {
  const login = await call('POST', '/api/v1/auth/login', {
    email: EMAIL,
    password: PASSWORD,
  });
  if (login.status !== 200) {
    console.error('sign-in failed:', login.status, login.text.slice(0, 200));
    process.exit(2);
  }
  token = login.json.access_token;

  const companies = await call('GET', '/api/v1/companies');
  company = companies.json?.data?.[0]?.id;
  if (!company) {
    console.error('no company on this account');
    process.exit(2);
  }
  console.log(`company ${company}\n`);

  // --- Customers -----------------------------------------------------------
  //
  // A mix on purpose: a long legal name that will test a table column, a short
  // one, one on account and one that pays at the till. The customers screen is
  // read by somebody chasing money, and a list where every row is the same
  // shape teaches nothing about whether the real one will be legible.
  console.log('customers');
  const customers = [
    { name: 'Al Nakheel Contracting and General Trading Establishment', code: `NAK${tag}`, terms: 30, limit: '25000.00', kind: 'wholesale' },
    { name: 'Rahman Textiles', code: `RAH${tag}`, terms: 14, limit: '8000.00', kind: 'wholesale' },
    { name: 'Noor Boutique', code: `NOO${tag}`, terms: 0, limit: null, kind: 'retail' },
    { name: 'Gulf Interiors LLC', code: `GLF${tag}`, terms: 60, limit: '120000.00', kind: 'wholesale' },
    { name: 'S. Karim', code: `KAR${tag}`, terms: 7, limit: '1500.00', kind: 'retail' },
  ];
  for (const c of customers) {
    await must(c.name, 'POST', '/api/v1/customers', {
      name: c.name,
      code: c.code,
      customer_type: c.kind,
      payment_terms_days: c.terms,
      credit_limit: c.limit ?? '',
      phone: '+9665' + Math.floor(10000000 + Math.random() * 89999999),
    });
  }

  // --- Suppliers -----------------------------------------------------------
  console.log('\nsuppliers');
  const suppliers = [];
  for (const s of [
    { name: 'Jeddah Fabric Mills Company Limited', code: `JFM${tag}`, terms: 45 },
    { name: 'Dhaka Garments Export House', code: `DGE${tag}`, terms: 30 },
    { name: 'Riyadh Packaging', code: `RPK${tag}`, terms: 15 },
  ]) {
    const made = await must(s.name, 'POST', '/api/v1/purchasing/suppliers', {
      legal_name: s.name,
      code: s.code,
      payment_terms_days: s.terms,
    });
    if (made?.id) suppliers.push({ ...s, id: made.id });
  }

  // --- What there is to order ---------------------------------------------
  const warehouses = await call('GET', '/api/v1/purchasing/warehouses');
  const warehouse = (warehouses.json?.data ?? warehouses.json)?.[0]?.id;

  const catalogue = await call('GET', '/api/v1/catalog/snapshot');
  const snapshot = catalogue.json?.data ?? catalogue.json ?? {};
  const variants = (
    Array.isArray(snapshot) ? snapshot : snapshot.variants ?? snapshot.items ?? []
  ).slice(0, 4);

  if (!warehouse || variants.length === 0) {
    console.log('\nno warehouse or no catalogue — stopping before purchasing');
    console.log(`warehouse=${warehouse} variants=${variants.length}`);
    return;
  }

  // --- Purchase orders, deliveries and bills -------------------------------
  //
  // Three orders in three different states, because the buying screen's whole
  // job is telling them apart: one still a draft, one issued and delivered and
  // billed, one issued and waiting.
  console.log('\npurchase orders');
  for (const [i, supplier] of suppliers.entries()) {
    const lines = variants.slice(0, 2 + i).map((v, n) => ({
      variant_id: v.variant_id ?? v.id,
      description: v.name ?? v.description ?? 'Item',
      qty: String((n + 2) * 10),
      unit_cost: (18 + n * 7.5).toFixed(2),
    }));

    const order = await must(
      `order for ${supplier.name}`,
      'POST',
      '/api/v1/purchasing/orders',
      {
        supplier_id: supplier.id,
        warehouse_id: warehouse,
        expected_on: daysAgo(-7 - i),
        lines,
      },
    );
    if (!order?.id) continue;

    // The first stays a draft. The rest are sent to the supplier.
    if (i === 0) continue;
    await must(`  issued`, 'POST', `/api/v1/purchasing/orders/${order.id}/issue`);

    // The second is delivered and billed; the third is left outstanding, which
    // is what "goods received, not yet invoiced" on the dashboard is made of.
    if (i !== 1) continue;

    const detail = await call('GET', `/api/v1/purchasing/orders/${order.id}`);
    const orderLines = (detail.json?.data ?? detail.json)?.lines ?? [];

    await must(
      `  delivery`,
      'POST',
      '/api/v1/purchasing/receipts',
      {
        uuid: crypto.randomUUID(),
        po_id: order.id,
        delivery_note_ref: `DN-${tag}-${i}`,
        lines: orderLines.map((l) => ({
          po_line_id: l.id,
          qty_received: l.qty ?? l.qty_ordered,
          qty_rejected: '0',
        })),
      },
    );

    await must(`  bill`, 'POST', '/api/v1/purchasing/bills', {
      uuid: crypto.randomUUID(),
      supplier_id: supplier.id,
      po_id: order.id,
      supplier_ref: `INV-${tag}-${i}`,
      bill_date: daysAgo(3),
      lines: orderLines.map((l) => ({
        po_line_id: l.id,
        variant_id: l.variant_id ?? '',
        description: l.description,
        qty: l.qty ?? l.qty_ordered,
        unit_cost: l.unit_cost,
      })),
    });
  }

  // --- Expenses ------------------------------------------------------------
  console.log('\nexpenses');
  const heads = await call('GET', '/api/v1/expenses/heads');
  const headList = heads.json?.data ?? heads.json ?? [];
  if (headList.length === 0) {
    console.log('  no expense heads seeded — skipping');
  } else {
    const amounts = ['4500.00', '820.50', '1275.00', '310.00', '96.75'];
    for (const [i, amount] of amounts.entries()) {
      const head = headList[i % headList.length];
      await must(
        `${head.name} ${amount}`,
        'POST',
        '/api/v1/expenses',
        {
          uuid: crypto.randomUUID(),
          expense_date: daysAgo(i * 3),
          paid_from: i % 2 === 0 ? 'bank' : 'cash',
          description: ['Monthly rent', 'Electricity', 'Delivery van fuel', 'Cleaning', 'Stationery'][i],
          // Net, never gross: the server computes the tax from the registry
          // rate for the expense date, so a client cannot decide what the VAT
          // return claims.
          lines: [{ head_id: head.id, net_amount: amount, description: '' }],
        },
      );
    }
  }

  console.log('\ndone — reload the back office to see it');
}

main().catch((e) => {
  console.error(e);
  process.exit(2);
});
