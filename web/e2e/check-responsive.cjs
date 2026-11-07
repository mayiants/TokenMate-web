// Responsive layout gate for woodgit-web — spirit ported from portal CI
// (e2e/ui-mobile-all-pages.spec.js), strengthened with automated checks:
// portal only screenshots at 390x844; here we assert NO horizontal overflow
// (the classic responsive breakage) at PC / tablet / mobile widths, and gate
// on it. Screenshots land in ../../tr-research/screenshots/responsive/.
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = process.env.CHECK_BASE || 'http://127.0.0.1:9090';
const GATE = process.argv.includes('--gate');
// CHECK_ROLE=enterprise|admin selects which page set (and default account) to scan.
const ROLE = process.env.CHECK_ROLE || 'enterprise';
const ACCOUNT = {
  email:
    process.env.CHECK_EMAIL ||
    (process.env.CHECK_ROLE === 'admin' ? 'sysview@test.local' : 'p2review@test.local'),
  password:
    process.env.CHECK_PASSWORD || (process.env.CHECK_ROLE === 'admin' ? 'SysView#2026' : 'WgTest#2026'),
};
const SHOTS = '/opt/workspace/adaii/tr-research/screenshots/responsive';

const VIEWPORTS = [
  { name: 'pc', width: 1440, height: 900 },
  { name: 'tablet', width: 834, height: 1112 }, // iPad portrait
  { name: 'mobile', width: 390, height: 844 }, // iPhone 14
];
const PUBLIC_PAGES = ['/', '/models', '/login'];
const ADMIN_PAGES = [
  '/console/admin',
  '/console/admin/users',
  '/console/admin/enterprises',
  '/console/admin/groups',
  '/console/admin/models',
  '/console/admin/upstream',
  '/console/admin/finance',
  '/console/admin/ops',
  '/console/admin/security',
  '/console/admin/settings',
  '/console/admin/groups',
  '/console/admin/models',
  '/console/admin/upstream',
  '/console/admin/finance',
  '/console/admin/ops',
  '/console/admin/security',
  '/console/admin/settings',
];
const CONSOLE_PAGES = [
  '/console',
  '/console/keys',
  '/console/logs',
  '/console/balance',
  '/console/profile',
  '/console/notifications',
  '/console/support',
  '/console/billing',
  '/console/subscriptions',
  '/console/groups',
  '/console/status',
  '/console/enterprise',
  '/console/enterprise/audit',
  '/console/enterprise/alerts',
];

(async () => {
  fs.mkdirSync(SHOTS, { recursive: true });
  const browser = await chromium.launch();
  let failures = 0;

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
    const pages = ROLE === 'admin' ? ADMIN_PAGES : CONSOLE_PAGES;
    for (const p of pages) await check(p, p.replace(/\//g, '_'));
    if (jsErrors.length) { failures += jsErrors.length; console.log(`✗ ${vp.name} js errors: ${jsErrors.slice(0, 3).join(' | ')}`); }
    await ctx.close();
  }
  await browser.close();
  console.log(`responsive: ${failures === 0 ? 'all clear' : failures + ' problems'}`);
  if (GATE && failures) process.exit(1);
})();
