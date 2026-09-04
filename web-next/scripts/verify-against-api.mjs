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

/** A POST, optionally as somebody other than the current token. */
async function post(path, body, as = null) {
  const res = await fetch(API + path, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${as ?? token}`,
      'Content-Type': 'application/json',
      // The client sends one on every mutation; the server should accept it
      // here too, and a run that omitted it would not be exercising the
      // request the product actually makes.
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

console.log('\nSTOCK, BEYOND WHAT IS ON HAND');
{
  const places = await check(
    'GET /stock/locations',
    `/stock/locations?company_id=${CO}`,
    ['id', 'code', 'name', 'kind', 'is_active', 'holds_stock'],
    (j) => j.data[0],
  );
  // The route answers with the branches beside the locations, and the
  // movements filter and the transfer screens both read them.
  if (places && !('branches' in places)) {
    console.log('  x /stock/locations no longer carries branches');
    failures += 1;
  }

  const moves = await check(
    'GET /stock/movements',
    `/stock/movements?company_id=${CO}&limit=5`,
    null,
  );
  if (moves?.data?.[0]) {
    // A movement has no id: it is a line in a ledger rather than a document,
    // which is why the screen keys rows on what makes them distinct.
    expectFields('  movement', moves.data[0], [
      'occurred_at',
      'product',
      'sku',
      'location',
      'reason',
      // Signed. The screen colours it and prefixes a plus, because -2 and 2
      // are opposite events.
      'delta',
      'value',
    ]);
  } else {
    console.log('  -  nothing has moved; the ledger shape was not exercised');
  }

  const adjustments = await check(
    'GET /stock/adjustments',
    `/stock/adjustments?company_id=${CO}&limit=5`,
    ['id', 'adjustment_no', 'kind', 'reason', 'status', 'location', 'value', 'currency'],
    (j) => j.data[0],
  );
  const adjID = adjustments?.data?.[0]?.id;
  if (adjID) {
    const one = await check(
      'GET /stock/adjustments/{id}',
      `/stock/adjustments/${adjID}?company_id=${CO}`,
      ['id', 'adjustment_no', 'kind', 'status', 'lines'],
    );
    if (one?.lines?.[0]) {
      // system_qty beside delta IS the evidence: -2 against 74 is a breakage,
      // -2 against 2 is a shelf that was already empty.
      expectFields('  adjustment line', one.lines[0], [
        'variant_id',
        'sku',
        'product',
        'system_qty',
        'delta',
        'value',
      ]);
    }
  }

  await check('GET /stock/transfers', `/stock/transfers?company_id=${CO}&limit=5`, null);
  await check('GET /stock/batches', `/stock/batches?company_id=${CO}&limit=5`, null);

  // Availability is what a channel asks before promising a unit. It needs
  // both a variant and a warehouse; neither alone is a question it can
  // answer.
  const variant = (await call(`/stock/on-hand?company_id=${CO}&limit=1`)).json?.data?.[0];
  // `check` hands back the whole envelope, and this route wants one row.
  const place = places?.data?.[0];
  if (variant && place) {
    await check(
      'GET /stock/availability',
      `/stock/availability?company_id=${CO}&variant_id=${variant.variant_id}&warehouse_id=${place.id}`,
      ['variant_id', 'on_hand', 'reserved', 'available_to_sell'],
    );
  }
}

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

console.log('\nBUYING');
{
  const suppliers = await check(
    'GET /purchasing/suppliers',
    `/purchasing/suppliers?company_id=${CO}`,
    // `outstanding` is what the list is for: a buyer choosing a supplier needs
    // to know they are already forty thousand behind on that account, and a
    // figure they have to go and look up is one they will not look up.
    ['id', 'code', 'legal_name', 'payment_terms_days', 'is_active', 'outstanding'],
    (j) => j.data[0],
  );
  if (!suppliers?.data?.length) {
    console.log('  -  no suppliers seeded; the rest of buying was not exercised');
  } else {
    await check(
      'GET /purchasing/warehouses',
      `/purchasing/warehouses?company_id=${CO}`,
      ['id', 'code', 'name'],
      (j) => j.data[0],
    );

    const orders = await check(
      'GET /purchasing/orders',
      `/purchasing/orders?company_id=${CO}`,
      ['id', 'po_number', 'supplier', 'status', 'ordered_on', 'currency', 'total_inclusive'],
      (j) => j.data[0],
    );
    // The list screen shows a total and no items BECAUSE of this. If lines ever
    // start arriving here, the comment on that screen stops being true.
    if (orders?.data?.[0] && 'lines' in orders.data[0]) {
      console.log('  -  the order list now carries lines; the list screen could show them');
    }

    const poID = orders?.data?.[0]?.id;
    if (poID) {
      const one = await check(
        'GET /purchasing/orders/{id}',
        `/purchasing/orders/${poID}?company_id=${CO}`,
        ['id', 'po_number', 'status', 'subtotal_net', 'tax_total', 'total_inclusive', 'lines'],
      );
      if (one?.lines?.[0]) {
        expectFields('  order line', one.lines[0], [
          'id',
          'line_no',
          'qty_ordered',
          'qty_received',
          'qty_outstanding',
          'qty_billed',
          'unit_cost',
          // Added by migration 0125. Without them an editor reading a draft
          // cannot send its lines back unchanged, and PUT rewrites all of
          // them -- so changing a delivery date would change the tax.
          'tax_treatment',
          'tax_rate',
          // Added by 0126. Without it a receiving screen can only discover
          // which lines need a supplier lot by submitting the delivery and
          // reading the error -- so a clerk types a whole pallet, then is told.
          'tracks_batches',
          'net_amount',
          'tax_amount',
          'gross_amount',
        ]);
      } else {
        console.log('  -  the first order has no lines; the line shape was not exercised');
      }

      await check(
        'GET /purchasing/orders/{id}/receipts',
        `/purchasing/orders/${poID}/receipts?company_id=${CO}`,
        null,
      );
    }

    // Receiving, on an order that is open. This is the only route in the
    // product that increases stock through a purchase, so it is worth driving
    // rather than reading -- but only on an order this run can safely touch.
    // Both statuses are open for receiving, which is what canReceive() on
    // the screen says too: an order part-delivered is still owed the rest.
    const openOrder =
      (await call(`/purchasing/orders?company_id=${CO}&status=issued`)).json
        ?.data?.[0] ??
      (await call(`/purchasing/orders?company_id=${CO}&status=receiving`)).json
        ?.data?.[0];
    if (!openOrder) {
      console.log('  -  no order is open for receiving; it was not exercised');
    } else {
      const detail = (
        await call(`/purchasing/orders/${openOrder.id}?company_id=${CO}`)
      ).json;
      const line = detail?.lines?.[0];
      if (!line) {
        console.log('  -  that order has no lines; receiving was not exercised');
      } else {
        // The route takes a CLIENT-assigned uuid and answers with the original
        // receipt if it has seen it before. The screen mints one per delivery
        // for exactly that reason: a clerk on a bad connection who presses the
        // button twice books one delivery, not two.
        const docUUID = crypto.randomUUID();
        const body = {
          uuid: docUUID,
          po_id: openOrder.id,
          delivery_note_ref: 'verify:api',
          lines: [{ po_line_id: line.id, qty_received: '1', qty_rejected: '0' }],
        };

        const first = await post(`/purchasing/receipts?company_id=${CO}`, body);
        if (first.status !== 200 && first.status !== 201) {
          console.log(`  x POST /purchasing/receipts: HTTP ${first.status}`);
          failures += 1;
        } else {
          expectFields('POST /purchasing/receipts', first.json, [
            'id',
            'grn_number',
            'po_id',
            'received_on',
            'already_received',
            'order_status',
            // C13: what this delivery put right on sales that went below zero
            // before it arrived. The screen shows it only when it is not zero,
            // and cannot show it at all if it stops being sent.
            'cost_correction',
            'units_recosted',
          ]);
          if (first.json.already_received !== false) {
            console.log('  x a new uuid came back already_received');
            failures += 1;
          }

          const again = await post(`/purchasing/receipts?company_id=${CO}`, body);
          if (
            again.json?.already_received === true &&
            again.json.grn_number === first.json.grn_number
          ) {
            console.log('  ok the same uuid books one delivery, not two');
          } else {
            console.log(
              `  x replaying the uuid made ${again.json?.grn_number} beside ${first.json.grn_number}`,
            );
            failures += 1;
          }
        }
      }
    }

    const bills = await check(
      'GET /purchasing/bills',
      `/purchasing/bills?company_id=${CO}`,
      [
        'id',
        'supplier',
        'supplier_ref',
        'bill_date',
        'due_date',
        'currency',
        'total_inclusive',
        'amount_paid',
        'outstanding',
        'status',
        // A held-back bill is recorded and deliberately NOT in the ledger, so
        // the list has to be able to say which is which.
        'posted',
      ],
      (j) => j.data[0],
    );

    const billID = bills?.data?.[0]?.id;
    if (billID) {
      const one = await check(
        'GET /purchasing/bills/{id}',
        `/purchasing/bills/${billID}?company_id=${CO}`,
        ['id', 'supplier_ref', 'status', 'posted', 'subtotal_net', 'tax_total'],
      );
      if (one?.match?.[0]) {
        // The evidence the match KEPT, which is what the bill screen renders.
        // Recomputing it later would give a different answer once somebody
        // amends the order -- which is exactly when anyone would want to
        // check what it originally said.
        expectFields('  match line', one.match[0], [
          'dimension',
          'variance',
          'outcome',
        ]);
      } else {
        console.log('  -  no match evidence on that bill; the shape was not exercised');
      }
    }

    // Paying a supplier. The route is idempotent on a client-assigned uuid --
    // "a payment must carry an identifier so a retry does not pay twice" -- and
    // the screen mints one per payment for exactly that reason.
    const payable = bills?.data?.find(
      (b) => b.status !== 'blocked' && !/^0(.0*)?$/.test(b.outstanding ?? '0'),
    );
    if (!payable) {
      console.log('  -  nothing is payable; paying a supplier was not exercised');
    } else {
      const uuid = crypto.randomUUID();
      const body = {
        uuid,
        supplier_id: payable.supplier_id,
        method: 'bank_transfer',
        reference: 'verify:api',
        allocations: [{ bill_id: payable.id, amount: payable.outstanding }],
      };
      const first = await post(`/purchasing/payments?company_id=${CO}`, body);
      if (first.status !== 200 && first.status !== 201) {
        console.log(`  x POST /purchasing/payments: HTTP ${first.status}`);
        failures += 1;
      } else {
        expectFields('POST /purchasing/payments', first.json, [
          'id',
          'payment_number',
          'supplier',
          'amount',
          'currency',
          'settled',
          'already_paid',
        ]);
        if (first.json.settled?.[0]) {
          // The screen shows what each invoice was left owing, so a part
          // payment reads differently from a settlement.
          expectFields('  settled bill', first.json.settled[0], [
            'bill_id',
            'amount',
            'outstanding',
            'status',
          ]);
        }
        const again = await post(`/purchasing/payments?company_id=${CO}`, body);
        if (
          again.json?.already_paid === true &&
          again.json.payment_number === first.json.payment_number
        ) {
          console.log('  ok the same uuid pays once, not twice');
        } else {
          console.log(
            `  x replaying the uuid made ${again.json?.payment_number} beside ${first.json.payment_number}`,
          );
          failures += 1;
        }
      }
    }

    const ageing = await check(
      'GET /purchasing/ageing',
      `/purchasing/ageing?company_id=${CO}`,
      ['as_of', 'rows', 'total', 'base_currency'],
    );
    if (ageing?.rows?.[0]) {
      // Aged from the DUE date, per B6: a 60-day bill raised 45 days ago is not
      // late, and ageing from the bill date would have a buyer chasing a
      // supplier who is owed nothing yet. The screen writes that down.
      expectFields('  ageing row', ageing.rows[0], [
        'supplier_id',
        'supplier',
        'not_due',
        'days_0_30',
        'days_31_60',
        'days_61_90',
        'days_90_plus',
        'total',
      ]);
    } else {
      console.log('  -  nothing is owed; the bucket shape was not exercised');
    }

    // Where the rate comes from, which is the whole point of the block below.
    //
    // A purchase used to be priced from whatever the caller sent, defaulting to
    // zero — so an order raised without an explicit rate carried no input VAT,
    // silently. It is now resolved from the regulatory register at the order
    // date, the same rule the sale path and the expenses path follow. The
    // screens send a treatment and no rate at all, which only works if this
    // holds.
    const warehouse = (
      await call(`/purchasing/warehouses?company_id=${CO}`)
    ).json?.data?.[0];
    const variant = (
      await call(`/stock/on-hand?company_id=${CO}&limit=1`)
    ).json?.data?.[0];

    if (!warehouse || !variant) {
      console.log('  -  no warehouse or stock; purchase pricing was not exercised');
    } else {
      const draft = (lines) => ({
        supplier_id: suppliers.data[0].id,
        warehouse_id: warehouse.id,
        lines,
      });

      const priced = await post(
        `/purchasing/orders?company_id=${CO}`,
        draft([
          {
            variant_id: variant.variant_id,
            description: 'verify:api',
            qty: '10',
            unit_cost: '20.00',
            tax_treatment: 'standard',
          },
        ]),
      );
      if (priced.status !== 201) {
        console.log(`  x POST /purchasing/orders with no rate: HTTP ${priced.status}`);
        failures += 1;
      } else if (priced.json.tax_total === '0.0000' || priced.json.tax_total === '0') {
        console.log(
          '  x an order raised with no tax_rate carried NO TAX. The register is not being read.',
        );
        failures += 1;
      } else {
        console.log(
          `  ok a standard line with no rate supplied is priced from the register (tax ${priced.json.tax_total})`,
        );
      }

      // A rate that disagrees with the register is refused rather than used or
      // quietly ignored — ignoring it is how a caller sends last year's rate
      // for a year and never finds out.
      const disagrees = await post(
        `/purchasing/orders?company_id=${CO}`,
        draft([
          {
            variant_id: variant.variant_id,
            qty: '1',
            unit_cost: '10.00',
            tax_treatment: 'standard',
            tax_rate: '0.05',
          },
        ]),
      );
      if (disagrees.status === 400) {
        console.log('  ok a rate that disagrees with the register is refused');
      } else {
        console.log(`  x a disagreeing rate came back ${disagrees.status}, want 400`);
        failures += 1;
      }

      // And a treatment the market does not use, named rather than defaulted.
      const odd = await post(
        `/purchasing/orders?company_id=${CO}`,
        draft([
          {
            variant_id: variant.variant_id,
            qty: '1',
            unit_cost: '10.00',
            tax_treatment: 'not_a_treatment',
          },
        ]),
      );
      if (odd.status === 400) {
        console.log('  ok a treatment this market does not use is refused');
      } else {
        console.log(`  x an unknown treatment came back ${odd.status}, want 400`);
        failures += 1;
      }
    }
  }
}

console.log('\nSHIFT (at a counter, which is the only way these routes answer)');
{
  // Every shift route refuses a token with no device on it -- "Only a
  // registered till can open a session" -- so a browser sign-in cannot reach
  // any of this. The till binds a counter first and so does this.
  const counters = await call(`/pos/counters?company_id=${CO}`);
  const counter = counters.json?.data?.find((c) => c.status === 'active')
    ?? counters.json?.data?.[0];

  if (!counter) {
    console.log('  -  no counter on this company; the shift routes were not exercised');
  } else {
    const bound = await post('/pos/counter-sessions', { device_id: counter.id });
    if (bound.status !== 200 && bound.status !== 201) {
      console.log(`  x POST /pos/counter-sessions: HTTP ${bound.status}`);
      failures += 1;
    } else {
      expectFields('POST /pos/counter-sessions', bound.json, [
        'access_token',
        'expires_at',
      ]);
      // A counter-bound token, and deliberately no refresh token with it: the
      // counter is re-checked whenever this expires.
      const till = bound.json.access_token;

      // A till restarted mid-shift finds its session here rather than from the
      // open response, which is the only other copy of the id. 404 is a real
      // answer -- it means this counter is between shifts.
      const current = await fetch(`${API}/shifts/current`, {
        headers: { Authorization: `Bearer ${till}` },
      });
      let session = current.ok ? await current.json() : null;
      let opened = false;

      if (session) {
        console.log(`  ok GET /shifts/current -> session ${session.session_no} already open`);
      } else if (current.status === 404) {
        console.log('  ok GET /shifts/current -> 404, this counter is between shifts');
        // Blind, because that is the half with an invariant worth checking.
        const open = await post(
          '/shifts',
          { opening_float: '200.00', blind_close: true },
          till,
        );
        if (open.status !== 201) {
          console.log(`  x POST /shifts: HTTP ${open.status}`);
          failures += 1;
        } else {
          session = open.json;
          opened = true;
          expectFields('POST /shifts', session, [
            'id',
            'session_no',
            'device_id',
            'store_id',
            'state',
            'opened_at',
            'opening_float',
            'blind_close',
          ]);
        }
      } else {
        console.log(`  x GET /shifts/current: HTTP ${current.status}`);
        failures += 1;
      }

      if (session) {
        const peek = await fetch(`${API}/shifts/${session.id}`, {
          headers: { Authorization: `Bearer ${till}` },
        });
        const report = peek.ok ? await peek.json() : null;
        expectFields('GET /shifts/{id}', report, [
          'session_no',
          'state',
          'opened_at',
          'opening_float',
          'invoice_count',
          'gross_sales',
          'net_sales',
          'tax_total',
          'refund_total',
        ]);

        // THE invariant the close screen is built around. On a blind session
        // the expected figure is withheld -- and so are the three takings it
        // could be derived from, because hiding the total alone left
        // gross - refunds - non-cash as the cash takings exactly. A cashier who
        // can see what the drawer should hold can make it agree, and then the
        // variance reads zero on every shift.
        if (session.blind_close && report) {
          const leaked = [
            'expected_cash',
            'cash_takings',
            'non_cash_takings',
            'cash_movements',
          ].filter((f) => f in report);
          if (leaked.length) {
            console.log(`  x blind close leaks ${leaked.join(', ')} to the cashier`);
            failures += 1;
          } else {
            console.log('  ok blind close withholds the drawer from the cashier');
          }
        } else if (report) {
          // Worth saying out loud. The invariant is the reason the close screen
          // is shaped the way it is, and a run that quietly skipped it reads
          // exactly like a run that checked it.
          console.log('  -  this session is not blind; the withholding rule was NOT exercised');
        }

        // The supervisor's half, as the OWNER: report.view, which a cashier
        // does not hold. The same session, and here the figure is present --
        // if it were not, the two routes would differ for no reason and the
        // X report would be useless.
        const x = await fetch(`${API}/shifts/${session.id}/x-report`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (!x.ok) {
          console.log(`  x GET /shifts/{id}/x-report: HTTP ${x.status}`);
          failures += 1;
        } else {
          const xr = await x.json();
          expectFields('GET /shifts/{id}/x-report', xr, [
            'expected_cash',
            'cash_takings',
            'non_cash_takings',
            'cash_movements',
          ]);
        }

        // Only the session this run opened is touched further: a shift that was
        // already running belongs to somebody counting a real drawer.
        if (opened) {
          const drop = await post(
            `/shifts/${session.id}/cash-drop`,
            { amount: '-50.00', reason: 'safe_drop', note: 'verify:api' },
            till,
          );
          if (drop.status !== 204) {
            console.log(`  x POST /shifts/{id}/cash-drop: HTTP ${drop.status}`);
            failures += 1;
          } else {
            console.log('  ok POST /shifts/{id}/cash-drop');
          }

          // 200 float, less a 50 drop, no sales: the drawer should hold 150,
          // and counting exactly that is the case where the variance is zero.
          const close = await post(
            `/shifts/${session.id}/close`,
            { counted_cash: '150.00', note: 'verify:api' },
            till,
          );
          if (close.status !== 200) {
            console.log(`  x POST /shifts/{id}/close: HTTP ${close.status}`);
            failures += 1;
          } else {
            expectFields('POST /shifts/{id}/close', close.json, [
              'state',
              'closed_at',
              'expected_cash',
              'counted_cash',
              'variance',
            ]);
            // The count is committed, so the figures come back -- withholding
            // them now would stop anyone reconciling the variance they are
            // being asked to explain.
            console.log(
              `  -  expected ${close.json.expected_cash}, counted ${close.json.counted_cash}, variance ${close.json.variance}`,
            );
          }
        } else {
          console.log('  -  a shift was already open; it was left alone, not closed');
        }
      }
    }
  }
}

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
