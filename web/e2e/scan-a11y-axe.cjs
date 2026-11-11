// Accessibility scan for woodgit-web — ported from ai-portal-go e2e/scan-a11y-axe.js.
// Gate mode fails on serious/critical violations, and also fails when the
// authenticated half of the run could not log in (silently scanning the login
// page for /console/* would look green while covering nothing).
// Prereq: an app server reachable at CHECK_BASE (default http://127.0.0.1:9090)
// that proxies /api to a running portal API.
const { chromium } = require('playwright');
const { AxeBuilder } = require('@axe-core/playwright');
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

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  const report = [];

  const scan = async (path) => {
    await page.goto(BASE + path, { waitUntil: 'networkidle', timeout: 45000 }).catch(() => {});
    await page.waitForTimeout(1500);
    const res = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa'])
      // Semi component-library internals are out of our control (and Semi's
      // own a11y debt): nav list markup, decorative icons, combobox aria
      // pointing at unmounted popups. Everything we render stays scanned.
      .exclude('.wg-console-sider')
      .exclude('.wg-console-header')
      .exclude('.semi-icon')
      .exclude('.semi-select')
      .exclude('.semi-page')
      .analyze();
    const gateHits = res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
    report.push({ path, violations: res.violations.length, gateHits });
    if (gateHits.length) {
      console.log(`✗ ${path}`);
      for (const v of gateHits) {
        console.log(`   [${v.impact}] ${v.id}: ${v.help} (${v.nodes.length} nodes)`);
        for (const n of v.nodes.slice(0, 3)) console.log(`      → ${n.target}`);
      }
    } else {
      console.log(`✓ ${path} (violations: ${res.violations.length})`);
    }
  };

  for (const p of PUBLIC_PAGES) await scan(p);

  // CHECK_PUBLIC_ONLY=1: no backend available (e.g. GitHub CI) — public pages
  // only, and the missing authenticated coverage is reported as a skip rather
  // than a failure.
  if (PUBLIC_ONLY) {
    await browser.close();
    const failedPublic = report.filter((r) => r.gateHits.length > 0);
    console.log(
      `\naxe scan (public only): ${report.length} pages, ${failedPublic.length} with serious/critical violations`
    );
    if (GATE && failedPublic.length) process.exit(1);
    return;
  }

  // login for console pages
  await page.goto(BASE + '/login', { waitUntil: 'networkidle' }).catch(() => {});
  await page.waitForTimeout(2500);
  const emailInput = page.locator('input').nth(0);
  const passInput = page.locator('input[type="password"]');
  if ((await emailInput.count()) && (await passInput.count())) {
    await emailInput.fill(ACCOUNT.email);
    await passInput.fill(ACCOUNT.password);
    await page.click('button[type="submit"], button:has-text("登录"), button:has-text("Sign in")').catch(() => {});
    await page.waitForTimeout(5000);
  }

  const authed = !page.url().includes('/login');
  if (!authed) {
    console.log(
      `! login as ${ACCOUNT.email} failed — authenticated pages NOT scanned. ` +
        'Point CHECK_BASE at a server whose /api proxy reaches a running portal API.'
    );
  } else {
    const pages = ROLE === 'admin' ? ADMIN_PAGES : CONSOLE_PAGES;
    for (const p of pages) {
      await scan(p);
      if (page.url().includes('/login')) {
        console.log('! session lost mid-run — remaining pages not scanned');
        break;
      }
    }
  }

  await browser.close();
  const failed = report.filter((r) => r.gateHits.length > 0);
  console.log(`\naxe scan: ${report.length} pages, ${failed.length} with serious/critical violations`);
  if (GATE && (failed.length || !authed)) process.exit(1);
})();
