const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots';

const SUPERADMIN = {
  email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
  password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!'
};

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

async function switchLang(page, lang) {
  // Navigate to dashboard, switch language via the lang switcher
  await page.goto(`${BASE_URL}/dashboard`);
  await page.waitForLoadState('networkidle');
  await page.evaluate((l) => {
    document.cookie = 'locale=' + l + '; path=/';
  }, lang);
  // Reload to apply cookie
  await page.reload();
  await page.waitForLoadState('networkidle');
}

async function screenshot(page, name) {
  await page.screenshot({ path: `${SCREENSHOT_DIR}/${name}.png`, fullPage: true });
}

/* ---------- Test: Public Subscriptions Page ---------- */

test.describe('Subscriptions Page - Plan Display', () => {
  test('should show 4 token plans with correct names and durations in English mode', async ({ page }) => {
    await login(page, SUPERADMIN);
    await switchLang(page, 'en');
    await screenshot(page, 'subs-00-english-lang');

    // Navigate to public subscriptions page
    await page.goto(`${BASE_URL}/subscriptions`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(1000);
    await screenshot(page, 'subs-01-english-page');

    // Verify page title
    await expect(page.locator('body')).toContainText(/Subscription|订阅/);

    // Verify 4 plan names are displayed
    const body = await page.locator('body').innerText();
    expect(body).toContain('Lite');
    expect(body).toContain('Standard');
    expect(body).toContain('Pro');
    expect(body).toContain('Max');

    // Verify duration shows "1 Month" in English mode
    const durationElements = page.locator('.plan-duration');
    const count = await durationElements.count();
    expect(count).toBeGreaterThanOrEqual(4);
    // Check at least one says "1 Month" (or "Per Month")
    let foundMonth = false;
    for (let i = 0; i < count; i++) {
      const text = await durationElements.nth(i).innerText();
      if (text.includes('Month')) {
        foundMonth = true;
        break;
      }
    }
    expect(foundMonth).toBe(true);

    // Verify dynamic descriptions contain expected values
    const planCards = page.locator('.plan-card');
    const cardCount = await planCards.count();
    expect(cardCount).toBeGreaterThanOrEqual(4);

    // Check first card has dynamic description with tokens/month
    const firstCardDesc = await planCards.first().locator('.plan-desc').innerText();
    expect(firstCardDesc).toMatch(/Tokens\/month|M\s*Tokens|times\/month/);

    console.log('  ✅ Subscriptions page: 4 plans visible, English duration, dynamic descriptions');
    await screenshot(page, 'subs-02-english-verified');
  });

  test('should show 4 token plans with correct names and durations in Chinese mode', async ({ page }) => {
    await login(page, SUPERADMIN);
    await switchLang(page, 'zh');
    await screenshot(page, 'subs-10-chinese-lang');

    // Navigate to public subscriptions page
    await page.goto(`${BASE_URL}/subscriptions`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(1000);
    await screenshot(page, 'subs-11-chinese-page');

    // Verify 4 plan names
    const body = await page.locator('body').innerText();
    expect(body).toContain('Lite');
    expect(body).toContain('Standard');
    expect(body).toContain('Pro');
    expect(body).toContain('Max');

    // Verify duration shows "1 个月" in Chinese mode
    const durationElements = page.locator('.plan-duration');
    const count = await durationElements.count();
    expect(count).toBeGreaterThanOrEqual(4);
    let foundMonth = false;
    for (let i = 0; i < count; i++) {
      const text = await durationElements.nth(i).innerText();
      if (text.includes('个月')) {
        foundMonth = true;
        break;
      }
    }
    expect(foundMonth).toBe(true);

    // Verify no hardcoded Chinese descriptions (no "入门套餐", "专业套餐" etc.)
    expect(body).not.toContain('入门套餐');
    expect(body).not.toContain('专业套餐');
    expect(body).not.toContain('企业套餐');

    console.log('  ✅ Subscriptions page (Chinese): 4 plans, "个月" duration, no hardcoded descriptions');
    await screenshot(page, 'subs-12-chinese-verified');
  });
});
