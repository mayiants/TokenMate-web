const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots/mobile';

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

const MOBILE_VIEWPORT = { width: 390, height: 844 };

const USER_PAGES = [
  '/dashboard',
  '/profile',
  '/apikeys',
  '/subscriptions',
  '/billing',
  '/plan-and-group',
  '/status',
  '/usage-logs',
  '/invoices',
  '/voucher',
];

const ADMIN_PAGES = [
  '/admin',
  '/admin/users',
  '/admin/groups',
  '/admin/enterprise',
  '/admin/keypool',
  '/admin/models',
  '/admin/models-aws',
  '/admin/providers',
  '/admin/subscriptions',
  '/admin/vouchers',
  '/admin/announcements',
  '/admin/audit-logs',
  '/admin/settings',
  '/admin/monitor-upstream',
  '/admin/monitor-downstream',
  '/admin/upstream-ops',
  '/admin/provider-health',
  '/admin/reconciliation',
  '/super/admin-management',
  '/super/payment',
];

const ENTERPRISE_PAGES = [
  '/enterprise',
  '/enterprise/users',
  '/enterprise/groups',
  '/enterprise/keys',
  '/enterprise/billing',
  '/enterprise/usage',
  '/enterprise/audit',
];

async function login(page, account) {
  await page.goto(`${BASE_URL}/login`);
  await expect(page.locator('input[name="email"]')).toBeVisible();
  await page.fill('input[name="email"]', account.email);
  await page.fill('input[name="password"]', account.password);
  await Promise.all([
    page.waitForURL(/\/dashboard/),
    page.locator('form[action="/login"] button[type="submit"]').click(),
  ]);
}

test.describe(`Mobile All Pages - ${MOBILE_VIEWPORT.width}x${MOBILE_VIEWPORT.height}`, () => {
  test.use({ viewport: MOBILE_VIEWPORT });

  for (const path of USER_PAGES) {
    test(`user ${path}`, async ({ page }) => {
      await login(page, ACCOUNTS.user);
      await page.goto(`${BASE_URL}${path}`);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);
      const safeName = path.replace(/\//g, '_') || '_root';
      await page.screenshot({ path: `${SCREENSHOT_DIR}/mobile-user${safeName}.png`, fullPage: true });
      console.log(`  ✅ mobile user ${path}`);
    });
  }

  for (const path of ADMIN_PAGES) {
    test(`admin ${path}`, async ({ page }) => {
      await login(page, ACCOUNTS.superadmin);
      await page.goto(`${BASE_URL}${path}`);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);
      const safeName = path.replace(/\//g, '_') || '_root';
      await page.screenshot({ path: `${SCREENSHOT_DIR}/mobile-admin${safeName}.png`, fullPage: true });
      console.log(`  ✅ mobile admin ${path}`);
    });
  }

  for (const path of ENTERPRISE_PAGES) {
    test(`enterprise ${path}`, async ({ page }) => {
      await login(page, ACCOUNTS.enterprise);
      await page.goto(`${BASE_URL}${path}`);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);
      const safeName = path.replace(/\//g, '_') || '_root';
      await page.screenshot({ path: `${SCREENSHOT_DIR}/mobile-enterprise${safeName}.png`, fullPage: true });
      console.log(`  ✅ mobile enterprise ${path}`);
    });
  }

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
