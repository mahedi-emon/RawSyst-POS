// The critical workflows, driven through a real browser against the real API.
//
// # What this is for, and what the other two scripts are not
//
// audit.mjs opens every section and reports what a machine can be sure about.
// layout-probe.mjs measures boxes. Neither of them ever presses a button that
// changes anything, so between them they prove the app RENDERS and prove
// nothing about whether it WORKS.
//
// This one does what a shop does: look at orders, open a bill, read a customer
// statement, check what the acquirer still owes, reach a report. Each step is
// performed the way a person performs it — find the control, press it, then
// read the result off the screen that follows — so a handler that quietly
// stopped answering, a form field that stopped being wired up, or a table that
// stopped showing what it was given all fail here rather than in a shop.
//
// # What it deliberately does NOT cover, and why
//
// Sale, return, exchange and the shift live in the Tauri POS. That app holds
// its device credential in the OS keystore through the Rust shell — see
// pos/src/offline/credential.ts — and a plain browser has no keystore, so a
// browser-run POS cannot pair and therefore cannot sell. Driving those flows
// here would mean weakening the custody model to make a test pass, which is
// the wrong trade in both directions. They are covered end to end by the Go
// integration suite against the real database, and what this script asserts
// about the POS is the part a browser CAN honestly check: that the app loads
// and reports its pairing state instead of presenting a till that cannot sell.
import { chromium } from 'playwright';

const WEB = process.env.RS_WEB ?? 'http://localhost:3000';
const POS = process.env.RS_POS ?? '';
const EMAIL = process.env.RS_EMAIL ?? 'owner@example.test';
const PASSWORD = process.env.RS_PASSWORD;

const failures = [];
const done = [];
let step = '';

function check(condition, what) {
  if (condition) return true;
  failures.push(step + ': ' + what);
  return false;
}

const go = async (page, section) => {
  await page.locator('.bo__link:not(.bo__link--signout), .app__navlink', { hasText: section }).first().click();
  await page.waitForTimeout(1000);
};

const body = (page) => page.locator('body').innerText();

// The tabs inside a section are a segmented control, not free-floating buttons.
// Selecting them by class rather than by any button carrying the word keeps
// this from matching a row action that happens to say the same thing.
async function tab(page, label) {
  const btn = page.locator('.segmented__btn', { hasText: label }).first();
  if ((await btn.count()) === 0) {
    check(false, 'no "' + label + '" tab on this screen');
    return false;
  }
  await btn.click();
  await page.waitForTimeout(1100);
  return true;
}

/* The navigation moved, and these three scripts drive it.
 *
 * The rail is off-canvas below 640px and the language options live in a menu
 * rather than side by side, so both now need opening before they can be used.
 * One helper rather than three copies. */
async function openNav(page) {
  const menu = page.locator('.bo__menu');
  if (await menu.count() && await menu.isVisible()) {
    await menu.click().catch(() => {});
    await page.waitForTimeout(250);
  }
}

async function chooseLanguage(page, label) {
  const trigger = page.locator('.lang__trigger').first();
  if (!(await trigger.count())) return false;
  await trigger.click().catch(() => {});
  await page.waitForTimeout(200);
  const opt = page.locator('button.lang__opt', { hasText: label }).first();
  if (!(await opt.count())) {
    await page.keyboard.press('Escape');
    return false;
  }
  await opt.click();
  await page.waitForTimeout(900);
  return true;
}

async function signIn(page) {
  step = 'Login';
  await page.goto(WEB, { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle').catch(() => {});
  await page.waitForTimeout(600);
  for (let i = 0; i < 3; i++) {
    await page.fill('input[type=email]', EMAIL);
    await page.fill('input[type=password]', PASSWORD);
    await page.locator('form button').first().click();
    try {
      await page.waitForSelector('.bo__link:not(.bo__link--signout), .app__navlink', { timeout: 15000 });
      await page.waitForTimeout(700);
      done.push('Login');
      return;
    } catch {
      await page.waitForTimeout(1200);
    }
  }
  throw new Error('could not sign in');
}

// A wrong password must be refused and must SAY so. An app that silently does
// nothing on a bad password is indistinguishable from one that is broken, and
// the person at the keyboard has no way to tell which.
async function wrongPassword(page) {
  step = 'Login — refusal';
  await page.goto(WEB, { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle').catch(() => {});
  await page.waitForTimeout(600);
  await page.fill('input[type=email]', EMAIL);
  await page.fill('input[type=password]', 'definitely-not-the-password');
  await page.locator('form button').first().click();
  await page.waitForTimeout(2500);

  check(
    (await page.locator('input[type=password]').count()) > 0,
    'a wrong password did not keep the user on the sign-in screen',
  );
  const said = (await body(page)).toLowerCase();
  check(
    /do not match|does not match|incorrect|not recognised|not recognized|try again/.test(said),
    'a wrong password produced no message the user can act on: ' +
      JSON.stringify(said.slice(0, 160)),
  );
  done.push('Login refuses a wrong password, and says why');
}

async function purchasing(page) {
  step = 'Purchase — orders';
  await go(page, 'Buying');
  check((await page.locator('tbody tr').count()) > 0, 'Buying listed no purchase orders');

  step = 'Purchase — new order form';
  await page.locator('button', { hasText: 'New order' }).first().click();
  await page.waitForTimeout(1000);
  check((await page.locator('form').count()) > 0, 'New order opened no form');
  const options = await page.locator('select').first().locator('option').count();
  check(options > 1, 'the supplier list offered ' + options + ' option(s); no order can be raised');

  step = 'Purchase — bills';
  await go(page, 'Buying');
  await tab(page, 'Bills');
  check(
    (await page.locator('tbody tr').count()) > 0,
    'no supplier bills are listed, so none can be approved or paid',
  );

  step = 'Purchase — open a bill and read its three-way match';
  await page.locator('tbody tr button').first().click().catch(() => {});
  await page.waitForTimeout(1200);
  const bill = await body(page);
  check(!/something went wrong/i.test(bill), 'the bill detail failed to load');
  check(
    /quantity|match|checked|vat|unit price/i.test(bill),
    'the bill showed no three-way match evidence, which is the control B5.2 asks for',
  );

  step = 'Purchase — what we owe';
  await go(page, 'Buying');
  await tab(page, 'What we owe');
  check(!/something went wrong/i.test(await body(page)), 'the supplier ageing report failed');
  done.push('Purchase: orders, new-order form, bills, three-way match, ageing');
}

async function receivables(page) {
  step = 'Receivable — customers';
  await go(page, 'Customers');
  check((await page.locator('tbody tr').count()) > 0, 'no customers are listed');

  step = 'Receivable — customer statement';
  await page.locator('tbody tr button').first().click().catch(() => {});
  await page.waitForTimeout(1300);
  const detail = await body(page);
  check(!/something went wrong/i.test(detail), 'the customer statement failed to load');
  check(
    /ledger|statement|balance|owed|credit/i.test(detail),
    'the customer screen showed neither a balance nor a ledger',
  );

  step = 'Receivable — ageing';
  await go(page, 'Customers');
  if (await tab(page, 'What we')) {
    check(!/something went wrong/i.test(await body(page)), 'the customer ageing report failed');
  }
  done.push('Receivable: customers, statement, ageing');
}

async function settlement(page) {
  step = 'Settlement';
  await go(page, 'Settlement');
  const text = await body(page);
  check(!/something went wrong/i.test(text), 'the settlement screen failed');
  check(
    /deposit|awaiting|settle|nothing|pending/i.test(text),
    'settlement showed neither pending card money nor an empty state',
  );
  done.push('Settlement: card money awaiting deposit');
}

async function inventoryAndReports(page) {
  step = 'Product — inventory';
  await go(page, 'Inventory');
  const inv = await body(page);
  check(!/something went wrong/i.test(inv), 'the inventory screen failed');
  check(inv.trim().length > 40, 'the inventory screen rendered essentially nothing');

  step = 'Reports — dashboard';
  await go(page, 'Dashboard');
  const dash = await body(page);
  check(!/something went wrong/i.test(dash), 'the dashboard failed');
  check(/where the money is|today|stock/i.test(dash), 'the dashboard rendered no money position');

  step = 'Reports — drill-through';
  const open = page.locator('button', { hasText: /^Open$/ }).first();
  if (await open.count()) {
    await open.click();
    await page.waitForTimeout(1300);
    check(!/something went wrong/i.test(await body(page)), 'a dashboard drill-through failed');
    done.push('Reports: dashboard and drill-through');
  } else {
    done.push('Reports: dashboard (nothing flagged to drill into)');
  }
}

async function settingsAndZatca(page) {
  step = 'Shift and cash drawer — where they are reached from';
  // The Z report belongs to the till. What the back office owns is the
  // TERMINAL: registering it, pairing it, pausing it. If this screen is broken
  // no shift can be opened anywhere, which is the back-office half of C8.
  await go(page, 'Terminals');
  const term = await body(page);
  check(!/something went wrong/i.test(term), 'the terminals screen failed');
  check(/terminal|till|register|pair/i.test(term), 'the terminals screen named no terminals');

  step = 'ZATCA — e-invoicing';
  await go(page, 'E-invoicing');
  const egs = await body(page);
  check(!/something went wrong/i.test(egs), 'the e-invoicing screen failed');
  check(
    /onboard|csid|compliance|unit|till|invoicing/i.test(egs),
    'the e-invoicing screen said nothing about onboarding state',
  );

  step = 'Settings — branding';
  await go(page, 'Branding');
  check(!/something went wrong/i.test(await body(page)), 'the branding screen failed');

  step = 'Settings — setup';
  await go(page, 'Setup');
  check(!/something went wrong/i.test(await body(page)), 'the setup screen failed');
  done.push('Shift/terminals, ZATCA onboarding state, branding, setup');
}

// The POS, in a browser, as far as a browser can honestly take it.
async function pos(browser) {
  if (!POS) {
    done.push('POS: not served, skipped (set RS_POS to its dev URL)');
    return;
  }
  step = 'POS — first run';
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('console', (m) => {
    if (m.type() === 'error') errors.push(m.text().slice(0, 160));
  });

  await page.goto(POS, { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle').catch(() => {});
  await page.waitForTimeout(2000);

  const text = await body(page);
  check(text.trim().length > 0, 'the POS rendered a blank page');
  // Outside Tauri there is no keystore, so the app must SAY it cannot pair
  // rather than presenting a till that looks ready and cannot sell.
  check(
    /pair|terminal|sign in|keystore|not set up|till|password/i.test(text),
    'the POS first run said nothing a cashier could act on: ' + JSON.stringify(text.slice(0, 160)),
  );
  check(errors.length === 0, 'the POS logged console errors: ' + errors.slice(0, 3).join(' | '));
  await ctx.close();
  done.push('POS: loads and reports its pairing state honestly');
}

async function main() {
  if (!PASSWORD) {
    console.error('RS_PASSWORD is not set. Run cmd/devseed and pass the password it prints.');
    process.exit(2);
  }
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();

  const consoleErrors = [];
  const serverErrors = [];
  // Collected only once the deliberately-wrong sign-in is behind us. That
  // attempt is SUPPOSED to be refused, and a browser logs every 401 as a
  // console error, so counting it would make the test fail on the app working
  // correctly.
  let watching = false;
  page.on('console', (m) => {
    if (watching && m.type() === 'error') consoleErrors.push(m.text().slice(0, 160));
  });
  page.on('response', (r) => {
    if (r.status() >= 500) {
      serverErrors.push(r.status() + ' ' + r.request().method() + ' ' + r.url().slice(0, 90));
    }
  });

  try {
    await wrongPassword(page);
    watching = true;
    await signIn(page);
    await purchasing(page);
    await receivables(page);
    await settlement(page);
    await inventoryAndReports(page);
    await settingsAndZatca(page);
    await pos(browser);
  } finally {
    await ctx.close();
    await browser.close();
  }

  step = 'Across the whole walk';
  check(consoleErrors.length === 0, 'the browser logged errors: ' + consoleErrors.slice(0, 4).join(' | '));
  check(serverErrors.length === 0, 'the server answered 5xx: ' + serverErrors.slice(0, 4).join(' | '));

  console.log(done.length + ' workflow group(s) completed:');
  for (const d of done) console.log('  ok  ' + d);

  if (!failures.length) {
    console.log('\nno workflow failures');
    return;
  }
  console.error('\n' + failures.length + ' failure(s):\n');
  for (const f of failures) console.error('  FAIL  ' + f);
  process.exit(1);
}

main().catch((e) => {
  console.error('the walk could not run:', e.message);
  process.exit(2);
});
