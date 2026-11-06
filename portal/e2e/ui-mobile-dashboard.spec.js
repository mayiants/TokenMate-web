const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots/mobile';

const ACCOUNTS = {
  user: {
    email: process.env.E2E_USER_EMAIL || 'e2e-enterprise@example.com',
    password: process.env.E2E_USER_PASSWORD || 'E2E_TestPass123!',
  },
  superadmin: {
    email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
    password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!',
  },
};

const MOBILE_VIEWPORTS = [
  { name: 'iphone-se', width: 375, height: 667 },
  { name: 'iphone-14', width: 390, height: 844 },
  { name: 'pixel-7', width: 412, height: 915 },
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
  await expect(page).toHaveURL(/\/dashboard/);
}

async function screenshot(page, name) {
  await page.screenshot({ path: `${SCREENSHOT_DIR}/${name}.png`, fullPage: true });
}

for (const vp of MOBILE_VIEWPORTS) {
  test.describe(`Mobile Dashboard - ${vp.name} (${vp.width}x${vp.height})`, () => {
    test.use({ viewport: { width: vp.width, height: vp.height } });

    test('user dashboard', async ({ page }) => {
      await login(page, ACCOUNTS.user);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);
      await screenshot(page, `mobile-${vp.name}-user-dashboard`);
      console.log(`  ✅ ${vp.name} user dashboard screenshot saved`);
    });

    test('admin dashboard', async ({ page }) => {
      await login(page, ACCOUNTS.superadmin);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);
      await screenshot(page, `mobile-${vp.name}-admin-dashboard`);
      console.log(`  ✅ ${vp.name} admin dashboard screenshot saved`);
    });
  });
}
