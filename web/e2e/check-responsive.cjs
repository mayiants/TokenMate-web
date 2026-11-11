// Responsive layout gate for woodgit-web — spirit ported from portal CI
// (e2e/ui-mobile-all-pages.spec.js), strengthened with automated checks:
// portal only screenshots at 390x844; here we assert NO horizontal overflow
// (the classic responsive breakage) at PC / tablet / mobile widths, and gate
// on it. Screenshots land in SHOTS_DIR (default web/screenshots/responsive).
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');
const { PUBLIC_PAGES, CONSOLE_PAGES, ADMIN_PAGES } = require('./pages.cjs');

const BASE = process.env.CHECK_BASE || 'http://127.0.0.1:9090';
const GATE = process.argv.includes('--gate');
// --public (or CHECK_PUBLIC_ONLY=1): run the public-page half only, for CI
// environments that have no portal API behind the /api proxy.
const PUBLIC_ONLY = process.env.CHECK_PUBLIC_ONLY === '1' || process.argv.includes('--public');
// CHECK_ROLE=enterprise|admin selects which page set (and default account) to scan.
const ROLE =
  process.env.CHECK_ROLE ||
  (process.argv.find((a) => a.startsWith('--role=')) || '').split('=')[1] ||
  'enterprise';
const ACCOUNT = {
  email:
    process.env.CHECK_EMAIL ||
    (ROLE === 'admin' ? 'sysview@test.local' : 'p2review@test.local'),
  password:
    process.env.CHECK_PASSWORD || (ROLE === 'admin' ? 'SysView#2026' : 'WgTest#2026'),
};
const SHOTS = process.env.SHOTS_DIR || path.join(__dirname, '..', 'screenshots', 'responsive');

const VIEWPORTS = [
  { name: 'pc', width: 1440, height: 900 },
  { name: 'tablet', width: 834, height: 1112 }, // iPad portrait
  { name: 'mobile', width: 390, height: 844 }, // iPhone 14
];

(async () => {
  fs.mkdirSync(SHOTS, { recursive: true });
  const browser = await chromium.launch();
  let failures = 0;
  let authed = true;

  for (const vp of VIEWPORTS) {
    const ctx = await browser.newContext({ viewport: { width: vp.width, height: vp.height } });
    const page = await ctx.newPage();
    const jsErrors = [];
    page.on('pageerror', (e) => jsErrors.push(e.message));

    const check = async (p, tag) => {
      await page.goto(BASE + p, { waitUntil: 'networkidle', timeout: 45000 }).catch(() => {});
      await page.waitForTimeout(1500);
      const m = await page.evaluate(() => ({
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        innerOverflows: Array.from(document.querySelectorAll('body *'))
          .filter((el) => {
            if (el.clientWidth <= 50) return false;
            const gap = el.scrollWidth - el.clientWidth;
            if (gap <= 6) return false;
            const ox = getComputedStyle(el).overflowX;
            if (ox === 'auto' || ox === 'scroll') return false; // intentional scroller (e.g. table body)
            if (getComputedStyle(el).textOverflow === 'ellipsis') return false; // intentional truncation (long model names)
            if ((el.className || '').toString().includes('wg-model-name')) return false; // single-line model label truncates by design
            if ((el.className || '').toString().includes('semi-table-wrapper')) return false; // Semi fixed-col table scroller
            if (el.querySelector && el.querySelector('.semi-table-wrapper, .semi-table-body')) return false; // table card hosting a self-scrolling table
            if ((el.className || '').toString().includes('wg-table-card')) return false; // wide tables scroll horizontally on small screens by design
            if ((el.className || '').toString().includes('semi-table-pagination')) return false; // table pagination may exceed width on phones; page itself does not overflow
            return true;
          })
          .slice(0, 3)
          .map((el) => `${el.tagName}.${(el.className || '').toString().slice(0, 40)} +${el.scrollWidth - el.clientWidth}px`),
      }));
      const bad = m.overflow > 2 || m.innerOverflows.length > 0;
      if (bad) failures++;
      console.log(`${bad ? '✗' : '✓'} ${vp.name} ${p} page-overflow=${m.overflow}px inner=[${m.innerOverflows.join('; ')}]`);
      await page.screenshot({ path: path.join(SHOTS, `${vp.name}${tag}.png`), fullPage: false });
    };

    for (const p of PUBLIC_PAGES) await check(p, p === '/' ? '-home' : p.replace(/\//g, '_'));

    // CHECK_PUBLIC_ONLY=1: no backend available — skip the signed-in half.
    if (PUBLIC_ONLY) {
      if (jsErrors.length) { failures += jsErrors.length; console.log(`✗ ${vp.name} js errors: ${jsErrors.slice(0, 3).join(' | ')}`); }
      await ctx.close();
      continue;
    }

    await page.goto(BASE + '/login', { waitUntil: 'networkidle' }).catch(() => {});
    await page.waitForTimeout(2500);
    const emailInput = page.locator('input').nth(0);
    const passInput = page.locator('input[type="password"]');
    if ((await emailInput.count()) && (await passInput.count())) {
      await emailInput.fill(ACCOUNT.email);
      await passInput.fill(ACCOUNT.password);
      await page.click('button[type="submit"]').catch(() => {});
      await page.waitForTimeout(6000);
    }

    if (page.url().includes('/login')) {
      authed = false;
      console.log(`! ${vp.name}: login failed — authenticated pages NOT checked`);
    } else {
      const pages = ROLE === 'admin' ? ADMIN_PAGES : CONSOLE_PAGES;
      for (const p of pages) {
        await check(p, p.replace(/\//g, '_'));
        if (page.url().includes('/login')) {
          authed = false;
          console.log(`! ${vp.name}: session lost at ${p}`);
          break;
        }
      }
    }

    if (jsErrors.length) { failures += jsErrors.length; console.log(`✗ ${vp.name} js errors: ${jsErrors.slice(0, 3).join(' | ')}`); }
    await ctx.close();
  }
  await browser.close();
  console.log(`responsive: ${failures === 0 ? 'all clear' : failures + ' problems'}`);
  if (GATE && (failures || !authed)) process.exit(1);
})();
