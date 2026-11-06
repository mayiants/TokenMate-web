const { test, expect } = require('@playwright/test');

// Use a seeded admin JWT from the environment; do not hard-code real tokens in git.
const ADMIN_TOKEN = process.env.E2E_ADMIN_JWT_TOKEN || '';

test.describe('Voucher pages (merged from redemption + invitation)', () => {
  test.beforeEach(async ({ page, context }) => {
    test.skip(!ADMIN_TOKEN, 'E2E_ADMIN_JWT_TOKEN is not set');
    await context.addCookies([{
      name: 'jwt',
      value: ADMIN_TOKEN,
      domain: '127.0.0.1',
      path: '/',
      httpOnly: false,
      secure: false,
      sameSite: 'Lax'
    }]);
  });

  test('admin vouchers page should not show undefined', async ({ page }) => {
    await page.goto('/admin/vouchers');
    await page.waitForTimeout(2000);

    const pageText = await page.locator('body').textContent();
    expect(pageText).not.toContain('undefined');

    await page.screenshot({ path: 'e2e/screenshots/admin-vouchers.png', fullPage: true });
  });

  test('user voucher page should not show undefined', async ({ page }) => {
    await page.goto('/voucher');
    await page.waitForTimeout(2000);

    const pageText = await page.locator('body').textContent();
    expect(pageText).not.toContain('undefined');

    await page.screenshot({ path: 'e2e/screenshots/user-voucher.png', fullPage: true });
  });

  test('admin announcements page should not show undefined', async ({ page }) => {
    await page.goto('/admin/announcements');
    await page.waitForTimeout(2000);

    const pageText = await page.locator('body').textContent();
    expect(pageText).not.toContain('undefined');

    await page.screenshot({ path: 'e2e/screenshots/admin-announcements.png', fullPage: true });
  });
});
