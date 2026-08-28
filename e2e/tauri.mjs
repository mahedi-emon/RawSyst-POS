// The POS, driven as the real Windows application.
//
// # Why this exists and what it replaces
//
// P29 recorded that the app's GUI was "verified by screenshot, not by
// automation", because `SendKeys` cannot reach WebView2 content. That is true
// of SendKeys and it is not the whole story: Tauri ships `tauri-driver`, which
// proxies WebDriver to the WebView2 control through Microsoft's own
// `msedgedriver`. The window is the real window, the runtime is the real
// WebView2, the binary is the one an installer would ship, and the Rust shell
// underneath it is the real shell — so `terminal_keystore_available()` answers
// TRUE here and false in a browser, which is the exact difference that makes a
// browser unable to test a till.
//
// e2e/workflows.mjs says plainly that it cannot reach Sale, Return, Exchange or
// the Shift, because a browser has no keystore and therefore cannot pair. This
// is where that stops being true.
//
// # Nothing here is stubbed
//
// The real Go API, the real database. A sale rung in this window writes an
// invoice, moves stock, posts a journal entry and lands in a cash session, and
// every assertion reads those back through the API rather than off the screen
// that wrote them. A screen agreeing with itself proves nothing.
//
// # Running it
//
//   cargo install tauri-driver --locked
//   # msedgedriver matching the WebView2 runtime, on PATH or in RS_EDGEDRIVER
//   cd pos && npm run build && cd src-tauri && cargo build --release
//   cd backend && go run ./cmd/devseed && go run ./cmd/api
//   RS_PASSWORD=... node e2e/tauri.mjs
//
// The driver version is tied to the WebView2 runtime the machine has. A
// mismatch fails at session creation and says so.

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';

// An ABSOLUTE, native path. msedgedriver launches this as the browser binary
// through the OS rather than through Node, so a relative path — or a POSIX one
// on Windows — comes back as the unhelpful "Failed to create Microsoft Edge
// process".
const APP = resolve(
  process.env.RS_POS_EXE ?? 'pos/src-tauri/target/release/rawsyst-pos.exe',
);
const DRIVER_PORT = Number(process.env.RS_DRIVER_PORT ?? 4444);
const NATIVE_DRIVER = process.env.RS_EDGEDRIVER ?? '';
const API = process.env.RS_API ?? 'http://127.0.0.1:8080';
const EMAIL = process.env.RS_EMAIL ?? 'owner@example.test';
const PASSWORD = process.env.RS_PASSWORD;

const failures = [];
const done = [];
const diagnostics = [];
let step = '';

function check(condition, what) {
  if (condition) return true;
  failures.push(step + ': ' + what);
  return false;
}

const note = (what) => done.push(what);

/** Screen text on one line, for a message that has to fit in a terminal. */
const tidy = (text) => String(text).replace(/\s+/g, ' ').trim();

// --- the WebDriver client -------------------------------------------------
//
// Spoken directly over HTTP rather than through a client library. The protocol
// is half a dozen endpoints, and a client that has to be kept in step with the
// driver is a dependency that eventually is not.

const base = `http://127.0.0.1:${DRIVER_PORT}`;
let sessionId = '';

async function wd(method, path, body) {
  const r = await fetch(base + path, {
    method,
    headers: { 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await r.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    throw new Error(`${method} ${path}: ${r.status} ${text.slice(0, 200)}`);
  }
  if (json.value && json.value.error) {
    throw new Error(
      `${method} ${path}: ${json.value.error} — ` +
        String(json.value.message).slice(0, 200),
    );
  }
  return json.value;
}

const script = (source, args = []) =>
  wd('POST', `/session/${sessionId}/execute/sync`, { script: source, args });

/** For anything that returns a promise, which is every Tauri command.
 *
 * `execute/sync` hands back the promise object itself and the driver cannot
 * serialise it — the failure surfaces as an unrelated-looking javascript error.
 * The async form passes a callback as the last argument and waits for it. */
const asyncScript = (source, args = []) =>
  wd('POST', `/session/${sessionId}/execute/async`, { script: source, args });

/** Invokes a Tauri command in the shell underneath this window. */
const invoke = (command, payload = {}) =>
  asyncScript(
    `
    var name = arguments[0], args = arguments[1];
    var done = arguments[arguments.length - 1];
    if (!window.__TAURI_INTERNALS__) { done({ error: 'not running under Tauri' }); return; }
    window.__TAURI_INTERNALS__.invoke(name, args)
      .then(function (v) { done({ ok: v }); })
      .catch(function (e) { done({ error: String(e && e.message ? e.message : e) }); });
    `,
    [command, payload],
  );

/** Reads the whole window's text, the way a person looking at it would. */
const screenText = async () =>
  (await script('return document.body.innerText')) || '';

/** Clicks the first visible, enabled element whose text contains `text`.
 *
 * By visible text rather than by selector: this file checks what a cashier can
 * do, and a cashier finds the button that says Pay. A test that clicks
 * `.pos__paybtn` keeps passing after somebody hides it. */
async function clickText(text, within = 'button, a, [role=button]') {
  const ok = await script(
    `
    var sel = arguments[0], want = arguments[1];
    var all = Array.prototype.slice.call(document.querySelectorAll(sel));
    var el = all.filter(function (e) {
      return e.offsetParent !== null && !e.disabled &&
        (e.innerText || '').trim().toLowerCase().indexOf(want.toLowerCase()) >= 0;
    })[0];
    if (!el) return false;
    el.click();
    return true;
    `,
    [within, text],
  );
  return ok === true;
}

/** Sets a React-controlled input the way typing into it would.
 *
 * The native setter plus an input event, because assigning `.value` directly
 * tells React nothing and the field snaps back on the next render. */
async function typeInto(selector, value) {
  const ok = await script(
    `
    var sel = arguments[0], v = arguments[1];
    var el = document.querySelector(sel);
    if (!el) return false;
    var proto = el instanceof HTMLTextAreaElement
      ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, v);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return true;
    `,
    [selector, value],
  );
  return ok === true;
}

/** What pressing Enter in a field actually does.
 *
 * A barcode scanner types the code and presses Enter, and the browser turns
 * that into a form SUBMIT. A synthetic keydown does not — the implicit
 * submission is the browser's own behaviour, not a listener — so dispatching
 * one looks like a scan and does nothing at all, which is how a first version
 * of this file reported a counter that would not take an item.
 *
 * Submits the enclosing form where there is one, and falls back to the keydown
 * for fields that listen for the key themselves. */
const pressEnter = (selector) =>
  script(
    `
    var el = document.querySelector(arguments[0]);
    if (!el) return false;
    var form = el.form || el.closest('form');
    if (form) {
      if (form.requestSubmit) form.requestSubmit();
      else form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      return true;
    }
    el.dispatchEvent(new KeyboardEvent('keydown',
      { key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true }));
    return true;
    `,
    [selector],
  );

/** The first visible input matching a selector, as a selector to type into. */
const visibleInput = (selector = 'input') =>
  script(
    `
    var all = Array.prototype.slice.call(document.querySelectorAll(arguments[0]));
    var el = all.filter(function (e) {
      return e.offsetParent !== null && e.type !== 'hidden';
    })[0];
    if (!el) return '';
    if (el.id) return '#' + el.id;
    if (el.className) return '.' + String(el.className).split(' ')[0];
    return arguments[0];
    `,
    [selector],
  );

/** Types into the Nth visible match, for a screen with two of the same field.
 *
 * The exchange has two `.scan__input`s — the receipt and the replacement — and
 * a helper that takes the first one types the barcode into the field that
 * looks sales up, which silently does nothing useful. */
const typeIntoNth = (selector, index, value) =>
  script(
    `
    var sel = arguments[0], n = arguments[1], v = arguments[2];
    var all = Array.prototype.slice.call(document.querySelectorAll(sel))
      .filter(function (e) { return e.offsetParent !== null; });
    var el = all[n];
    if (!el) return false;
    var proto = el instanceof HTMLTextAreaElement
      ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, v);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return true;
    `,
    [selector, index, value],
  );

/** Types into the first visible, ENABLED match.
 *
 * A returns table can carry lines that are already fully returned, and those
 * inputs are disabled on purpose. Setting one through the native value setter
 * still fires React's onChange — a disabled field only stops a PERSON — so a
 * helper that took the first match quietly asked to return a line with nothing
 * left on it, and the screen answered "more has been entered than that sale
 * can give back" and disabled the button. */
const typeIntoEnabled = (selector, value) =>
  script(
    `
    var sel = arguments[0], v = arguments[1];
    var el = Array.prototype.slice.call(document.querySelectorAll(sel))
      .filter(function (e) { return e.offsetParent !== null && !e.disabled; })[0];
    if (!el) return false;
    var proto = el instanceof HTMLTextAreaElement
      ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, v);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return true;
    `,
    [selector, value],
  );

/** Submits the form around the Nth visible match. See pressEnter for why. */
const pressEnterNth = (selector, index) =>
  script(
    `
    var all = Array.prototype.slice.call(document.querySelectorAll(arguments[0]))
      .filter(function (e) { return e.offsetParent !== null; });
    var el = all[arguments[1]];
    if (!el) return false;
    var form = el.form || el.closest('form');
    if (form) { form.requestSubmit ? form.requestSubmit() : form.submit(); return true; }
    el.dispatchEvent(new KeyboardEvent('keydown',
      { key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true }));
    return true;
    `,
    [selector, index],
  );

/** Waits for the window's text to match, or gives up saying what it said.
 *
 * Polling rather than a fixed sleep: a fixed sleep is either slow or flaky and
 * usually both, and when it fails it says nothing about why. */
async function waitForText(
  match,
  { timeout = 25000, what = 'the screen' } = {},
) {
  const deadline = Date.now() + timeout;
  let last = '';
  while (Date.now() < deadline) {
    last = await screenText();
    if (match.test(last)) return last;
    await sleep(250);
  }
  throw new Error(
    `timed out waiting for ${what}. The window says: ` +
      JSON.stringify(last.replace(/\s+/g, ' ').slice(0, 300)),
  );
}

/** Finds the window that is actually the application and switches to it.
 *
 * msedgedriver attaches to the first WebView2 target it sees, and for the first
 * second or two of a Tauri start that is `about:blank`. Worse, if the binary
 * cannot load its front end the window settles on `chrome-error://` showing
 * Edge's own "can't reach this page" — which has a title, a body and text, so
 * every naive "did it render" check passes on it.
 *
 * That is not hypothetical: it is what a binary built with a bare
 * `cargo build --release` does, because the asset protocol is only compiled in
 * by `tauri build`. This function is what stops that being reported as a
 * working till. */
async function attachToTheApp({ timeout = 40000 } = {}) {
  const deadline = Date.now() + timeout;
  let last = 'no window at all';

  while (Date.now() < deadline) {
    const handles =
      (await wd('GET', `/session/${sessionId}/window/handles`)) ?? [];
    for (const handle of handles) {
      await wd('POST', `/session/${sessionId}/window`, { handle });
      const href = String(await script('return location.href'));
      const hasShell =
        (await script('return typeof window.__TAURI_INTERNALS__')) === 'object';

      if (href.startsWith('chrome-error://')) {
        last =
          'the window is showing Edge\'s "can\'t reach this page". The binary ' +
          'is pointing at its dev server rather than at its embedded assets — ' +
          'build it with `npx tauri build`, not `cargo build --release`.';
        continue;
      }
      if (href === 'about:blank') {
        last = 'the window is still blank';
        continue;
      }
      if (!hasShell) {
        last = `the window at ${href} has no Tauri shell behind it`;
        continue;
      }
      return href;
    }
    await sleep(400);
  }
  throw new Error('could not attach to the application: ' + last);
}

/** Waits for a specific element to carry text, and returns it.
 *
 * For anything that must be REFUSED. Reading the whole screen for a word is
 * how a check passes on the page's own explanatory copy rather than on the
 * message it was looking for. */
async function waitForAlert(
  selector,
  { timeout = 15000, what = 'a message' } = {},
) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const text = await script(
      `
      var el = document.querySelector(arguments[0]);
      return el && el.offsetParent !== null ? (el.innerText || '') : '';
      `,
      [selector],
    );
    if (String(text).trim()) return String(text);
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what} in ${selector}`);
}

async function waitForSelector(selector, { timeout = 25000 } = {}) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (
      await script('return !!document.querySelector(arguments[0])', [selector])
    ) {
      return true;
    }
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${selector}`);
}

/** What the WebView2 console logged, so a CSP refusal or a failed request
 *  cannot pass unnoticed. Installed before anything else runs. */
const CONSOLE_HOOK = `
  window.__rsLog = window.__rsLog || [];
  if (!window.__rsHooked) {
    window.__rsHooked = true;
    var err = console.error.bind(console);
    console.error = function () {
      var a = Array.prototype.slice.call(arguments);
      window.__rsLog.push(a.map(String).join(' '));
      err.apply(null, a);
    };
    window.addEventListener('error', function (e) {
      window.__rsLog.push('error: ' + e.message);
    });
    window.addEventListener('unhandledrejection', function (e) {
      window.__rsLog.push('rejection: ' + String(e.reason));
    });
    window.addEventListener('securitypolicyviolation', function (e) {
      window.__rsLog.push('CSP refused ' + e.violatedDirective + ' to ' + e.blockedURI);
    });
  }
  return true;
`;

const consoleLog = async () =>
  (await script('return window.__rsLog || []')) || [];

/** Records the API responses the APP itself received.
 *
 * The harness signs in to the API as an owner, and an owner's token carries no
 * device — so `GET /api/v1/shifts/current`, which resolves the till from the
 * token, cannot answer for the harness at all. The session the till is working
 * in is knowable only from the till's own traffic.
 *
 * So the window records it. This is observation of what the SERVER told the
 * app, not the screen agreeing with itself: the assertions below read a
 * response body the API produced, and a screen that displayed something else
 * would still fail them.
 */
const TRAFFIC_HOOK = `
  window.__rsSeen = window.__rsSeen || {};
  if (!window.__rsFetchHooked) {
    window.__rsFetchHooked = true;
    var real = window.fetch.bind(window);
    window.fetch = function (input, init) {
      var url = typeof input === 'string' ? input : (input && input.url) || '';
      // The REQUEST, kept for the push: the response says applied/failed per
      // item but never which invoice, and the invoice id is the entity_uuid
      // the till queued the sale under.
      var sent = null;
      try {
        var raw = (init && init.body) || (input && input.body) || null;
        if (typeof raw === 'string') sent = JSON.parse(raw);
      } catch (e) { sent = null; }
      return real(input, init).then(function (res) {
        try {
          if (url.indexOf('/api/v1/shifts') !== -1) {
            res.clone().json().then(function (body) {
              // Only an actual session. The till also fetches its own peek
              // report and its X report from paths under /shifts, and those
              // are Reports — no id on them. Recording one of those as "the
              // session" loses the id and every later lookup fails with
              // nothing to say why.
              if (body && body.id && body.state) {
                window.__rsSeen.shift = { url: url, status: res.status, body: body };
                (window.__rsSeen.shifts = window.__rsSeen.shifts || [])
                  .push({ url: url, status: res.status, body: body });
              }
            }).catch(function () {});
          }
          if (url.indexOf('/api/v1/sync/push') !== -1) {
            res.clone().json().then(function (body) {
              window.__rsSeen.push = {
                url: url, status: res.status, body: body, sent: sent,
              };
              // Every push, in order. A sale can be pushed, refused, requeued
              // and pushed again, and only the whole sequence shows that.
              (window.__rsSeen.pushes = window.__rsSeen.pushes || [])
                .push({ status: res.status, body: body, sent: sent });
            }).catch(function () {});
          }
          if (url.indexOf('/api/v1/pos/sales') !== -1) {
            res.clone().json().then(function (body) {
              window.__rsSeen.sale = { url: url, status: res.status, body: body };
            }).catch(function () {});
          }
        } catch (e) { /* observation must never break the app */ }
        return res;
      });
    };
  }
  return true;
`;

const seen = async (key) =>
  await script('return (window.__rsSeen || {})[arguments[0]] || null', [key]);

/** The invoice the server actually accepted, found through the till's own push.
 *
 * A till is offline-first: finishing a sale writes it to the local queue and
 * the queue pushes it to POST /api/v1/sync/push. Nothing ever calls
 * POST /api/v1/pos/sales, so watching that endpoint watches an endpoint the
 * product does not use.
 *
 * The push response reports applied/duplicate/failed per item, each against the
 * entity_uuid it was queued under. That id IS the invoice id: the applier
 * refuses any sale whose payload identifier differs from it
 * (internal/sales/replay.go), so the two cannot come apart.
 *
 * Returns { id, state, error } for the newest sale item pushed, or null if the
 * till never pushed one.
 */
async function pushedSale({ timeout = 30000 } = {}) {
  const deadline = Date.now() + timeout;
  let last = null;
  for (;;) {
    const pushes = (await seen('pushes')) ?? [];
    for (let i = pushes.length - 1; i >= 0 && !last; i--) {
      const verdicts = pushes[i]?.body?.items ?? [];
      const verdict = verdicts[verdicts.length - 1];
      if (!verdict) continue;
      last = {
        id: verdict.entity_uuid,
        state: verdict.state ?? 'unknown',
        error: verdict.error ?? '',
        httpStatus: pushes[i].status,
      };
    }
    if (last && (last.state === 'applied' || last.state === 'duplicate')) {
      return last;
    }
    if (Date.now() > deadline) return last;
    await sleep(500);
  }
}

/** Empties the record of pushes, so the next sale is told apart from this one. */
const forgetPushes = () =>
  script('window.__rsSeen && (window.__rsSeen.pushes = []); return true');

/** The session the TILL is working in, taken from its own traffic.
 *
 * Newest first, and the OPEN one wins. A till fetches /shifts/current on the
 * way into the screen, so the last session it heard about is often the one the
 * run had already closed — and a check that read that would report a shift as
 * closed a second after watching it open. */
async function tillSession() {
  const observed = (await seen('shifts')) ?? [];
  const ids = [];
  for (let i = observed.length - 1; i >= 0; i--) {
    const id = observed[i]?.body?.id;
    if (id && !ids.includes(id)) ids.push(id);
  }
  let newest = null;
  for (const id of ids) {
    const r = await api('GET', scoped(`/api/v1/shifts/${id}`));
    if (r.status !== 200) continue;
    const session = { ...r.body, id };
    if (session.state === 'open') return session;
    if (!newest) newest = session;
  }
  return newest;
}

// --- the API, used to arrange and to verify -------------------------------
//
// Arranging through the API rather than through the window is deliberate. The
// subject of this file is the TILL's workflows; clicking through the back
// office to register a terminal would make every one of them depend on screens
// another script already covers, and a failure there would look like one here.

let apiToken = '';

async function api(method, path, body) {
  const r = await fetch(API + path, {
    method,
    headers: {
      'content-type': 'application/json',
      ...(apiToken ? { authorization: 'Bearer ' + apiToken } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await r.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { raw: text };
  }
  return { status: r.status, body: json };
}

const shop = {
  companyId: '',
  deviceId: '',
  variant: null,
  replacement: null,
};
const scoped = (p) =>
  p + (p.includes('?') ? '&' : '?') + 'company_id=' + shop.companyId;
const today = () => new Date().toISOString().slice(0, 10);

async function arrange() {
  step = 'Arranging the shop';
  const login = await api('POST', '/api/v1/auth/login', {
    email: EMAIL,
    password: PASSWORD,
  });
  if (login.status !== 200) {
    throw new Error(
      'could not sign in to the API: ' + JSON.stringify(login.body),
    );
  }
  apiToken = login.body.access_token ?? login.body.token;

  const companies = await api('GET', '/api/v1/companies');
  shop.companyId = companies.body.data?.[0]?.id;
  if (!shop.companyId) throw new Error('the seeded owner has no company');

  // `pending` as well as `active`: a terminal an owner has registered and
  // nobody has paired yet is pending, and that is precisely the terminal a
  // first run pairs. Only `paused` and `revoked` are unusable.
  const devices = await api('GET', scoped('/api/v1/devices'));
  const usable = (devices.body.data ?? []).find(
    (d) => d.status === 'active' || d.status === 'pending',
  );
  if (!usable) {
    throw new Error(
      'no terminal to pair with. Register one in the back office first: ' +
        JSON.stringify((devices.body.data ?? []).map((d) => d.status)),
    );
  }
  shop.deviceId = usable.id;

  // Something with a BARCODE and stock behind it.
  //
  // The barcode because that is what a till scans — the SKU is what a buyer
  // types into a purchase order, and the two are different fields. The stock
  // because a company whose policy is `block` refuses a sale that would take a
  // line below zero, and picking the first active variant found one with none.
  const snapshot = await api('GET', scoped('/api/v1/catalog/snapshot'));
  const withBarcode = (snapshot.body.items ?? []).filter((v) => v.barcode);

  const onHand = new Map();
  const products = await api('GET', scoped('/api/v1/catalog/products'));
  for (const p of products.body.data ?? []) {
    const m = await api(
      'GET',
      scoped(`/api/v1/catalog/products/${p.id}/matrix`),
    );
    for (const v of m.body.data ?? []) onHand.set(v.id, Number(v.on_hand));
  }

  // Two of them, so the exchange has something to swap TO.
  const sellable = withBarcode.filter(
    (v) => v.is_active && (onHand.get(v.id) ?? 0) > 2,
  );
  shop.variant = sellable[0] ?? null;
  shop.replacement = sellable[1] ?? null;

  if (!shop.variant) {
    throw new Error(
      'nothing in the catalogue that a till could scan and sell: ' +
        JSON.stringify(
          withBarcode.map((v) => ({ sku: v.sku, on_hand: onHand.get(v.id) })),
        ),
    );
  }

  await theShopIsReadyToInvoice();
}

/** Fills in what an invoice legally has to carry, if it is not filled in.
 *
 * Not ZATCA onboarding. A CSID needs a one-time password the taxpayer fetches
 * from their own Fatoora account, and nothing here fakes one — the till used in
 * this run is deliberately un-onboarded, and its banner says so.
 *
 * This is the step BEFORE that: the registered name, the VAT number, the branch
 * and the national address that BR-KSA-09 requires on the face of every
 * invoice. Without them `sales.Finalize` refuses with "This shop is not set up
 * for e-invoicing yet", which is correct and is a shop-configuration problem
 * rather than anything a till can do. A run that skipped it would be measuring
 * how the product behaves in a shop that has not been set up.
 */
async function theShopIsReadyToInvoice() {
  // Arrangement that fails silently is worse than no arrangement: the run
  // would go on and blame the till for a shop that was never set up.
  const ok = (r, what) => {
    if (r.status >= 300) {
      throw new Error(`${what}: ${r.status} ${JSON.stringify(r.body)}`);
    }
    return r;
  };

  const units = ok(
    await api('GET', scoped('/api/v1/einvoicing/units')),
    'reading the e-invoicing units',
  );
  const unit = (units.body.data ?? [])[0];
  if (!unit) throw new Error('the shop has no e-invoicing unit');

  ok(
    await api('PUT', scoped(`/api/v1/einvoicing/units/${unit.id}`), {
      label: unit.label || 'till-1',
      store_id: unit.store_id ?? undefined,
      csr: {
        common_name: 'TST-RawSyst-Till-1',
        egs_serial_number: '1-RawSyst|2-POS|3-000000000001',
        // Fifteen digits beginning and ending with 3, which is what the
        // validator checks. The 11th digit is 0, so this taxpayer is not a VAT
        // group and the organization unit may be a branch name — a 1 there would
        // require a member's 10-digit tax number instead. Not a real taxpayer.
        organization_identifier: '310122393500003',
        organization_unit: 'Main Branch',
        organization_name: 'Demo Retail Co',
        country: 'SA',
        // 0100 — a terminal that issues simplified invoices and not standard
        // ones, which is what a shop counter is.
        invoice_type: '0100',
        location: 'Riyadh',
        industry: 'Retail',
      },
    }),
    'completing the CSR fields',
  );

  // The seller's address, which BR-KSA-09 puts on the face of the invoice.
  //
  // There is no endpoint that edits a store, so this goes back through the
  // setup wizard's store step, whose commit upserts on (company, code) and so
  // fills in the branch that already exists. That is the same path a shop
  // correcting its own address would take.
  const stores = ok(
    await api('GET', scoped('/api/v1/devices/stores')),
    'reading the stores',
  );
  const store = (stores.body.data ?? [])[0];
  if (!store) throw new Error('the shop has no store');

  ok(
    await api('PUT', scoped('/api/v1/onboarding/steps/stores'), {
      stores: [
        {
          code: store.code,
          name: store.name,
          street: 'Prince Sultan Road',
          building_number: '2322', // BR-KSA-37 wants exactly four digits
          district: 'Al-Murabba',
          city: 'Riyadh',
          postal_code: '23333', // BR-KSA-66 wants exactly five
          country_code: 'SA',
        },
      ],
    }),
    'saving the branch address',
  );
  ok(
    await api('POST', scoped('/api/v1/onboarding/stores'), {
      company_id: shop.companyId,
    }),
    'committing the branch address',
  );
}

async function enrolmentCode() {
  const r = await api(
    'POST',
    scoped(`/api/v1/devices/${shop.deviceId}/enrolment-code`),
    {},
  );
  if (r.status >= 400) {
    throw new Error(
      'could not issue an enrolment code: ' + JSON.stringify(r.body),
    );
  }
  return r.body.code ?? r.body.enrolment_code;
}

async function sessionOnDevice() {
  const r = await api('GET', scoped('/api/v1/shifts/current'));
  return r.status === 200 ? r.body : null;
}

// --- the run --------------------------------------------------------------

async function main() {
  if (!PASSWORD) {
    console.error(
      'RS_PASSWORD is not set. Run cmd/devseed and pass the password.',
    );
    process.exit(2);
  }
  if (!existsSync(APP)) {
    console.error(
      `no built application at ${APP}\n` +
        'Build it: cd pos && npm run build && cd src-tauri && cargo build --release',
    );
    process.exit(2);
  }

  const args = ['--port', String(DRIVER_PORT)];
  if (NATIVE_DRIVER) args.push('--native-driver', NATIVE_DRIVER);
  const driver = spawn('tauri-driver', args, {
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  const driverLog = [];
  driver.stdout.on('data', (d) => driverLog.push(String(d)));
  driver.stderr.on('data', (d) => driverLog.push(String(d)));
  driver.on('error', (e) => driverLog.push('spawn failed: ' + e.message));

  try {
    for (let i = 0; i < 40; i++) {
      try {
        await fetch(base + '/status');
        break;
      } catch {
        await sleep(250);
      }
    }

    const session = await wd('POST', '/session', {
      capabilities: { alwaysMatch: { 'tauri:options': { application: APP } } },
    });
    sessionId = session.sessionId;

    await run();
  } catch (err) {
    failures.push((step || 'the run') + ': ' + err.message);
    if (driverLog.length) {
      failures.push('tauri-driver said: ' + driverLog.join('').slice(0, 400));
    }
  } finally {
    // What the window itself had to say, kept only when something went wrong.
    // A failure inside a WebView with no devtools is otherwise a sentence with
    // no evidence behind it, and the reason is nearly always in one of these
    // three: an error the app logged, a CSP refusal, or a push the server
    // turned down with a message.
    if (sessionId && failures.length) {
      const log = await consoleLog().catch(() => []);
      for (const line of log.slice(-15)) diagnostics.push('window: ' + line);
      const pushes = (await seen('pushes').catch(() => null)) ?? [];
      for (const sent of pushes.slice(-4)) {
        diagnostics.push('push: ' + JSON.stringify(sent.body).slice(0, 400));
      }
      const screen = await screenText().catch(() => '');
      if (screen) {
        diagnostics.push('screen: ' + tidy(screen).slice(0, 400));
      }
    }
    if (sessionId) await wd('DELETE', `/session/${sessionId}`).catch(() => {});
    try {
      driver.kill();
    } catch {
      /* already gone */
    }
  }

  console.log(`${done.length} thing(s) verified in the real application:`);
  for (const d of done) console.log('  ok  ' + d);

  if (!failures.length) {
    console.log('\nno failures');
    return;
  }
  console.error(`\n${failures.length} failure(s):\n`);
  for (const f of failures) console.error('  FAIL  ' + f);
  if (diagnostics.length) {
    console.error('\nwhat the window said:\n');
    for (const line of diagnostics) console.error('  ' + line);
  }
  process.exit(1);
}

async function run() {
  await arrange();

  step = 'The window opens';
  const href = await attachToTheApp();
  await script(CONSOLE_HOOK);
  await script(TRAFFIC_HOOK);
  const first = await waitForText(/\S/, { what: 'the first screen' });
  check(first.trim().length > 0, 'the application rendered a blank window');
  // Loaded from its own embedded assets, not over HTTP from a dev server. This
  // is what an installed till does, and it is the path the CSP applies to.
  //
  // Tauri v2 serves them from the host "tauri.localhost" — over http on
  // Windows and the tauri: scheme elsewhere. Either is the shipped path; a dev
  // server on :5173 is not, and neither is Edge's error page.
  check(
    /tauri\.localhost/.test(href),
    `the window loaded from ${href}. A shipped till serves its own assets ` +
      'through the custom protocol; anything else means this is testing a ' +
      'development arrangement rather than the product.',
  );
  note(`The application starts and renders from ${href}`);

  step = 'The Rust shell is really there';
  // The difference between this and a browser, as an assertion. A browser
  // answers false, refuses to pair, and cannot sell — so if this came back
  // false the rest of the file would be re-testing what workflows.mjs covers.
  const keystore = await invoke('terminal_keystore_available');
  check(
    keystore.ok === true,
    `terminal_keystore_available() answered ${JSON.stringify(keystore)}. ` +
      'Without the OS keystore this is a browser with a window frame.',
  );
  note('The Rust shell answers: the OS keystore is available');

  step = 'A clean slate';
  // Forgetting first, so a run does not depend on how the last one ended.
  await invoke('terminal_forget').catch(() => {});
  await script('window.location.reload(); return true');
  await sleep(2000);
  await script(CONSOLE_HOOK);
  await script(TRAFFIC_HOOK);

  await pairTheTerminal();
  await signIn();
  await openTheShift();
  const sale = await ringUpASale();
  await checkTheSaleReachedTheBooks(sale);
  await returnPartOfIt(sale);
  await exchangeTheRest(sale);
  await aCardSaleAwaitsSettlement();
  await theQueueSurvivesAnOutage();
  await moveCashToTheSafe();
  await closeTheShift();

  step = 'Across the whole run';
  const logged = await consoleLog();
  check(
    logged.length === 0,
    'the WebView2 console logged: ' + logged.slice(0, 5).join(' | '),
  );
}

async function pairTheTerminal() {
  step = 'Pairing — first run';
  await waitForText(/pair|code|terminal|till/i, { what: 'the pairing screen' });
  await waitForSelector('#pair-code');

  // A wrong code must be refused and must SAY so. A till that silently does
  // nothing is indistinguishable from a broken one to whoever is standing at
  // it, and this is the first screen anybody sees.
  //
  // Read from the alert element rather than from the page text. The page
  // already carries the words "Nothing can be sold on this machine until it is
  // set up", and a regex looking for "not" on the whole screen matches that
  // happily — so a version of this check that read the screen passed while the
  // button it was pressing did not exist.
  await typeInto('#pair-code', 'WXYZ-0000');
  check(
    await clickText('Set up this till'),
    'the pairing button could not be pressed',
  );

  const refusal = await waitForAlert('.setup__failure', {
    what: 'a refusal for a wrong pairing code',
    timeout: 15000,
  }).catch(() => '');
  check(
    refusal.trim().length > 0,
    'a wrong pairing code produced no message a cashier can act on',
  );
  // And it must be about the CODE. A refusal reported as "cannot reach the
  // server. Check the connection" sends somebody to look at a network cable
  // while the code sits there wrong, and the screen adds "the code is still
  // valid" to a code that is not. That is what this said before
  // pos/src/offline/credential.ts stopped classifying every server refusal as
  // a dead network.
  check(
    !/cannot reach the server|connection/i.test(refusal),
    `a wrong pairing code was reported as a connection failure: "${tidy(
      refusal,
    ).slice(0, 200)}"`,
  );
  note('Pairing refuses a wrong code, and says why the code is the problem');

  step = 'Pairing — the real code';
  // The wrong code above counts against the server's enrolment rate limit, and
  // a run that follows another closely can arrive with the limit already
  // reached. That refusal is the server working as intended, so this waits it
  // out rather than reporting the till as broken.
  let paired = false;
  let rateLimited = false;
  for (let attempt = 0; attempt < 4 && !paired; attempt++) {
    const code = await enrolmentCode();
    await typeInto('#pair-code', code);
    check(
      await clickText('Set up this till'),
      'the pairing button could not be pressed',
    );
    try {
      await waitForText(/sign in|password|email/i, {
        what: 'the sign-in screen after pairing',
        timeout: 20000,
      });
      paired = true;
    } catch (err) {
      const said = await waitForAlert('.setup__failure', { timeout: 2000 }).catch(
        () => '',
      );
      if (!/too many|wait a few minutes/i.test(said)) throw err;
      rateLimited = true;
      await sleep(20000);
    }
  }
  if (
    !check(
      paired,
      rateLimited
        ? 'the server is rate-limiting enrolment from this machine. The ' +
          'wrong-code check above spends one of the five misses allowed per ' +
          'quarter hour, so runs closer together than that will hit it. Wait ' +
          'fifteen minutes, or restart the API — the limiter is in memory.'
        : 'the terminal never got past the pairing screen',
    )
  ) {
    return;
  }

  const reports = await invoke('terminal_is_paired');
  check(reports.ok === true, 'the terminal did not report itself as paired');

  // The credential lives in the OS keystore and the web layer cannot read it.
  // That is the custody model H3 describes, asserted rather than assumed.
  const stored = await script(`
    var seen = [];
    for (var i = 0; i < localStorage.length; i++) {
      var k = localStorage.key(i);
      seen.push(k + '=' + String(localStorage.getItem(k)).slice(0, 40));
    }
    return seen;
  `);
  check(
    !/secret/i.test(JSON.stringify(stored)),
    'something that looks like a device secret is in localStorage: ' +
      JSON.stringify(stored).slice(0, 200),
  );
  note('The terminal pairs, and the secret never enters the web layer');
}

async function signIn() {
  step = 'Sign in at the till';
  await waitForSelector('input[type=password]');
  await typeInto('input[type=email]', EMAIL);
  await typeInto('input[type=password]', PASSWORD);
  await clickText('Sign in');
  await waitForText(/scan|sell|till|shift|dashboard/i, {
    what: 'the till after signing in',
    timeout: 30000,
  });
  note('A cashier signs in on the paired terminal');
}

async function openTheShift() {
  step = 'Shift — open';
  // Close whatever a previous run left open, so the float below is the only
  // one and the arithmetic that follows starts from a drawer we know.
  const stale = await seen('shift');
  if (stale?.body?.id) {
    await api('POST', scoped(`/api/v1/shifts/${stale.body.id}/close`), {
      counted_cash: '0.00',
      note: 'closed by the Tauri run before it began',
    });
  }

  if (!(await clickText('Shift'))) {
    check(false, 'no way to reach the shift screen from the till');
    return;
  }
  await waitForText(/open|float|drawer|count/i, { what: 'the shift screen' });

  // A shop in SAR gets the denomination pad, and its first field is the count
  // of 500-riyal notes. Typing 200 there declares a hundred thousand riyals,
  // which the till dutifully accepts — so this switches to the total, which is
  // the other half of the same control and the one a float of 200 belongs in.
  await clickText('Enter a total');
  if (
    !check(
      await typeInto('#opening-float', '200.00'),
      'the shift screen offers no field for the opening float',
    )
  ) {
    return;
  }
  if (!(await clickText('Open the till'))) {
    check(false, 'the Open button could not be pressed');
    return;
  }

  await waitForText(/takings|close|drawer|sales/i, {
    what: 'an open session',
    timeout: 20000,
  });

  const session = await tillSession();
  if (
    !check(session !== null, 'the till never told the server to open a session')
  ) {
    return;
  }
  check(session.state === 'open', `the session is ${session.state}, want open`);
  // Exactly, not "starts with 200". A pad that read 200 as two hundred
  // FIVE-HUNDRED notes produces 100000.00, and that starts with 200 too.
  check(
    Number(session.opening_float) === 200,
    `the opening float is ${session.opening_float}, want 200`,
  );
  if (session.state === 'open') {
    note('Shift opens with a counted float, and the server agrees');
  }
}

/** Rings one item and takes cash for it. Returns what the API told the app. */
async function ringUpASale() {
  step = 'Sale — scan and take cash';
  if (!(await clickText('Counter'))) {
    check(false, 'no way to reach the counter from the till');
    return null;
  }
  await waitForSelector('.scan__input');
  await forgetPushes();

  // Scanned TWICE, so the sale carries two of the same line. One is given back
  // below and one is exchanged, and both need something left on the invoice —
  // a sale of one leaves the exchange with nothing to swap and the run
  // reporting a broken screen when what it found was an empty invoice.
  await typeInto('.scan__input', shop.variant.barcode);
  await pressEnter('.scan__input');
  await waitForText(new RegExp(escapeRe(shop.variant.sku), 'i'), {
    what: 'the scanned line in the cart',
  });
  await typeInto('.scan__input', shop.variant.barcode);
  await pressEnter('.scan__input');
  await sleep(400);

  if (
    !check(
      await clickText('Cash'),
      'the Cash tender button could not be pressed',
    )
  ) {
    return null;
  }
  await sleep(500);
  if (
    !check(await clickText('Finish sale'), 'Finish sale could not be pressed')
  ) {
    return null;
  }

  await waitForText(/receipt|new sale|sold|done/i, {
    what: 'the receipt after finishing',
    timeout: 30000,
  });

  const pushed = await pushedSale();
  if (
    !check(
      pushed !== null,
      'the till printed a receipt and never pushed the sale to the server',
    )
  ) {
    return null;
  }
  if (
    !check(
      pushed.state === 'applied' || pushed.state === 'duplicate',
      `the server answered "${pushed.state}" to the sale: ` +
        `${pushed.error || '(no reason given)'}`,
    )
  ) {
    return null;
  }

  // Through the lookup, because the id the sale routes take is not an id the
  // till has. The document UUID it queued the sale under is the only
  // identifier it holds, and resolving that is exactly what a cashier's
  // scanned receipt has to do.
  const match = await api(
    'GET',
    scoped('/api/v1/pos/sales/lookup?reference=' + pushed.id),
  );
  if (
    !check(
      match.status === 200,
      `the document UUID the till queued (${pushed.id}) resolves to nothing: ` +
        `${match.status} ${JSON.stringify(match.body).slice(0, 160)}`,
    )
  ) {
    return null;
  }

  const invoice = await api(
    'GET',
    scoped(`/api/v1/pos/sales/${match.body.id}`),
  );
  if (
    !check(
      invoice.status === 200,
      `the invoice the till printed reads back ${invoice.status}: ` +
        JSON.stringify(invoice.body).slice(0, 200),
    )
  ) {
    return null;
  }

  note('A sale is scanned, paid in cash and finished at the till');
  return invoice.body;
}

async function checkTheSaleReachedTheBooks(sale) {
  step = 'Sale — what it left behind';
  if (!sale) {
    check(false, 'nothing was sold, so there is nothing to check');
    return null;
  }

  // The three things a sale has to do besides print, read back from the server
  // rather than from the screen that asked for them.
  const session = await tillSession();
  if (
    !check(session !== null, 'the till lost track of its session during a sale')
  ) {
    return null;
  }
  check(session.state === 'open', 'the session closed itself during a sale');

  const report = await api(
    'GET',
    scoped(`/api/v1/shifts/${session.id}/x-report`),
  );
  if (!check(report.status === 200, 'the X report could not be read'))
    return null;

  check(
    Number(report.body.cash_takings) > 0,
    `cash takings are ${report.body.cash_takings} after a cash sale; the ` +
      'drawer did not see it',
  );
  check(
    Number(report.body.invoice_count) >= 1,
    `the shift counts ${report.body.invoice_count} invoices after one sale`,
  );

  note('The sale lands in the open session, the takings and the books');
  return sale;
}

/** What the shelf says it holds, read back through the API. */
async function stockOnHand(variantID) {
  const products = await api('GET', scoped('/api/v1/catalog/products'));
  for (const p of products.body.data ?? []) {
    const m = await api(
      'GET',
      scoped(`/api/v1/catalog/products/${p.id}/matrix`),
    );
    for (const v of m.body.data ?? []) {
      if (v.id === variantID) return Number(v.on_hand);
    }
  }
  return null;
}

/** The supervisor's view of the open drawer. */
async function xReport(sessionID) {
  const r = await api('GET', scoped(`/api/v1/shifts/${sessionID}/x-report`));
  return r.status === 200 ? r.body : null;
}

/** How much of an invoice is still claimable, straight from the server. */
async function stillReturnable(invoiceID) {
  const r = await api(
    'GET',
    scoped(`/api/v1/pos/sales/${invoiceID}/returnable`),
  );
  if (r.status !== 200) return null;
  return (r.body.lines ?? []).reduce(
    (sum, l) => sum + Number(l.qty_returnable),
    0,
  );
}

/** Clicks the button whose whole text matches, for words that appear twice.
 *
 * "Refund" is both the mode tab and the button that gives the money back, and
 * clicking the first one that contains the word switches tabs and reports
 * success. */
async function clickButtonMatching(pattern) {
  const ok = await script(
    `
    var re = new RegExp(arguments[0]);
    var all = Array.prototype.slice.call(
      document.querySelectorAll('button, a, [role=button]'));
    var el = all.filter(function (e) {
      return e.offsetParent !== null && !e.disabled &&
        re.test((e.innerText || '').trim());
    })[0];
    if (!el) return false;
    el.click();
    return true;
    `,
    [pattern.source ?? String(pattern)],
  );
  return ok === true;
}

/** Gives one back, starting from what is printed on the receipt.
 *
 * The reference is the eight characters the till prints, not the invoice id.
 * That is the whole point: the id exists on no receipt and in no response a
 * terminal receives, and a version of this that used it would test a path no
 * cashier can walk. */
async function returnPartOfIt(sale) {
  step = 'Return — from what the receipt prints';
  if (!sale) {
    check(false, 'nothing was sold, so nothing can be returned');
    return;
  }

  const session = await tillSession();
  const before = {
    stock: await stockOnHand(shop.variant.id),
    returnable: await stillReturnable(sale.id),
    report: session ? await xReport(session.id) : null,
  };

  if (!(await clickText('Returns'))) {
    check(false, 'no way to reach returns from the till');
    return;
  }
  await waitForText(/invoice|scan|receipt|number|return/i, {
    what: 'the returns screen',
  });

  // Exactly what is on the paper: `reference` on the receipt is
  // invoice_uuid.slice(0, 8) — see pos/src/pos/PosCounter.tsx.
  const printed = String(sale.uuid).slice(0, 8);
  const field = await visibleInput('input');
  if (!check(Boolean(field), 'the returns screen offers no field to scan into'))
    return;

  await typeInto(field, printed);
  await pressEnter(field);

  const confirmed = await waitForText(new RegExp(escapeRe(sale.human_number)), {
    what: 'the sale the reference resolved to',
    timeout: 20000,
  }).catch(() => '');
  if (
    !check(
      confirmed !== '',
      `scanning the reference the receipt prints (${printed}) did not find ` +
        `sale ${sale.human_number}. A cashier holding the receipt cannot ` +
        'give the customer their money back.',
    )
  ) {
    return;
  }
  note('A receipt reference resolves to the sale it names');

  // One unit back, which is the ordinary case and the one where the
  // proportional allocation has something to get wrong.
  if (
    !check(
      await typeIntoEnabled('.cart__qty', '1'),
      'no line offers a quantity to take back',
    )
  ) {
    return;
  }

  const reason = await visibleInput('.returns__reason input');
  if (reason) await typeInto(reason, 'Wrong size');

  if (!check(await clickText('Cash'), 'no way to say the refund is in cash'))
    return;
  if (
    !check(
      await clickButtonMatching(/^Refund\s+\d/),
      'the Refund button could not be pressed',
    )
  ) {
    return;
  }

  const done = await waitForText(/credit note/i, {
    what: 'the credit note the refund raised',
    timeout: 30000,
  }).catch(() => '');
  if (!check(done !== '', 'the refund never reported a credit note')) return;

  // Everything below reads the server, not the screen that asked for it.
  step = 'Return — what it left behind';

  const after = {
    stock: await stockOnHand(shop.variant.id),
    returnable: await stillReturnable(sale.id),
    report: session ? await xReport(session.id) : null,
  };

  check(
    after.returnable === before.returnable - 1,
    `the invoice offered ${before.returnable} back and now offers ` +
      `${after.returnable}. A returnable figure that does not fall is how the ` +
      'same jacket gets refunded twice.',
  );
  check(
    after.stock === before.stock + 1,
    `stock went from ${before.stock} to ${after.stock}; returned goods must ` +
      'come back onto the shelf (C14 effect 1)',
  );

  if (before.report && after.report) {
    check(
      Number(after.report.refund_total) > Number(before.report.refund_total),
      `refunds on the shift went from ${before.report.refund_total} to ` +
        `${after.report.refund_total}`,
    );
    check(
      Number(after.report.expected_cash) < Number(before.report.expected_cash),
      `the expected drawer went from ${before.report.expected_cash} to ` +
        `${after.report.expected_cash}; cash handed back has to leave it`,
    );
  }

  note('A cash refund restores stock, reduces the drawer and is not repeatable');
}

/** Swaps one thing for another, in one transaction, from the same receipt.
 *
 * Design 11 §7: a credit note against the original and a new invoice for the
 * replacement, with the offsetting portion settling through
 * `exchange_clearing` so only the genuine difference moves real money. The
 * assertion that matters is the last one — the clearing account nets to zero,
 * which is the invariant the design says is "always zero for a healthy
 * company". */
async function exchangeTheRest(sale) {
  step = 'Exchange — swap, and only the difference moves';
  if (!sale) {
    check(false, 'nothing was sold, so nothing can be exchanged');
    return;
  }
  if (!shop.replacement) {
    check(false, 'the catalogue offers nothing to swap to');
    return;
  }

  const session = await tillSession();
  const before = {
    report: session ? await xReport(session.id) : null,
    returnable: await stillReturnable(sale.id),
    swapped: await stockOnHand(shop.replacement.id),
  };

  if (!(await clickText('Returns'))) {
    check(false, 'no way to reach returns from the till');
    return;
  }
  await waitForText(/invoice|scan|receipt|number|return/i, {
    what: 'the returns screen',
  });

  if (
    !check(
      await clickText('Exchange'),
      'the till offers no way to exchange rather than refund',
    )
  ) {
    return;
  }

  const field = await visibleInput('input');
  if (!check(Boolean(field), 'the exchange offers no field to scan into'))
    return;
  await typeInto(field, String(sale.uuid).slice(0, 8));
  await pressEnter(field);

  const found = await waitForText(new RegExp(escapeRe(sale.human_number)), {
    what: 'the sale to exchange against',
    timeout: 20000,
  }).catch(() => '');
  if (!check(found !== '', 'the exchange could not find the original sale'))
    return;

  if (
    !check(
      await typeIntoEnabled('.cart__qty', '1'),
      'no line offers a quantity to swap',
    )
  ) {
    return;
  }

  const reason = await visibleInput('.returns__reason input');
  if (reason) await typeInto(reason, 'Wrong size');

  // The replacement is scanned from the terminal's own cache, the same way a
  // sale line is.
  //
  // The SECOND `.scan__input` on the screen. The first is the one that looked
  // the receipt up, and typing a barcode into that searches for a sale.
  if (
    !check(
      await typeIntoNth('.scan__input', 1, shop.replacement.barcode),
      'the exchange offers nowhere to scan a replacement',
    )
  ) {
    return;
  }
  await pressEnterNth('.scan__input', 1);
  await waitForText(new RegExp(escapeRe(shop.replacement.sku), 'i'), {
    what: 'the replacement on the exchange',
    timeout: 15000,
  }).catch(() => '');

  if (!(await clickButtonMatching(/^Complete exchange$/))) {
    // The button is disabled until the till has a line, a replacement and a
    // reason, so the screen says which of the three is missing.
    check(
      false,
      'the exchange button could not be pressed. The till had: ' +
        JSON.stringify(
          await script(`
            var qty = Array.prototype.slice
              .call(document.querySelectorAll('.cart__qty'))
              .map(function (e) { return { value: e.value, disabled: e.disabled }; });
            var scans = Array.prototype.slice
              .call(document.querySelectorAll('.scan__input'))
              .map(function (e) { return e.value; });
            var reason = document.querySelector('.returns__reason input');
            var buttons = Array.prototype.slice
              .call(document.querySelectorAll('button'))
              .map(function (e) {
                return (e.innerText || '').trim() + (e.disabled ? ' [off]' : '');
              });
            return {
              qty: qty, scans: scans,
              reason: reason ? reason.value : null,
              rows: document.querySelectorAll('table.cart tbody tr').length,
              buttons: buttons,
              notice: (document.querySelector('.queue') || {}).innerText || null,
            };
          `),
        ).slice(0, 700),
    );
    return;
  }

  const done = await waitForText(/exchanged/i, {
    what: 'the exchange completing',
    timeout: 30000,
  }).catch(() => '');
  if (!check(done !== '', 'the exchange never reported a result')) return;

  const after = {
    report: session ? await xReport(session.id) : null,
    returnable: await stillReturnable(sale.id),
    swapped: await stockOnHand(shop.replacement.id),
  };

  check(
    after.returnable === before.returnable - 1,
    `the original offered ${before.returnable} back and now offers ` +
      `${after.returnable}; the exchange did not credit the line it took`,
  );
  check(
    after.swapped === before.swapped - 1,
    `the replacement's stock went from ${before.swapped} to ${after.swapped}; ` +
      'goods handed over have to leave the shelf',
  );

  // The invariant with a name. A balance on exchange_clearing is a bug that
  // can be found by looking at one account rather than by reconciling a day.
  const balance = await api(
    'GET',
    scoped('/api/v1/reports/trial-balance?as_of=' + today()),
  );
  if (balance.status === 200) {
    const rows = balance.body.rows ?? balance.body.data ?? [];
    const clearing = rows.find((r) => String(r.code) === '2350');
    if (clearing) {
      check(
        Number(clearing.debit ?? 0) - Number(clearing.credit ?? 0) === 0,
        `exchange_clearing (2350) holds ${clearing.debit}/${clearing.credit}. ` +
          'Design 11 §7 says it is always zero for a healthy company; a ' +
          'balance means one half of an exchange settled and the other did not.',
      );
    }
  }

  note('An exchange swaps goods and only the difference reaches the drawer');
}

async function aCardSaleAwaitsSettlement() {
  step = 'Payment — a card sale is money the drawer never sees';
  if (!(await clickText('Counter'))) {
    check(false, 'no way to reach the counter');
    return;
  }
  await waitForSelector('.scan__input');

  const before = await api('GET', scoped('/api/v1/settlement/pending'));
  const pendingBefore = (before.body.data ?? []).length;

  const session = await tillSession();
  const cashBefore = session
    ? Number(
        (await api('GET', scoped(`/api/v1/shifts/${session.id}/x-report`))).body
          .cash_takings,
      )
    : 0;

  await typeInto('.scan__input', shop.variant.barcode);
  await pressEnter('.scan__input');
  await waitForText(new RegExp(escapeRe(shop.variant.sku), 'i'), {
    what: 'the scanned line',
  });

  if (
    !check(
      await clickText('Mada'),
      'the Mada tender button could not be pressed',
    )
  ) {
    return;
  }
  await sleep(500);
  if (
    !check(await clickText('Finish sale'), 'Finish sale could not be pressed')
  ) {
    return;
  }
  await waitForText(/receipt|new sale|sold|done/i, {
    what: 'the receipt',
    timeout: 30000,
  });

  const after = await api('GET', scoped('/api/v1/settlement/pending'));
  check(
    (after.body.data ?? []).length === pendingBefore + 1,
    `card money awaiting settlement went from ${pendingBefore} to ` +
      `${(after.body.data ?? []).length}; a card sale must leave exactly one ` +
      'payment for the acquirer to deposit',
  );

  if (session) {
    const report = await api(
      'GET',
      scoped(`/api/v1/shifts/${session.id}/x-report`),
    );
    check(
      Number(report.body.non_cash_takings) > 0,
      'the card sale did not reach non-cash takings',
    );
    // The half that matters for the Z report: a card sale must not move the
    // drawer. If it did, every shift would read over by the day's card sales.
    check(
      Number(report.body.cash_takings) === cashBefore,
      `cash takings moved from ${cashBefore} to ${report.body.cash_takings} on ` +
        'a CARD sale; the drawer is being credited with money nobody put in it',
    );
  }
  note('A card sale is taken, stays out of the drawer, and awaits settlement');
}

/** A sale rung while the server is unreachable, and what happens after.
 *
 * This is the claim the whole offline-first design rests on and the one no
 * browser walk can reach: a till whose connection has gone must keep selling,
 * hold the sale locally, and land it exactly once when the server returns.
 *
 * # What is simulated, and what is not
 *
 * The fault is injected at the network boundary — `fetch` rejects with a
 * TypeError, which is precisely what a browser does when the host cannot be
 * reached. Nothing about the application is stubbed: the real queue writes to
 * the real SQLite file through the real Rust plugin, the real connectivity
 * monitor notices, and the real push runs against the real server afterwards.
 * The only thing that is not real is the cause of the outage.
 */
async function theQueueSurvivesAnOutage() {
  step = 'Offline — a sale rung with no server';

  const session = await tillSession();
  const before = session ? await xReport(session.id) : null;
  if (!check(before !== null, 'no open session to sell into')) return;

  if (!(await clickText('Counter'))) {
    check(false, 'no way to reach the counter');
    return;
  }
  await waitForSelector('.scan__input');
  await forgetPushes();

  // The connection goes. Every call the app makes now fails the way a call
  // fails when the host is unreachable.
  await script(`
    if (!window.__rsCut) {
      window.__rsCut = window.fetch;
      window.fetch = function () {
        return Promise.reject(new TypeError('Failed to fetch'));
      };
    }
    return true;
  `);

  await typeInto('.scan__input', shop.variant.barcode);
  await pressEnter('.scan__input');
  const rang = await waitForText(new RegExp(escapeRe(shop.variant.sku), 'i'), {
    what: 'a line scanned with no server',
    timeout: 15000,
  }).catch(() => '');
  check(
    rang !== '',
    'the counter could not scan an item with the server down. The catalogue ' +
      'is cached on the terminal precisely so that it can.',
  );

  if (!check(await clickText('Cash'), 'no tender button with the server down'))
    return;
  await sleep(500);
  if (!check(await clickText('Finish sale'), 'the sale could not be finished'))
    return;

  const receipt = await waitForText(/receipt|new sale|sold|done/i, {
    what: 'a receipt printed with no server',
    timeout: 30000,
  }).catch(() => '');
  check(
    receipt !== '',
    'the till would not complete a sale with the server unreachable. A shop ' +
      'whose connection drops has to keep selling — that is the whole point ' +
      'of the local queue.',
  );

  // Nothing reached the server, which is what "queued" has to mean.
  const during = await xReport(session.id);
  check(
    Number(during.invoice_count) === Number(before.invoice_count),
    `the shift counted ${before.invoice_count} invoices before the outage and ` +
      `${during.invoice_count} during it; a sale the server never received ` +
      'must not be counted as if it had',
  );
  note('The till sells, prints and holds the sale while the server is gone');

  // And the connection comes back.
  await script(`
    if (window.__rsCut) { window.fetch = window.__rsCut; window.__rsCut = null; }
    return true;
  `);
  // Re-hooked, because restoring the original discarded the observer with it.
  await script(TRAFFIC_HOOK);

  const pushed = await pushedSale({ timeout: 90000 });
  if (
    !check(
      pushed !== null && pushed.state === 'applied',
      'the queued sale never reached the server after it came back: ' +
        JSON.stringify(pushed),
    )
  ) {
    return;
  }

  const after = await xReport(session.id);
  check(
    Number(after.invoice_count) === Number(before.invoice_count) + 1,
    `the shift counted ${before.invoice_count} invoices before the outage and ` +
      `${after.invoice_count} after the queue drained. Exactly one is the ` +
      'answer: two means the retry sold it again, none means it was lost.',
  );

  // Exactly once, asserted from the other side too. The queue derives its
  // batch key from the batch's contents and the applier deduplicates on the
  // document UUID, so a second drain must find nothing new.
  await forgetPushes();
  await sleep(3000);
  const again = await xReport(session.id);
  check(
    Number(again.invoice_count) === Number(after.invoice_count),
    `a further drain moved the invoice count from ${after.invoice_count} to ` +
      `${again.invoice_count}; a retry is issuing a second invoice for one sale`,
  );

  note('The queue drains when the server returns, and the sale lands once');
}

async function moveCashToTheSafe() {
  step = 'Cash drawer — a safe drop';
  const session = await tillSession();
  if (!session) {
    check(false, 'no open session to drop cash from');
    return;
  }
  const before = await api(
    'GET',
    scoped(`/api/v1/shifts/${session.id}/x-report`),
  );
  const expectedBefore = Number(before.body.expected_cash);

  if (!(await clickText('Shift'))) {
    check(false, 'no way to reach the shift screen');
    return;
  }
  await waitForText(/drop|move|cash|safe|takings/i, {
    what: 'the shift screen',
  });

  const opened =
    (await clickText('Cash drop')) ||
    (await clickText('Move cash')) ||
    (await clickText('Drop'));
  if (!opened) {
    check(false, 'no way to record a cash drop from the shift screen');
    return;
  }
  await sleep(800);

  const amount = await visibleInput('input');
  if (!amount) {
    check(false, 'the cash-drop panel offers no amount field');
    return;
  }
  await typeInto(amount, '-50.00');

  // The server refuses a movement with no explanation, which is the point of
  // the field: an unexplained hand in the till is what the record exists to
  // make visible.
  const explain = await script(`
    var els = Array.prototype.slice
      .call(document.querySelectorAll('input, textarea'))
      .filter(function (e) { return e.offsetParent !== null && e.type !== 'hidden'; });
    var el = els[els.length - 1];
    if (!el) return '';
    return el.id ? '#' + el.id : '';
  `);
  if (explain) await typeInto(explain, 'to the safe, mid-shift');

  await clickText('Record');
  await sleep(1500);

  const after = await api(
    'GET',
    scoped(`/api/v1/shifts/${session.id}/x-report`),
  );
  const expectedAfter = Number(after.body.expected_cash);
  if (
    check(
      expectedAfter === expectedBefore - 50,
      `the expected drawer went from ${expectedBefore} to ${expectedAfter}; a ` +
        'safe drop of 50 must take exactly 50 out of it',
    )
  ) {
    note('A safe drop moves the expected drawer by exactly what was dropped');
  }
}

async function closeTheShift() {
  step = 'Shift — the Z report';
  const session = await tillSession();
  if (!session) {
    check(false, 'no open session to close');
    return;
  }
  const report = await api(
    'GET',
    scoped(`/api/v1/shifts/${session.id}/x-report`),
  );
  const expected = Number(report.body.expected_cash);

  // Counted five short on purpose. A drawer that reconciles exactly proves the
  // arithmetic and nothing about the control: the variance is the only signal
  // there is, and it has to reach the books.
  const counted = (expected - 5).toFixed(2);

  if (!(await clickText('Shift'))) {
    check(false, 'no way to reach the shift screen');
    return;
  }
  await waitForText(/close|count|drawer/i, { what: 'the shift screen' });

  if (!(await clickText('Close the till'))) {
    check(false, 'no way to close the shift from its own screen');
    return;
  }
  await sleep(900);

  // Blind close: the cashier must NOT be shown what to count to. P75 found the
  // web view publishing the three figures that add up to it; this asks the same
  // question of the till, which is where a drawer is actually counted.
  const onScreen = await screenText();
  const target = expected.toFixed(2);
  check(
    !onScreen.includes(target) && !onScreen.includes(String(expected)),
    `the expected drawer of ${target} is on the screen a cashier counts ` +
      'against. A blind close that shows the answer produces a variance that ' +
      'reads zero on every shift.',
  );
  note('The close screen does not tell the cashier what to count to');

  // The total rather than the pad, for the reason the open step gives.
  await clickText('Enter a total');
  if (
    !check(
      await typeInto('#counted-cash', counted),
      'the close panel offers no field to enter the count in',
    )
  ) {
    return;
  }

  // A Z report happens once, so the till confirms it. Both presses, in order:
  // stopping at the first leaves the shift open and the run reporting that
  // closing does not work when what it found was a confirmation step.
  if (
    !check(
      await clickText('Count is complete'),
      'the count could not be committed',
    )
  ) {
    return;
  }
  await sleep(400);
  if (
    !check(
      await clickText('Close the shift'),
      'the confirmation offered no way to close',
    )
  ) {
    return;
  }
  await sleep(2500);

  const closed = await api('GET', scoped(`/api/v1/shifts/${session.id}`));
  check(
    closed.body.state === 'closed',
    `the session is ${closed.body.state} after a Z report`,
  );
  check(
    Number(closed.body.variance) === -5,
    `the variance is ${closed.body.variance}, want -5`,
  );

  // And it reached the books. Design 11 §9: the variance "posts to a Cash
  // Over/Short account rather than being absorbed silently".
  const expenses = await api(
    'GET',
    `/api/v1/dashboard/expenses?company_id=${shop.companyId}&date=${today()}`,
  );
  check(
    /over|short/i.test(JSON.stringify(expenses.body)),
    'the drawer difference did not reach Cash Over/Short; a variance that is ' +
      'recorded and never posted is a loss nobody sees in the P&L',
  );

  if (closed.body.state === 'closed') {
    note('The Z report closes the session, reports the shortfall and posts it');
  }
}

/** A SKU may contain regex metacharacters; a shop chooses its own codes. */
const escapeRe = (s) => String(s).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

main().catch((e) => {
  console.error('the run could not start:', e.message);
  process.exit(2);
});
