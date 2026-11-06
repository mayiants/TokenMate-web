const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots';

const ACCOUNTS = {
  superadmin: {
    email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
    password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!',
  },
};

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
  await page.waitForTimeout(300);
  if (errors.length > 0) {
    console.log(`  ⚠️ ${label} console errors:`, errors.slice(0, 3));
  }
}

async function screenshot(page, name) {
  await page.screenshot({ path: `${SCREENSHOT_DIR}/${name}.png`, fullPage: true });
}

test.describe('Auth Pages Flow', () => {
  test('landing page loads', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/`);
    expect(resp.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
    await assertNoError(page, 'landing');
    await assertNoConsoleErrors(page, 'landing');
    await screenshot(page, 'auth-landing');
    console.log('  ✅ landing: page loads');
  });

  test('login page loads and authenticates', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/login`);
    expect(resp.status()).toBeLessThan(400);
    await expect(page.locator('input[name="email"]')).toBeVisible();
    await assertNoError(page, 'login');
    await assertNoConsoleErrors(page, 'login');
    await screenshot(page, 'auth-login');

    await page.fill('input[name="email"]', ACCOUNTS.superadmin.email);
    await page.fill('input[name="password"]', ACCOUNTS.superadmin.password);
    await Promise.all([
      page.waitForURL(/\/dashboard/),
      page.locator('form[action="/login"] button[type="submit"]').click(),
    ]);
    await expect(page).toHaveURL(/\/dashboard/);
    console.log('  ✅ login: authentication works');
  });

  test('register page loads', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/register`);
    expect(resp.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
    await assertNoError(page, 'register');
    await assertNoConsoleErrors(page, 'register');
    await screenshot(page, 'auth-register');
    console.log('  ✅ register: page loads');
  });

  test('forgot-password page loads', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/forgot-password`);
    expect(resp.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
    await assertNoError(page, 'forgot-password');
    await assertNoConsoleErrors(page, 'forgot-password');
    await screenshot(page, 'auth-forgot-password');
    console.log('  ✅ forgot-password: page loads');
  });
});
