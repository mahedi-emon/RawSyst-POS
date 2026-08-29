// An authenticated walk of the back office, in a real browser.
//
// # Why this exists as a checked-in script rather than a note
//
// Three classes of defect are invisible to unit tests, to a typecheck, and to
// reading the source, and all three have actually shipped here before:
//
//   * a stylesheet the browser app never imports, so primary navigation
//     rendered as bare text buttons on desktop while every unit test passed
//   * English left on an Arabic screen in places a source scan structurally
//     cannot see -- text passed as a JSX attribute, prose containing an inline
//     element, labels under the length floor
//   * controls too small to tap, and pages that scroll sideways on a phone
//
// Each was found by opening the app and looking. This is that, written down, so
// it happens on every change instead of when somebody remembers.
//
// # What it deliberately does NOT assert
//
// Anything cosmetic. It reports facts a machine can be sure about: the page
// scrolled sideways or it did not; the console logged an error or it did not;
// the thing a finger has to hit was 44 pixels tall or it was not. Judgement
// about whether a layout looks right belongs to a person.
//
// Usage:
//   node e2e/audit.mjs                       # against localhost:3000
//   RS_WEB=http://host:3000 node e2e/audit.mjs
//
// Requires a signed-in-able account. Defaults match cmd/devseed.

import { chromium } from 'playwright';

const WEB = process.env.RS_WEB ?? 'http://localhost:3000';
const EMAIL = process.env.RS_EMAIL ?? 'owner@example.test';
const PASSWORD = process.env.RS_PASSWORD;

// touch matters as much as width: the tap-target rules key on
// `pointer: coarse`, so a browser reporting a mouse would not match them and
// the audit would report a phone as comfortable when a real phone is not.
const WIDTHS = [
  { name: 'phone', w: 390, h: 844, touch: true },
  { name: 'tablet', w: 768, h: 1024, touch: true },
  { name: 'desktop', w: 1440, h: 900, touch: false },
];

// A tap target is 44px because that is what a fingertip needs. Applied at
// phone and tablet only: the same rule on a monitor would make a data table
// 44 pixels a row, which is a different kind of wrong.
const TAP_MIN = 44;

/**
 * Runs inside the page.
 *
 * Two corrections here were learned the hard way, and both matter enough to
 * state:
 *
 *  1. The tap target is the LABEL when one wraps the control. A 13px checkbox
 *     inside a 44px label is fine, and measuring the input alone condemns a
 *     perfectly usable control.
 *  2. The page overflows only when body.scrollWidth exceeds clientWidth. An
 *     element extending past the viewport inside a clipped or scrollable
 *     ancestor is not an overflow -- the visually-hidden table header is
 *     off-screen ON PURPOSE, so screen readers keep the column associations.
 */
const probe = (tapMin) => {
  const de = document.documentElement;
  const out = {
    dir: de.getAttribute('dir') || getComputedStyle(de).direction,
    pageScrollsSideways: document.body.scrollWidth > de.clientWidth + 1,
    scrollWidth: document.body.scrollWidth,
    clientWidth: de.clientWidth,
    small: [],
    unlabelled: [],
    english: [],
  };

  const invisible = (el) => {
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') return true;
    const r = el.getBoundingClientRect();
    return r.width <= 1 && r.height <= 1;
  };

  const describe = (el) => {
    const cls =
      typeof el.className === 'string' && el.className
        ? '.' + el.className.trim().split(/\s+/).slice(0, 2).join('.')
        : '';
    return (el.tagName.toLowerCase() + cls).slice(0, 60);
  };

  for (const el of document.querySelectorAll(
    'button, a[href], input:not([type=hidden]), select, textarea, [role=button], summary',
  )) {
    if (invisible(el)) continue;
    // An inline link inside a paragraph is read, not aimed at.
    if (el.tagName === 'A' && el.closest('p')) continue;

    const label = el.closest('label');
    const target = label || el;
    const r = target.getBoundingClientRect();
    if (r.height < tapMin) {
      out.small.push({
        el: describe(el),
        via: label ? 'label' : 'self',
        h: Math.round(r.height),
        text: (target.innerText || '').trim().slice(0, 30),
      });
    }
  }

  for (const el of document.querySelectorAll('input:not([type=hidden]), select, textarea')) {
    if (invisible(el)) continue;
    const named =
      el.getAttribute('aria-label') ||
      el.getAttribute('aria-labelledby') ||
      (el.id && document.querySelector(`label[for="${CSS.escape(el.id)}"]`)) ||
      el.closest('label') ||
      el.getAttribute('title');
    if (!named) out.unlabelled.push({ el: describe(el), type: el.type || el.tagName });
  }

  // English left on Arabic chrome. Tenant data -- a shop's own product and
  // branch names -- is legitimately untouched, so this looks only at the
  // product's own furniture.
  const LATIN = /[A-Za-z]{4,}/;
  const ALLOWED =
    /^(RawSyst|SAR|BDT|USD|VAT|ZATCA|IBAN|QR|PDF|CSV|CSID|OTP|English|BR-KSA[-0-9]*|[A-Z]{2,5}-?\d*)$/;
  for (const el of document.querySelectorAll(
    'button, a, h1, h2, h3, th, label > span, .ds-badge, .app__navlink',
  )) {
    if (invisible(el)) continue;
    // `.detail__rowbtn` is the control that opens a record, and everything
    // inside it is that record: a purchase order number, a supplier's legal
    // name, a customer's code. All tenant data by construction, and none of it
    // this product's to translate. It is a <button>, so the sweep above
    // reached it and reported a shop's own customer list as untranslated
    // interface — which would push somebody to "fix" it by translating a
    // customer's name.
    if (el.closest('.detail__rowbtn')) continue;
    const txt = (el.innerText || '').trim();
    if (!txt || txt.length > 80 || !LATIN.test(txt)) continue;
    const offending = txt
      .split(/\s+/)
      .filter((w) => LATIN.test(w))
      .filter((w) => !ALLOWED.test(w.replace(/[.,:()]/g, '')));
    if (offending.length) out.english.push({ el: describe(el), text: txt.slice(0, 60) });
  }

  return out;
};

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

async function signIn(page, width) {
  await page.goto(WEB + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('input[type=email]', { timeout: 30000 });

  // Wait for React to hydrate before touching anything.
  //
  // The markup arrives server-rendered and the handlers attach afterwards, so
  // a click that lands in between hits a button that looks identical and does
  // nothing -- no request, no error, no clue. This cost an hour: the first
  // version of this script "failed to sign in" against an app whose sign-in
  // was working perfectly, because a warm browser had always beaten it to
  // hydration and a cold one did not.
  await page.waitForLoadState('networkidle').catch(() => {});
  await page.waitForTimeout(500);

  // Submit, and check it took. Retrying is cheap; a false failure here would
  // report the whole application broken.
  for (let attempt = 1; attempt <= 3; attempt++) {
    await page.fill('input[type=email]', EMAIL);
    await page.fill('input[type=password]', PASSWORD);
    await page.locator('form button').first().click();

    try {
      await page.waitForSelector('.bo__link, .app__navlink', { timeout: 15000 });
      await page.waitForTimeout(1000);
      return;
    } catch {
      if (attempt === 3) {
        const shown = await page.evaluate(() => document.body.innerText.slice(0, 200));
        throw new Error(
          'could not sign in after 3 attempts. The page shows: ' + JSON.stringify(shown),
        );
      }
      await page.waitForTimeout(1500);
    }
  }
}

async function setLanguage(page, which) {
  await chooseLanguage(page, which);
}

async function walk(page, width, language, problems) {
  // The rail is off-canvas on a phone, so its links are not clickable until
  // the drawer is open. They are in the DOM either way, which is why reading
  // the section names still works from here.
  await openNav(page);
  const sections = await page.evaluate(() =>
    Array.from(document.querySelectorAll('.bo__link, .app__navlink')).map((e) => e.innerText.trim()),
  );
  if (sections.length === 0) {
    problems.push({ what: 'no navigation rendered', language });
    return 0;
  }

  let checks = 0;
  {
    for (const section of sections) {
      const link = page.locator('.bo__link, .app__navlink', { hasText: section }).first();
      if ((await link.count()) === 0) {
        problems.push({
          what: 'a navigation entry vanished mid-walk',
          where: `${language}/${width.name}/${section}`,
        });
        continue;
      }
      try {
        await link.click({ timeout: 5000 });
      } catch (err) {
        // Reported rather than skipped. A section that cannot be opened is the
        // most serious thing this script can find, and an earlier version
        // swallowed it -- the run came back one check short of the expected
        // count and said "no problems found", which is how a broken screen
        // hides inside a green report.
        problems.push({
          what: 'a section could not be opened',
          where: `${language}/${width.name}/${section}`,
          detail: String(err.message || err).slice(0, 120),
        });
        continue;
      }
      await page.waitForTimeout(900);
      checks++;

      const tapMin = width.touch ? TAP_MIN : 0;
      const r = await page.evaluate(probe, tapMin);
      const where = `${language}/${width.name}/${section}`;

      if (r.pageScrollsSideways) {
        problems.push({
          what: 'the page scrolls sideways',
          where,
          detail: `${r.scrollWidth}px of content in ${r.clientWidth}px`,
        });
      }
      for (const s of r.small) {
        problems.push({
          what: 'tap target under ' + TAP_MIN + 'px',
          where,
          detail: `${s.h}px ${s.el} (${s.via}) ${JSON.stringify(s.text)}`,
        });
      }
      for (const u of r.unlabelled) {
        problems.push({
          what: 'input a screen reader cannot name',
          where,
          detail: `${u.el} type=${u.type}`,
        });
      }
      if (language === 'Arabic') {
        if (r.dir !== 'rtl') {
          problems.push({ what: 'Arabic is not right-to-left', where, detail: 'dir=' + r.dir });
        }
        for (const e of r.english) {
          problems.push({ what: 'English on an Arabic screen', where, detail: `${e.el}: ${e.text}` });
        }
      }
    }
  }
  return checks;
}

async function main() {
  if (!PASSWORD) {
    console.error(
      'RS_PASSWORD is not set. Run cmd/devseed and pass the password it prints:\n' +
        '  RS_PASSWORD=... node e2e/audit.mjs',
    );
    process.exit(2);
  }

  const browser = await chromium.launch();

  const problems = [];
  const consoleErrors = [];
  const failedRequests = [];
  let checks = 0;

  try {
    // A context per device, not a viewport resize.
    //
    // The tap rules ask `pointer: coarse`, and resizing a window does not
    // change what the browser reports as its pointer -- so a resize-only audit
    // measures a mouse-sized layout and calls a phone comfortable. hasTouch is
    // what actually makes the query true.
    for (const width of WIDTHS) {
      const context = await browser.newContext({
        viewport: { width: width.w, height: width.h },
        hasTouch: width.touch,
        isMobile: width.touch,
      });
      const page = await context.newPage();

      page.on('console', (m) => {
        if (m.type() === 'error') consoleErrors.push(`[${width.name}] ` + m.text().slice(0, 200));
      });
      page.on('response', (r) => {
        if (r.status() >= 400) {
          failedRequests.push(
            `[${width.name}] ${r.status()} ${r.request().method()} ${r.url().slice(0, 100)}`,
          );
        }
      });

      await signIn(page, width);

      await setLanguage(page, 'English');
      checks += await walk(page, width, 'English', problems);

      await setLanguage(page, 'العربية');
      checks += await walk(page, width, 'Arabic', problems);

      await context.close();
    }
  } finally {
    await browser.close();
  }

  for (const e of consoleErrors) problems.push({ what: 'console error', detail: e });
  for (const f of failedRequests) problems.push({ what: 'request failed', detail: f });

  console.log(`${checks} section/language combinations walked across ${WIDTHS.length} devices`);

  if (problems.length === 0) {
    console.log('no problems found');
    return;
  }

  console.error(`\n${problems.length} problem(s):\n`);
  for (const p of problems) {
    console.error(`  ${p.what}${p.where ? '  [' + p.where + ']' : ''}`);
    if (p.detail) console.error(`      ${p.detail}`);
  }
  process.exit(1);
}

main().catch((err) => {
  console.error('the audit could not run:', err.message);
  process.exit(2);
});
