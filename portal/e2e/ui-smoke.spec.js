const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots';

const ACCOUNTS = {
  superadmin: {
    email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
    password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!',
    role: 'super_admin'
  },
  enterprise: {
    // Use the existing enterprise_admin test account in the local DB
    email: process.env.E2E_ENTERPRISE_EMAIL || 'e2e-enterprise@example.com',
    password: process.env.E2E_ENTERPRISE_PASSWORD || 'E2E_TestPass123!',
    role: 'enterprise_admin'
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
  console.log(`✅ Login success: ${account.email}`);
}

async function assertNoError(page, label) {
  const body = await page.locator('body').innerText();
  const lower = body.toLowerCase();
  if (lower.includes('internal server error') || lower.includes("can't evaluate field") || lower.includes('template:')) {
    throw new Error(`${label}: Page contains server error`);
  }
}

async function screenshot(page, name) {
  await page.screenshot({ path: `${SCREENSHOT_DIR}/${name}.png`, fullPage: true });
  console.log(`  📸 ${name}`);
}

/* ---------- Super Admin Interactive Tests ---------- */

test.describe('Super Admin - Full Interaction Flow', () => {
  test('login → dashboard → admin pages with clicks', async ({ page }) => {
    test.setTimeout(60000);

    /* 1. Login */
    await login(page, ACCOUNTS.superadmin);
    await screenshot(page, '01-dashboard');
    await assertNoError(page, 'dashboard');

    /* 2. /admin - overview page */
    await page.goto(`${BASE_URL}/admin`);
    await page.waitForTimeout(500);
    await assertNoError(page, '02-admin');
    await expect(page.locator('body')).toContainText(/Total Users|总用户数|Active Enterprises|活跃企业/);
    await screenshot(page, '02-admin');

    /* 3. /admin/enterprise - drawer interactions */
    await page.goto(`${BASE_URL}/admin/enterprise`);
    await page.waitForTimeout(500);
    await assertNoError(page, '03-admin-enterprise');
    await screenshot(page, '03-admin-enterprise-before');

    // 3a. Click the New enterprise button → verify create drawer opens
    const createBtn = page.locator('button[onclick="openCreateDrawer()"]').first();
    await createBtn.click();
    await expect(page.locator('#create-drawer')).toBeVisible();
    await page.fill('#create-drawer input[name="name"]', 'E2E Test Enterprise');
    await page.fill('#create-drawer input[name="contact_email"]', 'test@e2e.com');
    await screenshot(page, '03a-create-drawer-filled');
    // Close without creating (avoid data pollution)
    await page.evaluate(() => closeCreateDrawer());
    await page.waitForTimeout(500);
    console.log('  ✅ Create enterprise drawer: open → fill → close');

    // 3b. Click "Edit" on first enterprise row → verify edit drawer opens
    const firstEditBtn = page.locator('button:has-text("Edit"), button:has-text("编辑")').first();
    if (await firstEditBtn.isVisible()) {
      await firstEditBtn.click();
      await expect(page.locator('#edit-drawer')).toBeVisible();
      await screenshot(page, '03b-edit-drawer-open');
      await page.evaluate(() => closeEditDrawer());
      await page.waitForTimeout(500);
      console.log('  ✅ Edit enterprise drawer: open → close');
    }

    // 3c. Click "Members" on first enterprise row → verify members modal opens
    const firstMemberBtn = page.locator('button:has-text("Members"), button:has-text("成员")').first();
    if (await firstMemberBtn.isVisible()) {
      await firstMemberBtn.click();
      await expect(page.locator('#members-modal')).toBeVisible();
      await screenshot(page, '03c-members-modal-open');
      await page.click('#members-modal .modal-close');
      await expect(page.locator('#members-modal')).toBeHidden();
      console.log('  ✅ Members modal: open → close');
    }

    // 3d. Final screenshot of enterprise page
    await screenshot(page, '03-admin-enterprise');

    /* 4. /admin/users - search + edit interaction */
    await page.goto(`${BASE_URL}/admin/users`);
    await page.waitForTimeout(500);
    await assertNoError(page, '04-admin-users');
    await screenshot(page, '04-admin-users');
    // Click first user's "Edit" button
    const firstUserEdit = page.locator('button:has-text("Edit"), button:has-text("编辑")').first();
    if (await firstUserEdit.isVisible()) {
      await firstUserEdit.click();
      await page.waitForTimeout(300);
      await screenshot(page, '04a-user-edit-clicked');
      console.log('  ✅ User edit button clicked');
    }

    /* 5. /admin/groups */
    await page.goto(`${BASE_URL}/admin/groups`);
    await page.waitForTimeout(500);
    await assertNoError(page, '05-admin-groups');
    await screenshot(page, '05-admin-groups');

    /* 6. /admin/keypool */
    await page.goto(`${BASE_URL}/admin/keypool`);
    await page.waitForTimeout(500);
    await assertNoError(page, '06-admin-keypool');
    await screenshot(page, '06-admin-keypool');

    /* 7. /admin/models */
    await page.goto(`${BASE_URL}/admin/models`);
    await page.waitForTimeout(500);
    await assertNoError(page, '07-admin-models');
    await screenshot(page, '07-admin-models');

    /* 8. /admin/providers */
    await page.goto(`${BASE_URL}/admin/providers`);
    await page.waitForTimeout(500);
    await assertNoError(page, '08-admin-providers');
    await screenshot(page, '08-admin-providers');

    /* 9. /admin/subscriptions */
    await page.goto(`${BASE_URL}/admin/subscriptions`);
    await page.waitForTimeout(500);
    await assertNoError(page, '09-admin-subscriptions');
    await screenshot(page, '09-admin-subscriptions');
  });
});

/* ---------- Enterprise Admin Interactive Tests ---------- */

test.describe('Enterprise Admin - Full Interaction Flow', () => {
  test('login → enterprise pages with clicks', async ({ page }) => {
    /* 1. Login */
    await login(page, ACCOUNTS.enterprise);
    await screenshot(page, '10-enterprise-dashboard');
    await assertNoError(page, 'enterprise-dashboard');

    /* 2. /enterprise/users - invite member modal */
    await page.goto(`${BASE_URL}/enterprise/users`);
    await page.waitForTimeout(500);
    await assertNoError(page, '11-enterprise-users');
    await screenshot(page, '11-enterprise-users');

    // Check if there are action buttons and click one
    const actionBtns = page.locator('.action-bar button, td button');
    if (await actionBtns.first().isVisible().catch(() => false)) {
      console.log('  ✅ Enterprise users page has action buttons');
    }

    /* 3. /enterprise/keys - create key flow */
    await page.goto(`${BASE_URL}/enterprise/keys`);
    await page.waitForTimeout(500);
    await assertNoError(page, '12-enterprise-keys');
    await screenshot(page, '12-enterprise-keys');

    // Try to find and click the New key button if present
    const createBtn = page.locator('button[onclick="openCreateKeyDrawer()"]').first();
    if (await createBtn.isVisible().catch(() => false)) {
      await createBtn.click();
      await page.waitForTimeout(300);
      await screenshot(page, '12a-create-key-modal');
      // Close the create key drawer by clicking the cancel/close button
      const cancelBtn = page.locator('#create-key-drawer .drawer-close, #create-key-drawer button:has-text("取消"), #create-key-drawer button:has-text("Cancel"), .drawer-close').first();
      if (await cancelBtn.isVisible().catch(() => false)) {
        await cancelBtn.click();
      }
      console.log('  ✅ Create key modal: open → cancel');
    }

    /* 4. /enterprise/billing */
    await page.goto(`${BASE_URL}/enterprise/billing`);
    await page.waitForTimeout(500);
    await assertNoError(page, '13-enterprise-billing');
    await screenshot(page, '13-enterprise-billing');
  });
});

/* ---------- Cross-role Access Checks ---------- */

test.describe('Cross-role access checks', () => {
  test('enterprise admin should NOT access /admin', async ({ page }) => {
    await login(page, ACCOUNTS.enterprise);
    await page.goto(`${BASE_URL}/admin`);
    await page.waitForTimeout(500);
    const url = page.url();
    expect(url).not.toContain('/admin');
    console.log('✅ Enterprise admin correctly blocked from /admin');
    await screenshot(page, '14-enterprise-blocked-admin');
  });
});
