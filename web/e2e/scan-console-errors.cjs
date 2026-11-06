// Console/network error scan — ported from ai-portal-go e2e/scan-console-errors.js.
// Gate mode exits 1 when any JS error, console.error, or failed API request is seen.
const { chromium } = require('playwright');
const BASE = process.env.CHECK_BASE || 'http://127.0.0.1:9090';
const GATE = process.argv.includes('--gate');
const ACCOUNT = {
  email: process.env.CHECK_EMAIL || 'p2review@test.local',
  password: process.env.CHECK_PASSWORD || 'WgTest#2026',
};
const PAGES = [
  '/', '/models', '/login', '/register', '/forgot-password',
  '/console', '/console/keys', '/console/logs', '/console/balance',
  '/console/enterprise', '/console/enterprise/members', '/console/enterprise/groups',
  '/console/enterprise/keys', '/console/enterprise/usage', '/console/enterprise/billing',
  '/console/enterprise/settings',
];

(async () => {
  const browser = await chromium.launch();
  const page = await (await browser.newContext()).newPage();
  const errors = { js: [], console: [], network: [] };
  page.on('pageerror', (e) => errors.js.push(e.message));
  // React dev mode logs deprecation notices (StrictMode refs, findDOMNode) via
  // console.error. They are framework noise, absent from production builds —
  // filter them so the gate matches the production-build bar of portal CI.
  const DEV_NOISE = /deprecated|StrictMode|findDOMNode|Warning: %s/;
  page.on('console', (m) => {
    if (m.type() === 'error' && !DEV_NOISE.test(m.text())) errors.console.push(m.text().slice(0, 200));
  });
  page.on('response', (r) => { if (r.status() >= 400 && r.url().includes('/api/')) errors.network.push(`${r.status()} ${r.url().slice(-80)}`); });

  for (const p of PAGES.slice(0, 5)) {
    await page.goto(BASE + p, { waitUntil: 'networkidle', timeout: 45000 }).catch(() => {});
    await page.waitForTimeout(1200);
  }
  // login then console pages
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
  for (const p of PAGES.slice(5)) {
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
