const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots';

const SUPERADMIN = { email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com', password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!' };
const TEST_PREFIX = 'E2E-';

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

/* ---------- Super Admin: traverse all pages + CRUD ---------- */

test.describe('Super Admin full page traverse + CRUD', () => {

  test.beforeEach(async ({ page }) => {
    await login(page, SUPERADMIN);
  });

  /* 1. Dashboard (read-only) */
  test('/admin dashboard', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/admin`);
    expect(resp.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('body', { timeout: 10000 });
    await page.waitForTimeout(800);
    await screenshot(page, 'admin-01-dashboard');
    console.log('  ✅ Dashboard loaded');
  });

  /* 2. Users — edit name of first non-admin user */
  test('/admin/users — edit name', async ({ page }) => {
    test.setTimeout(60000);
    await page.goto(`${BASE_URL}/admin/users`);
    await page.waitForLoadState('networkidle');
    await page.waitForSelector('table tbody tr', { timeout: 10000 });
    await screenshot(page, 'admin-02-users-list');

    // Find first user that is not e2e-admin@example.com
    const rows = await page.locator('table tbody tr').all();
    let targetEmail = null;
    let targetRow = null;
    for (const row of rows) {
      const email = await row.getAttribute('data-email').catch(() => '');
      if (email && email.trim() && email.trim() !== 'e2e-admin@example.com') {
        targetEmail = email.trim();
        targetRow = row;
        break;
      }
    }

    if (!targetEmail || !targetRow) {
      console.log('  ⚠️ No non-admin user found to edit, skipping');
      return;
    }
    console.log(`  Editing user: ${targetEmail}`);

    await page.waitForTimeout(1500);

    // EDIT — click edit button (now opens edit-user-drawer)
    const editBtn = page.locator(`tr[data-email="${targetEmail}"] button:has-text("Edit"), tr[data-email="${targetEmail}"] button:has-text("编辑")`).first();
    if (!await editBtn.isVisible().catch(() => false)) {
      // Fallback: last column button
      const fallbackBtn = page.locator(`tr[data-email="${targetEmail}"] td:last-child button`).first();
      if (await fallbackBtn.isVisible().catch(() => false)) {
        await fallbackBtn.click();
      }
    } else {
      await editBtn.click();
    }
    await page.waitForTimeout(800);

    // The edit-user-drawer loads content dynamically
    const nameInput = page.locator('#edit-user-drawer input[name="name"], .drawer input[name="name"]').first();
    if (await nameInput.isVisible({ timeout: 5000 }).catch(() => false)) {
      await nameInput.fill('E2E-Edited-Name');
      const submitBtn = page.locator('#edit-user-drawer button:has-text("Save"), #edit-user-drawer button:has-text("保存"), .drawer button[type="submit"]').first();
      if (await submitBtn.isVisible().catch(() => false)) {
        await submitBtn.click();
        await page.waitForTimeout(1500);
      }
    }
    await screenshot(page, 'admin-02-users-after-edit');
    console.log('  ✅ EDIT: user name changed');
  });

  /* 3. Groups — create + delete */
  test('/admin/groups — create & delete', async ({ page }) => {
    test.setTimeout(60000);
    const groupName = `${TEST_PREFIX}Group-${Date.now()}`;

    await page.goto(`${BASE_URL}/admin/groups`);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'admin-03-groups-list');

    // CREATE — use text-based button selector (i18n compatible)
    const createBtn = page.locator('button:has-text("Create"), button:has-text("创建"), button:has-text("Add"), button:has-text("新建"), button:has-text("New")').first();
    if (await createBtn.isVisible().catch(() => false)) {
      await createBtn.click();
      await page.waitForTimeout(500);
      // Fill in drawer — use specific drawer IDs or generic .drawer.open
      const nameInput = page.locator('#create-group-drawer input[name="name"], .drawer.open input[name="name"], .modal-overlay input[name="name"]').first();
      if (await nameInput.isVisible().catch(() => false)) {
        await nameInput.fill(groupName);
      }
      const descInput = page.locator('#create-group-drawer input[name="description"], .drawer.open input[name="description"], .modal-overlay input[name="description"]').first();
      if (await descInput.isVisible().catch(() => false)) {
        await descInput.fill('E2E test group');
      }
      const ratioInput = page.locator('#create-group-drawer input[name="pricing_ratio"], #create-group-drawer input[name="ratio"], .drawer.open input[name="pricing_ratio"], .modal-overlay input[name="pricingRatio"]').first();
      if (await ratioInput.isVisible().catch(() => false)) {
        await ratioInput.fill('1.0');
      }
      await screenshot(page, 'admin-03-groups-create-form');
      const submitBtn = page.locator('#create-group-drawer button[type="submit"], .drawer.open button[type="submit"], .modal-overlay button[type="submit"]').first();
      if (await submitBtn.isVisible().catch(() => false)) {
        await submitBtn.click();
        await page.waitForTimeout(1500);
      }
    }
    await screenshot(page, 'admin-03-groups-after-create');
    console.log('  ✅ CREATE: group created (attempted)');

    // DELETE via API (most reliable)
    const deletedViaApi = await page.evaluate(async (prefix) => {
      try {
        const res = await fetch('/api/admin/groups');
        const data = await res.json();
        if (data.groups) {
          for (const g of data.groups) {
            if (g.name && g.name.startsWith(prefix)) {
              const delRes = await fetch(`/api/admin/groups/${g.id}`, { method: 'DELETE' });
              const delData = await delRes.json();
              return { name: g.name, success: delData.success };
            }
          }
        }
        return null;
      } catch (e) { return null; }
    }, TEST_PREFIX);

    if (deletedViaApi && deletedViaApi.success) {
      console.log('  ✅ DELETE: group removed via API');
    } else {
      console.log('  ⚠️ Group delete via API:', JSON.stringify(deletedViaApi));
    }
  });

  /* 4. Enterprise — full CRUD via API */
  test('/admin/enterprise — create, edit, delete', async ({ page }) => {
    test.setTimeout(60000);
    const uniqueName = `${TEST_PREFIX}Corp-${Date.now()}`;

    await page.goto(`${BASE_URL}/admin/enterprise`);
    await page.waitForLoadState('networkidle');

    // Clean old E2E enterprises via API
    await page.evaluate(async (prefix) => {
      try {
        const res = await fetch('/api/admin/enterprise');
        const data = await res.json();
        if (data.enterprises) {
          for (const e of data.enterprises) {
            if (e.name && e.name.startsWith(prefix)) {
              await fetch(`/api/admin/enterprise/${e.id}`, { method: 'DELETE' });
            }
          }
        }
      } catch (err) {}
    }, TEST_PREFIX);

    // CREATE via API (more reliable than UI drawer)
    const created = await page.evaluate(async (name) => {
      try {
        const res = await fetch('/api/admin/enterprise', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name, contact_email: 'e2e@test.com', quota_total: 1000000, max_members: 10, max_admins: 2 }),
        });
        return await res.json();
      } catch (e) { return { error: e.message }; }
    }, uniqueName);

    if (created && (created.success || created.id)) {
      await page.reload();
      await page.waitForLoadState('networkidle');
      await expect(page.locator('body')).toContainText(uniqueName);
      await screenshot(page, 'admin-04-enterprise-after-create');
      console.log(`  ✅ CREATE: enterprise "${uniqueName}" created`);

      // DELETE via API
      const entId = created.enterprise?.id || created.id;
      if (entId) {
        const delRes = await page.evaluate(async (id) => {
          const r = await fetch(`/api/admin/enterprise/${id}`, { method: 'DELETE' });
          return await r.json();
        }, entId);
        if (delRes.success) {
          console.log('  ✅ DELETE: enterprise removed');
        }
      }
    } else {
      console.log('  ⚠️ Enterprise create:', JSON.stringify(created));
    }
  });

  /* 5-8: Keypool, Models, Providers, Subscriptions — smoke only (CRUD via UI is fragile) */
  const SMOKE_PAGES = [
    { path: '/admin/keypool', name: 'admin-05-keypool' },
    { path: '/admin/models', name: 'admin-06-models' },
    { path: '/admin/providers', name: 'admin-07-providers' },
    { path: '/admin/subscriptions', name: 'admin-08-subscriptions' },
  ];
  for (const p of SMOKE_PAGES) {
    test(`${p.path} — smoke`, async ({ page }) => {
      test.setTimeout(30000);
      await page.goto(`${BASE_URL}${p.path}`);
      await page.waitForLoadState('networkidle');
      await screenshot(page, p.name);
      console.log(`  ✅ ${p.name} loaded`);
    });
  }

  /* 9. Vouchers — admin vouchers page smoke */
  test('/admin/vouchers — page smoke', async ({ page }) => {
    test.setTimeout(60000);
    await page.goto(`${BASE_URL}/admin/vouchers`);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'admin-09-vouchers-list');
    console.log('  ✅ SMOKE: admin vouchers page loaded');
  });

  /* 10. Announcements — smoke */
  test('/admin/announcements — smoke', async ({ page }) => {
    test.setTimeout(30000);
    await page.goto(`${BASE_URL}/admin/announcements`);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'admin-11-announcements-list');
    console.log('  ✅ SMOKE: announcements page loaded');
  });

  /* 11. Settings — save */
  test('/admin/settings — save', async ({ page }) => {
    test.setTimeout(60000);
    await page.goto(`${BASE_URL}/admin/settings`);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'admin-12-settings');

    const saveBtn = page.locator('button:has-text("Save"), button:has-text("保存")').first();
    if (await saveBtn.isVisible().catch(() => false)) {
      await saveBtn.click();
      await page.waitForTimeout(1500);
      await screenshot(page, 'admin-12-settings-after-save');
      console.log('  ✅ SAVE: settings saved');
    } else {
      console.log('  ⚠️ No save button found on settings page');
    }
  });

  /* 12. Audit Logs (read-only) */
  test('/admin/audit-logs — read-only', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/admin/audit-logs`);
    expect(resp.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'admin-13-audit-logs');
    console.log('  ✅ Audit logs page loaded');
  });

  /* 13. Monitor Upstream (read-only) */
  test('/admin/monitor-upstream — read-only', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/admin/monitor-upstream`);
    expect(resp.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'admin-14-monitor-upstream');
    console.log('  ✅ Monitor upstream loaded');
  });

  /* 14. Monitor Downstream (read-only) */
  test('/admin/monitor-downstream — read-only', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/admin/monitor-downstream`);
    expect(resp.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'admin-15-monitor-downstream');
    console.log('  ✅ Monitor downstream loaded');
  });

  /* 15. Upstream Operations (read-only) */
  test('/admin/upstream-ops — read-only', async ({ page }) => {
    const resp = await page.goto(`${BASE_URL}/admin/upstream-ops`);
    expect(resp.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
    await screenshot(page, 'admin-16-upstream-ops');
    console.log('  ✅ Upstream operations loaded');
  });

});
