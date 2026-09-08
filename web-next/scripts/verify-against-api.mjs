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

/**
 * A write, for the few assertions that are about a REFUSAL rather than a shape.
 *
 * Used sparingly and only where the refusal is the contract: a settled field
 * that answered 200 instead of 409 would let a screen report success on a
 * change that never happened, and no amount of reading the GET would catch it.
 */
async function write(method, path, body) {
  const res = await fetch(API + path, {
    method,
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body ?? {}),
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

async function put(path, body) {
  const res = await fetch(API + path, {
    method: 'PUT',
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
      'Idempotency-Key': crypto.randomUUID(),
    },
    body: JSON.stringify(body),
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
// Kept so the platform section can hand it back. That section signs in as an
// operator who deliberately holds ZERO permissions, and `token` is module
// level, so every request made after it authenticates as the wrong person and
// answers 403. Whoever appends the next section to the end of this file should
// not have to know that.
const tenantToken = token;

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

// A product with variants, not merely the first product. A product created
// and not yet given variants is a real state -- `/products/new` lands the user
// on exactly that -- and picking it here checked an empty matrix and reported
// the screen contract broken when nothing was.
const withVariants = (products?.data ?? []).find((p) => p.variant_count > 0);
const productId = withVariants?.id;
if (!productId && (products?.data ?? []).length > 0) {
  console.log('  -  no product has variants yet; the matrix shape was not exercised');
}
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

console.log('\nCASH, BANK AND WHAT WAS SPENT');
{
  const accounts = await check(
    'GET /treasury/accounts',
    `/treasury/accounts?company_id=${CO}`,
    ['id', 'kind', 'name', 'currency', 'is_active', 'account_id', 'account_code', 'balance'],
    (j) => j.data[0],
  );
  if (!accounts?.data?.length) {
    console.log('  -  no money accounts; the balance shape was not exercised');
  }

  const transfers = await check(
    'GET /treasury/transfers',
    `/treasury/transfers?company_id=${CO}&limit=3`,
    null,
  );
  if (transfers?.data?.[0]) {
    expectFields('  transfer', transfers.data[0], [
      'id',
      'transfer_no',
      // Names rather than ids: the screen shows where money went, and an id
      // in that column would be an id somebody has to look up.
      'from',
      'to',
      'amount',
      'currency',
      'moved_on',
    ]);
  } else {
    console.log('  -  nothing has been moved; the transfer shape was not exercised');
  }

  // --- proving the books agree with the bank (C11) ---

  const statements = await check(
    'GET /treasury/statements',
    `/treasury/statements?company_id=${CO}`,
    null,
  );

  const bank = accounts?.data?.find(
    (a) => a.is_active && ['bank', 'card_settlement', 'gateway'].includes(a.kind),
  );
  const till = accounts?.data?.find((a) => a.kind === `cash`);

  if (!bank) {
    console.log('  -  no bank account; reconciliation was not exercised');
  } else {
    // A till has no statement. This is what makes the account picker on the
    // import form a filtered list rather than every money account.
    if (till) {
      const wrongKind = await post(`/treasury/statements?company_id=${CO}`, {
        account_id: till.id,
        starts_on: '2026-09-01',
        ends_on: '2026-09-30',
        opening_balance: '0.00',
        closing_balance: '10.00',
        lines: [{ value_date: '2026-09-02', description: 'x', amount: '10.00' }],
      });
      if (wrongKind.status === 400) {
        console.log('  ok a till has no statement, and the import is refused');
      } else {
        console.log(`  x a till statement came back ${wrongKind.status}, want 400`);
        failures += 1;
      }
    }

    // The bank's own arithmetic, checked before anything is stored. The form
    // checks it too, beside the box, because a statement that does not reach
    // its own closing figure is a paste that stopped short.
    const short = await post(`/treasury/statements?company_id=${CO}`, {
      account_id: bank.id,
      starts_on: '2026-09-01',
      ends_on: '2026-09-30',
      opening_balance: '1000.00',
      closing_balance: '9999.00',
      lines: [{ value_date: '2026-09-02', description: 'Charge', amount: '-25.00' }],
    });
    if (short.status === 400 && /does not add up/i.test(short.json?.error?.message ?? "")) {
      console.log('  ok a statement that does not reach its closing balance is refused');
    } else {
      console.log(`  x a short statement came back ${short.status}, want 400`);
      failures += 1;
    }

    const empty = await post(`/treasury/statements?company_id=${CO}`, {
      account_id: bank.id,
      starts_on: '2026-09-01',
      ends_on: '2026-09-30',
      opening_balance: '0.00',
      closing_balance: '0.00',
      lines: [],
    });
    if (empty.status === 400) {
      console.log('  ok a statement with no lines on it proves nothing, and is refused');
    } else {
      console.log(`  x an empty statement came back ${empty.status}, want 400`);
      failures += 1;
    }

    const stamp = String(Date.now()).slice(-6);
    const brought = await post(`/treasury/statements?company_id=${CO}`, {
      account_id: bank.id,
      starts_on: '2026-09-01',
      ends_on: '2026-09-30',
      opening_balance: '0.00',
      closing_balance: '-21.50',
      reference: `VERIFY-${stamp}`,
      lines: [
        { value_date: '2026-09-02', description: 'Bank charge', reference: 'CHG-1', amount: '-25.00' },
        { value_date: '2026-09-03', description: 'Interest', reference: 'INT-1', amount: '3.50' },
      ],
    });
    if (brought.status !== 201) {
      console.log(`  x POST /treasury/statements -> ${brought.status}, want 201`);
      failures += 1;
    } else {
      expectFields('  statement', brought.json, [
        'id',
        'account',
        'starts_on',
        'ends_on',
        'opening_balance',
        'closing_balance',
        // Whether anybody has signed it off, which is NOT the same question
        // as whether the arithmetic currently comes out. The badge reads
        // this and the figure reads `difference`.
        'status',
        // What the books say for the same account on the same date.
        'ledger_balance',
        // What is left once both exception lists are taken off. The figure
        // the whole exercise is about.
        'difference',
        'reconciled',
        'lines',
      ]);

      const detail = await check(
        'GET /treasury/statements/{id}',
        `/treasury/statements/${brought.json.id}?company_id=${CO}`,
        ['id', 'lines', 'difference', 'ledger_balance'],
      );
      if (detail?.lines?.[0]) {
        expectFields('  statement line', detail.lines[0], [
          'id',
          'value_date',
          'description',
          // Signed from the BANK\u2019s point of view: positive is money
          // arriving. The paste box says so, because nothing else could.
          'amount',
        ]);
      }

      // C11\u2019s exception report, and the more useful of the two lists: the
      // cheque that never cleared and the payment recorded twice.
      const books = detail?.unmatched_in_books ?? [];
      if (books[0]) {
        expectFields('  entry the bank has not seen', books[0], [
          'id',
          'entry_date',
          'entry_no',
          'amount',
        ]);
      } else {
        console.log('  -  the books hold nothing unmatched; that list was not exercised');
      }

      const line = (detail?.lines ?? []).find((l) => !l.matched_to);
      if (line && books[0]) {
        const paired = await post(
          `/treasury/lines/${line.id}/match?company_id=${CO}`,
          { journal_line_id: books[0].id },
        );
        // An empty journal line is how the route expresses "undo". One route
        // rather than two, because a person toggles between them.
        const undone = await post(
          `/treasury/lines/${line.id}/match?company_id=${CO}`,
          { journal_line_id: '' },
        );
        if (paired.status === 204 && undone.status === 204) {
          console.log('  ok a line can be paired by hand and unpaired again');
        } else {
          console.log(
            `  x pairing -> ${paired.status}, undoing -> ${undone.status}, want 204 and 204`,
          );
          failures += 1;
        }
      }

      // Signing off is refused while anything is unexplained, and the refusal
      // names the amount. That refusal is the feature: a reconciliation that
      // could be signed with an unexplained difference is a piece of paper.
      const signOff = await post(
        `/treasury/statements/${brought.json.id}/reconcile?company_id=${CO}`,
        {},
      );
      if (signOff.status === 409) {
        console.log(
          '  ok signing off is refused while something is unexplained, and says how much',
        );
      } else if (signOff.status === 200) {
        console.log(
          '  ok nothing was left unexplained, and the statement signed off',
        );
        // A signed-off statement is frozen by a trigger. The refusal has to
        // reach the caller as a refusal: it used to arrive as a 500, because
        // the trigger raises with `restrict_violation` and Translate knew
        // only the default RAISE code.
        const frozenLine = (detail?.lines ?? [])[0];
        if (frozenLine) {
          const frozen = await post(
            `/treasury/lines/${frozenLine.id}/match?company_id=${CO}`,
            { journal_line_id: '' },
          );
          if (frozen.status === 409 || frozen.status === 422) {
            console.log('  ok a signed-off statement refuses a change, rather than failing');
          } else {
            console.log(
              `  x changing a signed-off statement came back ${frozen.status}, want a refusal`,
            );
            failures += 1;
          }
        }
      } else {
        console.log(`  x signing off came back ${signOff.status}`);
        failures += 1;
      }
    }
  }
  if (!statements?.data) {
    console.log('  -  the statement list did not answer with data');
  }

  // A PERIOD, not a page: totals for a date range with the expenses inside
  // it, because "what did we spend last month" is the question and a list
  // alone cannot answer it.
  const period = await check(
    'GET /expenses',
    `/expenses?company_id=${CO}`,
    [
      'from',
      'to',
      'total',
      // E2.3 restricts input VAT recovery by CATEGORY. The half that cannot
      // be reclaimed is part of the cost, and one combined tax figure would
      // hide it.
      'tax_recoverable',
      'tax_absorbed',
      'by_head',
      'count',
      'expenses',
    ],
  );

  const expenseID = period?.expenses?.[0]?.id;
  if (!expenseID) {
    console.log('  -  nothing spent in the period; the voucher shape was not exercised');
  } else {
    expectFields('  expense row', period.expenses[0], [
      'expense_no',
      'expense_date',
      // A ROLE -- cash or bank -- rather than an account id, because every
      // company has one of each and the chart already maps them.
      'paid_from',
      'subtotal_net',
      'tax_total',
      'total_inclusive',
    ]);

    const one = await check(
      'GET /expenses/{id}',
      `/expenses/${expenseID}?company_id=${CO}`,
      ['id', 'expense_no', 'subtotal_net', 'tax_recoverable', 'tax_absorbed', 'lines'],
    );
    if (one?.lines?.[0]) {
      expectFields('  expense line', one.lines[0], [
        'head_id',
        'net_amount',
        'tax_treatment',
        'tax_amount',
        'tax_recoverable',
        'tax_absorbed',
        // Net plus whatever tax was absorbed: what actually lands in the
        // expense account.
        'charge_amount',
      ]);
    }
  }

  const heads = await check(
    'GET /expenses/heads',
    `/expenses/heads?company_id=${CO}`,
    [
      'id',
      'name',
      'account_code',
      // What decides whether the tax on a line comes back. The form says so
      // as soon as a category is chosen, rather than letting somebody find
      // out on the VAT return.
      'input_vat_recoverable',
      'is_active',
    ],
    (j) => j.data[0],
  );
  if (!heads?.data?.length) {
    console.log('  -  no expense heads seeded');
  }

  // --- what an expense is booked TO, which the setup screen configures ---

  // Behind `expense.manage_heads`, not `expense.view`: the chart a category
  // posts to is a configuration decision, and the sidebar entry is gated on
  // the same permission so the link and the screen behind it agree.
  const ledger = await check(
    'GET /expenses/accounts',
    `/expenses/accounts?company_id=${CO}`,
    ['id', 'code', 'name'],
    (j) => j.data[0],
  );

  await check(
    'GET /expenses/departments',
    `/expenses/departments?company_id=${CO}&include_inactive=true`,
    null,
  );
  const schedules = await check(
    'GET /expenses/recurring',
    `/expenses/recurring?company_id=${CO}`,
    null,
  );
  if (schedules?.data?.[0]) {
    expectFields('  standing cost', schedules.data[0], [
      'id',
      'name',
      'head_id',
      'amount',
      'paid_from',
      // A frequency AND an interval. Quarterly is monthly every three; the
      // service refuses "quarterly" as a frequency and says so.
      'frequency',
      'interval_count',
      'starts_on',
      // Stored rather than derived, because "the same day next month" is a
      // judgement somebody has to be able to see and override.
      'next_due_on',
      'is_active',
    ]);
  } else {
    console.log('  -  no standing costs; the schedule shape was not exercised');
  }

  const suffix = String(Date.now()).slice(-6);
  const account = ledger?.data?.[0];
  if (!account) {
    console.log('  -  no postable expense account; the category writes were not exercised');
  } else {
    // Omitting the VAT decision must be REFUSED, not defaulted. This is what
    // makes the empty first option on the form correct rather than a
    // nicety: "false silently stops a shop reclaiming VAT it is entitled to,
    // true silently claims VAT on entertainment."
    const undecided = await post(`/expenses/heads?company_id=${CO}`, {
      code: `VAT${suffix}`,
      name: 'Undecided',
      account_id: account.id,
    });
    if (undecided.status === 400 && undecided.json?.error?.fields?.input_vat_recoverable) {
      console.log('  ok a category with no VAT decision is refused, and the field is named');
    } else {
      console.log(
        `  x a category with no VAT decision came back ${undecided.status}, want 400`,
      );
      failures += 1;
    }

    const made = await post(`/expenses/heads?company_id=${CO}`, {
      code: `CFG${suffix}`,
      name: 'Vehicle fuel',
      name_ar: 'وقود المركبات',
      account_id: account.id,
      input_vat_recoverable: false,
    });
    if (made.status !== 201) {
      console.log(`  x POST /expenses/heads -> ${made.status}, want 201`);
      failures += 1;
    } else {
      expectFields('  new category', made.json, [
        'id',
        'code',
        'account_code',
        'input_vat_recoverable',
        'is_active',
        // What has gone through it, so a "where is my money going" list
        // reads without a second request.
        'spent',
        'currency',
      ]);

      // The UPDATE statement does not touch the code. The form disables the
      // field rather than sending a value that is accepted and ignored,
      // which reads as a save that did not stick.
      const edited = await put(`/expenses/heads/${made.json.id}?company_id=${CO}`, {
        code: 'CHANGED',
        name: 'Vehicle fuel and tolls',
        account_id: account.id,
        input_vat_recoverable: true,
      });
      if (edited.status === 200 && edited.json?.code === made.json.code) {
        console.log('  ok a category code is fixed once saved, and an edit cannot move it');
      } else {
        console.log(
          `  x PUT /expenses/heads -> ${edited.status}, code ${edited.json?.code}, want 200 and ${made.json.code}`,
        );
        failures += 1;
      }

      // Retire, never delete: expenses already booked to it have to keep
      // saying what they were for. 204, with no body -- unlike the
      // department toggle below, which answers with the row.
      const retired = await post(
        `/expenses/heads/${made.json.id}/active?company_id=${CO}`,
        { active: false },
      );
      const withRetired = await call(
        `/expenses/heads?company_id=${CO}&include_retired=true`,
      );
      const found = withRetired.json?.data?.find((h) => h.id === made.json.id);
      if (retired.status === 204 && found && found.is_active === false) {
        console.log('  ok a category is retired rather than deleted, and comes back on request');
      } else {
        console.log(
          `  x retiring a category -> ${retired.status}, found ${JSON.stringify(found?.is_active)}`,
        );
        failures += 1;
      }
      await post(`/expenses/heads/${made.json.id}/active?company_id=${CO}`, {
        active: true,
      });
    }
  }

  const dept = await post(`/expenses/departments?company_id=${CO}`, {
    code: `D${suffix}`,
    name: 'Kitchen',
  });
  if (dept.status !== 201) {
    console.log(`  x POST /expenses/departments -> ${dept.status}, want 201`);
    failures += 1;
  } else {
    expectFields('  new department', dept.json, ['id', 'code', 'name', 'is_active']);
    const off = await post(
      `/expenses/departments/${dept.json.id}/active?company_id=${CO}`,
      { active: false },
    );
    if (off.status === 200 && off.json?.is_active === false) {
      console.log('  ok a department answers its toggle with the row, unlike a category');
    } else {
      console.log(`  x retiring a department -> ${off.status}`);
      failures += 1;
    }
    await post(`/expenses/departments/${dept.json.id}/active?company_id=${CO}`, {
      active: true,
    });
  }

  const headForSchedule = heads?.data?.find((h) => h.is_active) ?? heads?.data?.[0];
  if (!headForSchedule) {
    console.log('  -  no category to hang a standing cost on');
  } else {
    const day = new Date().toISOString().slice(0, 10);

    // "quarterly" is not a frequency. The refusal names the field AND says
    // what to use instead, which is why the form offers the words a business
    // uses and sends the pair the API stores.
    const badCadence = await post(`/expenses/recurring?company_id=${CO}`, {
      name: 'Refused',
      head_id: headForSchedule.id,
      amount: '1.00',
      paid_from: 'bank',
      frequency: 'quarterly',
      interval_count: 1,
      starts_on: day,
    });
    if (badCadence.status === 400 && badCadence.json?.error?.fields?.frequency) {
      console.log('  ok quarterly is refused as a frequency, and the refusal says what to send');
    } else {
      console.log(`  x quarterly as a frequency came back ${badCadence.status}, want 400`);
      failures += 1;
    }

    const quarterly = await post(`/expenses/recurring?company_id=${CO}`, {
      name: `Insurance ${suffix}`,
      head_id: headForSchedule.id,
      amount: '900.00',
      paid_from: 'bank',
      frequency: 'monthly',
      interval_count: 3,
      starts_on: day,
    });
    if (quarterly.status === 201 && quarterly.json?.interval_count === 3) {
      console.log('  ok quarterly is monthly every three, and stores as one');
    } else {
      console.log(`  x monthly every three -> ${quarterly.status}`);
      failures += 1;
    }

    if (quarterly.status === 201) {
      const paused = await post(
        `/expenses/recurring/${quarterly.json.id}/active?company_id=${CO}`,
        { active: false },
      );
      if (paused.status === 200 && paused.json?.is_active === false) {
        console.log('  ok a standing cost is paused rather than deleted');
      } else {
        console.log(`  x pausing a standing cost -> ${paused.status}`);
        failures += 1;
      }
    }

    // Booking is behind `expense.record`, not the configuration permission:
    // it writes expenses, and running it twice is safe because the guard is
    // a unique index on (schedule, due date) rather than a check anybody
    // performs.
    const booked = await post(`/expenses/recurring/generate?company_id=${CO}`, {});
    if (booked.status === 200) {
      expectFields('  generate', booked.json, ['created', 'skipped', 'expenses']);
      const again = await post(`/expenses/recurring/generate?company_id=${CO}`, {});
      if (again.status === 200 && again.json?.created === 0) {
        console.log('  ok booking what is due twice books nothing the second time');
      } else {
        console.log(
          `  x a second generate created ${again.json?.created}, want 0`,
        );
        failures += 1;
      }
    } else {
      console.log(`  x POST /expenses/recurring/generate -> ${booked.status}`);
      failures += 1;
    }
  }

  // paid_from is a role. An account id there is refused, which is what makes
  // the two-option select on the form correct rather than a simplification.
  const wrong = await post(`/expenses?company_id=${CO}`, {
    uuid: crypto.randomUUID(),
    expense_date: new Date().toISOString().slice(0, 10),
    paid_from: '00000000-0000-0000-0000-000000000000',
    lines: [{ head_id: heads?.data?.[0]?.id ?? '', net_amount: '1.00', tax_treatment: 'standard' }],
  });
  if (wrong.status === 400) {
    console.log('  ok paid_from is a role, and an account id is refused');
  } else {
    console.log(`  x an account id in paid_from came back ${wrong.status}, want 400`);
    failures += 1;
  }
}

console.log('\nTHE FINANCIAL STATEMENTS');
{
  const year = new Date().getFullYear();
  const from = `${year}-01-01`;
  const to = new Date().toISOString().slice(0, 10);

  const trial = await check(
    'GET /reports/trial-balance',
    `/reports/trial-balance?company_id=${CO}`,
    ['as_of', 'base_currency', 'rows'],
  );
  if (trial?.rows?.[0]) {
    expectFields('  trial balance row', trial.rows[0], [
      'account_id',
      'code',
      'name',
      // The TYPE, so the screen can group without a second call to the chart.
      'type',
      'debit',
      'credit',
    ]);
  }

  const pl = await check(
    'GET /reports/profit-and-loss',
    `/reports/profit-and-loss?company_id=${CO}&from=${from}&to=${to}`,
    [
      'from',
      'to',
      'revenue',
      'revenue_total',
      'cost_of_sales',
      'cost_of_sales_total',
      // Stated rather than left to the reader to subtract, which is the whole
      // point of a statement.
      'gross_profit',
      'expenses',
      'expenses_total',
      'net_profit',
    ],
  );

  const sheet = await check(
    'GET /reports/balance-sheet',
    `/reports/balance-sheet?company_id=${CO}`,
    [
      'as_of',
      'assets',
      'assets_total',
      'liabilities',
      'liabilities_total',
      'equity',
      'equity_total',
      // This year's profit is equity that has not been closed off yet, and
      // leaving it out is why a balance sheet appears not to balance.
      'current_earnings',
      'equity_and_liabilities',
      'difference',
      // The SERVER's answer. The screen shows it and never recomputes it.
      'balanced',
    ],
  );
  if (sheet && sheet.balanced !== true) {
    console.log(`  x the balance sheet does not balance: out by ${sheet.difference}`);
    failures += 1;
  } else if (sheet) {
    console.log('  ok the balance sheet balances');
  }

  // The two statements have to agree about the same period's profit, or one of
  // them is describing a different set of books.
  if (pl && sheet && pl.net_profit !== undefined) {
    if (sheet.current_earnings === pl.net_profit) {
      console.log("  ok the balance sheet carries the profit and loss's own figure");
    } else {
      console.log(
        `  -  current_earnings ${sheet.current_earnings} against net_profit ${pl.net_profit};` +
          ' the two cover different periods unless the year started on the from-date',
      );
    }
  }

  await check(
    'GET /reports/cash-flow',
    `/reports/cash-flow?company_id=${CO}&from=${from}&to=${to}`,
    ['from', 'to', 'opening', 'closing', 'in', 'out'],
  );

  const vat = await check(
    'GET /reports/vat-return',
    `/reports/vat-return?company_id=${CO}&from=${from}&to=${to}`,
    [
      // The market names its own tax. Nothing on the screen says "VAT".
      'country',
      'model',
      'supplies',
      'total_net',
      'output_tax_total',
      'input_tax_total',
      'net_payable',
      // Whether it agrees with the ledger it was drawn from.
      'reconciled',
      'filed',
    ],
  );
  if (vat?.supplies?.[0]) {
    expectFields('  taxable supply', vat.supplies[0], [
      'treatment',
      'net_amount',
      'tax_amount',
      'invoice_count',
    ]);
  }

  // The field the whole screen is built around: what stands between these
  // figures and a filing, in the server's own words. A return that reported
  // nothing outstanding AND did not reconcile would be the dangerous case.
  if (vat) {
    const blockers = vat.outstanding ?? [];
    if (blockers.length > 0) {
      console.log(
        `  ok the return says what stops it being filed (${blockers.length} reasons)`,
      );
    } else if (vat.reconciled) {
      console.log('  ok nothing is outstanding on this return');
    } else {
      console.log('  x the return does not reconcile and reports no reason');
      failures += 1;
    }
  }
}
console.log('\nWHAT CUSTOMERS OWE, AND TAKING IT');
{
  // The mirror of the supplier ageing, and the same five buckets -- so the two
  // screens share one table. Aged from the DUE date on both sides.
  const owed = await check(
    'GET /receivables/ageing',
    `/receivables/ageing?company_id=${CO}`,
    ['as_of', 'rows', 'total', 'base_currency'],
  );
  if (owed?.rows?.[0]) {
    expectFields('  customer ageing row', owed.rows[0], [
      'customer_id',
      'customer',
      'not_due',
      'days_0_30',
      'days_31_60',
      'days_61_90',
      'days_90_plus',
      'total',
    ]);
  } else {
    console.log('  -  nobody owes anything; the ageing row was not exercised');
  }

  const owing = owed?.rows?.[0]?.customer_id;
  if (!owing) {
    console.log('  -  no customer with a balance; taking a payment was not exercised');
  } else {
    const open = await check(
      'GET /customers/{id}/open-invoices',
      `/customers/${owing}/open-invoices?company_id=${CO}`,
      null,
    );
    const invoice = open?.data?.[0];
    if (!invoice) {
      console.log('  -  that customer has no open invoice');
    } else {
      expectFields('  open invoice', invoice, [
        'invoice_id',
        'due_date',
        // What came back off the account through a RETURN, apart from money
        // paid: a customer querying their balance has to tell the two apart.
        'credited',
        'received',
        'outstanding',
      ]);

      // More than the invoice owes is refused, so the figure on the screen is
      // capped rather than the whole receipt being thrown away.
      const tooMuch = await post(`/receivables/receipts?company_id=${CO}`, {
        uuid: crypto.randomUUID(),
        customer_id: owing,
        method: 'cash',
        allocations: [{ invoice_id: invoice.invoice_id, amount: '999999.00' }],
      });
      if (tooMuch.status === 400 || tooMuch.status === 409) {
        console.log('  ok more than an invoice owes cannot be allocated to it');
      } else {
        console.log(`  x an over-allocation came back ${tooMuch.status}`);
        failures += 1;
      }

      // Nothing allocated at all: a receipt has to say what it settles,
      // because guessing produces a statement the customer disputes.
      const unallocated = await post(`/receivables/receipts?company_id=${CO}`, {
        uuid: crypto.randomUUID(),
        customer_id: owing,
        method: 'cash',
        allocations: [],
      });
      if (unallocated.status === 400) {
        console.log('  ok a receipt that settles nothing is refused');
      } else {
        console.log(`  x an unallocated receipt came back ${unallocated.status}`);
        failures += 1;
      }

      const receiptUUID = crypto.randomUUID();
      const body = {
        uuid: receiptUUID,
        customer_id: owing,
        method: 'cash',
        reference: 'verify:api',
        received_on: new Date().toISOString().slice(0, 10),
        allocations: [{ invoice_id: invoice.invoice_id, amount: invoice.outstanding }],
      };
      const taken = await post(`/receivables/receipts?company_id=${CO}`, body);
      if (taken.status !== 201 && taken.status !== 200) {
        console.log(`  x POST /receivables/receipts: HTTP ${taken.status}`);
        failures += 1;
      } else {
        expectFields('POST /receivables/receipts', taken.json, [
          'id',
          // What a customer is read on the phone.
          'receipt_number',
          'amount',
          // Which invoices it settled and what each is left owing, so a part
          // payment reads differently from a settlement.
          'settled',
        ]);

        const retry = await post(`/receivables/receipts?company_id=${CO}`, body);
        if (
          retry.json?.already_taken === true &&
          retry.json?.receipt_number === taken.json.receipt_number
        ) {
          console.log('  ok the same receipt arriving twice takes the money once');
        } else {
          console.log(
            `  x a retried receipt made ${retry.json?.receipt_number} beside ${taken.json.receipt_number}`,
          );
          failures += 1;
        }

        // And what they owed has fallen.
        const after = await call(
          `/customers/${owing}/open-invoices?company_id=${CO}`,
        );
        const still = (after.json?.data ?? []).find(
          (i) => i.invoice_id === invoice.invoice_id,
        );
        if (!still || Number(still.outstanding) < Number(invoice.outstanding)) {
          console.log('  ok what the customer owes falls by what was taken');
        } else {
          console.log(
            `  x outstanding stayed at ${still?.outstanding} after a payment`,
          );
          failures += 1;
        }
      }
    }
  }
}
console.log('\nTHE LEDGER BY HAND');
{
  // The chart of accounts. Nothing listed it until now: every posting path
  // resolves accounts by ROLE, which is right for a rule and useless to a
  // person writing an adjustment, and a journal line names an account_id.
  const chart = await check(
    'GET /accounting/chart',
    `/accounting/chart?company_id=${CO}`,
    ['id', 'code', 'name', 'type', 'is_postable', 'is_control', 'balance'],
    (j) => j.data[0],
  );

  const accounts = chart?.data ?? [];
  const control = accounts.find((a) => a.is_control);
  if (control) {
    // C9.3 makes three of these hard invariants. The screen marks them,
    // because a difference between a control account and its sub-ledger is a
    // real error rather than a rounding.
    if (control.control_of) {
      console.log(`  ok a control account says what it controls (${control.control_of})`);
    } else {
      console.log('  x a control account does not say what it controls');
      failures += 1;
    }
  }
  if (accounts.some((a) => a.role)) {
    console.log('  ok the chart says what the posting rules call an account');
  } else {
    console.log('  -  no account is mapped to a rule role on this company');
    }

  const postable = accounts.filter((a) => a.is_postable && a.is_active);
  await check(
    'GET /accounting/journals',
    `/accounting/journals?company_id=${CO}`,
    null,
  );

  if (postable.length < 2) {
    console.log('  -  fewer than two postable accounts; journals were not exercised');
  } else {
    const [one, two] = postable;
    const day = new Date().toISOString().slice(0, 10);
    const entry = (over) => ({
      uuid: crypto.randomUUID(),
      entry_date: day,
      reason: 'verify:api -- an accrual',
      lines: [
        { account_id: one.id, debit: '250.00' },
        { account_id: two.id, credit: '250.00' },
      ],
      ...over,
    });

    // C10 wants a reason on every hand-written entry: it is what somebody
    // reading the ledger a year from now has to go on.
    const silent = await post(`/accounting/journals?company_id=${CO}`, entry({ reason: '' }));
    if (silent.status === 400 && silent.json?.error?.fields?.reason) {
      console.log('  ok a journal with no reason is refused, and the field is named');
    } else {
      console.log(`  x a journal with no reason came back ${silent.status}, want 400`);
      failures += 1;
    }

    // The refusal states the DIFFERENCE, which is the number a person has to
    // find -- not the two totals, which leaves them doing the subtraction.
    const lopsided = await post(`/accounting/journals?company_id=${CO}`, entry({
      lines: [
        { account_id: one.id, debit: '100.00' },
        { account_id: two.id, credit: '60.00' },
      ],
    }));
    if (lopsided.status === 400 && /difference of 40/i.test(lopsided.json?.error?.message ?? '')) {
      console.log('  ok an unbalanced journal is refused, and the refusal says by how much');
    } else {
      console.log(`  x an unbalanced journal came back ${lopsided.status}`);
      failures += 1;
    }

    // A line is a debit or a credit. Both is ambiguous and neither is empty.
    const bothSides = await post(`/accounting/journals?company_id=${CO}`, entry({
      lines: [
        { account_id: one.id, debit: '10.00', credit: '10.00' },
        { account_id: two.id, credit: '10.00' },
      ],
    }));
    if (bothSides.status === 400) {
      console.log('  ok a line cannot carry a debit and a credit at once');
    } else {
      console.log(`  x a two-sided line came back ${bothSides.status}`);
      failures += 1;
    }

    const body = entry({});
    const posted = await post(`/accounting/journals?company_id=${CO}`, body);
    if (posted.status !== 201) {
      console.log(`  x POST /accounting/journals: HTTP ${posted.status}`);
      failures += 1;
    } else {
      expectFields('POST /accounting/journals', posted.json, [
        'id',
        'journal_no',
        // The ledger entry it became. A manual journal is a document ABOUT a
        // posting, not the posting itself.
        'journal_entry_id',
        'entry_no',
        'total',
        'lines',
      ]);
      expectFields('  journal line', posted.json.lines[0], [
        'account_id',
        // The code and the name, so a screen shows "1100 Cash" without a
        // second request per line.
        'account_code',
        'account_name',
        'debit',
        'credit',
      ]);

      // A retry answers 200 with the entry already written. It answered 201
      // either way, which says "created" of something it did not create.
      const retry = await post(`/accounting/journals?company_id=${CO}`, body);
      if (
        retry.status === 200 &&
        retry.json?.journal_no === posted.json.journal_no &&
        retry.json?.already_recorded === true
      ) {
        console.log('  ok the same journal arriving twice is posted once, and says so');
      } else {
        console.log(
          `  x a retried journal came back ${retry.status} as ${retry.json?.journal_no}`,
        );
        failures += 1;
      }

      await check(
        'GET /accounting/journals/{id}',
        `/accounting/journals/${posted.json.id}?company_id=${CO}`,
        ['id', 'journal_no', 'reason', 'lines'],
      );

      // Reversing wants its OWN reason: the opposite entry is a separate fact
      // in the ledger and this is the only place that says what it was for.
      const noWhy = await post(
        `/accounting/journals/${posted.json.id}/reverse?company_id=${CO}`,
        {},
      );
      if (noWhy.status === 400) {
        console.log('  ok a reversal with no reason on it is refused');
      } else {
        console.log(`  x a reversal with no reason came back ${noWhy.status}`);
        failures += 1;
      }

      const undo = {
        uuid: crypto.randomUUID(),
        reason: 'verify:api -- the accrual was invoiced after all',
      };
      const reversed = await post(
        `/accounting/journals/${posted.json.id}/reverse?company_id=${CO}`,
        undo,
      );
      if (reversed.status !== 201) {
        console.log(`  x POST .../reverse: HTTP ${reversed.status}`);
        failures += 1;
      } else {
        const flipped =
          reversed.json?.reverses_id === posted.json.id &&
          reversed.json?.lines?.[0]?.credit === posted.json.lines[0].debit;
        if (flipped) {
          console.log('  ok a reversal is the opposite of what was posted, and links to it');
        } else {
          console.log('  x a reversal does not mirror the entry it undoes');
          failures += 1;
        }

        const original = await call(
          `/accounting/journals/${posted.json.id}?company_id=${CO}`,
        );
        if (original.json?.reversed_by) {
          console.log('  ok the original says it has been reversed');
        } else {
          console.log('  x the original does not know it was reversed');
          failures += 1;
        }
      }
    }
  }
}
console.log('\nORDERS');
{
  const orders = await check(
    'GET /orders',
    `/orders?company_id=${CO}&limit=5`,
    ['id', 'order_no', 'state', 'channel', 'currency', 'subtotal', 'discount', 'total', 'created_at'],
    (j) => j.data[0],
  );

  const orderID = orders?.data?.[0]?.id;
  if (!orderID) {
    console.log('  -  no orders; the line and document shapes were not exercised');
  } else {
    const one = await check(
      'GET /orders/{id}',
      `/orders/${orderID}?company_id=${CO}`,
      ['id', 'order_no', 'state', 'total', 'lines'],
    );
    if (one?.lines?.[0]) {
      // qty_picked and qty_delivered are what the screen puts beside the
      // ordered quantity: how far along each line is, which is a different
      // question from how far along the order is.
      expectFields('  order line', one.lines[0], [
        'id',
        'line_no',
        'sku',
        'product',
        'qty',
        'unit_price',
        'discount',
        'line_total',
        'qty_picked',
        'qty_delivered',
      ]);
    }

    // B11 draws three, and they are three different jobs. The delivery note
    // carries no prices AT ALL -- the type it is built from has no fields
    // for them -- and the screen depends on that being true.
    for (const kind of ['picking', 'packing', 'delivery']) {
      const doc = await call(`/orders/${orderID}/documents/${kind}?company_id=${CO}`);
      if (doc.status !== 200) {
        console.log(`  x GET /orders/{id}/documents/${kind}: HTTP ${doc.status}`);
        failures += 1;
        continue;
      }
      expectFields(`  ${kind} slip`, doc.json, ['kind', 'order_no', 'printed_at', 'lines']);
      const priced = (doc.json.lines ?? []).some(
        (l) => 'unit_price' in l || 'line_total' in l || 'price' in l,
      );
      if (priced) {
        console.log(`  x the ${kind} document carries prices, which B11 forbids`);
        failures += 1;
      }
    }

    // Anything else is refused by name rather than drawn as something else.
    const odd = await call(`/orders/${orderID}/documents/invoice?company_id=${CO}`);
    if (odd.status === 400) {
      console.log('  ok a document kind this product does not print is refused');
    } else {
      console.log(`  x an unknown document kind came back ${odd.status}, want 400`);
      failures += 1;
    }
  }
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
          // A lot when the line needs one. `tracks_batches` is on the PO
          // line for exactly this reason -- so a receiving screen knows
          // before it submits, rather than finding out from the refusal --
          // and a checker that ignored it would only ever exercise the
          // untracked half of receiving.
          lines: [
            {
              po_line_id: line.id,
              qty_received: '1',
              qty_rejected: '0',
              ...(line.tracks_batches
                ? {
                    batch_no: `VERIFY-${Date.now()}`,
                    expires_on: new Date(Date.now() + 90 * 86400000)
                      .toISOString()
                      .slice(0, 10),
                  }
                : {}),
            },
          ],
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
        console.log(`  x POST /purchasing/payments: HTTP ${first.status} ${JSON.stringify(first.json)}`);
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

  // --- goods going back to a supplier (B5) ---
  //
  // Against the BILL, because a debit note has no meaning without the invoice
  // it corrects. Goods refused at the door never entered stock and are already
  // recorded as rejected on the receipt.
  await check('GET /purchasing/returns', `/purchasing/returns?company_id=${CO}`, null);

  const billForReturn = bills?.data?.[0];
  if (!billForReturn) {
    console.log('  -  no bill to claim against; returns were not exercised');
  } else {
    const returnable = await check(
      'GET /purchasing/bills/{id}/returnable',
      `/purchasing/bills/${billForReturn.id}/returnable?company_id=${CO}`,
      null,
    );
    const line = returnable?.data?.[0];
    if (!line) {
      console.log('  -  that bill has no lines; the claim was not exercised');
    } else {
      expectFields('  returnable line', line, [
        'bill_line_id',
        // Cumulative across every earlier return. A screen that worked this
        // out for itself would eventually claim the same pallet twice.
        'qty_returnable',
        // The SUPPLIER's price and the SUPPLIER's rate, which is what the
        // debit note claims -- not what the stock is carrying.
        'unit_cost',
        'tax_rate',
      ]);

      // A claim with no reason is refused. An unexplained return is how the
      // value of a pallet goes missing between a clerk and a driver.
      const silent = await post(`/purchasing/returns?company_id=${CO}`, {
        uuid: crypto.randomUUID(),
        bill_id: billForReturn.id,
        reason: '',
        lines: [{ bill_line_id: line.bill_line_id, qty: '1' }],
      });
      if (silent.status === 400) {
        console.log('  ok a return with no reason on it is refused');
      } else {
        console.log(`  x a return with no reason came back ${silent.status}, want 400`);
        failures += 1;
      }

      const places = await call(`/purchasing/warehouses?company_id=${CO}`);
      const rooms = places.json?.data ?? [];

      // A business with two stock locations has to say which one the goods are
      // leaving from -- the same rule a sale follows.
      if (rooms.length > 1) {
        const nowhere = await post(`/purchasing/returns?company_id=${CO}`, {
          uuid: crypto.randomUUID(),
          bill_id: billForReturn.id,
          reason: 'verify:api -- faulty',
          lines: [{ bill_line_id: line.bill_line_id, qty: '1' }],
        });
        if (nowhere.status === 400) {
          console.log(
            '  ok a return that does not say which shelf the goods left is refused',
          );
        } else {
          console.log(`  x a return naming no location came back ${nowhere.status}`);
          failures += 1;
        }
      }

      // More than is left is refused, and the refusal says how much there is.
      const greedy = await post(`/purchasing/returns?company_id=${CO}`, {
        uuid: crypto.randomUUID(),
        bill_id: billForReturn.id,
        warehouse_id: rooms[0]?.id,
        reason: 'verify:api -- faulty',
        lines: [{ bill_line_id: line.bill_line_id, qty: '99999' }],
      });
      if (greedy.status === 409 || greedy.status === 400) {
        console.log('  ok more than the bill carried cannot be sent back');
      } else {
        console.log(`  x an over-claim came back ${greedy.status}`);
        failures += 1;
      }

      // The real one, from wherever the stock actually is.
      const onHandRows = await call(`/stock/on-hand?company_id=${CO}&limit=100`);
      const holding = (onHandRows.json?.data ?? []).find(
        (h) => h.variant_id === line.variant_id && Number(h.on_hand) > 0,
      );
      const from = rooms.find((w) => w.name === holding?.location) ?? rooms[0];

      const docUUID = crypto.randomUUID();
      const claim = {
        uuid: docUUID,
        bill_id: billForReturn.id,
        warehouse_id: from?.id,
        returned_on: new Date().toISOString().slice(0, 10),
        reason: 'verify:api -- one arrived faulty',
        lines: [{ bill_line_id: line.bill_line_id, qty: '1' }],
      };
      const sent = await post(`/purchasing/returns?company_id=${CO}`, claim);

      if (sent.status === 409) {
        // The shelf is short. That refusal is the feature -- goods that are
        // not there cannot go back -- so it is reported rather than failed.
        console.log(
          '  -  the shelf holds none of that line; the claim was not exercised',
        );
      } else if (sent.status !== 201) {
        console.log(`  x POST /purchasing/returns: HTTP ${sent.status}`);
        failures += 1;
      } else {
        expectFields('POST /purchasing/returns', sent.json, [
          'id',
          'return_no',
          'subtotal_net',
          'tax_total',
          'total_inclusive',
          // What the SHELF gave up, which the costing method decides and which
          // is a different figure from the claim whenever freight was added on
          // delivery or a cheaper batch has been bought since.
          'stock_value',
          'variance',
          'lines',
        ]);

        await check(
          'GET /purchasing/returns/{id}',
          `/purchasing/returns/${sent.json.id}?company_id=${CO}`,
          ['id', 'return_no', 'lines', 'stock_value', 'variance'],
        );

        // A retry claims once AND says what it said. Matching ids is not the
        // same as replaying.
        const retry = await post(`/purchasing/returns?company_id=${CO}`, claim);
        const same =
          retry.status === 200 &&
          retry.json?.return_no === sent.json.return_no &&
          retry.json?.total_inclusive === sent.json.total_inclusive &&
          retry.json?.already_returned === true;
        if (same) {
          console.log('  ok a retried return claims once and replays the whole claim');
        } else {
          console.log(
            `  x a retried return came back ${retry.status} as ${retry.json?.return_no}`,
          );
          failures += 1;
        }

        // And what is left to send back has fallen by what went.
        const after = await call(
          `/purchasing/bills/${billForReturn.id}/returnable?company_id=${CO}`,
        );
        const nowLeft = (after.json?.data ?? []).find(
          (l) => l.bill_line_id === line.bill_line_id,
        );
        if (Number(nowLeft?.qty_returnable) === Number(line.qty_returnable) - 1) {
          console.log('  ok what may go back falls by what went back');
        } else {
          console.log(
            `  x returnable went from ${line.qty_returnable} to ${nowLeft?.qty_returnable}`,
          );
          failures += 1;
        }
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

        // --- swapping goods, which is a credit note and a sale at once ---
        //
        // Run at the counter and before the close, because both halves
        // join this terminal's invoice chain and a till with no open
        // session cannot ring anything up.
        const sellable = (
          await call(`/catalog/snapshot?company_id=${CO}&limit=5`)
        ).json?.items?.filter((i) => i.is_active) ?? [];

        // A branch with more than one stock location must SAY which one.
        // The till sent nothing at all, so in such a shop no sale, return
        // or exchange was possible; found by driving a sale rather than
        // by reading the code.
        const stockLocations = await call(`/stock/locations?company_id=${CO}`);
        const here = (stockLocations.json?.data ?? []).filter(
          (l) => l.is_active && l.store === counter.store,
        );
        const sellFrom = here.length > 1 ? here[0].id : null;
        // Where to ASK about stock, which is a different question from
        // whether the till has to SAY where it is selling from. A shop
        // with one location need not name it, and this used to reuse
        // sellFrom for both -- so in every single-location shop the
        // replacement search below was skipped and the exchange went
        // unexercised, which is most shops.
        const stockAt = here.length ? here[0].id : null;
        if (here.length > 1) {
          const nowhere = await post(
            `/pos/sales?company_id=${CO}`,
            {
              invoice_uuid: crypto.randomUUID(),
              doc_type: 'simplified',
              issued_at: new Date().toISOString(),
              lines: [
                {
                  variant_id: sellable[0]?.id,
                  description: sellable[0]?.name,
                  qty: '1',
                  unit_price: sellable[0]?.price,
                  tax_treatment: sellable[0]?.tax_treatment,
                },
              ],
              tenders: [{ method: 'cash', amount: sellable[0]?.price }],
            },
            till,
          );
          if (nowhere.status === 400) {
            console.log(
              '  ok a branch with two stock locations refuses a sale that does not name one',
            );
          } else {
            console.log(
              `  x a sale naming no stock location came back ${nowhere.status}, want 400`,
            );
            failures += 1;
          }
        }

        // The replacement has to be something actually on the shelf. This
        // asserted 201 unconditionally and started failing once the dev shop
        // had been sold down -- by this very section, which rings up a sale on
        // every run. A 409 saying "there are 1 fewer Abaya, Black in stock
        // than this needs" is the server being RIGHT, and a check that reads
        // correct behaviour as a mismatch trains people to ignore it.
        //
        // The catalogue snapshot carries no stock on purpose: it is the till's
        // offline copy, and what is on the shelf is a different question asked
        // of a different route.
        let replacement = null;
        if (stockAt) {
          for (const candidate of sellable.slice(1)) {
            const stock = await call(
              `/stock/availability?company_id=${CO}` +
                `&variant_id=${candidate.id}&warehouse_id=${stockAt}`,
            );
            if (Number(stock.json?.available_to_sell ?? 0) >= 1) {
              replacement = candidate;
              break;
            }
          }
        }

        if (sellable.length < 2) {
          console.log('  -  fewer than two sellable items; the exchange was not exercised');
        } else if (!replacement) {
          console.log(
            '  -  nothing on the shelf can serve as a replacement; the exchange ' +
              'was not exercised',
          );
        } else {
          const saleUUID = crypto.randomUUID();
          const sold = await post(
            `/pos/sales?company_id=${CO}`,
            {
              invoice_uuid: saleUUID,
              doc_type: 'simplified',
              issued_at: new Date().toISOString(),
              ...(sellFrom ? { warehouse_id: sellFrom } : {}),
              lines: [
                {
                  variant_id: sellable[0].id,
                  description: sellable[0].name,
                  qty: '1',
                  unit_price: sellable[0].price,
                  tax_treatment: sellable[0].tax_treatment,
                },
              ],
              tenders: [{ method: 'cash', amount: sellable[0].price }],
            },
            till,
          );
          if (sold.status !== 201) {
            console.log(`  x POST /pos/sales: HTTP ${sold.status}`);
            failures += 1;
          } else {
            const back = await fetch(
              `${API}/pos/sales/${sold.json.invoice_id}/returnable`,
              { headers: { Authorization: `Bearer ${till}` } },
            );
            const returnable = await back.json();
            const line = returnable?.lines?.[0];
            if (!line) {
              console.log('  x GET /pos/sales/{id}/returnable returned no lines');
              failures += 1;
            } else {
              expectFields('GET /pos/sales/{id}/returnable', line, [
                'line_id',
                // What may still go back, which the till must never work
                // out for itself: earlier credit notes live on the server.
                'qty_returnable',
                // Tax-inclusive, which is what an exchange settles on.
                'gross_returnable',
                'tax_treatment',
              ]);

              const creditUUID = crypto.randomUUID();
              const swap = {
                credit_note_uuid: creditUUID,
                invoice_uuid: crypto.randomUUID(),
                original_invoice_id: sold.json.invoice_id,
                issued_at: new Date().toISOString(),
                ...(sellFrom ? { warehouse_id: sellFrom } : {}),
                reason: 'verify:api -- wrong size',
                returning: [{ line_id: line.line_id, qty: '1' }],
                replacement: {
                  doc_type: 'simplified',
                  lines: [
                    {
                      variant_id: replacement.id,
                      description: replacement.name,
                      qty: '1',
                      unit_price: replacement.price,
                      tax_treatment: replacement.tax_treatment,
                    },
                  ],
                },
                settlement: [
                  {
                    method: 'cash',
                    amount: Math.abs(
                      Number(replacement.price) - Number(line.gross_returnable),
                    ).toFixed(2),
                  },
                ],
              };

              // The server states the difference and requires it EXACTLY:
              // an overpayment is change owed, not part of the sale.
              const overpaid = await post(
                `/pos/exchanges?company_id=${CO}`,
                {
                  ...swap,
                  credit_note_uuid: crypto.randomUUID(),
                  invoice_uuid: crypto.randomUUID(),
                  settlement: [{ method: 'cash', amount: '999999.00' }],
                },
                till,
              );
              if (
                overpaid.status === 400 &&
                /settles at/i.test(overpaid.json?.error?.message ?? '')
              ) {
                console.log(
                  '  ok an exchange settles at the amount the server states, and says what it is',
                );
              } else {
                console.log(
                  `  x an over-settled exchange came back ${overpaid.status}, want 400`,
                );
                failures += 1;
              }

              // C14 wants a reason on every return, and an exchange holds
              // one: unexplained returns are how refund fraud is hidden.
              const silent = await post(
                `/pos/exchanges?company_id=${CO}`,
                {
                  ...swap,
                  credit_note_uuid: crypto.randomUUID(),
                  invoice_uuid: crypto.randomUUID(),
                  reason: '',
                },
                till,
              );
              if (silent.status === 400) {
                console.log('  ok an exchange with no reason on it is refused');
              } else {
                console.log(`  x an exchange with no reason came back ${silent.status}`);
                failures += 1;
              }

              const swapped = await post(
                `/pos/exchanges?company_id=${CO}`,
                swap,
                till,
              );
              if (swapped.status !== 201) {
                console.log(`  x POST /pos/exchanges: HTTP ${swapped.status}`);
                failures += 1;
              } else {
                expectFields('POST /pos/exchanges', swapped.json, [
                  'credit_note',
                  'replacement',
                  // The offsetting portion, which goes through a clearing
                  // account and never touches the drawer.
                  'credit_applied',
                  // Signed, and the only money that actually moves.
                  'difference',
                  'customer_paid',
                ]);
                expectFields('  credit note', swapped.json.credit_note, [
                  'credit_note_id',
                  // What a cashier reads to the customer.
                  'human_number',
                  'total_inclusive',
                ]);

                // A retry after a lost response must answer what it
                // answered before, not merely point at the same rows. It
                // used to come back with the right ids, a blank number and
                // every figure zero.
                const retry = await post(
                  `/pos/exchanges?company_id=${CO}`,
                  swap,
                  till,
                );
                const same =
                  retry.status === 200 &&
                  retry.json?.credit_note?.credit_note_id ===
                    swapped.json.credit_note.credit_note_id &&
                  retry.json?.credit_note?.human_number ===
                    swapped.json.credit_note.human_number &&
                  retry.json?.difference === swapped.json.difference;
                if (same) {
                  console.log(
                    '  ok a retried exchange replays the same documents AND the same figures',
                  );
                } else {
                  console.log(
                    `  x a retried exchange came back ${retry.status} with ` +
                      `${JSON.stringify(retry.json?.credit_note?.human_number)} / ` +
                      `${retry.json?.difference}`,
                  );
                  failures += 1;
                }
              }
            }
          }
        }

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

console.log('\nSTAFF, ATTENDANCE AND PAYROLL');
{
  const staff = await check('GET /employees', `/employees?company_id=${CO}`, null);
  const person = staff?.data?.[0];
  if (person) {
    expectFields('  employee', person, [
      'id',
      'employee_no',
      'full_name',
      'joined_on',
      'status',
      // C5's alert, DERIVED on the server. A screen that recomputed it from
      // the date would be a second answer that can disagree with the one the
      // /employees/expiring route gives.
      'id_expiring_soon',
      'id_expired',
      'is_saudi',
      'commission_eligible',
    ]);

    // The pay fields are omitted for a caller without hr.view_pay, so they are
    // checked only where they arrived at all. The owner holds it, so their
    // absence here would mean the route stopped sending them.
    if ('basic_salary' in person) {
      expectFields('  employee pay', person, [
        'basic_salary',
        'housing_allowance',
        'transport_allowance',
        'other_allowance',
        'currency',
      ]);
    } else {
      console.log('  x the owner holds hr.view_pay and got no pay fields');
      failures += 1;
    }

    await check(
      'GET /employees/{id}',
      `/employees/${person.id}?company_id=${CO}`,
      ['id', 'employee_no', 'full_name', 'status'],
    );
  } else {
    console.log('  -  nobody is employed; the employee shape was not exercised');
  }

  // The route that answered 500 on every call it ever received, because
  // nothing had a screen to call it from.
  const expiring = await check(
    'GET /employees/expiring',
    `/employees/expiring?company_id=${CO}&days=60`,
    null,
  );
  if (expiring?.data?.[0]) {
    expectFields('  expiring document', expiring.data[0], [
      'id',
      'full_name',
      'id_expires_on',
      'id_expiring_soon',
      'id_expired',
    ]);
  } else {
    console.log('  -  no document is close to expiry; the alert row was not exercised');
  }

  const month = new Date();
  const first = `${month.getFullYear()}-${String(month.getMonth() + 1).padStart(2, '0')}-01`;
  const attendance = await check(
    'GET /attendance',
    `/attendance?company_id=${CO}&from=${first}`,
    null,
  );
  if (attendance?.data?.[0]) {
    expectFields('  attendance day', attendance.data[0], [
      'id',
      'employee_id',
      'on_date',
      'status',
      'hours_worked',
      'overtime_hours',
      'late_minutes',
    ]);
  } else {
    console.log('  -  no attendance this month; the day shape was not exercised');
  }

  const leave = await check('GET /leave', `/leave?company_id=${CO}`, null);
  if (leave?.data?.[0]) {
    expectFields('  leave request', leave.data[0], [
      'id',
      'employee_id',
      'kind',
      'is_paid',
      'starts_on',
      'ends_on',
      'days',
      'status',
    ]);
  } else {
    console.log('  -  nobody has asked for leave; the request shape was not exercised');
  }

  const advances = await check('GET /advances', `/advances?company_id=${CO}`, null);
  if (advances?.data?.[0]) {
    expectFields('  advance', advances.data[0], [
      'id',
      'advance_no',
      'employee_id',
      'amount',
      // What is still owed, which is the figure the screen shows. The amount
      // lent stops being the answer the moment one instalment is recovered.
      'outstanding',
      'installments',
      'currency',
      'issued_on',
    ]);
  } else {
    console.log('  -  nothing has been advanced; the advance shape was not exercised');
  }

  const runs = await check('GET /payroll', `/payroll?company_id=${CO}`, null);
  const run = runs?.data?.[0];
  if (run) {
    expectFields('  payroll run', run, [
      'id',
      'run_no',
      'period',
      'status',
      'currency',
      'gross_total',
      'deduction_total',
      'net_total',
      // The employer's own contribution, which is never deducted from anybody
      // and which the cost figure on the screen is built from.
      'employer_gosi',
    ]);

    const one = await check(
      'GET /payroll/{id}',
      `/payroll/${run.id}?company_id=${CO}`,
      ['id', 'run_no', 'period', 'status', 'gross_total', 'net_total'],
    );
    const slip = one?.payslips?.[0];
    if (slip) {
      expectFields('  payslip', slip, [
        'id',
        'employee_id',
        'employee',
        'basic',
        'housing',
        'transport',
        'other_allowance',
        'overtime',
        'commission',
        'bonus',
        'gross',
        // All four deductions. The accrual rule left two of them out of its
        // posting and a month with an absence could not be approved at all;
        // a payslip missing one is the same omission the other way round.
        'absence_deduction',
        'gosi_employee',
        'advance_recovery',
        'other_deduction',
        'deductions',
        'net',
        'gosi_employer',
      ]);

      // The payslip has to add up, or the screen is showing a total that
      // disagrees with the lines above it.
      const money = (v) => Math.round(Number(v) * 100);
      const earnings =
        money(slip.basic) +
        money(slip.housing) +
        money(slip.transport) +
        money(slip.other_allowance) +
        money(slip.overtime) +
        money(slip.commission) +
        money(slip.bonus);
      const taken =
        money(slip.absence_deduction) +
        money(slip.gosi_employee) +
        money(slip.advance_recovery) +
        money(slip.other_deduction);
      if (earnings !== money(slip.gross)) {
        console.log(
          `  x the payslip's earnings come to ${earnings / 100} against a gross of ${slip.gross}`,
        );
        failures += 1;
      } else if (taken !== money(slip.deductions)) {
        console.log(
          `  x the payslip's deductions come to ${taken / 100} against a stated ${slip.deductions}`,
        );
        failures += 1;
      } else if (money(slip.gross) - taken !== money(slip.net)) {
        console.log(
          `  x the payslip nets to ${(money(slip.gross) - taken) / 100} against a stated ${slip.net}`,
        );
        failures += 1;
      } else {
        console.log('  ok the payslip adds up: earnings, deductions and net agree');
      }
    } else {
      console.log('  -  the run has no payslips; the slip shape was not exercised');
    }

    // A run that says social insurance is missing must SAY WHY. A screen
    // showing a blank warning is worse than no warning.
    if (run.gosi_unavailable && !run.gosi_blocked_reason) {
      console.log('  x a run reports social insurance unavailable and gives no reason');
      failures += 1;
    } else if (run.gosi_unavailable) {
      console.log('  ok the run names what stopped social insurance being computed');
    }
  } else {
    console.log('  -  no payroll has been run; the run shape was not exercised');
  }

  const eosb = await check('GET /eosb', `/eosb?company_id=${CO}`, null);
  if (eosb?.data?.[0]) {
    expectFields('  end of service position', eosb.data[0], [
      'employee_id',
      'employee',
      'months_of_service',
      'accrued',
      'currency',
    ]);
  } else {
    console.log('  -  nobody is employed; the end-of-service shape was not exercised');
  }
}

console.log('\nROLES AND PERMISSIONS');
{
  const perms = await check('GET /permissions', `/permissions?company_id=${CO}`, null);
  const rows = perms?.data ?? [];
  if (rows.length === 0) {
    console.log('  x the role builder is offered no permissions at all');
    failures += 1;
  } else {
    expectFields('  permission', rows[0], [
      'permission',
      'section',
      'label',
      // Whether the CALLER may grant it. The builder draws a box it cannot
      // tick as disabled and explained, rather than letting somebody tick it
      // and collect the subset refusal on save.
      'holds',
    ]);

    // A permission with no catalogue row falls through to
    // {section: 'other', label: <the key>} -- the right fallback, and not a
    // place to live. Two permissions had lived there since 0005.
    const undescribed = rows.filter(
      (p) => p.label === p.permission || p.section === 'other',
    );
    if (undescribed.length > 0) {
      console.log(
        `  x ${undescribed.length} permissions are shown as their own identifier: ` +
          undescribed.map((p) => p.permission).join(', '),
      );
      failures += 1;
    } else {
      console.log(`  ok all ${rows.length} permissions say what they let somebody do`);
    }

    // A section is a heading. One with a single member under it reads as a
    // grouping somebody forgot to finish.
    const counts = new Map();
    for (const p of rows) counts.set(p.section, (counts.get(p.section) ?? 0) + 1);
    const lonely = [...counts].filter(([, n]) => n === 1).map(([s]) => s);
    if (lonely.length > 0) {
      console.log(`  x a heading is drawn for a single tick box: ${lonely.join(', ')}`);
      failures += 1;
    } else {
      console.log(`  ok the builder groups into ${counts.size} readable sections`);
    }

    const translated = rows.filter((p) => p.label_ar && p.label_bn);
    if (translated.length !== rows.length) {
      console.log(
        `  x ${rows.length - translated.length} permissions have English only`,
      );
      failures += 1;
    } else {
      console.log('  ok every permission is described in all three languages');
    }
  }

  const roles = await check('GET /people/roles', `/people/roles?company_id=${CO}`, null);
  const role = roles?.data?.[0];
  if (role) {
    expectFields('  role', role, [
      'id',
      'key',
      'name',
      'permissions',
      // False when the caller does not hold everything in it, with
      // `withheld_permissions` saying which -- shown greyed, never filtered
      // out, or an owner hunts for a role they know exists.
      'assignable',
      // Which roles the product ships and keeps current. Without it a screen
      // asks for each role individually to draw one table, or offers Edit on
      // everything and lets the server refuse.
      'is_system',
      // A role cannot be removed while anybody holds it.
      'in_use',
    ]);

    const builtin = (roles.data ?? []).filter((r) => r.is_system);
    if (builtin.length === 0) {
      console.log('  x not one seeded role is marked as the product’s own');
      failures += 1;
    } else {
      console.log(`  ok ${builtin.length} of ${roles.data.length} roles are the product’s own`);
    }

    const held = (roles.data ?? []).some((r) => r.in_use > 0);
    if (!held) {
      console.log('  x every role reports nobody holding it, and somebody signed in');
      failures += 1;
    } else {
      console.log('  ok the list says how many people hold each role');
    }

    await check(
      'GET /roles/{id}',
      `/roles/${role.id}?company_id=${CO}`,
      ['id', 'key', 'name', 'permissions', 'is_system', 'in_use'],
      (j) => j.role,
    );
  } else {
    console.log('  -  no roles at all; the role shape was not exercised');
  }

  const people = await check('GET /people', `/people?company_id=${CO}`, null);
  if (people?.data?.[0]) {
    expectFields('  person', people.data[0], [
      'id',
      'email',
      'full_name',
      'status',
      // Four states, not two: suspended is an administrator's decision,
      // locked is the sign-in system's, and a one-time password is an account
      // that works and has not been collected. Each has a different next step.
      'must_change_password',
      'locked',
      'roles',
    ]);
    const assignment = people.data.find((p) => p.roles?.length > 0)?.roles?.[0];
    if (assignment) {
      expectFields('  role assignment', assignment, [
        'id',
        'role_id',
        'role_name',
        'store_ids',
        'warehouse_ids',
      ]);
    } else {
      console.log('  -  nobody holds a role; the assignment shape was not exercised');
    }
  } else {
    console.log('  -  nobody can sign in; the person shape was not exercised');
  }
}

console.log('\nTAX, E-INVOICING AND COMPLIANCE');
{
  const wrapper = await check('GET /compliance', `/compliance?company_id=${CO}`, null);
  const report = wrapper?.report;
  if (!report) {
    console.log('  x the compliance dashboard returned no report');
    failures += 1;
  } else {
    expectFields('  compliance report', report, [
      'invoicing',
      'vat',
      'privacy',
      'storefront',
      'payroll',
      'people',
      'records',
      // A legal value nobody has verified is not the same as one that stops
      // something working, and the screen bands them differently.
      'unverified_rules',
      'blocking_rules',
    ]);
    expectFields('  vat reading', report.vat, [
      'registered',
      // Every one of these comes from the regulatory register. Nothing on the
      // screen computes a rate or infers a deadline.
      'open_ended_periods',
    ]);
    expectFields('  invoicing reading', report.invoicing, [
      'started',
      'devices',
      'devices_ready',
      'pending',
      'failed',
      // A rejection is the authority refusing a document and will not fix
      // itself; a failure will be retried. The screen ranks them differently.
      'rejected',
    ]);
    expectFields('  people reading', report.people, [
      'expiring_soon',
      'expired',
      // The residency permits, which E7 names and which the reading used to
      // miss entirely: it counted the document shelf and not the staff.
      'staff_expiring_soon',
      'staff_expired',
    ]);
    expectFields('  payroll reading', report.payroll, [
      'unsubmitted_runs',
      'deadline_known',
    ]);
    expectFields('  records reading', report.records, ['retention_years']);
    expectFields('  storefront reading', report.storefront, ['missing']);

    // The month a run belongs to, not a day inside it: the rest of the product
    // speaks of a run as "2026-08", and this scanned a DATE into a string and
    // answered 500 for every business that had ever paid anybody.
    const period = report.payroll.last_run_period;
    if (period !== undefined && !/^\d{4}-\d{2}$/.test(period)) {
      console.log(`  x the dashboard reports the payroll period as ${period}`);
      failures += 1;
    } else if (period) {
      console.log(`  ok the dashboard names the month payroll last ran (${period})`);
    }

    // The staff figure is inside the total, never beside it.
    if (report.people.staff_expiring_soon > report.people.expiring_soon) {
      console.log('  x more staff permits are expiring than documents in total');
      failures += 1;
    } else {
      console.log('  ok expiring staff permits are counted inside the total');
    }

    // A dashboard that reports a rate has to have got it from the register.
    // A business with no rate on file must say so rather than showing zero.
    if (report.vat.registered && report.vat.standard_rate === undefined) {
      console.log('  -  registered with no standard rate on file; the screen says so');
    } else if (report.vat.standard_rate) {
      console.log(`  ok the standard rate comes off the register (${report.vat.standard_rate})`);
    }
  }

  const units = await check(
    'GET /einvoicing/units',
    `/einvoicing/units?company_id=${CO}`,
    null,
  );
  const unit = units?.data?.[0];
  if (unit) {
    expectFields('  signing unit', unit, [
      'id',
      'label',
      'architecture',
      'csr',
      'csid_status',
      'terminals',
      'invoices',
      // Whether the nine certificate-request fields are filled. A unit that
      // cannot request a certificate should say so before somebody fetches a
      // one-time password for it.
      'csr_complete',
    ]);

    // The status route REQUIRES an environment. Sandbox and production are
    // different bindings, and defaulting to either would be choosing for
    // somebody -- onboarding into the wrong one produces a till that appears
    // to work and reports nothing.
    const noEnv = await call(`/einvoicing/units/${unit.id}/onboarding?company_id=${CO}`);
    if (noEnv.status === 200) {
      console.log('  x the onboarding status defaults to an environment rather than asking');
      failures += 1;
    } else {
      console.log(`  ok onboarding status insists on an environment -> ${noEnv.status}`);
    }

    for (const env of ['sandbox', 'production']) {
      const state = await check(
        `GET onboarding (${env})`,
        `/einvoicing/units/${unit.id}/onboarding?company_id=${CO}&environment=${env}`,
        ['egs_unit_id', 'environment', 'connected', 'needs_renewal', 'next_action'],
      );
      // The sentence the screen shows verbatim. An empty one would leave a
      // person looking at a panel that says nothing about what to do.
      if (state && !String(state.next_action ?? '').trim()) {
        console.log(`  x onboarding in ${env} says nothing about what to do next`);
        failures += 1;
      }
    }
  } else {
    console.log('  -  no signing units; the e-invoicing shapes were not exercised');
  }

  const rates = await check(
    'GET /exchange-rates',
    `/exchange-rates?company_id=${CO}`,
    null,
  );
  if (rates?.data?.[0]) {
    expectFields('  exchange rate', rates.data[0], [
      'id',
      'from_currency',
      'to_currency',
      'rate',
      'as_of',
    ]);
  } else {
    console.log('  -  no exchange rates on file; the rate shape was not exercised');
  }
}

console.log('\nANALYTICS, SAVED REPORTS AND EXPORTS');
{
  const year = new Date().getFullYear();
  const from = `${year}-01-01`;
  const to = new Date().toISOString().slice(0, 10);
  const period = `&from=${from}&to=${to}`;

  const kpis = await check(
    'GET /analytics/kpis',
    `/analytics/kpis?company_id=${CO}${period}`,
    [
      'from',
      'to',
      'currency',
      'revenue',
      'gross_profit',
      'gross_margin_pct',
      'orders',
      'average_order_value',
      'units_per_transaction',
      'discount_ratio_pct',
      'return_rate_pct',
      // These four come back EMPTY on a young business rather than zero, and
      // the screen renders a dash with a reason. Present-but-blank is the
      // contract; missing entirely would be a different bug.
      'inventory_turnover',
      'repeat_customer_pct',
      'customer_lifetime_value',
      'sales_per_store',
      'sales_per_employee',
    ],
  );
  if (kpis) {
    const blank = [
      'inventory_turnover',
      'repeat_customer_pct',
      'customer_lifetime_value',
    ].filter((k) => String(kpis[k] ?? '').trim() === '');
    if (blank.length > 0) {
      console.log(
        `  ok ${blank.length} figures this shop cannot answer yet come back blank, not zero`,
      );
    } else {
      console.log('  -  every figure is answerable here; the blank case was not exercised');
    }
  }

  const movers = await check(
    'GET /analytics/movers',
    `/analytics/movers?company_id=${CO}${period}`,
    null,
  );
  if (movers?.data?.[0]) {
    expectFields('  mover', movers.data[0], [
      'variant_id',
      'sku',
      'product',
      'sold_qty',
      'revenue',
      'profit',
      'on_hand',
      'velocity',
      // -1 when it has never sold. Zero would mean "sold today", and a screen
      // that read one as the other would call today's best seller dead stock.
      'days_since_sold',
    ]);
    const never = movers.data.filter((m) => m.days_since_sold === -1);
    if (never.length > 0) {
      console.log(`  ok ${never.length} lines report never having sold as -1`);
      const withCover = never.filter((m) => m.days_cover !== undefined);
      if (withCover.length > 0) {
        console.log('  x a line that never sold reports days of cover, which is arithmetic on nothing');
        failures += 1;
      } else {
        console.log('  ok a line that never sold reports no cover at all');
      }
    }
  } else {
    console.log('  -  nothing to measure; the mover shape was not exercised');
  }

  const forecast = await check(
    'GET /analytics/forecast',
    `/analytics/forecast?company_id=${CO}${period}`,
    null,
  );
  if (forecast?.data?.[0]) {
    expectFields('  forecast line', forecast.data[0], [
      'variant_id',
      'sku',
      'expected_demand',
      'on_hand',
      'shortfall',
      // Said out loud, because an owner ordering against a number has to know
      // it is arithmetic on the past rather than a prediction.
      'basis',
    ]);
    if (!String(forecast.data[0].basis ?? '').trim()) {
      console.log('  x the forecast does not say what it is based on');
      failures += 1;
    }
  } else {
    console.log('  -  nothing has sold; the forecast shape was not exercised');
  }

  const profit = await check(
    'GET /analytics/profitability',
    `/analytics/profitability?company_id=${CO}${period}`,
    null,
  );
  if (profit?.data?.[0]) {
    expectFields('  profitability line', profit.data[0], [
      'label',
      'revenue',
      'cost',
      'profit',
      'margin_pct',
      'units',
    ]);
  }

  const workforce = await check(
    'GET /reports/workforce',
    `/reports/workforce?company_id=${CO}`,
    ['total', 'saudi', 'non_saudi', 'saudi_share', 'expiring_soon', 'expired'],
    (j) => j.workforce,
  );
  void workforce;

  const saved = await check(
    'GET /reports/saved',
    `/reports/saved?company_id=${CO}`,
    null,
  );
  if (saved?.data?.[0]) {
    expectFields('  saved report', saved.data[0], [
      'id',
      'name',
      'kind',
      // A relative phrase, never two dates -- and what it resolves to TODAY,
      // so the screen can show "1 - 30 September" beside "last month".
      'period',
      'from',
      'to',
      'is_active',
    ]);
  } else {
    console.log('  -  no saved reports; the saved shape was not exercised');
  }

  // Every statement on a screen has to be takeable away. Two were not: the
  // cash flow and the tax return, and the return is the one a business files
  // from.
  for (const kind of [
    'sales',
    'expenses',
    'stock',
    'trial-balance',
    'profit-and-loss',
    'balance-sheet',
    'cash-flow',
    'vat-return',
  ]) {
    const res = await fetch(
      `${API}/reports/${kind}/export?company_id=${CO}${period}`,
      { headers: { Authorization: `Bearer ${token}` } },
    );
    if (res.status !== 200) {
      console.log(`  x exporting ${kind} answered ${res.status}`);
      failures += 1;
      continue;
    }
    const disposition = res.headers.get('content-disposition') ?? '';
    if (!disposition.includes('.csv')) {
      console.log(`  x exporting ${kind} does not offer a file: ${disposition}`);
      failures += 1;
      continue;
    }
    const body = await res.text();
    if (kind === 'vat-return') {
      // The caveats go ABOVE the totals. A spreadsheet is scrolled, printed
      // and forwarded, and a caveat under the figures is one somebody files
      // without reading.
      const caveats = body.indexOf('Not included, and why');
      const totals = body.indexOf('Output tax');
      if (caveats !== -1 && totals !== -1 && caveats > totals) {
        console.log('  x the tax return export buries its caveats under the totals');
        failures += 1;
        continue;
      }
      if (!body.includes('Filed')) {
        console.log('  x the tax return export does not say it has not been filed');
        failures += 1;
        continue;
      }
    }
    console.log(`  ok exporting ${kind} gives a file`);
  }
}

console.log('\nTILLS, DISCOUNTS, POINTS AND CREDIT');
{
  const shifts = await check('GET /shifts', `/shifts?company_id=${CO}`, null);
  const shift = shifts?.data?.[0];
  if (shift) {
    expectFields('  shift', shift, [
      'id',
      'session_no',
      'state',
      // Who and where, not just an id. A supervisor asks "who was on number
      // two last night", never "what is my session id".
      'store',
      'device',
      'opened_by',
      'opened_at',
      'opening_float',
      'blind_close',
    ]);

    // On an OPEN session the counted, expected and variance figures must be
    // absent rather than zero: a drawer nobody has counted is not a drawer
    // counted at nothing, and a zero would send a supervisor to every till.
    const open = (shifts.data ?? []).filter((s) => s.state === 'open');
    const zeroed = open.filter(
      (s) =>
        'counted_cash' in s || 'expected_cash' in s || 'variance' in s,
    );
    if (open.length === 0) {
      console.log('  -  no open session; the uncounted-drawer case was not exercised');
    } else if (zeroed.length > 0) {
      console.log('  x an open session reports a counted or expected figure');
      failures += 1;
    } else {
      console.log(`  ok ${open.length} open sessions report nothing counted yet`);
    }
  } else {
    console.log('  -  no shifts in the window; the shift shape was not exercised');
  }

  const promos = await check('GET /promotions', `/promotions?company_id=${CO}`, null);
  if (promos?.data?.[0]) {
    expectFields('  promotion', promos.data[0], [
      'id',
      'code',
      'name',
      'kind',
      // The scope in WORDS. A list that made a reader join a category, a brand
      // and a variant in their head to learn what a discount touches is a list
      // nobody checks.
      'applies_to',
      'is_active',
      // What it has cost, on the row: "is this campaign working" is asked
      // while looking at the list.
      'times_used',
      'discount_given',
      'sales_generated',
    ]);
  } else {
    console.log('  -  no promotions; the promotion shape was not exercised');
  }

  const program = await check(
    'GET /loyalty/program',
    `/loyalty/program?company_id=${CO}`,
    [
      'is_active',
      'spend_per_point',
      'point_value',
      'currency',
      // Whether a scheme was set up AT ALL. A company with none is not a
      // company whose scheme earns nothing, and a form of defaults would read
      // as a scheme somebody configured.
      'exists',
      // What the shop owes in points, as money. The figure an owner asks about.
      'owed',
      'points_outstanding',
    ],
  );
  if (program && !program.exists) {
    const zeroed = ['spend_per_point', 'point_value'].filter(
      (k) => String(program[k] ?? '').trim() !== '',
    );
    if (zeroed.length > 0) {
      console.log(
        `  x no scheme exists and yet ${zeroed.join(', ')} carry a rate`,
      );
      failures += 1;
    } else {
      console.log('  ok a company with no scheme reports no rates, not zeros');
    }
  }

  const members = await check(
    'GET /loyalty/members',
    `/loyalty/members?company_id=${CO}`,
    null,
  );
  if (members?.data?.[0]) {
    expectFields('  loyalty member', members.data[0], [
      'customer_id',
      'customer',
      'points',
      'worth',
      'lifetime_spend',
      'visits',
      'segment',
    ]);
  }

  const wallets = await check('GET /wallets', `/wallets?company_id=${CO}`, null);
  if (wallets?.data) {
    if (wallets.data[0]) {
      expectFields('  wallet', wallets.data[0], [
        'customer_id',
        'customer',
        'balance',
        'currency',
      ]);
    }
    // Nobody with an empty wallet. A page of names with zero beside most of
    // them is not a list of what is owed.
    const empty = wallets.data.filter((w) => Number(w.balance) === 0);
    if (empty.length > 0) {
      console.log(`  x ${empty.length} wallets with nothing on them are listed`);
      failures += 1;
    } else {
      console.log('  ok only wallets holding something are listed');
    }
  }

  await check('GET /gift-cards', `/gift-cards?company_id=${CO}`, null);
}

console.log('\nTHE BRANCHES A SCREEN HAS TO NAME');
{
  // Three screens were built with a branch dropdown pointing at /stores, which
  // did not exist. Every branch list in the product belonged to another module:
  // /devices/stores is behind devices.view, /stock/locations carries them as a
  // side payload. This is the general one.
  const stores = await check('GET /stores', `/stores?company_id=${CO}`, null);
  if (stores?.data?.[0]) {
    expectFields('  branch', stores.data[0], [
      'id',
      'code',
      'name',
      // Said rather than filtered on: a record made in a branch that has since
      // closed still names it, and dropping closed ones would leave that
      // record with no branch at all.
      'is_active',
    ]);
    const order = stores.data.map((s) => s.is_active);
    if (order.join(',') !== [...order].sort((a, b) => (b ? 1 : 0) - (a ? 1 : 0)).join(',')) {
      console.log('  x closed branches are not sorted below open ones');
      failures += 1;
    } else {
      console.log('  ok open branches come before closed ones');
    }
  } else {
    console.log('  x a company with a shop reports no branches');
    failures += 1;
  }
}

console.log('\nBUSINESS SETTINGS AND THE ACCOUNTING CALENDAR');
{
  const templates = await check(
    'GET /companies/{id}/templates',
    `/companies/${CO}/templates?company_id=${CO}`,
    null,
  );
  if (templates?.data?.[0]) {
    expectFields('  document template', templates.data[0], [
      'doc_type',
      'header_text',
      'header_text_ar',
      'footer_text',
      'return_policy',
      'payment_terms',
      'show_logo',
      'show_tax_number',
      // Whether anybody has set this one up, as opposed to it being defaults.
      // A screen that could not tell would show four configured documents to a
      // business that has configured none.
      'configured',
    ]);
    const kinds = templates.data.map((t) => t.doc_type).sort();
    console.log(`  ok stationery for ${kinds.length} document types: ${kinds.join(', ')}`);
  } else {
    console.log('  x no document templates at all');
    failures += 1;
  }

  await check('GET /companies/{id}/logo', `/companies/${CO}/logo?company_id=${CO}`, null);

  const privacyWrapper = await check(
    'GET /privacy/disclosure',
    `/privacy/disclosure?company_id=${CO}`,
    // `missing` is the list the compliance dashboard reads too. Two lists would
    // let the settings screen and the dashboard disagree about whether a
    // business may trade online.
    ['missing'],
    (j) => j.disclosure,
  );
  // `check` hands back the whole response; the `pick` above only steers which
  // object its field assertion looks at. Reading `.missing` off the wrapper
  // silently found undefined and reported nothing missing on a storefront with
  // six disclosures absent.
  const privacy = privacyWrapper?.disclosure;
  if (privacy) {
    const owned = ['cr_number', 'vat_number'];
    const here = (privacy.missing ?? []).filter((f) => !owned.includes(f));
    const elsewhere = (privacy.missing ?? []).filter((f) => owned.includes(f));
    console.log(
      `  ok ${here.length} disclosures are fixable on the settings screen, ` +
        `${elsewhere.length} come from the business record`,
    );
  }

  const calendar = await check(
    'GET /accounting/periods',
    `/accounting/periods?company_id=${CO}`,
    ['years'],
  );
  const year = calendar?.years?.[0];
  if (year?.periods?.[0]) {
    expectFields('  period', year.periods[0], [
      'id',
      'fiscal_year',
      'period_no',
      'starts_on',
      'ends_on',
      'state',
      // Closing an empty month is hygiene; closing one with four hundred
      // entries is a decision, and the screen must not make them look alike.
      'entries',
    ]);

    // Periods must not overlap, or a posting date would fall in two of them
    // and the one it landed in would depend on query order.
    const all = calendar.years.flatMap((y) => y.periods)
      .sort((a, b) => (a.starts_on < b.starts_on ? -1 : 1));
    let overlapping = 0;
    for (let i = 1; i < all.length; i += 1) {
      if (all[i].starts_on <= all[i - 1].ends_on) overlapping += 1;
    }
    if (overlapping > 0) {
      console.log(`  x ${overlapping} periods overlap the one before them`);
      failures += 1;
    } else {
      console.log(`  ok ${all.length} periods, none overlapping`);
    }
  } else {
    console.log('  -  no fiscal calendar; the period shape was not exercised');
  }
}

console.log('\nTHE BUSINESS RECORD AND ITS BRANCHES');
{
  // Probed rather than asserted, because this route may not be in the build
  // being checked. A 404 here is "this server does not have it yet", which is
  // a different thing from "it answered wrongly" -- and a verification script
  // that failed on the first is one people stop trusting on the second.
  const probe = await call(`/companies/${CO}?company_id=${CO}`);
  const record = probe.status === 200 ? probe.json : null;
  if (probe.status === 404) {
    console.log('  -  GET /companies/{id} is not in this build; skipped');
  } else if (probe.status !== 200) {
    console.log(`  x GET /companies/{id}: HTTP ${probe.status}`);
    failures += 1;
  } else {
    console.log('  ok GET /companies/{id}');
  }
  const business = record?.business;
  if (record && !business) {
    console.log('  x the business record came back without a business on it');
    failures += 1;
  } else if (business) {
    expectFields('  business', business, [
      'id',
      'legal_name',
      'trade_name',
      'country',
      'market_name',
      'base_currency',
      'cr_number',
      'vat_number',
      'costing_method',
      'fiscal_year_start_month',
      // The map from field name to the sentence saying why it can no longer
      // change. Absence from it means editable, so a screen without this has
      // to guess -- and a guess here is a 409 it cannot explain.
      'settled',
    ]);

    const settled = Object.keys(business.settled ?? {});
    if (settled.length === 0) {
      console.log('  -  nothing has settled on this company; the fixed-field case was not exercised');
    } else {
      // Every settled field must carry a REASON, not merely be listed. The
      // screen shows the sentence instead of a greyed box, and an empty one
      // would leave a field fixed with no explanation at all.
      const silent = settled.filter((f) => !String(business.settled[f] ?? '').trim());
      if (silent.length > 0) {
        console.log(`  x ${silent.join(', ')} are settled and say no reason why`);
        failures += 1;
      } else {
        console.log(`  ok ${settled.length} settled fields each carry their reason`);
      }

      // And naming one must be REFUSED rather than quietly ignored: a screen
      // that sent it and got a 200 would report success on a change that never
      // happened.
      const probe = await write('PUT', `/companies/${CO}?company_id=${CO}`, {
        [settled[0]]: 'verify:api probe',
      });
      if (probe.status !== 409) {
        console.log(
          `  x amending the settled field ${settled[0]} answered ${probe.status}, want 409`,
        );
        failures += 1;
      } else if (!probe.json?.error?.fields?.[settled[0]]) {
        console.log(`  x the refusal does not say why ${settled[0]} is settled`);
        failures += 1;
      } else {
        console.log(`  ok amending a settled field is refused, with the reason`);
      }
    }
  }

  const branches = record?.branches ?? [];
  if (record && branches[0]) {
    expectFields('  branch', branches[0], [
      'id',
      'code',
      'name',
      'is_active',
      'street',
      'district',
      'city',
      'postal_code',
      // As stored, and what actually gets printed. They differ: the document
      // layer falls back to the company's country when a branch has none.
      'country_code',
      'effective_country_code',
      // The server's answer, never derived on the client.
      'can_invoice',
      'incomplete',
    ]);

    // The trap worth asserting. A branch with NO country code of its own can
    // still invoice, because the company's is used. Deriving `can_invoice`
    // from `country_code` would report every such branch unable to invoice.
    const inherited = branches.filter(
      (b) => !String(b.country_code ?? '').trim() && b.effective_country_code,
    );
    if (inherited.length === 0) {
      console.log('  -  every branch stores its own country; the fallback was not exercised');
    } else {
      const wronglyBlocked = inherited.filter((b) => !b.can_invoice && b.incomplete.length === 0);
      if (wronglyBlocked.length > 0) {
        console.log('  x a branch inheriting its country cannot invoice and says no reason');
        failures += 1;
      } else {
        console.log(
          `  ok ${inherited.length} branches inherit the company's country and still invoice`,
        );
      }
    }

    // A branch that cannot invoice has to say which parts are missing, or the
    // screen shows a refusal nobody can act on.
    const blocked = branches.filter((b) => !b.can_invoice);
    const mute = blocked.filter((b) => (b.incomplete ?? []).length === 0);
    if (mute.length > 0) {
      console.log(`  x ${mute.length} branches cannot invoice and name nothing missing`);
      failures += 1;
    } else if (blocked.length > 0) {
      console.log(`  ok ${blocked.length} branches that cannot invoice each say why`);
    }
  } else if (record) {
    console.log('  -  no branches; the branch shape was not exercised');
  }
}

// --- item 22: the screens that had no frontend at all ----------------------
//
// Most of these lists are empty on a development seed, and an empty list is a
// real answer worth asserting: it proves the route exists, is reachable by
// somebody holding the permission the screen asks for, and answers the shape
// the screen destructures. A 403 here would mean the screen renders a refusal
// for the person it was built for, which is what these caught in the devices
// block. Where a row does exist, its fields are checked.
console.log('\nAPPROVALS, DELIVERIES, MONEY AND AFTER-SALES');
{
  const shapes = [
    [
      'GET /approvals',
      `/approvals?company_id=${CO}`,
      'approval',
      ['id', 'subject', 'summary', 'status', 'current_step', 'steps_total', 'requested_at'],
    ],
    ['GET /approvals/mine', `/approvals/mine?company_id=${CO}`, null, null],
    [
      'GET /deliveries',
      `/deliveries?company_id=${CO}`,
      'delivery',
      ['id', 'delivery_no', 'status', 'address', 'fee', 'is_cod', 'cod_amount'],
    ],
    [
      'GET /assets',
      `/assets?company_id=${CO}`,
      'asset',
      ['id', 'asset_no', 'name', 'cost', 'book_value', 'monthly_charge', 'months_due', 'currency'],
    ],
    [
      'GET /investors',
      `/investors?company_id=${CO}`,
      'investor',
      ['id', 'name', 'contributed', 'withdrawn', 'net', 'currency', 'is_active'],
    ],
    [
      'GET /installments',
      `/installments?company_id=${CO}`,
      'instalment plan',
      ['id', 'plan_no', 'status', 'financed', 'outstanding', 'tenure_months', 'currency'],
    ],
    [
      'GET /service-jobs',
      `/service-jobs?company_id=${CO}`,
      'repair job',
      ['id', 'job_no', 'kind', 'status', 'fault_reported', 'parts_cost', 'labour_cost', 'charged'],
    ],
    [
      'GET /portal/return-requests',
      `/portal/return-requests?company_id=${CO}`,
      'return request',
      ['id', 'request_no', 'kind', 'reason', 'items', 'status', 'created_at'],
    ],
    [
      'GET /portal/contacts',
      `/portal/contacts?company_id=${CO}`,
      'supplier contact',
      ['id', 'supplier_id', 'supplier_name', 'full_name', 'email', 'is_active', 'invited_at'],
    ],
  ];

  for (const [label, path, noun, fields] of shapes) {
    const body = await check(label, path, null);
    if (!body) continue;
    if (!noun) continue;
    if (body.data?.[0]) expectFields(`  ${noun}`, body.data[0], fields);
    else console.log(`  -  none on file; the ${noun} shape was not exercised`);
  }

  // The money account a capital movement and an asset disposal both resolve.
  // `id` and `account_id` are BOTH uuids on this row and they are not
  // interchangeable: the routes want the money account, and sending the chart
  // account 404s with a message that names neither.
  const treasury = await check(
    'GET /treasury/accounts',
    `/treasury/accounts?company_id=${CO}`,
    null,
  );
  if (treasury?.data?.[0]) {
    expectFields('  money account', treasury.data[0], [
      'id',
      'account_id',
      'name',
      'kind',
      'currency',
      'is_active',
    ]);
  } else {
    console.log('  -  no cash or bank accounts; the money account shape was not exercised');
  }
}

// --- the person, rather than the business -------------------------------
//
// Four features whose backend was complete and whose frontend was absent: no
// forgot-password page behind the link sign-in already carried, no way to enrol
// a second factor although signing in with one worked, a notification centre
// nothing called, and a search route with no box. These assert the shapes the
// new screens read.
// --- H6: what lets another system act on this business --------------------
console.log('\nKEYS AND CALLBACKS');
{
  const keys = await check('GET /api-keys', `/api-keys?company_id=${CO}`, null);
  if (keys?.data?.[0]) {
    expectFields('  api key', keys.data[0], [
      'id',
      'name',
      'key_prefix',
      'permissions',
      'created_at',
    ]);
    // The list must never carry the key itself. A screen cannot leak what it
    // was not sent, and this is the assertion that keeps it that way if the
    // payload ever grows a field.
    const leaked = keys.data.filter((k) => 'secret' in k || 'key' in k);
    if (leaked.length > 0) {
      console.log('  x /api-keys returned a secret; the list must carry a prefix only');
      failures += 1;
    } else {
      console.log('  ok the list carries a prefix and never the key');
    }
  } else {
    console.log('  -  no api keys; the key shape was not exercised');
  }

  // The event vocabulary travels WITH the list, which is what stops the form
  // offering an event the server would refuse. If it stopped arriving, the
  // webhook form would render an empty set of checkboxes and look broken.
  const hooks = await check('GET /webhooks', `/webhooks?company_id=${CO}`, null);
  if (!Array.isArray(hooks?.events)) {
    console.log('  x /webhooks carries no `events` vocabulary; the form has nothing to offer');
    failures += 1;
  } else {
    console.log(`  ok ${hooks.events.length} events travel with the list`);
  }
  if (hooks?.data?.[0]) {
    expectFields('  endpoint', hooks.data[0], [
      'id',
      'name',
      'url',
      'is_active',
      'events',
      'queued',
      'failed',
    ]);
    const deliveries = (
      await call(`/webhooks/${hooks.data[0].id}/deliveries?company_id=${CO}`)
    ).json;
    if (deliveries?.data?.[0]) {
      expectFields('  delivery', deliveries.data[0], [
        'id',
        'event',
        'status',
        'attempts',
        'created_at',
      ]);
    } else {
      console.log('  -  nothing delivered yet; the delivery shape was not exercised');
    }
  } else {
    console.log('  -  no callbacks registered; the endpoint shape was not exercised');
  }
}

console.log('\nYOUR OWN ACCOUNT');
{
  const mfa = await check('GET /auth/mfa', '/auth/mfa', null);
  expectFields('  second factor', mfa?.mfa, ['enabled', 'recovery_remaining']);

  const sessions = await check('GET /auth/sessions', '/auth/sessions', null);
  if (sessions?.data?.[0]) {
    expectFields('  session', sessions.data[0], [
      'id',
      'created_at',
      'expires_at',
      'current',
    ]);
    // Exactly one session is the caller's own. The screen refuses to revoke
    // that one, so more than one — or none — would make the guard meaningless.
    const current = sessions.data.filter((s) => s.current).length;
    if (current !== 1) {
      console.log(`  x ${current} sessions claim to be the current one, want exactly 1`);
      failures += 1;
    } else {
      console.log('  ok exactly one session is marked as this one');
    }
  }

  // Company-scoped, although the route is merely authenticated: notifyScope
  // resolves a company from the request, so a notification belongs to a person
  // IN a business. Calling it without one answers 400, which is how the
  // notification screen was found to be sending no scope at all.
  const notes = await check('GET /notifications', `/notifications?company_id=${CO}`, null);
  if (notes?.data?.[0]) {
    expectFields('  notification', notes.data[0], [
      'id',
      'kind',
      'severity',
      'title',
      'is_read',
      'created_at',
    ]);
  } else {
    console.log('  -  nothing has been notified; the notice shape was not exercised');
  }
  await check(
    'GET /notifications/unread',
    `/notifications/unread?company_id=${CO}`,
    null,
  );

  const prefs = await check(
    'GET /notifications/preferences',
    `/notifications/preferences?company_id=${CO}`,
    null,
  );
  if (prefs?.data?.[0]) {
    expectFields('  preference', prefs.data[0], ['kind', 'in_app', 'email', 'sms', 'push']);
  } else {
    console.log('  -  no preferences on file; the preference shape was not exercised');
  }

  // Search is a lens over what the caller may already reach, so the shape
  // matters more than the hits: a screen that grouped by a `kind` the route
  // stopped sending would render every result under nothing.
  // The term is taken from a customer that exists rather than guessed, so the
  // hit shape is exercised on every seed instead of only on one whose data
  // happens to match a hard-coded string.
  const someone = (await call(`/customers?company_id=${CO}&limit=1`)).json?.data?.[0];
  const term = (someone?.display_name ?? someone?.name ?? 'sh').slice(0, 3);
  const hits = (await call(`/search?company_id=${CO}&q=${encodeURIComponent(term)}`)).json;
  if (hits?.data?.[0]) {
    expectFields('  search hit', hits.data[0], ['kind', 'id', 'label']);
    const KINDS = new Set([
      'product', 'customer', 'supplier', 'invoice', 'order', 'employee', 'serial',
    ]);
    const strange = [...new Set(hits.data.map((h) => h.kind))].filter(
      (k) => !KINDS.has(k),
    );
    if (strange.length > 0) {
      console.log(`  x search returned kinds the screen cannot group: ${strange.join(', ')}`);
      failures += 1;
    } else {
      console.log('  ok every hit is a kind the screen groups');
    }
  } else {
    console.log('  -  nothing matched; the search hit shape was not exercised');
  }
}

console.log("\nWHAT A BUSINESS PAYS, AND WHAT IT MAY ASK FOR");
{
  const sub = await check('GET /subscription', '/subscription', null);
  expectFields('  subscription', sub?.subscription, [
    'tier',
    'cycle',
    'price',
    'currency',
    'outstanding',
    'limits',
  ]);
  // The tenant's own view carries the same ceilings AND counts the platform
  // screen shows, because the business asks the same question of it.
  expectFields('  allowances', sub?.subscription?.limits, [
    'max_companies',
    'max_stores',
    'max_users',
    'max_terminals',
    'companies',
    'stores',
    'users',
    'terminals',
  ]);

  // Every feature the entitlements route can name needs a translated label, or
  // the plan screen renders a blank chip. This asserts the set the screen keys
  // against still covers what the server sends.
  const ent = await check(
    'GET /subscription/entitlements',
    '/subscription/entitlements',
    null,
  );
  if (ent?.data?.[0]) {
    expectFields('  entitlement', ent.data[0], ['feature', 'allowed', 'in_plan']);
    const KEYED = new Set([
      'analytics', 'api_access', 'approvals', 'assets', 'consolidation',
      'einvoicing', 'installments', 'label_studio', 'loyalty', 'multi_company',
      'online_orders', 'payroll', 'promotions', 'warranty', 'webhooks',
      'wholesale',
    ]);
    const unkeyed = ent.data.map((e) => e.feature).filter((f) => !KEYED.has(f));
    if (unkeyed.length > 0) {
      console.log(
        `  x the plan screen has no label for: ${unkeyed.join(', ')}`,
      );
      failures += 1;
    } else {
      console.log(`  ok all ${ent.data.length} features have a translated label`);
    }
  }

  const tickets = await check('GET /support/tickets', '/support/tickets', null);
  if (tickets?.data?.[0]) {
    expectFields('  ticket', tickets.data[0], [
      'id',
      'ticket_no',
      'subject',
      'body',
      'kind',
      'priority',
      'status',
      'created_at',
    ]);
    // The thread lives on the detail route only. The support screen opens a
    // ticket to read it, and renders the opening body plus `messages` — so a
    // detail response without `messages` would silently lose the conversation.
    const one = (await call(`/support/tickets/${tickets.data[0].id}`)).json;
    if (one && !('messages' in one)) {
      console.log('  -  this ticket has no replies; the thread was not exercised');
    } else if (one?.messages?.[0]) {
      expectFields('  ticket message', one.messages[0], [
        'id',
        'body',
        'from_platform',
        'created_at',
      ]);
    }
  } else {
    console.log('  -  no tickets raised; the ticket shape was not exercised');
  }
}

console.log('\nTILLS, BARCODES AND SERIAL NUMBERS');
{
  const tills = await check('GET /devices', `/devices?company_id=${CO}`, null);
  if (tills?.data?.[0]) {
    expectFields('  till', tills.data[0], [
      'id',
      'store_id',
      'store',
      'terminal_label',
      'status',
      // Which decides what `pending` MEANS. On a paired till it is the normal
      // state between registering the counter and the machine enrolling; on a
      // session till it should never last. One word for both would send a
      // manager to fix a till that is working correctly.
      'binding',
      // Whether a code is outstanding, so the screen offers "new code" rather
      // than "get a code" and nobody issues a second one by accident.
      'pending_code',
    ]);

    const pending = tills.data.filter((d) => d.status === 'pending');
    const paired = pending.filter((d) => d.binding === 'paired');
    const session = pending.filter((d) => d.binding === 'session');
    console.log(
      `  ok ${tills.data.length} tills; ${paired.length} awaiting a machine, ` +
        `${session.length} pending on a session binding`,
    );
  } else {
    console.log('  -  no tills; the terminal shape was not exercised');
  }

  await check('GET /devices/stores', `/devices/stores?company_id=${CO}`, null);

  const first = tills?.data?.[0];
  if (first) {
    await check(
      'GET /devices/{id}/settings',
      `/devices/${first.id}/settings?company_id=${CO}`,
      // Every field is optional on the way in, but the READ must state the
      // device it belongs to, or a screen editing two tills could write one's
      // settings onto the other.
      ['device_id'],
    );
  }

  const scheme = await check('GET /labels/scheme', `/labels/scheme?company_id=${CO}`, [
    'parts',
    'separator',
    'symbology',
    'part_length',
    // The server's own example of what the NEXT code looks like. A screen
    // building one would be a second implementation of the rule that mints
    // them, free to disagree with it.
    'example',
  ]);
  if (scheme && !String(scheme.example ?? '').trim()) {
    console.log('  x the barcode scheme shows no example of what it produces');
    failures += 1;
  } else if (scheme) {
    console.log(`  ok the scheme states its own next code (${scheme.example})`);
  }

  const templates = await check(
    'GET /labels/templates',
    `/labels/templates?company_id=${CO}`,
    null,
  );
  if (templates?.data?.[0]) {
    expectFields('  label layout', templates.data[0], [
      'id',
      'name',
      'kind',
      'width_mm',
      'height_mm',
      'fields',
      'is_default',
    ]);
    // A roll has no grid, so it must not claim a per-sheet count: "1 per
    // sheet" invites somebody to work out how many sheets a roll needs.
    const rolls = templates.data.filter((t) => t.kind === 'thermal');
    const claiming = rolls.filter((t) => t.per_sheet > 0);
    if (claiming.length > 0) {
      console.log(`  x ${claiming.length} thermal rolls report a per-sheet count`);
      failures += 1;
    } else if (rolls.length > 0) {
      console.log(`  ok ${rolls.length} rolls report no per-sheet count`);
    }
  } else {
    console.log('  -  no label layouts; the layout shape was not exercised');
  }

  const serials = await check('GET /serials', `/serials?company_id=${CO}`, null);
  if (serials?.data?.[0]) {
    expectFields('  serial', serials.data[0], [
      'id',
      'serial_no',
      'variant_id',
      'status',
      // Derived from the date on the server, never stored, because a flag
      // would be wrong every morning until a job ran -- and the warranty desk
      // is exactly where a stale answer costs the shop money.
      'under_warranty',
    ]);

    // A unit that has not been sold cannot be under warranty: the clock starts
    // at the sale. If one claims to be, the derivation is reading the wrong
    // date and a counter would honour a warranty that never began.
    const unsoldButCovered = serials.data.filter(
      (s) => !s.sold_at && s.under_warranty,
    );
    if (unsoldButCovered.length > 0) {
      console.log(
        `  x ${unsoldButCovered.length} unsold units report being under warranty`,
      );
      failures += 1;
    } else {
      console.log('  ok no unsold unit claims a warranty');
    }
  } else {
    console.log('  -  nothing is tracked by serial; the serial shape was not exercised');
  }

  // A serial nobody has on file is an ordinary answer at a counter, not a
  // fault, and the screen says so rather than showing an error.
  const unknown = await call(`/serials/NOT-A-REAL-SERIAL?company_id=${CO}`);
  if (unknown.status !== 404) {
    console.log(`  x an unknown serial answered ${unknown.status}, want 404`);
    failures += 1;
  } else {
    console.log('  ok an unknown serial is a plain not-found');
  }
}

console.log('\nTHE PRIVACY REGISTERS, AND THE TWO CLOCKS ON THEM');
{
  const requests = await check('GET /privacy/requests', `/privacy/requests?company_id=${CO}`, null);
  if (requests?.data?.[0]) {
    expectFields('  subject request', requests.data[0], [
      'id',
      'request_no',
      'kind',
      'status',
      'subject_name',
      'received_at',
      'due_at',
      // The statutory clock, counted by the server. The screen prints this
      // rather than subtracting due_at from today, because a deadline this
      // product worked out itself would be a second answer to a regulatory
      // question -- and the register was filed against the server's.
      'days_left',
      // A hold stops an erasure. Without this the queue cannot say why a
      // request it keeps showing has not moved.
      'legal_hold_applied',
    ]);

    // Sent always, not omitempty: a request due TODAY has days_left 0, and a
    // field that disappeared at zero would drop the most urgent row on the
    // screen. This is the assertion that pins that.
    const live = requests.data.filter(
      (r) => r.status !== 'fulfilled' && r.status !== 'refused',
    );
    const unclocked = live.filter((r) => typeof r.days_left !== 'number');
    if (unclocked.length > 0) {
      console.log(`  x ${unclocked.length} open requests carry no days_left`);
      failures += 1;
    } else {
      console.log(`  ok ${live.length} open requests each state their own deadline`);
    }

    // An extended request is counted against the new date. Showing the
    // original would have somebody reporting a breach of a deadline that
    // legitimately moved.
    const extended = requests.data.filter((r) => r.extended_to);
    if (extended.length > 0) {
      console.log(`  ok ${extended.length} extended, and each names the date it moved to`);
    }
  } else {
    console.log('  -  nobody has asked; the request shape was not exercised');
  }

  const incidents = await check(
    'GET /privacy/incidents',
    `/privacy/incidents?company_id=${CO}`,
    null,
  );
  if (incidents?.data?.[0]) {
    expectFields('  breach', incidents.data[0], [
      'id',
      'incident_no',
      'title',
      'severity',
      'status',
      'discovered_at',
      'notify_due_at',
      // Hours, not days. The notification window is three days, so a count
      // rounded to days would report "1 day left" for anything between one
      // hour and thirty-six.
      'hours_left',
    ]);

    // Absence is not zero. An incident still under investigation may not know
    // how many people it touched, and a screen printing 0 would say it
    // touched nobody -- which is a different report to make to an authority.
    const counted = incidents.data.filter((i) => 'subjects_affected' in i);
    const zeroed = counted.filter((i) => i.subjects_affected === 0);
    if (zeroed.length > 0) {
      console.log(
        `  -  ${zeroed.length} breaches report exactly 0 people affected; ` +
          'the screen must not read that as "not yet counted"',
      );
    }
    console.log(
      `  ok ${incidents.data.length} breaches; ${counted.length} state a count`,
    );
  } else {
    console.log('  -  no breaches recorded; the breach shape was not exercised');
  }

  const consents = await check(
    'GET /privacy/consents',
    `/privacy/consents?company_id=${CO}`,
    null,
  );
  if (consents?.data?.[0]) {
    expectFields('  consent', consents.data[0], [
      'id',
      'subject_type',
      'subject_id',
      'lawful_basis',
      'purpose',
      'channel',
      'granted',
      'granted_at',
      // Why it can be shown to somebody who disputes it. A consent register
      // that cannot say how consent was obtained proves nothing.
      'proof',
    ]);

    // The contract the screen turns on. Withdrawing sets granted false AND
    // stamps withdrawn_at; the schema requires the two to agree. A row where
    // they disagreed would be a consent that is live and withdrawn at once,
    // and whichever column a caller happened to read would decide whether
    // somebody who said stop gets marketed to.
    const disagreeing = consents.data.filter(
      (c) => c.granted === Boolean(c.withdrawn_at),
    );
    if (disagreeing.length > 0) {
      console.log(
        `  x ${disagreeing.length} consents where granted and withdrawn_at disagree`,
      );
      failures += 1;
    } else {
      const off = consents.data.filter((c) => c.withdrawn_at).length;
      console.log(
        `  ok granted and withdrawn_at agree on all ${consents.data.length}` +
          ` (${off} withdrawn, and each keeps its row)`,
      );
    }
  } else {
    console.log('  -  no consent on file; the consent shape was not exercised');
  }

  const activities = await check(
    'GET /privacy/activities',
    `/privacy/activities?company_id=${CO}`,
    null,
  );
  if (activities?.data?.[0]) {
    expectFields('  processing record', activities.data[0], [
      'id',
      'name',
      'purpose',
      'lawful_basis',
      'data_categories',
      'subject_categories',
      'cross_border',
    ]);

    // The database refuses a cross-border entry naming no destination and no
    // safeguard, so anything already stored must satisfy it. A row that did
    // not would mean the constraint had been bypassed.
    const bad = activities.data.filter(
      (a) =>
        a.cross_border &&
        (!String(a.destination_country ?? '').trim() ||
          !String(a.transfer_safeguard ?? '').trim()),
    );
    if (bad.length > 0) {
      console.log(`  x ${bad.length} cross-border records name no destination or safeguard`);
      failures += 1;
    } else {
      console.log('  ok every cross-border record says where and under what protection');
    }
  } else {
    console.log('  -  the register is empty; the activity shape was not exercised');
  }

  await check('GET /privacy/retention', `/privacy/retention?company_id=${CO}`, null);

  const holds = await check('GET /privacy/holds', `/privacy/holds?company_id=${CO}`, null);
  if (holds?.data?.[0]) {
    expectFields('  legal hold', holds.data[0], ['id', 'name', 'reason', 'placed_at']);
  } else {
    console.log('  -  no holds in place; the hold shape was not exercised');
  }

  await check('GET /privacy/destructions', `/privacy/destructions?company_id=${CO}`, null);

  const settings = await check(
    'GET /privacy/settings',
    `/privacy/settings?company_id=${CO}`,
    ['dpo_external', 'data_region'],
    (j) => j?.settings,
  );
  if (settings?.settings?.data_region) {
    console.log(`  ok data is held in ${settings.settings.data_region}`);
  }

  const disclosure = await check(
    'GET /privacy/disclosure',
    `/privacy/disclosure?company_id=${CO}`,
    // `missing` is what stops the notice being published, and the screen
    // leads with it rather than showing the fields that ARE filled in.
    ['missing'],
    (j) => j?.disclosure,
  );
  const missing = disclosure?.disclosure?.missing;
  if (missing && !Array.isArray(missing)) {
    console.log('  x the disclosure does not say what is missing as a list');
    failures += 1;
  } else if (missing) {
    console.log(
      missing.length === 0
        ? '  ok the published notice has everything it needs'
        : `  ok the notice names its own ${missing.length} blockers (${missing.join(', ')})`,
    );
  }
}

console.log('\nWHAT HAPPENED, WHAT IS HELD, AND WHETHER IT COULD BE RESTORED');
{
  const trail = await check('GET /audit', `/audit?company_id=${CO}&limit=200`, [
    'data',
    // The verbs actually present in this tenant's trail, sent WITH the rows so
    // the filter offers what is there. A screen holding its own list goes
    // stale the day a module ships, and a filter that quietly stopped
    // offering a verb would hide the entries somebody came to find.
    'actions',
  ]);
  if (trail?.data?.[0]) {
    expectFields('  audit entry', trail.data[0], [
      'occurred_at',
      'action',
      'entity_type',
    ]);

    // The invariant the filter rests on. A verb in the rows and absent from
    // the vocabulary is one nobody can filter to.
    const vocabulary = new Set(trail.actions ?? []);
    const unlisted = [
      ...new Set(trail.data.map((r) => r.action).filter((a) => !vocabulary.has(a))),
    ];
    if (unlisted.length > 0) {
      console.log(
        `  x ${unlisted.length} verbs appear in the trail and not in its own ` +
          `vocabulary (${unlisted.slice(0, 4).join(', ')})`,
      );
      failures += 1;
    } else {
      console.log(
        `  ok every one of ${trail.data.length} entries uses one of the ` +
          `${vocabulary.size} verbs the trail offers`,
      );
    }

    // An entry carrying both sides is an amendment; one side is a creation or
    // a removal; neither is something that changed no stored row. The screen
    // tells those apart, so at least one shape has to be exercised.
    const withBoth = trail.data.filter((r) => r.before != null && r.after != null);
    const oneSided = trail.data.filter(
      (r) => (r.before == null) !== (r.after == null),
    );
    console.log(
      `  ok ${withBoth.length} amendments and ${oneSided.length} one-sided ` +
        'entries carry evidence',
    );
  } else {
    console.log('  -  the trail is empty; the entry shape was not exercised');
  }

  const documents = await check('GET /documents', `/documents?company_id=${CO}`, null);
  if (documents?.data?.[0]) {
    expectFields('  document', documents.data[0], [
      'id',
      'entity_type',
      'file_name',
      'content_type',
      'byte_size',
      // What makes this a register rather than a folder: proof that the file
      // is the one that was filed.
      'checksum',
      'classification',
    ]);

    // Absence is not zero. A permanent document omits `days_to_expiry`
    // entirely, and reading the missing field as 0 would file every one of
    // them as expiring this morning.
    const permanent = documents.data.filter((d) => !d.expires_on);
    const wronglyClocked = permanent.filter((d) => 'days_to_expiry' in d);
    if (wronglyClocked.length > 0) {
      console.log(
        `  x ${wronglyClocked.length} documents with no expiry still report a countdown`,
      );
      failures += 1;
    } else {
      console.log(
        `  ok ${permanent.length} documents that never expire report no countdown`,
      );
    }

    // `classification` is the data_class enum -- how sensitive the CONTENT is,
    // which the retention regime and the erasure path read. Not a document
    // kind, and not free text.
    const CLASSES = new Set(['public', 'internal', 'personal', 'sensitive_personal']);
    const odd = documents.data.filter((d) => !CLASSES.has(d.classification));
    if (odd.length > 0) {
      console.log(
        `  x ${odd.length} documents carry a sensitivity outside the enum ` +
          `(${[...new Set(odd.map((d) => d.classification))].join(', ')})`,
      );
      failures += 1;
    } else {
      console.log('  ok every document carries one of the four sensitivities');
    }
  } else {
    console.log('  -  nothing is filed; the document shape was not exercised');
  }

  // A sensitivity the enum does not have used to reach the insert and come
  // back as a 500, telling somebody who mistyped one word that the fault was
  // ours. Safe to assert from here: the write is refused, so nothing is
  // stored and the run leaves no rows behind.
  {
    const res = await post(`/documents?company_id=${CO}`, {
      entity_type: 'company',
      entity_id: CO,
      file_name: 'verify-api-should-not-stick.txt',
      data: 'eA==',
      classification: 'licence',
    });
    if (res.status !== 400) {
      console.log(`  x a mistyped sensitivity answered ${res.status}, want 400`);
      failures += 1;
    } else {
      console.log('  ok a mistyped sensitivity is refused, not reported as our fault');
    }
  }

  const health = await check('GET /backups/health', `/backups/health?company_id=${CO}`, [
    'recent_failures',
    'at_risk',
    // The server's own sentence. Shown as written, because recomposing it from
    // the parts would produce a second opinion on a question that needs one.
    'summary',
  ]);
  if (health && !String(health.summary ?? '').trim()) {
    console.log('  x the backup health says nothing about where it stands');
    failures += 1;
  } else if (health) {
    console.log(`  ok it says where it stands: "${health.summary}"`);
  }

  const backups = await check('GET /backups', `/backups?company_id=${CO}`, null);
  if (backups?.data?.[0]) {
    expectFields('  backup', backups.data[0], ['id', 'kind', 'status', 'started_at']);

    // A backup that ran is not a backup that restores. A run that finished
    // without being checked must NOT carry verified_at, or a dashboard reads
    // an unproven file as protection.
    const finished = backups.data.filter((b) => b.finished_at);
    const proven = finished.filter((b) => b.verified_at && !b.verify_error);
    const unproven = finished.filter((b) => !b.verified_at);
    if (finished.length > 0 && proven.length === finished.length && backups.data.length > 1) {
      console.log(
        '  -  every backup is verified, so the unverified state was not exercised',
      );
    }
    // And a failed verification must not stamp verified_at, which the router
    // comment says outright.
    const contradictory = backups.data.filter((b) => b.verify_error && b.verified_at);
    if (contradictory.length > 0) {
      console.log(
        `  x ${contradictory.length} backups are stamped verified AND carry a verify error`,
      );
      failures += 1;
    } else {
      console.log(
        `  ok ${proven.length} of ${finished.length} finished backups are proved ` +
          `readable, ${unproven.length} never checked`,
      );
    }
  } else {
    console.log('  -  nothing has been backed up; the backup shape was not exercised');
  }
}

console.log('\nTAKING A CARD, BRINGING RECORDS IN, AND READING BOOKS TOGETHER');
{
  const catalogue = await check(
    'GET /payment-providers',
    `/payment-providers?company_id=${CO}`,
    ['providers'],
  );
  const providers = catalogue?.providers ?? [];
  if (providers.length > 0) {
    expectFields('  provider', providers[0], ['key', 'name', 'fields', 'methods']);
    // The form is built from this, field by field. A provider with no fields
    // would render an empty form somebody could submit.
    const fieldless = providers.filter((p) => (p.fields ?? []).length === 0);
    if (fieldless.length > 0) {
      console.log(`  x ${fieldless.length} providers name no fields at all`);
      failures += 1;
    } else {
      console.log(`  ok ${providers.length} providers each name the fields they need`);
    }
    // The sealed half is marked, so the screen knows which box to make a
    // password field and which value it must never try to read back.
    const sealed = providers.filter((p) => (p.fields ?? []).some((f) => f.secret));
    console.log(`  ok ${sealed.length} of them have a sealed field, marked as such`);
  }

  const gateways = await check(
    'GET /payment-gateways',
    `/payment-gateways?company_id=${CO}`,
    ['gateways'],
  );
  if (gateways?.gateways?.[0]) {
    expectFields('  connection', gateways.gateways[0], [
      'id',
      'provider',
      'label',
      'mode',
      'settings',
      'is_active',
      // Whether a key is stored. The key itself must never appear.
      'has_secret',
    ]);

    // The secret must not be on the wire under any name. A payload that grew
    // one would hand every key to anybody who can read this list.
    const leaking = gateways.gateways.filter((g) =>
      ['secret', 'secret_key', 'api_key', 'password'].some((k) => k in g),
    );
    if (leaking.length > 0) {
      console.log(`  x ${leaking.length} connections carry a secret in the listing`);
      failures += 1;
    } else {
      console.log('  ok no connection carries a key back');
    }

    // Three answers, not two. `last_check_ok` is absent until a check runs,
    // and a screen reading absent as false calls a new connection broken.
    const unchecked = gateways.gateways.filter(
      (g) => typeof g.last_check_ok !== 'boolean',
    );
    const failing = gateways.gateways.filter((g) => g.last_check_ok === false);
    console.log(
      `  ok ${unchecked.length} never checked, ${failing.length} not answering, ` +
        `${gateways.gateways.length - unchecked.length - failing.length} answering`,
    );

    // The constraint the screen turns into a sequence: nothing can be live AND
    // switched on without having answered. If one ever is, the screen's
    // "configure, check, switch on" ordering is describing a rule that is no
    // longer enforced.
    const onWithoutAnswering = gateways.gateways.filter(
      (g) => g.is_active && g.mode === 'live' && g.last_check_ok !== true,
    );
    if (onWithoutAnswering.length > 0) {
      console.log(
        `  x ${onWithoutAnswering.length} live connections are switched on ` +
          'without having answered a check',
      );
      failures += 1;
    } else {
      console.log('  ok nothing is live and switched on without having answered');
    }

    // And the refusal says which step is missing. It used to be "That value is
    // not allowed", which names neither the value nor the remedy.
    const early = await post(`/payment-gateways?company_id=${CO}`, {
      provider: gateways.gateways[0].provider,
      label: 'verify:api should not stick',
      mode: 'live',
      settings: gateways.gateways[0].settings,
      methods: gateways.gateways[0].methods,
      is_active: true,
    });
    const said = early.json?.error?.message ?? '';
    if (early.status !== 400) {
      console.log(`  x switching on an unchecked live connection answered ${early.status}`);
      failures += 1;
    } else if (!/check/i.test(said)) {
      console.log(`  x the refusal does not name the step that is missing: ${said}`);
      failures += 1;
    } else {
      console.log('  ok switching one on too early is refused, and says why');
    }
  } else {
    console.log('  -  no card connection; the connection shape was not exercised');
  }

  await check('GET /payment-attempts', `/payment-attempts?company_id=${CO}`, ['attempts']);
  await check('GET /settlement/pending', `/settlement/pending?company_id=${CO}`, null);

  const shapes = await check('GET /imports/shapes', `/imports/shapes?company_id=${CO}`, null);
  if (shapes?.data?.[0]) {
    expectFields('  import shape', shapes.data[0], [
      'kind',
      'label',
      // What the screen builds its column check from. A shape with no required
      // columns would accept any file and fail row by row instead.
      'required',
      'optional',
    ]);
    const shapeless = shapes.data.filter((s) => (s.required ?? []).length === 0);
    if (shapeless.length > 0) {
      console.log(`  x ${shapeless.length} import kinds require no columns at all`);
      failures += 1;
    } else {
      console.log(
        `  ok ${shapes.data.length} import kinds each name the columns they need`,
      );
    }
  }

  const imports = await check('GET /imports', `/imports?company_id=${CO}`, null);
  if (imports?.data?.[0]) {
    expectFields('  import', imports.data[0], [
      'id',
      'kind',
      'status',
      'total_rows',
      'valid_rows',
      'error_rows',
    ]);
    // Counts that do not add up would make "import 1,847 of 2,000" a lie.
    const odd = imports.data.filter(
      (b) => b.valid_rows + b.error_rows > b.total_rows,
    );
    if (odd.length > 0) {
      console.log(`  x ${odd.length} imports count more rows than the file had`);
      failures += 1;
    } else {
      console.log(`  ok ${imports.data.length} imports count their rows consistently`);
    }
  } else {
    console.log('  -  nothing has been imported; the batch shape was not exercised');
  }

  // The screen holds no export list: `/imports/shapes` publishes it beside the
  // import shapes. This asserts every published kind actually answers, which
  // is what a copied list cannot promise -- the first version of that screen
  // copied `reports.ExportKinds`, a different list on a different route, and
  // six of its eight kinds did not exist here.
  {
    const published = shapes?.exports ?? [];
    if (published.length === 0) {
      console.log('  x /imports/shapes publishes no exports; the screen would offer none');
      failures += 1;
    } else {
      expectFields('  exportable', published[0], ['kind', 'label', 'filename']);
      const dead = [];
      for (const x of published) {
        const res = await call(`/exports/${x.kind}?company_id=${CO}`);
        if (res.status !== 200) dead.push(`${x.kind} -> ${res.status}`);
      }
      if (dead.length > 0) {
        console.log(`  x published exports that do not answer: ${dead.join(', ')}`);
        failures += 1;
      } else {
        console.log(`  ok all ${published.length} published exports answer`);
      }
    }
    // A kind nobody defined is refused rather than reaching a method that was
    // never meant to be exported, which is what the fixed list is for.
    const made = await call(`/exports/not-a-real-report?company_id=${CO}`);
    if (made.status === 200) {
      console.log('  x an export kind nobody defined answered 200');
      failures += 1;
    } else {
      console.log(`  ok an undefined export kind is refused (${made.status})`);
    }
  }

  // The distinction this whole screen rests on: a plan refusal is 402, not
  // 403. The caller holds group.view; what is missing is commercial. A screen
  // that showed "you may not" would send somebody to their manager to ask for
  // a permission they already have.
  const groups = await call(`/groups?company_id=${CO}`);
  if (groups.status === 402) {
    console.log(
      `  ok group consolidation is refused commercially, not by permission ` +
        `(${groups.json?.error?.code})`,
    );
  } else if (groups.status === 200) {
    console.log('  -  this plan includes groups; the 402 path was not exercised');
    if (groups.json?.data?.[0]) {
      expectFields('  group', groups.json.data[0], [
        'id',
        'name',
        'presentation_currency',
        'members',
      ]);
      const member = groups.json.data[0].members?.[0];
      if (member) {
        expectFields('  member', member, [
          'company_id',
          'name',
          // Decimal on the wire and decimal on the screen: three subsidiaries
          // at 33.33 each is where a float starts producing 99.99000000001.
          'ownership_pct',
          'is_parent',
        ]);
        if (typeof member.ownership_pct !== 'string') {
          console.log('  x ownership is not a string');
          failures += 1;
        }
      }
    }
  } else if (groups.status === 403) {
    console.log(
      '  x groups answered 403 for a caller holding group.view; a commercial ' +
        'refusal must not read as a permission one',
    );
    failures += 1;
  } else {
    console.log(`  x GET /groups: HTTP ${groups.status}`);
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
    // The list searches, filters and pages on the SERVER, and the screen
    // depends on all three. It used to do none of them: `LIMIT 500` with no
    // arguments, filtered in the browser. On a platform with more accounts than
    // that -- this database has nine and a half thousand -- an operator
    // searching for a client outside the newest five hundred was told there
    // were no matches, which is a wrong answer wearing the clothes of a right
    // one. If any of this regresses the screen silently goes back to lying.
    if (!tenants?.page || typeof tenants.page.has_more !== 'boolean') {
      console.log('  x /platform/tenants: no page metadata; the list cannot be walked');
      failures += 1;
    } else {
      console.log('  ok the list pages');
    }

    {
      const first = (await call('/platform/tenants?limit=2')).json;
      const rows = first?.data ?? [];
      if (rows.length === 2 && first.page?.cursor) {
        const second = (
          await call(`/platform/tenants?limit=2&after=${first.page.cursor}`)
        ).json;
        const overlap = (second?.data ?? []).filter((r) =>
          rows.some((f) => f.id === r.id),
        );
        if (overlap.length > 0) {
          console.log('  x /platform/tenants: the second page repeats the first');
          failures += 1;
        } else {
          console.log('  ok the cursor advances without repeating');
        }
      } else {
        console.log('  -  fewer than two tenants; paging was not exercised');
      }
    }

    const firstTenant = tenants?.data?.[0];
    if (firstTenant?.name) {
      // Searching for a name that exists must find it, and a name that cannot
      // exist must find nothing. The second half is what catches a filter that
      // is accepted and ignored.
      const hit = (
        await call(
          `/platform/tenants?limit=50&search=${encodeURIComponent(firstTenant.name)}`,
        )
      ).json;
      if ((hit?.data ?? []).length === 0) {
        console.log('  x /platform/tenants: search does not find a tenant that exists');
        failures += 1;
      }
      const miss = (
        await call('/platform/tenants?limit=50&search=zzzznosuchbusinesszzzz')
      ).json;
      if ((miss?.data ?? []).length !== 0) {
        console.log('  x /platform/tenants: search is accepted and ignored');
        failures += 1;
      }
      if ((hit?.data ?? []).length > 0 && (miss?.data ?? []).length === 0) {
        console.log('  ok the search narrows, and narrows to nothing when it should');
      }
    }

    // The register the operator keeps and, until this session, could not read:
    // the only read was scoped to a tenant and an operator has none.
    const subs = await check('GET /platform/subprocessors', '/platform/subprocessors', null);
    if (subs?.data?.[0]) {
      expectFields('  sub-processor', subs.data[0], [
        'id',
        'name',
        'purpose',
        'country',
        'data_categories',
        'is_active',
      ]);
    } else {
      console.log('  -  the sub-processor register is empty; the row shape was not exercised');
    }

    // --- the regulatory registry, and the three screens over it ---

    const rules = await check('GET /platform/rules', '/platform/rules', null);
    if (rules?.data?.[0]) {
      expectFields('  rule', rules.data[0], [
        'id',
        'rule_key',
        'country',
        'payload',
        'effective_from',
        'verified',
        'release_blocker',
      ]);
      // The screen leads with the markets that cannot take a new client, which
      // is release_blocker AND NOT verified -- not either one alone. A verified
      // blocker blocks nothing, and an unverified ordinary rule blocks nothing.
      const blocking = rules.data.filter((r) => r.release_blocker && !r.verified);
      const markets = [...new Set(blocking.map((r) => r.country))];
      console.log(
        `  ok ${blocking.length} unchecked blockers across ${markets.length} market(s)`,
      );
    } else {
      console.log('  -  the registry is empty; the rule shape was not exercised');
    }

    // A country is REQUIRED, and the screen renders the unchosen state
    // differently from an empty one because of this. If the route ever starts
    // answering every country, that distinction becomes a lie.
    {
      const none = (await call('/platform/jurisdictions')).json;
      if ((none?.data ?? []).length !== 0) {
        console.log(
          '  -  /platform/jurisdictions now answers without a country; the screen still asks for one',
        );
      } else {
        console.log('  ok jurisdictions answer nothing until a country is named');
      }
      const us = (await call('/platform/jurisdictions?country=us')).json;
      if (us?.data?.[0]) {
        expectFields('  jurisdiction', us.data[0], ['id', 'country', 'level', 'code', 'name']);
      } else {
        console.log('  -  no jurisdictions on file for us; the row shape was not exercised');
      }
    }

    const batches = await check(
      'GET /platform/jurisdictions/rates',
      '/platform/jurisdictions/rates',
      null,
    );
    if (batches?.data?.[0]) {
      // The four fields that NAME a schedule. There is no batch id, so every
      // action the screen offers sends these back; if one stopped arriving,
      // review, activation and sign-off would all silently address nothing.
      expectFields('  rate schedule', batches.data[0], [
        'country',
        'source_authority',
        'source_document',
        'treatment',
        'effective_from',
        'rates',
        'reviewed',
        'verified',
        'status',
      ]);
    } else {
      console.log('  -  no rate schedules imported; the batch shape was not exercised');
    }

    // --- what a client pays, and what their plan lets them do ---

    if (firstTenant?.id) {
      const sub = (
        await call(`/platform/tenants/${firstTenant.id}/subscription`)
      ).json;
      expectFields('  subscription', sub?.subscription, [
        'tier',
        'cycle',
        'price',
        'currency',
        'status',
        'outstanding',
        'limits',
      ]);
      // Ceilings AND the live counts, together. The screen renders a ceiling
      // against what is in use, because "why can this client not add another
      // till" is the question an operator opens it to answer.
      expectFields('  allowances', sub?.subscription?.limits, [
        'max_companies',
        'max_stores',
        'max_users',
        'max_terminals',
        'companies',
        'stores',
        'users',
        'terminals',
      ]);
      // Money as a string. A price rendered through a float is how a bill for
      // 0.30000000000000004 gets sent to a client.
      if (typeof sub?.subscription?.price !== 'string') {
        console.log('  x subscription price is not a string');
        failures += 1;
      }
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

// Back to the business identity, because the sections below are the shop's.
token = tenantToken;

console.log('\nHOW THE CATALOGUE IS ARRANGED  (/products/arrangement)');
const departments = await check(
  'GET /catalog/categories',
  `/catalog/categories?company_id=${CO}`,
  ['id', 'name', 'depth', 'sort_order', 'is_active', 'product_count'],
  (j) => j.data[0],
);
// `parent_id` is omitted on a top-level department rather than sent as null,
// so it cannot be in the required list -- but the screen indents on `depth`,
// and a tree whose depths were all zero would render flat and look correct.
if ((departments?.data ?? []).every((c) => c.depth === 0)) {
  console.log('  -  nothing is nested; the indentation was not exercised');
}
await check(
  'GET /catalog/brands',
  `/catalog/brands?company_id=${CO}`,
  ['id', 'name', 'is_active', 'product_count'],
  (j) => j.data[0],
);
await check(
  'GET /catalog/units',
  `/catalog/units?company_id=${CO}`,
  ['id', 'code', 'name', 'allows_fraction', 'is_active', 'product_count'],
  (j) => j.data[0],
);

console.log('\nWHAT A NEW PRODUCT MAY BE  (/products/new)');
const treatments = await check(
  'GET /catalog/tax-treatments',
  `/catalog/tax-treatments?company_id=${CO}`,
  ['country', 'model', 'treatments'],
);
if (treatments) {
  expectFields('  treatment', treatments.treatments?.[0], ['code', 'needs_reason']);
  // The form defaults to `standard` where the country has one. A country whose
  // list did not include it would leave the select empty on first render.
  if (!(treatments.treatments ?? []).some((t) => t.code === 'standard')) {
    console.log(
      `  -  ${treatments.country} has no "standard" treatment; the form falls back to the first`,
    );
  }
}

console.log('\nAPPROVALS, CONFIGURED  (/settings/approvals)');
const approvalRules = await check('GET /approval-rules', `/approval-rules?company_id=${CO}`);
if (approvalRules?.data?.length) {
  expectFields('  rule', approvalRules.data[0], [
    'id',
    'name',
    'is_active',
    'subject',
    'condition',
    'action',
    'steps',
    'priority',
  ]);
} else {
  console.log('  -  no rules configured; the row shape was not exercised');
}
const cover = await check(
  'GET /approval-delegations',
  `/approval-delegations?company_id=${CO}`,
);
if (cover?.data?.length) {
  expectFields('  cover', cover.data[0], [
    'id',
    'from',
    'to',
    'from_user_id',
    'to_user_id',
    'starts_on',
    'ends_on',
    'is_live',
  ]);
} else {
  console.log('  -  nobody is covering for anybody; the row shape was not exercised');
}

console.log('\nCOMMISSION SCHEMES  (/people/commission)');
const schemes = await check('GET /commission-rules', `/commission-rules?company_id=${CO}`);
if (schemes?.data?.length) {
  expectFields('  scheme', schemes.data[0], [
    'id',
    'name',
    'is_active',
    'basis',
    'rate',
    'tiers',
    'effective_from',
  ]);
} else {
  console.log('  -  no schemes configured; the row shape was not exercised');
}

console.log('\nTHE RECEIPT AN INSTALMENT IS COLLECTED AGAINST');
const takings = await check(
  'GET /receivables/receipts',
  `/receivables/receipts?company_id=${CO}`,
);
if (takings?.data?.length) {
  expectFields('  receipt', takings.data[0], [
    'id',
    'receipt_number',
    'customer_id',
    'customer',
    'received_on',
    'method',
    'amount',
    'unapplied',
    'currency',
  ]);
} else {
  console.log('  -  nothing has been received; the row shape was not exercised');
}
// The filter the picker actually uses. Its absence is what made the
// instalments screen ask somebody to type a UUID.
const offered = await call(`/receivables/receipts?company_id=${CO}&unapplied=true`);
if (offered.status !== 200) {
  console.log(`  x ?unapplied=true: HTTP ${offered.status}`);
  failures += 1;
} else {
  console.log('  ok ?unapplied=true narrows to what can still be collected');
}

console.log(
  `\n${failures === 0 ? 'ALL SCREEN CONTRACTS VERIFIED' : `${failures} MISMATCHES`}`,
);
process.exit(failures === 0 ? 0 : 1);
