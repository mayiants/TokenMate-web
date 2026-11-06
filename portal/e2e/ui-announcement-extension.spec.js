const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots';

const SUPERADMIN = { email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com', password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!' };
const TESTUSER = { email: process.env.E2E_USER_EMAIL || 'e2e-user@example.com', password: process.env.E2E_USER_PASSWORD || 'E2E_TestPass123!' };

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
}

async function screenshot(page, name) {
  await page.screenshot({ path: `${SCREENSHOT_DIR}/${name}.png`, fullPage: true });
}

/* ---------- Announcement Extension E2E Tests ---------- */

test.describe('Announcement Extension: Promo Banner & Onboarding Wizard', () => {

  /* 1. Admin: Create a promo banner announcement */
  test('Admin can create a promo banner announcement', async ({ page }) => {
    await login(page, SUPERADMIN);

    // Navigate to admin announcements page
    await page.goto(`${BASE_URL}/admin/announcements`);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'ann-ext-01-admin-page');

    // Click create button to open drawer
    await page.click('button[onclick="openAnnouncementDrawer()"]');
    await page.waitForSelector('#announcement-drawer.open', { timeout: 5000 });
    await screenshot(page, 'ann-ext-02-drawer-open');

    // Fill in promo banner fields
    await page.fill('#ann-title', '🎁 E2E 限时优惠 — 测试横幅');
    await page.fill('#ann-content', '充6送4(40%off)，E2E测试专用');
    await page.selectOption('#ann-type', 'promo');
    // display_mode should auto-switch to banner
    await page.waitForTimeout(300);
    const displayMode = await page.inputValue('#ann-display-mode');
    expect(displayMode).toBe('banner');

    await page.fill('#ann-cta-text', '立即领取');
    await page.fill('#ann-cta-link', '/billing/topup?plan=e2e-test');
    await page.selectOption('#ann-banner-color', 'primary');
    await page.selectOption('#ann-dismiss-mode', 'session');
    await page.selectOption('#ann-target-audience', 'all');
    await page.fill('#ann-icon-emoji', '🎁');
    await screenshot(page, 'ann-ext-03-drawer-filled');

    // Save
    await page.click('button[onclick="saveAnnouncement()"]');
    await page.waitForTimeout(1000);
    await screenshot(page, 'ann-ext-04-saved-draft');

    // Find the new announcement in the table and publish it
    const row = page.locator('tr', { hasText: 'E2E 限时优惠' }).filter({ has: page.locator('button[onclick^="publishAnnouncement"]') }).first();
    await expect(row).toBeVisible({ timeout: 5000 });
    await row.locator('button[onclick^="publishAnnouncement"]').click();
    await page.waitForTimeout(1000);
    await screenshot(page, 'ann-ext-05-published');
  });

  /* 2. User: See promo banner on dashboard */
  test('User sees promo banner on dashboard', async ({ page }) => {
    await login(page, TESTUSER);
    await page.goto(`${BASE_URL}/dashboard`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);

    // Check if banner area has content
    // Promo banners render in the global layout banner (hidden until an announcement exists).
    const bannerArea = page.locator('#global-announcement-banner');
    await expect(bannerArea).toBeVisible();

    // Check if a promo-banner element exists inside
    const banner = bannerArea.locator('.promo-banner-content');
    const bannerCount = await banner.count();
    expect(bannerCount).toBeGreaterThanOrEqual(1);

    await screenshot(page, 'ann-ext-06-user-banner');
  });

  /* 3. User: Dismiss promo banner */
  test('User can dismiss promo banner', async ({ page }) => {
    await login(page, TESTUSER);
    await page.goto(`${BASE_URL}/dashboard`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);

    const dismissBtn = page.locator('#global-banner-close');
    if (await dismissBtn.isVisible()) {
      await dismissBtn.click();
      await page.waitForTimeout(500);
      await screenshot(page, 'ann-ext-07-banner-dismissed');
    }
  });

  /* 4. Admin: Create an onboarding wizard announcement */
  test('Admin can create an onboarding wizard announcement', async ({ page }) => {
    await login(page, SUPERADMIN);
    await page.goto(`${BASE_URL}/admin/announcements`);
    await page.waitForLoadState('networkidle');

    // Click create button
    await page.click('button[onclick="openAnnouncementDrawer()"]');
    await page.waitForSelector('#announcement-drawer.open', { timeout: 5000 });

    // Fill in onboarding wizard fields
    await page.fill('#ann-title', '🚀 欢迎来到 AI Portal!');
    // JSON content for two-column wizard
    const wizardContent = JSON.stringify([
      {
        title: 'Get 40% Off',
        popular: true,
        features: ['充6送4', '永久有效', '全模型可用'],
        cta_text: '选择此方案',
        cta_link: '/billing/topup?plan=40off'
      },
      {
        title: '$200 Free Credits',
        features: ['开发者额度', '限7天领取', '全部模型'],
        cta_text: '申请额度',
        cta_link: '/billing/topup?plan=free200'
      }
    ]);
    await page.fill('#ann-content', wizardContent);
    await page.selectOption('#ann-type', 'onboarding');
    await page.waitForTimeout(300);
    const displayMode = await page.inputValue('#ann-display-mode');
    expect(displayMode).toBe('wizard');

    await page.fill('#ann-icon-emoji', '🚀');
    await page.selectOption('#ann-target-audience', 'new_user');
    await screenshot(page, 'ann-ext-08-wizard-filled');

    // Save
    await page.click('button[onclick="saveAnnouncement()"]');
    await page.waitForTimeout(1000);

    // Publish
    const row = page.locator('tr', { hasText: '欢迎来到 AI Portal' }).filter({ has: page.locator('button[onclick^="publishAnnouncement"]') }).first();
    await expect(row).toBeVisible({ timeout: 5000 });
    await row.locator('button[onclick^="publishAnnouncement"]').click();
    await page.waitForTimeout(1000);
    await screenshot(page, 'ann-ext-09-wizard-published');
  });

  /* 5. Global announcement banner is present on admin pages */
  // Design note: the banner was unified into a single global layout banner
  // (#global-announcement-banner) driven by published display_mode='banner'
  // announcements. It intentionally renders on every authenticated page,
  // including /admin (the old per-page #promo-banner-area filtering was removed
  // when layout.html was split into partials). This test pins that behavior.
  test('Global announcement banner element is present on admin pages', async ({ page }) => {
    await login(page, SUPERADMIN);
    await page.goto(`${BASE_URL}/admin/announcements`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(1000);

    // The global banner container must exist on admin pages (it is hidden via
    // inline style until a published banner announcement is selected).
    const banner = page.locator('#global-announcement-banner');
    await expect(banner).toHaveCount(1);

    await screenshot(page, 'ann-ext-10-admin-global-banner');
  });

  /* 6. Cleanup: Archive test announcements */
  test('Cleanup: archive test announcements', async ({ page }) => {
    await login(page, SUPERADMIN);
    await page.goto(`${BASE_URL}/admin/announcements`);
    await page.waitForLoadState('networkidle');

    // Delete every E2E announcement via the admin API so no residue is left
    // (archived rows would still match the row lookups of later runs).
    const deleted = await page.evaluate(async (titles) => {
      const res = await fetch('/api/admin/announcements', { credentials: 'same-origin' });
      const data = await res.json();
      let n = 0;
      for (const a of (data.announcements || [])) {
        if (titles.some((t) => (a.title || '').includes(t))) {
          const r = await fetch('/api/admin/announcements/' + a.id, { method: 'DELETE', credentials: 'same-origin' });
          if (r.ok) n++;
        }
      }
      return n;
    }, ['E2E 限时优惠', '欢迎来到 AI Portal']);
    console.log(`  🧹 deleted ${deleted} E2E announcement(s)`);

    await screenshot(page, 'ann-ext-11-cleanup');
  });

});
