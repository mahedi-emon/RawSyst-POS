// Screenshots of every back-office screen, so they can be LOOKED AT.
//
// The audit script reports facts a machine can be sure about. This one exists
// for the judgements it deliberately refuses to make: whether a screen is
// crowded, whether the hierarchy reads, whether a table is legible, whether the
// Arabic mirror actually mirrors. Those need eyes.
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';

const WEB = process.env.RS_WEB ?? 'http://localhost:3000';
const EMAIL = process.env.RS_EMAIL ?? 'owner@example.test';
const PASSWORD = process.env.RS_PASSWORD;
const OUT = process.env.RS_OUT ?? 'shots';
const ONLY = process.env.RS_WIDTH;

mkdirSync(OUT, { recursive: true });

const WIDTHS = [
  { name: 'phone', w: 390, h: 844, touch: true },
  { name: 'desktop', w: 1440, h: 900, touch: false },
];

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

/* By locale code rather than by the name in the menu.
 *
 * Matching on "العربية" meant passing Arabic through an environment variable,
 * and the shell handed it over re-encoded -- the option never matched, the
 * click never happened, and the run produced a second set of screenshots that
 * were quietly identical to the English ones. A two-letter code cannot be
 * mangled on the way in, and the menu stamps each option with the one it
 * selects. */
async function chooseLanguage(page, code) {
  if (code === 'en' && (await page.evaluate(() => document.documentElement.lang)) === 'en') {
    return true;
  }
  const trigger = page.locator('.lang__trigger').first();
  if (!(await trigger.count())) return false;
  await trigger.click().catch(() => {});
  await page.waitForTimeout(250);
  const opt = page.locator(`button.lang__opt[lang="${code}"]`).first();
  if (!(await opt.count())) {
    await page.keyboard.press('Escape');
    console.error(`no option for locale ${code}`);
    return false;
  }
  await opt.click();
  await page.waitForTimeout(1400);
  return true;
}

async function signIn(page) {
  await page.goto(WEB, { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle').catch(() => {});
  await page.waitForTimeout(600);
  for (let i = 0; i < 3; i++) {
    await page.fill('input[type=email]', EMAIL);
    await page.fill('input[type=password]', PASSWORD);
    await page.locator('form button').first().click();
    try {
      await page.waitForSelector('.bo__link:not(.bo__link--signout), .app__navlink', { timeout: 15000 });
      await page.waitForTimeout(800);
      return;
    } catch { await page.waitForTimeout(1200); }
  }
  throw new Error('sign-in failed');
}

async function main() {
  if (!PASSWORD) { console.error('RS_PASSWORD not set'); process.exit(2); }
  const browser = await chromium.launch();
  for (const width of WIDTHS) {
    if (ONLY && ONLY !== width.name) continue;
    const ctx = await browser.newContext({
      viewport: { width: width.w, height: width.h },
      hasTouch: width.touch, isMobile: width.touch,
      deviceScaleFactor: 1,
    });
    const page = await ctx.newPage();

    // The sign-in screen itself, before anything else.
    await page.goto(WEB, { waitUntil: 'domcontentloaded' });
    await page.waitForLoadState('networkidle').catch(() => {});
    await page.waitForTimeout(700);
    await page.screenshot({ path: `${OUT}/${width.name}-00-signin.png`, fullPage: true });

    await signIn(page);

    for (const lang of (process.env.RS_LANGS ?? 'en').split(',')) {
      if (!(await chooseLanguage(page, lang))) continue;

      await openNav(page);
      const sections = await page.evaluate(() =>
        Array.from(document.querySelectorAll('.bo__link:not(.bo__link--signout), .app__navlink')).map((e) => e.innerText.trim()));

      let i = 1;
      for (const s of sections) {
        /* Reopened before EVERY press, not once at the top.
         *
         * The drawer closes itself when a section is chosen, which is right --
         * a phone should show the page it just navigated to, not the menu over
         * it. But it meant every press after the first landed on a link that
         * was translated off-screen, so eleven of the twelve phone screenshots
         * were the dashboard photographed eleven times. */
        await openNav(page);
        const link = page.locator('.bo__link:not(.bo__link--signout), .app__navlink', { hasText: s }).first();
        if (!(await link.count())) continue;
        await link.click({ timeout: 6000 }).catch(() => {});
        await page.waitForTimeout(1100);
        const slug = String(i).padStart(2, '0') + '-' +
          s.toLowerCase().replace(/[^a-z0-9]+/g, '-').slice(0, 24);
        const tag = lang === 'en' ? '' : `-${lang}`;
        await page.screenshot({
          path: `${OUT}/${width.name}-${slug}${tag}.png`, fullPage: true,
        });
        i++;
      }
    }
    await ctx.close();
  }
  await browser.close();
  console.log('done');
}
main().catch((e) => { console.error(e.message); process.exit(2); });
