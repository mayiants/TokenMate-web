const { test, expect } = require('@playwright/test');

const BASE_URL = process.env.PORTAL_URL || 'http://127.0.0.1:3001';

const ACCOUNTS = {
  admin: {
    email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
    password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!',
  },
  user: {
    email: process.env.E2E_USER_EMAIL || 'e2e-user@example.com',
    password: process.env.E2E_USER_PASSWORD || 'E2E_TestPass123!',
  },
};

async function login(page, account) {
  await page.goto(`${BASE_URL}/login`);
  await page.fill('input[name="email"]', account.email);
  await page.fill('input[name="password"]', account.password);
  await page.locator('form[action="/login"] button[type="submit"]').click();
  // Wait for redirect or error
  await page.waitForTimeout(2000);
}

async function isLoggedIn(page) {
  const url = page.url();
  return !url.includes('/login');
}

function assertNoServerError(body) {
  const lower = body.toLowerCase();
  if (lower.includes('internal server error') || lower.includes("can't evaluate field") || lower.includes('template:')) {
    throw new Error('Page contains server error');
  }
}

test.describe('Enterprise Self-Registration', () => {
  test('User enterprise-apply page renders without errors', async ({ page }) => {
    await login(page, ACCOUNTS.user);
    if (!(await isLoggedIn(page))) {
      test.skip(true, 'User account not available in this environment');
      return;
    }

    await page.goto(`${BASE_URL}/enterprise-apply`);
    await page.waitForLoadState('networkidle');

    const body = await page.locator('body').innerText();
    assertNoServerError(body);

    // Page should contain the title
    expect(body).toContain('Enterprise');
  });

  test('User sees enterprise registration form or status', async ({ page }) => {
    await login(page, ACCOUNTS.user);
    if (!(await isLoggedIn(page))) {
      test.skip(true, 'User account not available');
      return;
    }

    await page.goto(`${BASE_URL}/enterprise-apply`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(1000); // wait for JS to load application status

    // After JS loads, either form or status should be visible
    // The page is a multi-step wizard: exactly one of these views is shown
    // depending on the application state (new / in review / edit).
    const views = ['#step-1-view', '#step-2-view', '#edit-info-view'];
    let formVisible = false;
    for (const v of views) { if (await page.locator(v).isVisible()) { formVisible = true; break; } }
    const statusVisible = await page.locator('#step-indicator').isVisible();
    expect(formVisible || statusVisible).toBeTruthy();
  });

  test('Admin enterprise-applications page renders without errors', async ({ page }) => {
    await login(page, ACCOUNTS.admin);
    if (!(await isLoggedIn(page))) {
      test.skip(true, 'Admin account not available in this environment');
      return;
    }

    await page.goto(`${BASE_URL}/admin/enterprise-applications`);
    await page.waitForLoadState('networkidle');

    const body = await page.locator('body').innerText();
    assertNoServerError(body);

    // Should see the applications table
    await expect(page.locator('#app-table')).toBeVisible();
  });

  test('Enterprise apply page has no console errors', async ({ page }) => {
    const errors = [];
    page.on('pageerror', (err) => errors.push(err.message));

    await login(page, ACCOUNTS.user);
    if (!(await isLoggedIn(page))) {
      test.skip(true, 'User account not available');
      return;
    }

    await page.goto(`${BASE_URL}/enterprise-apply`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(1000);

    expect(errors.length).toBe(0);
  });

  test('Admin enterprise-applications page has no console errors', async ({ page }) => {
    const errors = [];
    page.on('pageerror', (err) => errors.push(err.message));

    await login(page, ACCOUNTS.admin);
    if (!(await isLoggedIn(page))) {
      test.skip(true, 'Admin account not available');
      return;
    }

    await page.goto(`${BASE_URL}/admin/enterprise-applications`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(1000);

    expect(errors.length).toBe(0);
  });
});
