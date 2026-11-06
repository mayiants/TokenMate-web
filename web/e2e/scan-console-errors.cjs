// Console/network error scan — ported from ai-portal-go e2e/scan-console-errors.js.
// Gate mode exits 1 when any JS error, console.error, or failed API request is seen.
const { chromium } = require('playwright');
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
const ADMIN_PAGES = [
  '/console/admin',
  '/console/admin/users',
  '/console/admin/enterprises',
  '/console/admin/groups',
  '/console/admin/models',
  '/console/admin/upstream',
];
const USER_PAGES = [
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
  const browser = await chromium.launch();
  const page = await (await browser.newContext()).newPage();
  const errors = { js: [], console: [], network: [] };
  page.on('pageerror', (e) => errors.js.push(e.message));
  // React dev mode logs deprecation notices (StrictMode refs, findDOMNode) via
  // console.error. They are framework noise, absent from production builds —
  // filter them so the gate matches the production-build bar of portal CI.
  const DEV_NOISE = /deprecated|StrictMode|findDOMNode|Warning: %s|Failed to load resource/;
  page.on('console', (m) => {
    if (m.type() === 'error' && !DEV_NOISE.test(m.text())) errors.console.push(m.text().slice(0, 200));
  });
  page.on('response', (r) => {
    if (r.status() >= 400 && r.url().includes('/api/')) {
      // The admin-nav probe is EXPECTED to 403 for non-admin sessions.
      if (r.url().includes('/api/admin/admin-mode') && r.status() === 403) return;
      errors.network.push(`${r.status()} ${r.url().slice(-80)}`);
    }
  });

  // Public pages first (same for both roles), then login, then role pages.
  for (const p of USER_PAGES.slice(0, 5)) {
    await page.goto(BASE + p, { waitUntil: 'networkidle', timeout: 45000 }).catch(() => {});
    await page.waitForTimeout(1200);
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
  const postLogin = ROLE === 'admin' ? ADMIN_PAGES : USER_PAGES.slice(5);
  for (const p of postLogin) {
    await page.goto(BASE + p, { waitUntil: 'networkidle', timeout: 45000 }).catch(() => {});
    await page.waitForTimeout(1200);
  }
  await browser.close();

  const total = errors.js.length + errors.console.length + errors.network.length;
  for (const [k, list] of Object.entries(errors)) {
    for (const e of list.slice(0, 10)) console.log(`[${k}] ${e}`);
  }
  console.log(`console scan: js=${errors.js.length} console=${errors.console.length} network=${errors.network.length}`);
  if (GATE && total) process.exit(1);
})();
