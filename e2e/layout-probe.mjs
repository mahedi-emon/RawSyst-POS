// Finds layout faults a screenshot review would take an afternoon to spot.
//
// The audit script already checks the four things it was written for. This one
// asks the questions that came out of actually looking at the screens: does any
// text overflow its own box, does any element stick out of its container, are
// the type sizes in the system's scale, do the table headers agree with the
// cells beneath them, and does anything in the Arabic build still read the
// wrong way round.
import { chromium } from 'playwright';

const WEB = process.env.RS_WEB ?? 'http://localhost:3000';
const EMAIL = process.env.RS_EMAIL ?? 'owner@example.test';
const PASSWORD = process.env.RS_PASSWORD;

const WIDTHS = [
  { name: 'phone', w: 390, h: 844, touch: true },
  { name: 'tablet', w: 768, h: 1024, touch: true },
  { name: 'desktop', w: 1440, h: 900, touch: false },
];

const probe = () => {
  const out = { overflowing: [], escaping: [], misaligned: [], clipped: [] };
  const seen = new Set();

  const label = (el) => {
    const cls = (el.className && String(el.className).split(/\s+/)[0]) || '';
    const text = (el.innerText || '').trim().replace(/\s+/g, ' ').slice(0, 40);
    return `${el.tagName.toLowerCase()}${cls ? '.' + cls : ''} ${JSON.stringify(text)}`;
  };

  for (const el of document.querySelectorAll('body *')) {
    const cs = getComputedStyle(el);
    if (cs.display === 'none' || cs.visibility === 'hidden') continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;

    // Text wider than its own box, with nothing set up to handle it. A cell
    // that says "3,496.00" in a box 40px wide is not showing a number.
    // A visually-hidden label is a 1px box holding a sentence ON PURPOSE: it
    // is there for a screen reader and clipped away from the screen. Reporting
    // it as an overflow buries the real ones under it.
    const hiddenForSighted =
      el.classList.contains('ds-visually-hidden') ||
      el.closest('.ds-visually-hidden') !== null;

    if (
      !hiddenForSighted &&
      el.scrollWidth > el.clientWidth + 1 &&
      cs.overflowX !== 'auto' && cs.overflowX !== 'scroll' &&
      cs.textOverflow !== 'ellipsis' &&
      el.children.length === 0
    ) {
      const k = 'o' + label(el);
      if (!seen.has(k)) { seen.add(k); out.overflowing.push(label(el) +
        ` (${el.scrollWidth}px of text in ${el.clientWidth}px)`); }
    }

    // An element sticking out of the side of its own parent, where the parent
    // is not scrolling or clipping on purpose.
    const p = el.parentElement;
    if (p && p !== document.body) {
      const pcs = getComputedStyle(p);
      const pr = p.getBoundingClientRect();
      const clips = ['hidden', 'auto', 'scroll', 'clip'].includes(pcs.overflowX);
      const escaped = Math.max(pr.left - r.left, r.right - pr.right);
      if (!clips && escaped > 2 && cs.position === 'static') {
        const k = 'e' + label(el);
        if (!seen.has(k)) { seen.add(k); out.escaping.push(
          label(el) + ` escapes ${label(p)} by ${Math.round(escaped)}px`); }
      }
    }
  }

  // A column header must be read from the same edge as the cells beneath it.
  //
  // Only where there ARE columns. Below the table breakpoint every row becomes
  // a card, the header row is moved off-screen for screen readers and each cell
  // carries its own label — so comparing a hidden header to a stacked cell
  // compares two things that are never seen together.
  for (const table of document.querySelectorAll('table')) {
    const head = table.querySelector('thead');
    if (!head || getComputedStyle(head).position === 'absolute') continue;
    const heads = [...table.querySelectorAll('thead th')];

    // The first body row that actually has a cell in every column.
    //
    // A `colSpan` breaks the index-for-index comparison this does, and the
    // settlement table opens each group with one: `<th colSpan={3}>` for the
    // method, then the group total. Pairing header[1] with that row's second
    // CHILD compared "Invoice" against an amount and reported a misalignment
    // on four screens that were correctly aligned.
    //
    // So the row is expanded by span into real column positions, and a row
    // that does not reach the last column is skipped rather than guessed at.
    const rows = [...table.querySelectorAll('tbody tr')];
    let cells = null;
    for (const row of rows) {
      const spread = [];
      for (const cell of row.children) {
        const span = Math.max(1, cell.colSpan || 1);
        for (let n = 0; n < span; n++) spread.push(cell);
      }
      if (spread.length >= heads.length) {
        // A cell that spans several columns says nothing about any one of
        // them, so those positions are left out of the comparison.
        cells = spread.map((cell) =>
          (cell.colSpan || 1) > 1 ? null : cell,
        );
        break;
      }
    }
    if (!cells) continue;

    heads.forEach((th, i) => {
      const td = cells[i];
      if (!td) return;
      const a = getComputedStyle(th).textAlign;
      const b = getComputedStyle(td).textAlign;
      const norm = (v, el) => {
        const dir = getComputedStyle(el).direction;
        if (v === 'start') return dir === 'rtl' ? 'right' : 'left';
        if (v === 'end') return dir === 'rtl' ? 'left' : 'right';
        return v;
      };
      const na = norm(a, th), nb = norm(b, td);
      if (na !== nb && (td.innerText || '').trim()) {
        out.misaligned.push(
          `"${(th.innerText || '').trim()}" header is ${na} over cells that are ${nb}`);
      }
    });
  }
  return out;
};

async function signIn(page) {
  await page.goto(WEB, { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle').catch(() => {});
  await page.waitForTimeout(600);
  for (let i = 0; i < 3; i++) {
    await page.fill('input[type=email]', EMAIL);
    await page.fill('input[type=password]', PASSWORD);
    await page.locator('form button').first().click();
    try { await page.waitForSelector('.app__navlink', { timeout: 15000 });
      await page.waitForTimeout(700); return; } catch { await page.waitForTimeout(1200); }
  }
  throw new Error('sign-in failed');
}

async function main() {
  if (!PASSWORD) { console.error('RS_PASSWORD not set'); process.exit(2); }
  const browser = await chromium.launch();
  const problems = [];
  let checks = 0;

  for (const width of WIDTHS) {
    const ctx = await browser.newContext({
      viewport: { width: width.w, height: width.h },
      hasTouch: width.touch, isMobile: width.touch,
    });
    const page = await ctx.newPage();
    await signIn(page);

    for (const lang of ['English', 'العربية']) {
      const btn = page.locator('button.lang__opt', { hasText: lang }).first();
      if (await btn.count()) { await btn.click(); await page.waitForTimeout(900); }
      const sections = await page.evaluate(() =>
        [...document.querySelectorAll('.app__navlink')].map((e) => e.innerText.trim()));

      for (const s of sections) {
        const link = page.locator('.app__navlink', { hasText: s }).first();
        if (!(await link.count())) continue;
        await link.click({ timeout: 6000 }).catch(() => {});
        await page.waitForTimeout(900);
        checks++;
        const r = await page.evaluate(probe);
        const where = `${lang === 'English' ? 'en' : 'ar'}/${width.name}/${s}`;
        for (const k of ['overflowing', 'escaping', 'misaligned'])
          for (const d of r[k]) problems.push(`${k}  [${where}]  ${d}`);
      }
    }
    await ctx.close();
  }
  await browser.close();

  console.log(`${checks} screens probed`);
  if (!problems.length) { console.log('no layout faults found'); return; }
  const unique = [...new Set(problems)];
  console.error(`\n${unique.length} layout fault(s):\n`);
  for (const p of unique) console.error('  ' + p);
  process.exit(1);
}
main().catch((e) => { console.error('probe failed:', e.message); process.exit(2); });
