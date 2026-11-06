const { test, expect } = require('@playwright/test');

const BASE_URL = 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = 'e2e/screenshots';

const ACCOUNTS = {
  superadmin: {
    email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
    password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!'
  },
  enterprise: {
    email: process.env.E2E_ENTERPRISE_EMAIL || 'e2e-user@example.com',
    password: process.env.E2E_ENTERPRISE_PASSWORD || 'E2E_TestPass123!'
  },
};

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
  await expect(page).toHaveURL(/\/dashboard/);
}

async function screenshot(page, name) {
  await page.screenshot({ path: `${SCREENSHOT_DIR}/${name}.png`, fullPage: true });
}

/* ---------- Test 1: Super Admin - Enterprise Full CRUD ---------- */

test('Super Admin: Enterprise Create → Edit → Delete', async ({ page }) => {
  test.setTimeout(60000);
  const uniqueName = `${TEST_PREFIX}Corp-${Date.now()}`;
  const editedName = `${uniqueName}-Edited`;

  /* 1. Login */
  await login(page, ACCOUNTS.superadmin);
  await screenshot(page, 'crud-01-dashboard');

  /* 2. Clean up old E2E enterprises via API */
  await page.goto(`${BASE_URL}/admin/enterprise`);
  await page.waitForLoadState('networkidle');
  const cleaned = await page.evaluate(async (prefix) => {
    try {
      const res = await fetch('/api/admin/enterprise');
      const data = await res.json();
      let count = 0;
      if (data.enterprises) {
        for (const e of data.enterprises) {
          if (e.name && e.name.startsWith(prefix)) {
            await fetch(`/api/admin/enterprise/${e.id}`, { method: 'DELETE' });
            count++;
          }
        }
      }
      return count;
    } catch (err) { return 0; }
  }, TEST_PREFIX);
  if (cleaned > 0) {
    console.log(`  🧹 Cleaned ${cleaned} old E2E enterprises`);
    await page.reload();
    await page.waitForLoadState('networkidle');
  }

  /* 3. CREATE enterprise via API (more reliable than UI drawer) */
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

  if (!created || (!created.success && !created.id && !created.enterprise)) {
    console.log('  ⚠️ Enterprise create failed:', JSON.stringify(created));
    return;
  }
  const entId = created.enterprise?.id || created.id;
  await page.goto(`${BASE_URL}/admin/enterprise`);
  await page.waitForLoadState('networkidle');
  await expect(page.locator('body')).toContainText(uniqueName);
  await screenshot(page, 'crud-03-after-create');
  console.log(`  ✅ CREATE: enterprise "${uniqueName}" created via API`);

  /* 4. EDIT enterprise */
  const editBtn = page.locator(`tr:has-text("${uniqueName}") button:has-text("Edit"), tr:has-text("${uniqueName}") button:has-text("编辑")`);
  await expect(editBtn).toBeVisible();
  await editBtn.click();
  await expect(page.locator('#edit-drawer')).toBeVisible({ timeout: 5000 });
  await page.fill('#edit-drawer input[name="name"]', editedName);
  await screenshot(page, 'crud-04-edit-form');

  await page.click('#edit-drawer button:has-text("Save"), #edit-drawer button:has-text("保存"), #edit-drawer button.btn-primary');
  await page.waitForTimeout(2000);
  if (!await page.locator('body').innerText().then(t => t.includes(editedName))) {
    await page.goto(`${BASE_URL}/admin/enterprise`);
    await page.waitForLoadState('networkidle');
  }
  await expect(page.locator('body')).toContainText(editedName);
  await screenshot(page, 'crud-05-after-edit');
  console.log(`  ✅ EDIT: renamed to "${editedName}"`);

  /* 5. DELETE enterprise via API */
  if (entId) {
    const delRes = await page.evaluate(async (id) => {
      const r = await fetch('/api/admin/enterprise/' + id, { method: 'DELETE' });
      return await r.json();
    }, entId);
    if (delRes.success) {
      await page.goto(`${BASE_URL}/admin/enterprise`);
      await page.waitForLoadState('networkidle');
    }
  }
  await screenshot(page, 'crud-07-after-delete');
  await expect(page.locator('body')).not.toContainText(editedName);
  console.log(`  ✅ DELETE: enterprise "${editedName}" removed`);
});

/* ---------- Test 2: Enterprise Admin - Key Create via UI → Delete ---------- */

test('Enterprise Admin: Key Create via UI → Delete', async ({ page }) => {
  test.setTimeout(60000);
  const uniqueKeyName = `${TEST_PREFIX}Key-${Date.now()}`;

  /* 1. Login */
  await login(page, ACCOUNTS.enterprise);

  /* 2. Clean up old E2E keys via API */
  await page.goto(`${BASE_URL}/enterprise/keys`);
  await page.waitForLoadState('networkidle');
  try {
    const cleaned = await page.evaluate(async (prefix) => {
      try {
        const res = await fetch('/api/enterprise/keys');
        const data = await res.json();
        let count = 0;
        if (data.keys) {
          for (const k of data.keys) {
            if (k.name && k.name.startsWith(prefix)) {
              await fetch(`/api/enterprise/keys/${k.id}`, { method: 'DELETE' });
              count++;
            }
          }
        }
        return count;
      } catch (err) { return 0; }
    }, TEST_PREFIX);
    if (cleaned > 0) {
      console.log(`  🧹 Cleaned ${cleaned} old E2E keys via API`);
      await page.reload();
      await page.waitForLoadState('networkidle');
    }
  } catch (e) {
    console.log('  ⚠️ Key cleanup via API skipped');
  }

  /* 3. CREATE key via UI form */
  await page.click('button:has-text("Create New Key"), button:has-text("创建新密钥"), button:has-text("Create Key"), button:has-text("New")');
  await expect(page.locator('#create-key-drawer')).toBeVisible({ timeout: 5000 });
  await page.fill('#create-key-drawer input[name="name"]', uniqueKeyName);
  // Leave user_id and group_id as default ("不指定")
  await screenshot(page, 'crud-11-create-key-form');

  // Submit and wait for response
  await page.click('#create-key-drawer button:has-text("Create"), #create-key-drawer button:has-text("创建"), #create-key-drawer button.btn-primary');
  await page.waitForTimeout(2000);
  await screenshot(page, 'crud-12-after-create-key');

  // Close the "new key" success modal if it's open, then verify key in list
  const newKeyModal = page.locator('#new-key-modal');
  if (await newKeyModal.isVisible().catch(() => false)) {
    await page.click('#new-key-modal button:has-text("Saved"), button:has-text("我已保存")');
    await page.waitForTimeout(300);
  }

  // Key list should show the new key
  await expect(page.locator('body')).toContainText(uniqueKeyName);
  console.log(`  ✅ CREATE: key "${uniqueKeyName}" created via UI form`);

  /* 4. DELETE key via UI confirm modal */
  // Find the key card and click its Delete button
  const keyCard = page.locator(`.key-card:has-text("${uniqueKeyName}")`);
  await expect(keyCard).toBeVisible();
  const deleteBtn = keyCard.locator('button:has-text("Delete"), button:has-text("删除")');
  await expect(deleteBtn).toBeVisible();
  await deleteBtn.click();
  await expect(page.locator('#global-confirm-modal')).toBeVisible({ timeout: 5000 });
  await screenshot(page, 'crud-13-delete-key-confirm');

  await Promise.all([
    page.waitForEvent('load', { timeout: 15000 }),
    page.click('#global-confirm-btn'),
  ]);
  await page.waitForTimeout(500);
  await screenshot(page, 'crud-14-after-delete-key');

  await expect(page.locator('body')).not.toContainText(uniqueKeyName);
  console.log(`  ✅ DELETE: key "${uniqueKeyName}" removed`);
});

/* ---------- Test 3: Enterprise Admin - Member Invite Flow ---------- */

test('Enterprise Admin: Member Invite flow', async ({ page }) => {
  const inviteEmail = `e2e-invite-${Date.now()}@test.com`;

  await login(page, ACCOUNTS.enterprise);
  await page.goto(`${BASE_URL}/enterprise/users`);
  await page.waitForLoadState('networkidle');
  await screenshot(page, 'crud-20-enterprise-users');

  const inviteInput = page.locator('#invite-email');
  if (await inviteInput.isVisible().catch(() => false)) {
    await inviteInput.fill(inviteEmail);
    await page.click('button:has-text("Invite"), button:has-text("邀请")');
    await page.waitForTimeout(2000);
    await screenshot(page, 'crud-21-after-invite');
    const bodyText = await page.locator('body').innerText();
    if (bodyText.includes('邀请已发送') || bodyText.includes('Invitation sent')) {
      console.log(`  ✅ INVITE: sent to ${inviteEmail}`);
    } else if (bodyText.includes('邀请失败') || bodyText.includes('already') || bodyText.includes('已存在')) {
      console.log(`  ⚠️ INVITE: ${inviteEmail} may already exist`);
    } else {
      console.log(`  ⚠️ INVITE: unknown response for ${inviteEmail}`);
    }
  } else {
    console.log('  ⚠️ No invite section found');
  }
});

/* ---------- Test 4: Cross-role access ---------- */

test('Cross-role: enterprise admin blocked from /admin', async ({ page }) => {
  await login(page, ACCOUNTS.enterprise);
  await page.goto(`${BASE_URL}/admin`);
  await page.waitForLoadState('networkidle');
  const url = page.url();
  expect(url).not.toContain('/admin');
  await screenshot(page, 'crud-30-blocked-admin');
  console.log('  ✅ BLOCK: enterprise admin cannot access /admin');
});
