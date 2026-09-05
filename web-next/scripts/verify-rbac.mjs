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

/**
 * Creates a member of staff and signs in as them with the password issued.
 *
 * Says WHY when it cannot. Every caller used to print "no such role seeded" on
 * a null, and that sentence was wrong the day this database reached its plan's
 * seat limit: the roles were all there, the accounts could not be made, and
 * five boundaries silently stopped being checked while the run still ended
 * "EVERY BOUNDARY HELD". A verification that quietly stops verifying is worse
 * than one that fails.
 */
async function staff(label, role) {
  if (!role) {
    console.log(`  -  no role matching ${label}; that boundary was not exercised`);
    return null;
  }
  const email = `verify-${label}-${Date.now()}@example.test`;
  const made = await owner('POST', q('/people'), {
    email,
    full_name: `verify:rbac ${label}`,
    role_id: role.id,
    company_id: CO,
  });
  if (made.status !== 201 && made.status !== 200) {
    bad(
      `could not create the ${role.name} used to test ${label}: ` +
        `${made.status} ${made.json?.error?.message ?? ''}`.trim(),
    );
    return null;
  }
  const signed = await login({ email, password: made.json.data.temporary_password });
  if (!signed?.access_token) {
    bad(`created the ${role.name} for ${label} but could not sign in as them`);
    return null;
  }
  // Remembered so the run can put them back at the end. Nothing here deletes a
  // person -- their name would be on whatever they touched -- but a disabled
  // account does not hold a seat, and sixty-five of these had accumulated
  // before anybody noticed the dev database was one account away from refusing
  // to create any more.
  // The account is under `person`, beside the one-time password.
  seeded.push(made.json.data.person?.id);
  return { call: client(signed.access_token), email };
}

/** Every throwaway account this run created, so the run can retire them. */
const seeded = [];

/**
 * Disables everybody this run created.
 *
 * Called at the end, whatever happened. A verification tool that leaves a
 * business one seat smaller every time it runs stops being usable on the
 * database people actually develop against -- and the failure, when it comes,
 * looks like the roles having disappeared rather than like a leak.
 */
async function retireSeeded() {
  if (seeded.length === 0) return;
  let retired = 0;
  for (const id of seeded) {
    if (!id) continue;
    const off = await owner('POST', q(`/people/${id}/active`), { active: false });
    if (off.status === 200 || off.status === 204) retired += 1;
  }
  console.log(`  -  retired ${retired} of the ${seeded.length} accounts this run created`);
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

// --- E2.3: seeing what was spent is not deciding what comes back --------
//
// `input_vat_recoverable` is a tax position, not a label. It decides whether
// the VAT on every expense in a category goes to Input VAT Receivable or is
// absorbed into the cost, and a wrong answer either overstates a VAT return
// or quietly costs the business money it was entitled to reclaim.
//
// So the expense setup screen is behind `expense.manage_heads` rather than
// `expense.view`, and the chart of accounts it picks from is gated the same
// way. A Branch Manager may look at what their branch spent and may not
// touch any of it. This proves the server agrees.

console.log('\nWHAT WAS SPENT, AND WHO DECIDES WHAT COMES BACK');
const branch = await staff('branch', roleNamed(/Branch/));
const accountant = await staff('accountant', roleNamed(/Accountant/));

if (!branch || !accountant) {
  console.log('  -  Branch Manager or Accountant not seeded; the expense boundary was not exercised');
} else {
  const head = (await owner('GET', q('/expenses/heads'))).json?.data?.[0];

  // Reading is theirs. The screens a branch manager opens all day depend on
  // it, and refusing this would be refusing the wrong thing.
  for (const [label, path] of [
    ['GET /expenses', q('/expenses')],
    ['GET /expenses/heads', q('/expenses/heads')],
    ['GET /expenses/departments', q('/expenses/departments')],
    ['GET /expenses/recurring', q('/expenses/recurring')],
  ]) {
    expect(`branch manager: ${label}`, (await branch.call('GET', path)).status, 200);
  }

  // Configuring is not. Including the chart of accounts itself, which is why
  // the sidebar entry names `expense.manage_heads`: an entry shown on
  // `expense.view` would be a link somebody follows into a 403.
  expect(
    'branch manager: GET /expenses/accounts',
    (await branch.call('GET', q('/expenses/accounts'))).status,
    403,
  );
  expect(
    'branch manager: POST /expenses/heads',
    (await branch.call('POST', q('/expenses/heads'), {
      code: 'NOPE',
      name: 'Not allowed',
      account_id: head?.account_id,
      input_vat_recoverable: true,
    })).status,
    403,
  );
  if (head) {
    expect(
      'branch manager: PUT /expenses/heads/{id}',
      (await branch.call('PUT', q(`/expenses/heads/${head.id}`), {
        name: head.name,
        account_id: head.account_id,
        // The field that would be changed: a branch manager marking
        // entertainment reclaimable would overstate the return.
        input_vat_recoverable: !head.input_vat_recoverable,
      })).status,
      403,
    );
    expect(
      'branch manager: POST /expenses/heads/{id}/active',
      (await branch.call('POST', q(`/expenses/heads/${head.id}/active`), {
        active: false,
      })).status,
      403,
    );
  }
  expect(
    'branch manager: POST /expenses/departments',
    (await branch.call('POST', q('/expenses/departments'), {
      code: 'NOPE',
      name: 'Not allowed',
    })).status,
    403,
  );
  expect(
    'branch manager: POST /expenses/recurring',
    (await branch.call('POST', q('/expenses/recurring'), {
      name: 'Not allowed',
      head_id: head?.id,
      amount: '1.00',
      paid_from: 'bank',
      frequency: 'monthly',
      interval_count: 1,
      starts_on: new Date().toISOString().slice(0, 10),
    })).status,
    403,
  );

  // Booking a schedule is recording an expense, and it is gated as one. A
  // person who may not record an expense must not be able to make a schedule
  // do it for them, which is the reason the button is behind
  // `expense.record` rather than the permission that opens the screen.
  expect(
    'branch manager: POST /expenses/recurring/generate',
    (await branch.call('POST', q('/expenses/recurring/generate'), {})).status,
    403,
  );

  // And the person the screen is actually for.
  expect(
    'accountant: GET /expenses/accounts',
    (await accountant.call('GET', q('/expenses/accounts'))).status,
    200,
  );
  expect(
    'accountant: POST /expenses/recurring/generate',
    (await accountant.call('POST', q('/expenses/recurring/generate'), {})).status,
    200,
  );
}
// --- C11: a person who cannot see the bank ledger cannot reconcile it ----
//
// Migration 0081 gave reconciling its own verb and says why: `accounting.create`
// already covers posting a journal entry, and a transfer is one. Reconciliation
// is a different act -- it asserts that the books agree with an outside party,
// which is the assertion an auditor relies on.
//
// It went to Owner, Accountant and Auditor, and deliberately not to the Store
// Manager, whose role 0005 describes as unable to "see bank ledgers or true net
// profit". The seeded Branch Manager turns out to hold no `accounting.*` at all,
// which is that sentence enforced completely rather than partially -- and is
// why the nav entry names `accounting.reconcile` rather than `accounting.view`.
//
// The Auditor is the sharper case, and the reason the permission exists at all:
// they may reconcile and may not post. Somebody who could correct the books
// they are checking is not checking them.

console.log('\nPROVING THE BOOKS AGREE WITH THE BANK');
const auditor = await staff('auditor', roleNamed(/Auditor/));

if (!branch || !accountant || !auditor) {
  console.log(
    '  -  Branch Manager, Accountant or Auditor not seeded; the boundary was not exercised',
  );
} else {
  const anyStatement = (await owner('GET', q('/treasury/statements'))).json?.data?.[0];
  const bankAccount = (await owner('GET', q('/treasury/accounts'))).json?.data?.find(
    (a) => a.kind === 'bank',
  );

  // A branch manager cannot see the bank at all: not the accounts, not the
  // money moving between them, not the statements.
  for (const [label, path] of [
    ['GET /treasury/accounts', q('/treasury/accounts')],
    ['GET /treasury/transfers', q('/treasury/transfers')],
    ['GET /treasury/statements', q('/treasury/statements')],
  ]) {
    expect(`branch manager: ${label}`, (await branch.call('GET', path)).status, 403);
  }
  if (bankAccount) {
    expect(
      'branch manager: POST /treasury/statements',
      (
        await branch.call('POST', q('/treasury/statements'), {
          account_id: bankAccount.id,
          starts_on: '2026-09-01',
          ends_on: '2026-09-30',
          opening_balance: '0.00',
          closing_balance: '10.00',
          lines: [{ value_date: '2026-09-02', description: 'x', amount: '10.00' }],
        })
      ).status,
      403,
    );
  }
  if (anyStatement) {
    // The signature itself, and the one that matters most: a reconciliation
    // signed by somebody who cannot see the ledger it attests to is exactly the
    // piece of paper C11 exists to prevent.
    expect(
      'branch manager: POST /treasury/statements/{id}/reconcile',
      (
        await branch.call(
          'POST',
          q(`/treasury/statements/${anyStatement.id}/reconcile`),
          {},
        )
      ).status,
      403,
    );
  }

  // An auditor reads the bank and reconciles it.
  for (const [label, path] of [
    ['GET /treasury/accounts', q('/treasury/accounts')],
    ['GET /treasury/statements', q('/treasury/statements')],
  ]) {
    expect(`auditor: ${label}`, (await auditor.call('GET', path)).status, 200);
  }
  if (anyStatement) {
    expect(
      'auditor: GET /treasury/statements/{id}',
      (await auditor.call('GET', q(`/treasury/statements/${anyStatement.id}`))).status,
      200,
    );
  }

  // And does not post. Checking the books and being able to correct them are
  // not the same job, and one person holding both is how a difference gets
  // journalled away rather than explained.
  if (bankAccount) {
    expect(
      'auditor: POST /treasury/transfers',
      (
        await auditor.call('POST', q('/treasury/transfers'), {
          uuid: crypto.randomUUID(),
          from_account_id: bankAccount.id,
          to_account_id: bankAccount.id,
          amount: '1.00',
          moved_on: new Date().toISOString().slice(0, 10),
        })
      ).status,
      403,
    );
  }
  expect(
    'auditor: POST /treasury/accounts',
    (
      await auditor.call('POST', q('/treasury/accounts'), {
        kind: 'bank',
        name: 'Not allowed',
        currency: 'SAR',
      })
    ).status,
    403,
  );

  expect(
    'accountant: GET /treasury/statements',
    (await accountant.call('GET', q('/treasury/statements'))).status,
    200,
  );
}

// --- taking goods back and swapping them are two verbs -------------------
//
// `sales.refund` and `sales.exchange` are separate permissions, and the
// sidebar treated them as one: the Returns entry was shown on either, and the
// screen behind it is guarded on refund alone. Somebody holding only exchange
// saw a link and got a refusal.
//
// Whether the seeded roles hold them apart is the question this answers. The
// boundary is worth having either way: an exchange writes an INVOICE as well as
// a credit note, so it puts goods out of the shop, which taking a return does
// not.

console.log('\nTAKING GOODS BACK, AND SWAPPING THEM');
{
  const refundRole = roles.find((r) => (r.permissions ?? []).includes('sales.refund'));
  const exchangeRole = roles.find((r) => (r.permissions ?? []).includes('sales.exchange'));

  const held = (name) => {
    const role = roles.find((r) => r.name === name);
    const p = new Set(role?.permissions ?? []);
    return `${name}: refund=${p.has('sales.refund')} exchange=${p.has('sales.exchange')}`;
  };
  for (const name of ['Cashier / POS Operator', 'Branch / Store Manager', 'Owner']) {
    if (roles.some((r) => r.name === name)) console.log(`  -  ${held(name)}`);
  }

  if (!refundRole || !exchangeRole) {
    console.log('  -  neither verb is seeded on any role; the boundary was not exercised');
  } else {
    // A cashier who can do neither must be refused both, which is the case the
    // POS guard is built around.
    const plain = await staff('plain-cashier', roleNamed(/Customer Service/));
    if (!plain) {
      console.log('  -  no role without either verb; the refusals were not exercised');
    } else {
      expect(
        'somebody with neither verb: POST /pos/returns',
        (
          await plain.call('POST', q('/pos/returns'), {
            credit_note_uuid: crypto.randomUUID(),
            original_invoice_id: '00000000-0000-0000-0000-000000000000',
            issued_at: new Date().toISOString(),
            reason: 'verify:rbac',
            lines: [],
          })
        ).status,
        403,
      );
      expect(
        'somebody with neither verb: POST /pos/exchanges',
        (
          await plain.call('POST', q('/pos/exchanges'), {
            credit_note_uuid: crypto.randomUUID(),
            invoice_uuid: crypto.randomUUID(),
            original_invoice_id: '00000000-0000-0000-0000-000000000000',
            issued_at: new Date().toISOString(),
            reason: 'verify:rbac',
            returning: [],
            replacement: { doc_type: 'simplified', lines: [] },
            settlement: [],
          })
        ).status,
        403,
      );
      // And the read that both screens start from. Looking a sale up to refund
      // it is `sales.refund`, so a person who cannot refund cannot look.
      expect(
        'somebody with neither verb: GET /pos/sales/lookup',
        (await plain.call('GET', q('/pos/sales/lookup?reference=INV-1'))).status,
        403,
      );
    }
  }
}

// --- unloading a lorry is not deciding what the shop claims back ---------
//
// `purchasing.return_goods` is its own verb rather than part of
// `receive_goods`. Taking a delivery in is a warehouse act; sending one back
// reduces what the business owes and produces a document the supplier will
// argue with, so it went to the Owner and the Purchase Manager and not to the
// Store Manager — who holds `receive_goods` and not `record_bill`, and a return
// is a claim against a bill they cannot read.

console.log('\nSENDING GOODS BACK, WHICH IS NOT RECEIVING THEM');
{
  const keeperRole = roleNamed(/Inventory|Warehouse/);
  const storeRole = roleNamed(/Branch|Store/);
  const buyerRole = roleNamed(/Purchase Manager/);

  const who = (re, label) => {
    const role = roles.find((r) => re.test(r.name));
    if (!role) return;
    const p = new Set(role.permissions ?? []);
    console.log(
      `  -  ${label}: receive=${p.has('purchasing.receive_goods')}` +
        ` return=${p.has('purchasing.return_goods')}` +
        ` bill=${p.has('purchasing.record_bill')}`,
    );
  };
  who(/Branch|Store/, 'Store Manager');
  who(/Purchase Manager/, 'Purchase Manager');
  who(/Inventory|Warehouse/, 'Inventory Keeper');

  const storeman = await staff('storeman', storeRole ?? keeperRole);
  const buyer2 = await staff('buyer-returns', buyerRole);

  if (!storeman || !buyer2) {
    console.log('  -  roles not seeded; the return boundary was not exercised');
  } else {
    const anyBill = (await owner('GET', q('/purchasing/bills'))).json?.data?.[0];

    // Reading a claim is `purchasing.view`, which they have.
    expect(
      'store manager: GET /purchasing/returns',
      (await storeman.call('GET', q('/purchasing/returns'))).status,
      200,
    );

    // Raising one is not.
    expect(
      'store manager: POST /purchasing/returns',
      (
        await storeman.call('POST', q('/purchasing/returns'), {
          uuid: crypto.randomUUID(),
          bill_id: anyBill?.id ?? '00000000-0000-0000-0000-000000000000',
          reason: 'verify:rbac',
          lines: [],
        })
      ).status,
      403,
    );

    // The buyer may. A 403 here would mean the permission reached nobody.
    const buyerTry = await buyer2.call('POST', q('/purchasing/returns'), {
      uuid: crypto.randomUUID(),
      bill_id: anyBill?.id ?? '00000000-0000-0000-0000-000000000000',
      reason: 'verify:rbac',
      lines: [],
    });
    if (buyerTry.status === 403) {
      bad('purchase manager: POST /purchasing/returns -> 403, and they hold the verb');
    } else {
      // 400 is the right answer to a claim with no lines on it, and it proves
      // the request got past the permission check.
      ok(`purchase manager: POST /purchasing/returns -> ${buyerTry.status}, past the gate`);
    }
  }
}

// --- reading the ledger is not writing into it ---------------------------
//
// `accounting.create` is what 0101 calls "Write a journal entry by hand" and
// describes as posting "straight to the ledger, past every other screen". The
// Auditor is the case it exists for: they read everything, reconcile the bank,
// and cannot post -- because somebody who could correct the books they are
// checking is not checking them.

console.log('\nWRITING INTO THE LEDGER BY HAND');
if (!auditor || !accountant) {
  console.log('  -  Auditor or Accountant not seeded; the ledger boundary was not exercised');
} else {
  const chart = (await owner('GET', q('/accounting/chart'))).json?.data ?? [];
  const postable = chart.filter((a) => a.is_postable && a.is_active);
  const entry = {
    uuid: crypto.randomUUID(),
    entry_date: new Date().toISOString().slice(0, 10),
    reason: 'verify:rbac',
    lines: [
      { account_id: postable[0]?.id, debit: '1.00' },
      { account_id: postable[1]?.id, credit: '1.00' },
    ],
  };

  // The chart and the register are readable by anybody who may read the books.
  for (const [label, path] of [
    ['GET /accounting/chart', q('/accounting/chart')],
    ['GET /accounting/journals', q('/accounting/journals')],
  ]) {
    expect(`auditor: ${label}`, (await auditor.call('GET', path)).status, 200);
  }

  // Writing one is not.
  expect(
    'auditor: POST /accounting/journals',
    (await auditor.call('POST', q('/accounting/journals'), entry)).status,
    403,
  );

  // And a branch manager cannot even read the chart: 0005 says they cannot
  // "see bank ledgers or true net profit", and the seed holds no accounting.*
  // for them at all.
  if (branch) {
    expect(
      'branch manager: GET /accounting/chart',
      (await branch.call('GET', q('/accounting/chart'))).status,
      403,
    );
  }

  // The Accountant is the person the screen is for.
  const theirs = await accountant.call('POST', q('/accounting/journals'), {
    ...entry,
    uuid: crypto.randomUUID(),
  });
  if (theirs.status === 403) {
    bad('accountant: POST /accounting/journals -> 403, and they hold accounting.create');
  } else {
    ok(`accountant: POST /accounting/journals -> ${theirs.status}, past the gate`);
  }
}

// --- A6.2: rostering a branch is not learning what it is paid -----------
//
// A Store Manager holds `hr.view` and `hr.manage` and NOT `hr.view_pay`. They
// roster their branch, record who was in, grant a day off — and never see a
// salary. A6.2 puts it as staff being blockable from "other employees'
// salaries", and the seed is deliberate: the Store Manager row in 0091 carries
// two HR permissions and stops.
//
// What makes this worth proving on a live server rather than trusting: the
// boundary is enforced by OMITTING the pay fields from the payload, not by a
// screen choosing not to render them. A regression would not 500 and would not
// 403. It would quietly start sending every salary in the business to somebody
// who may not see one, and every screen would carry on working.
//
// So this asserts absence, field by field, on a payload that came back 200.

console.log('\nROSTERING A BRANCH IS NOT SEEING WHAT IT PAYS');
const manager = await staff('storemgr', roleNamed(/Store Manager/));
const hr = await staff('hr', roleNamed(/HR Manager/));

if (!manager) {
  console.log('  -  no Store Manager role seeded; the pay boundary was not exercised');
} else {
  // The directory is theirs: they cannot roster people they cannot see.
  const directory = await manager.call('GET', q('/employees'));
  expect('store manager: GET /employees', directory.status, 200);

  const rows = directory.json?.data ?? [];
  if (rows.length === 0) {
    console.log('  -  nobody is employed; the omitted-pay check had no row to read');
  } else {
    const PAY = [
      'basic_salary',
      'housing_allowance',
      'transport_allowance',
      'other_allowance',
    ];
    const leaked = [];
    for (const row of rows) {
      for (const field of PAY) {
        // `in`, not a truthiness test. A salary of "0.00" is a real answer for
        // a commission-only salesperson, and it must be absent here rather
        // than present and zero — a zero would be believed.
        if (field in row) leaked.push(`${row.full_name}.${field}`);
      }
    }
    if (leaked.length > 0) {
      bad(
        `store manager sees pay they may not: ${leaked.slice(0, 4).join(', ')}` +
          (leaked.length > 4 ? ` and ${leaked.length - 4} more` : ''),
      );
    } else {
      ok(`store manager: no pay field on any of ${rows.length} staff records`);
    }

    // One record, the same rule. The screen reads this route too.
    const one = await manager.call('GET', q(`/employees/${rows[0].id}`));
    expect('store manager: GET /employees/{id}', one.status, 200);
    if (one.status === 200 && 'basic_salary' in (one.json ?? {})) {
      bad('store manager: reading one employee returns their salary');
    } else if (one.status === 200) {
      ok('store manager: one employee, still no salary');
    }
  }

  // Rostering IS theirs. A boundary that also took away the job would be a
  // different bug, so the permitted side is proved as well as the refused one.
  expect('store manager: GET /attendance', (await manager.call('GET', q('/attendance'))).status, 200);
  expect('store manager: GET /leave', (await manager.call('GET', q('/leave'))).status, 200);

  // Money is not. Advances are behind hr.view_pay and payroll behind its own.
  expect('store manager: GET /advances', (await manager.call('GET', q('/advances'))).status, 403);
  expect('store manager: GET /payroll', (await manager.call('GET', q('/payroll'))).status, 403);
  expect('store manager: GET /eosb', (await manager.call('GET', q('/eosb'))).status, 403);
}

if (!hr) {
  console.log('  -  no HR Manager role seeded; the other side was not exercised');
} else {
  // The same route, a caller who may. Proving the omission is the PERMISSION
  // and not the route having quietly stopped sending pay at all.
  const theirs = await hr.call('GET', q('/employees'));
  expect('hr manager: GET /employees', theirs.status, 200);
  const row = theirs.json?.data?.[0];
  if (!row) {
    console.log('  -  nobody is employed; the visible-pay check had no row to read');
  } else if ('basic_salary' in row) {
    ok('hr manager: the same route carries pay for somebody holding hr.view_pay');
  } else {
    bad('hr manager holds hr.view_pay and got no pay fields; the route may have stopped sending them');
  }

  // C6 splits preparing from approving, and the seed gives HR the first only:
  // computing a month is a calculation, committing the business to those
  // figures is somebody else's signature.
  expect('hr manager: GET /payroll', (await hr.call('GET', q('/payroll'))).status, 200);
  let runs = (await hr.call('GET', q('/payroll'))).json?.data ?? [];
  let draft = runs.find((r) => r.status === 'draft');
  if (!draft) {
    // Compute one to try approving. This script already seeds staff and awards
    // an RFQ; a draft payroll posts nothing, which is the whole point of it
    // being a draft, so it is the cheapest thing here to create.
    const back = new Date();
    back.setMonth(back.getMonth() - 3);
    const period = `${back.getFullYear()}-${String(back.getMonth() + 1).padStart(2, '0')}`;
    const made = await owner('POST', q('/payroll'), { period });
    if (made.status === 201) draft = made.json;
  }
  if (draft) {
    expect(
      'hr manager: POST /payroll/{id}/approve',
      (await hr.call('POST', q(`/payroll/${draft.id}/approve`), {})).status,
      403,
    );
  } else {
    console.log('  -  no draft run to try approving; the approval split was not exercised');
  }
}

// --- A6.2: delegation must not become escalation -------------------------
//
// `identity.manage_roles` is the most powerful permission the product has:
// anybody holding it can put into a role anything they hold themselves. The
// subset rule is the only thing standing between that and "anybody who can
// build a role can build themselves an Owner".
//
// It is worth proving live because the failure would be silent and total: a
// Branch Manager builds a role called "Evening cover", quietly ticks
// `accounting.close_period` and `hr.view_pay`, assigns it to themselves, and
// the business has no owner-only anything any more. Nothing would 500. Nothing
// would look wrong.
//
// The screen mirrors the same rule so the box is drawn disabled rather than
// ticked and refused. The two must agree, and where they disagree the server
// wins — which is why this asserts the server.

console.log('\nBUILDING A ROLE CANNOT BUILD YOURSELF A BIGGER ONE');
const builder = await staff('rolebuilder', roleNamed(/HR Manager/));

if (!builder) {
  console.log('  -  no HR Manager seeded; the escalation boundary was not exercised');
} else {
  // They hold hr.view_pay and payroll.run. They do not hold identity.manage_roles
  // at all, so the whole builder is closed to them: the narrowest and most
  // important case, since a role builder open to everybody needs no subset rule.
  expect(
    'hr manager: GET /permissions',
    (await builder.call('GET', q('/permissions'))).status,
    403,
  );
  expect(
    'hr manager: POST /roles',
    (await builder.call('POST', q('/roles'), {
      name: 'Should never exist',
      permissions: ['hr.view'],
    })).status,
    403,
  );
}

// The other half, and the one the subset rule is actually for: somebody who
// CAN build roles, trying to put in something they do not hold.
const catalogue = (await owner('GET', q('/permissions'))).json?.data ?? [];
const heldByOwner = new Set(catalogue.filter((p) => p.holds).map((p) => p.permission));
if (catalogue.length === 0) {
  console.log('  -  the permission catalogue is empty; the subset rule was not exercised');
} else {
  ok(`the owner may grant ${heldByOwner.size} of ${catalogue.length} permissions`);

  // A permission that does not exist stands in for one the caller does not
  // hold: the owner holds everything, and the rule is the same test either way
  // -- "is this in the set I hold" -- so an unknown name exercises it exactly.
  const refused = await owner('POST', q('/roles'), {
    name: 'verify:rbac escalation probe',
    permissions: ['sales.view', 'not.a.real.permission'],
  });
  expect('a role holding something the caller does not', refused.status, 403);
  if (refused.status === 403) {
    const said = refused.json?.error?.message ?? '';
    if (said.includes('not.a.real.permission')) {
      ok('and the refusal names which permission stopped it');
    } else {
      bad(`the refusal does not name what was refused: ${said}`);
    }
  }

  // Building one out of what the caller does hold works, is readable, and can
  // be taken away again. A subset rule that refused everything would pass the
  // check above and break the feature.
  const made = await owner('POST', q('/roles'), {
    name: 'verify:rbac evening cover',
    description: 'Created by verify:rbac and removed at the end of it',
    permissions: ['sales.view', 'catalog.view'],
  });
  if (made.status !== 200 && made.status !== 201) {
    bad(`building a role out of held permissions answered ${made.status}`);
  } else {
    const id = made.json?.role?.id;
    ok(`built a role out of held permissions -> ${made.status}`);

    // It has to appear on the assignable list, marked as the tenant's own and
    // held by nobody -- the two fields the roles screen draws its buttons from.
    const listed = ((await owner('GET', q('/people/roles'))).json?.data ?? [])
      .find((r) => r.id === id);
    if (!listed) {
      bad('a role that was just built is not on the assignable list');
    } else if (listed.is_system) {
      bad('a role the business built is marked as the product’s own');
    } else if (listed.in_use !== 0) {
      bad(`a brand new role reports ${listed.in_use} holders`);
    } else {
      ok('it is listed as the business’s own, held by nobody');
    }

    // And a built-in refuses in a way that says what is true. It answered
    // "That role was not found" for a role in the list the caller just read.
    const shipped = ((await owner('GET', q('/people/roles'))).json?.data ?? [])
      .find((r) => r.is_system);
    if (shipped) {
      const gone = await owner('DELETE', q(`/roles/${shipped.id}`));
      if (gone.status === 404) {
        bad(
          'removing a built-in role reports it missing, and it is in the list ' +
            'the caller just read',
        );
      } else {
        expect('removing a built-in role', gone.status, 403);
      }
    }

    if (id) {
      expect('removing one the business built', (await owner('DELETE', q(`/roles/${id}`))).status, 204);
    }
  }
}

// --- binding the business's tax identity is not reading a certificate ----
//
// `einvoicing.view` is the status: a store manager whose till has stopped
// selling needs to see that its certificate expired, or they will spend the
// morning restarting hardware. `einvoicing.onboard` is what exchanges a
// one-time password for a credential, and it binds the BUSINESS's tax identity
// — so the seeded roles give it to the Owner and to nobody else.
//
// The screen offers the onboarding controls only where the second is held, and
// the status panel to everybody who holds the first. This proves the server
// agrees, because a screen is not a boundary.

console.log('\nSEEING A CERTIFICATE IS NOT ASKING FOR ONE');
const manager2 = await staff('einvoicing', roleNamed(/Branch/));
const unit = (await owner('GET', q('/einvoicing/units'))).json?.data?.[0];

if (!manager2 || !unit) {
  console.log('  -  no Branch Manager or no signing unit; that split was not exercised');
} else {
  expect(
    'branch manager: GET /einvoicing/units',
    (await manager2.call('GET', q('/einvoicing/units'))).status,
    200,
  );
  expect(
    'branch manager: GET onboarding status',
    (await manager2.call(
      'GET',
      q(`/einvoicing/units/${unit.id}/onboarding?environment=sandbox`),
    )).status,
    200,
  );

  // And not the act itself. A one-time password spent by the wrong person is
  // spent: the taxpayer has to fetch another from the portal.
  expect(
    'branch manager: POST onboarding/compliance',
    (await manager2.call('POST', q(`/einvoicing/units/${unit.id}/onboarding/compliance`), {
      environment: 'sandbox',
      csr: 'not-a-real-csr',
      otp: '000000',
    })).status,
    403,
  );
  expect(
    'branch manager: POST onboarding/production',
    (await manager2.call('POST', q(`/einvoicing/units/${unit.id}/onboarding/production`), {
      environment: 'sandbox',
    })).status,
    403,
  );
  expect(
    'branch manager: PUT /einvoicing/units/{id}',
    (await manager2.call('PUT', q(`/einvoicing/units/${unit.id}`), {
      label: 'renamed by verify:rbac',
    })).status,
    403,
  );
}

// --- a rate is recorded by whoever writes the books ----------------------
//
// Reading an exchange rate is `accounting.view`, because a rate explains a
// figure on a report. Entering one is `accounting.create`, the same verb that
// writes a journal by hand — because the rate decides what every
// foreign-currency document is worth in the book.

console.log('\nRECORDING AN EXCHANGE RATE IS WRITING IN THE BOOKS');
const reader = await staff('fxreader', roleNamed(/Auditor/));
if (!reader) {
  console.log('  -  no Auditor seeded; the exchange-rate split was not exercised');
} else {
  expect(
    'auditor: GET /exchange-rates',
    (await reader.call('GET', q('/exchange-rates'))).status,
    200,
  );
  expect(
    'auditor: PUT /exchange-rates',
    (await reader.call('PUT', q('/exchange-rates'), {
      from_currency: 'EUR',
      to_currency: 'SAR',
      rate: '4.10',
      as_of: new Date().toISOString().slice(0, 10),
      source: 'verify:rbac',
    })).status,
    403,
  );
}

// --- the compliance dashboard is its own permission ----------------------
//
// `compliance.view` rather than a reuse of accounting or identity: E7's
// readings span tax, privacy, payroll and the archive, and nobody who holds one
// of those necessarily holds the rest.

console.log('\nTHE COMPLIANCE DASHBOARD IS ITS OWN PERMISSION');
const cashier2 = await staff('cashier-compliance', cashierRole);
if (!cashier2) {
  console.log('  -  no Cashier seeded; the compliance boundary was not exercised');
} else {
  expect(
    'cashier: GET /compliance',
    (await cashier2.call('GET', q('/compliance'))).status,
    403,
  );
}

// --- reading figures, keeping them, and sending them out are three things -
//
// `report.view` reads a report on a screen. `report.save` keeps one and gives
// it a schedule, which emails figures out of the building every week without
// anybody signing in. `report.export` takes one away as a file.
//
// The seeded Branch Manager holds the first and neither of the others, which
// is the split worth proving: a shop floor manager reads their branch's numbers
// all day and does not get to set up a standing email of them.

console.log('\nREADING FIGURES IS NOT SENDING THEM OUT');
const branchReports = await staff('reports', roleNamed(/Branch/));
if (!branchReports) {
  console.log('  -  no Branch Manager seeded; the report split was not exercised');
} else {
  expect(
    'branch manager: GET /analytics/kpis',
    (await branchReports.call('GET', q('/analytics/kpis'))).status,
    200,
  );
  expect(
    'branch manager: GET /reports/saved',
    (await branchReports.call('GET', q('/reports/saved'))).status,
    200,
  );
  expect(
    'branch manager: PUT /reports/saved',
    (await branchReports.call('PUT', q('/reports/saved'), {
      name: 'verify:rbac should not exist',
      kind: 'sales',
      period: 'last_month',
    })).status,
    403,
  );
  expect(
    'branch manager: GET /reports/sales/export',
    (await branchReports.call('GET', q('/reports/sales/export'))).status,
    403,
  );
}

// --- a cashier counts the drawer; they do not read the target ------------
//
// The blind close asks a cashier to count without being told what the system
// expects, because the difference is the only signal it produces. A cashier who
// can read the expected figure can make tonight's drawer agree with it, and
// then every variance is zero and the practice means nothing.
//
// So the shift REGISTER and the X report are `report.view`, while the till
// itself is `sales.receive_payment`. This is the boundary the whole design of
// the close rests on.

console.log('\nCOUNTING A DRAWER IS NOT READING THE TARGET');
const tillHand = await staff('cashier-shifts', cashierRole);
if (!tillHand) {
  console.log('  -  no Cashier seeded; the blind-close boundary was not exercised');
} else {
  expect(
    'cashier: GET /shifts',
    (await tillHand.call('GET', q('/shifts'))).status,
    403,
  );
}

// --- what the shop owes is not what a shop assistant gives away ----------
//
// Store credit and points are liabilities. Reading them is `wallet.view` and
// `loyalty.view`, which a Cashier holds because a customer at the counter asks
// what is left on their card. Changing them is the `manage` half: putting
// credit on a wallet is giving money away, and expiring points reduces a
// liability somebody could later be asked to explain.
//
// The seed gives `manage` to the Owner, the Store Manager and Customer Service
// — the people who actually settle a complaint — and withholds it from the
// Cashier, the Sales Executive and the Online Manager. So the Cashier is the
// case worth proving: they see the balance and cannot move it.

console.log('\nREADING A BALANCE IS NOT GIVING CREDIT AWAY');
const tillCredit = await staff('cashier-credit', cashierRole);
const someCustomer = (await owner('GET', q('/customers'))).json?.data?.[0];
if (!tillCredit || !someCustomer) {
  console.log('  -  no Cashier or no customers; the credit boundary was not exercised');
} else {
  expect(
    'cashier: GET /wallets/{id}',
    (await tillCredit.call('GET', q(`/wallets/${someCustomer.id}`))).status,
    200,
  );
  expect(
    'cashier: POST /wallets/{id}/credit',
    (await tillCredit.call('POST', q(`/wallets/${someCustomer.id}/credit`), {
      amount: '10.00',
      reason: 'verify:rbac',
    })).status,
    403,
  );
  expect(
    'cashier: POST /loyalty/expire',
    (await tillCredit.call('POST', q('/loyalty/expire'), {})).status,
    403,
  );
  expect(
    'cashier: POST /promotions',
    (await tillCredit.call('POST', q('/promotions'), {
      code: 'VERIFY',
      name: 'verify:rbac',
      kind: 'percentage',
      value: '10',
    })).status,
    403,
  );
}

await retireSeeded();

console.log(
  `\n${failures === 0 ? 'EVERY BOUNDARY HELD' : `${failures} BOUNDARIES DID NOT HOLD`}`,
);
process.exit(failures === 0 ? 0 : 1);
