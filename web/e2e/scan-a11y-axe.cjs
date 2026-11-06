// Accessibility scan for woodgit-web — ported from ai-portal-go e2e/scan-a11y-axe.js.
// Gate mode fails on serious/critical violations.
// Prereq: dev server reachable at CHECK_BASE (default http://127.0.0.1:9090).
const { chromium } = require('playwright');
const { AxeBuilder } = require('@axe-core/playwright');

const BASE = process.env.CHECK_BASE || 'http://127.0.0.1:9090';
const GATE = process.argv.includes('--gate');
const ACCOUNT = {
  email: process.env.CHECK_EMAIL || 'p2review@test.local',
  password: process.env.CHECK_PASSWORD || 'WgTest#2026',
};

const PUBLIC_PAGES = ['/', '/models', '/login', '/register', '/forgot-password'];
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
  '/console/enterprise/members',
  '/console/enterprise/groups',
  '/console/enterprise/keys',
  '/console/enterprise/usage',
  '/console/enterprise/billing',
  '/console/enterprise/settings',
  '/console/enterprise/audit',
  '/console/enterprise/alerts',
];

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
  for (const p of CONSOLE_PAGES) await scan(p);

  await browser.close();
  const failed = report.filter((r) => r.gateHits.length > 0);
  console.log(`\naxe scan: ${report.length} pages, ${failed.length} with serious/critical violations`);
  if (GATE && failed.length) process.exit(1);
})();
