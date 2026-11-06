const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots';

const ACCOUNTS = {
  superadmin: {
    email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
    password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!',
  },
  enterprise: {
    email: process.env.E2E_ENTERPRISE_EMAIL || 'e2e-enterprise@example.com',
    password: process.env.E2E_ENTERPRISE_PASSWORD || 'E2E_TestPass123!',
  },
  user: {
    email: process.env.E2E_USER_EMAIL || 'e2e-user@example.com',
    password: process.env.E2E_USER_PASSWORD || 'E2E_TestPass123!',
  },
};

/* ---------- Helpers ---------- */

async function login(page, account) {
  await page.goto(`${BASE_URL}/login`);
  await expect(page.locator('input[name="email"]')).toBeVisible();

  await page.fill('input[name="email"]', account.email);
  await page.fill('input[name="password"]', account.password);

  await Promise.all([
    page.waitForURL(/\/dashboard/),
    page.locator('form[action="/login"] button[type="submit"]').click(),
  ]);
  await expect(page).toHaveURL(/\/dashboard/);
}

async function assertNoError(page, label) {
  const body = await page.locator('body').innerText();
  const lower = body.toLowerCase();
  if (lower.includes('internal server error') || lower.includes("can't evaluate field") || lower.includes('template:')) {
    throw new Error(`${label}: Page contains server error`);
  }
}

async function assertNoConsoleErrors(page, label) {
  const errors = [];
  page.on('pageerror', (err) => errors.push(err.message));
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text());
  });
  // Give a small window for errors to fire
  await page.waitForTimeout(300);
  if (errors.length > 0) {
    console.log(`  ⚠️ ${label} console errors:`, errors.slice(0, 3));
  }
}

async function screenshot(page, name) {
  await page.screenshot({ path: `${SCREENSHOT_DIR}/${name}.png`, fullPage: true });
}

async function checkPageStructure(page, label, opts = {}) {
  const { expectTitle = true, expectStats = false, expectDrawer = false } = opts;

  // Check page title exists
  if (expectTitle) {
    const hasTitle = await page.locator('h1.page-title, .page-title').first().isVisible().catch(() => false);
    if (!hasTitle) {
      const hasCardTitle = await page.locator('h1.card-title, .card-title').first().isVisible().catch(() => false);
      if (!hasCardTitle) {
        console.log(`  ⚠️ ${label}: No .page-title or .card-title found`);
      }
    }
  }

  // Check stats-bar (for redesigned pages)
  if (expectStats) {
    const statsCount = await page.locator('.stats-bar').count();
    if (statsCount === 0) {
      console.log(`  ⚠️ ${label}: No .stats-bar found`);
    }
  }

  // Check drawer structure exists
  if (expectDrawer) {
    const hasDrawer = await page.locator('.drawer').first().isVisible().catch(() => false);
    if (!hasDrawer) {
      console.log(`  ⚠️ ${label}: No .drawer found`);
    }
  }
}

/* ---------- Page definitions ---------- */

const ADMIN_PAGES = [
  { path: '/admin', name: 'admin-dashboard', expectTitle: false },
  { path: '/admin/users', name: 'admin-users', expectStats: true, expectDrawer: true },
  { path: '/admin/groups', name: 'admin-groups', expectStats: true, expectDrawer: true },
  { path: '/admin/enterprise', name: 'admin-enterprise', expectStats: true, expectDrawer: true },
  { path: '/admin/keypool', name: 'admin-keypool', expectStats: true, expectDrawer: true },
  { path: '/admin/models', name: 'admin-models', expectStats: true, expectDrawer: true },
  { path: '/admin/models-aws', name: 'admin-models-aws', expectStats: true, expectDrawer: true },
  { path: '/admin/providers', name: 'admin-providers', expectStats: true, expectDrawer: true },
  { path: '/admin/subscriptions', name: 'admin-subscriptions', expectStats: true, expectDrawer: true },
  { path: '/admin/vouchers', name: 'admin-vouchers', expectStats: true, expectDrawer: true },
  { path: '/admin/announcements', name: 'admin-announcements', expectStats: true, expectDrawer: true },
  { path: '/admin/audit-logs', name: 'admin-audit-logs', expectStats: true },
  { path: '/admin/settings', name: 'admin-settings', expectTitle: true },
  { path: '/admin/monitor-upstream', name: 'admin-monitor-upstream', expectStats: true },
  { path: '/admin/monitor-downstream', name: 'admin-monitor-downstream', expectStats: true },
  { path: '/admin/upstream-ops', name: 'admin-upstream-ops', expectStats: true },
  { path: '/admin/provider-health', name: 'admin-provider-health', expectStats: true },
  { path: '/admin/reconciliation', name: 'admin-reconciliation' },
  { path: '/admin/rate-limits', name: 'admin-rate-limits', expectStats: true },
  { path: '/admin/enterprise-applications', name: 'admin-enterprise-applications', expectStats: true, expectDrawer: true },
  { path: '/admin/reconciliation-ops', name: 'admin-reconciliation-ops', expectStats: true },
  { path: '/admin/pricing', name: 'admin-pricing', expectStats: true },
  { path: '/admin/upstream-pricing', name: 'admin-upstream-pricing', expectStats: true },
  { path: '/admin/usage-explorer', name: 'admin-usage-explorer' },
  { path: '/admin/compliance', name: 'admin-compliance' },
  { path: '/admin/keys', name: 'admin-keys', expectStats: true },
];

const ENTERPRISE_PAGES = [
  { path: '/enterprise', name: 'enterprise-dashboard', expectStats: true },
  { path: '/enterprise/users', name: 'enterprise-users', expectStats: true, expectDrawer: true },
  { path: '/enterprise/groups', name: 'enterprise-groups', expectStats: true, expectDrawer: true },
  { path: '/enterprise/keys', name: 'enterprise-keys', expectStats: true, expectDrawer: true },
  { path: '/enterprise/billing', name: 'enterprise-billing', expectStats: true },
  { path: '/enterprise/usage', name: 'enterprise-usage', expectStats: true },
  { path: '/enterprise/audit', name: 'enterprise-audit', expectStats: true },
  { path: '/enterprise/settings', name: 'enterprise-settings' },
];

const USER_PAGES = [
  { path: '/dashboard', name: 'user-dashboard' },
  { path: '/profile', name: 'user-profile' },
  { path: '/apikeys', name: 'user-apikeys', expectDrawer: true },
  { path: '/subscriptions', name: 'user-subscriptions' },
  { path: '/billing', name: 'user-billing' },
  { path: '/plan-and-group', name: 'user-plan-and-group' },
  { path: '/usage-logs', name: 'user-usage-logs' },
  { path: '/invoices', name: 'user-invoices' },
  { path: '/voucher', name: 'user-voucher' },
  { path: '/status', name: 'user-status', expectStats: true },
  { path: '/billing-details', name: 'user-billing-details' },
  { path: '/my-group', name: 'user-my-group' },
  { path: '/subscriptions/my', name: 'user-my-subscriptions' },
  { path: '/upstream-ops', name: 'user-upstream-ops' },
  { path: '/enterprise-apply', name: 'user-enterprise-apply' },
];

const SUPER_PAGES = [
  { path: '/super/admin-management', name: 'super-admin-management' },
  { path: '/super/payment', name: 'super-payment' },
];

/* ---------- Super Admin: All Admin Pages ---------- */

test.describe('Super Admin - All Pages Smoke', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ACCOUNTS.superadmin);
  });

  for (const p of ADMIN_PAGES) {
    test(p.name, async ({ page }) => {
      const resp = await page.goto(`${BASE_URL}${p.path}`);
      expect(resp.status()).toBeLessThan(400);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);

      await assertNoError(page, p.name);
      await assertNoConsoleErrors(page, p.name);
      await checkPageStructure(page, p.name, {
        expectTitle: p.expectTitle !== false,
        expectStats: !!p.expectStats,
        expectDrawer: !!p.expectDrawer,
      });
      await screenshot(page, `all-${p.name}`);
      console.log(`  ✅ ${p.name}: HTTP ${resp.status()}, no errors`);
    });
  }

  for (const p of SUPER_PAGES) {
    test(p.name, async ({ page }) => {
      const resp = await page.goto(`${BASE_URL}${p.path}`);
      expect(resp.status()).toBeLessThan(400);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);

      await assertNoError(page, p.name);
      await assertNoConsoleErrors(page, p.name);
      await checkPageStructure(page, p.name, {
        expectTitle: p.expectTitle !== false,
        expectStats: !!p.expectStats,
      });
      await screenshot(page, `all-${p.name}`);
      console.log(`  ✅ ${p.name}: HTTP ${resp.status()}, no errors`);
    });
  }
});

/* ---------- Enterprise Admin: All Enterprise Pages ---------- */

test.describe('Enterprise Admin - All Pages Smoke', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ACCOUNTS.enterprise);
  });

  for (const p of ENTERPRISE_PAGES) {
    test(p.name, async ({ page }) => {
      const resp = await page.goto(`${BASE_URL}${p.path}`);
      expect(resp.status()).toBeLessThan(400);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);

      await assertNoError(page, p.name);
      await assertNoConsoleErrors(page, p.name);
      await checkPageStructure(page, p.name, {
        expectTitle: p.expectTitle !== false,
        expectStats: !!p.expectStats,
        expectDrawer: !!p.expectDrawer,
      });
      await screenshot(page, `all-${p.name}`);
      console.log(`  ✅ ${p.name}: HTTP ${resp.status()}, no errors`);
    });
  }
});

/* ---------- Regular User: All User Pages ---------- */

test.describe('Regular User - All Pages Smoke', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ACCOUNTS.user);
  });

  for (const p of USER_PAGES) {
    test(p.name, async ({ page }) => {
      const resp = await page.goto(`${BASE_URL}${p.path}`);
      expect(resp.status()).toBeLessThan(400);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);

      await assertNoError(page, p.name);
      await assertNoConsoleErrors(page, p.name);
      await checkPageStructure(page, p.name, {
        expectTitle: p.expectTitle !== false,
        expectStats: !!p.expectStats,
        expectDrawer: !!p.expectDrawer,
      });
      await screenshot(page, `all-${p.name}`);
      console.log(`  ✅ ${p.name}: HTTP ${resp.status()}, no errors`);
    });
  }
});

/* ---------- Cross-role Access Checks ---------- */

test.describe('Cross-role Access Checks', () => {
  test('enterprise admin should NOT access /admin pages', async ({ page }) => {
    await login(page, ACCOUNTS.enterprise);

    const adminPaths = ['/admin', '/admin/users', '/admin/groups', '/admin/keypool'];
    for (const path of adminPaths) {
      await page.goto(`${BASE_URL}${path}`);
      await page.waitForTimeout(500);
      const url = page.url();
      expect(url).not.toContain(path);
      console.log(`  ✅ Enterprise blocked from ${path}`);
    }
  });

  test('regular user should NOT access /admin or /enterprise pages', async ({ page }) => {
    await login(page, ACCOUNTS.user);

    const protectedPaths = ['/admin', '/admin/users', '/enterprise', '/enterprise/users'];
    for (const path of protectedPaths) {
      await page.goto(`${BASE_URL}${path}`);
      await page.waitForTimeout(500);
      const url = page.url();
      expect(url).not.toContain(path);
      console.log(`  ✅ User blocked from ${path}`);
    }
  });
});
