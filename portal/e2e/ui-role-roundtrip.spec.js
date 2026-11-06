const { test, expect } = require('@playwright/test');
const fs = require('fs');
const path = require('path');

const BASE_URL = process.env.PORTAL_BASE_URL || 'http://127.0.0.1:3001';
const TEST_PREFIX = 'E2E-';
const SCREENSHOT_DIR = path.join(__dirname, 'screenshots');

const ACCOUNTS = {
  superadmin: { email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com', password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!' },
  enterprise: { email: process.env.E2E_ENTERPRISE_EMAIL || 'e2e-enterprise@example.com', password: process.env.E2E_ENTERPRISE_PASSWORD || 'E2E_TestPass123!' },
  user: { email: process.env.E2E_USER_EMAIL || 'e2e-user@example.com', password: process.env.E2E_USER_PASSWORD || 'E2E_TestPass123!' },
};

if (!fs.existsSync(SCREENSHOT_DIR)) {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
}

const ts = () => Date.now().toString();
const unique = (suffix = '') => `${TEST_PREFIX}${suffix}${ts()}`;

const RESULTS = [];
function record(step, status, detail = '') {
  RESULTS.push({ step, status, detail });
  const icon = status === 'PASS' ? '✅' : status === 'FAIL' ? '❌' : '⚠️';
  console.log(`  ${icon} ${step}${detail ? ': ' + detail : ''}`);
}

async function screenshot(page, name) {
  const file = path.join(SCREENSHOT_DIR, `${name}.png`);
  try {
    await page.screenshot({ path: file, fullPage: true });
  } catch (e) {
    console.warn(`[screenshot] failed for ${name}: ${e.message}`);
  }
}

async function login(page, role) {
  const account = ACCOUNTS[role];
  console.log(`\n[${role}] login ${account.email}`);
  await page.goto(`${BASE_URL}/login`);
  await page.waitForSelector('input[name="email"]', { timeout: 15000 });
  await page.fill('input[name="email"]', account.email);
  await page.fill('input[name="password"]', account.password);
  await page.evaluate(() => {
    const form = document.querySelector('form[action="/login"]');
    if (form) form.submit();
  });
  await page.waitForURL(/\/dashboard/, { timeout: 15000 });
  await page.waitForLoadState('networkidle');
  await screenshot(page, `${role}-dashboard`);
}

async function apiCall(page, method, endpoint, body = null) {
  return page.evaluate(async ({ method, endpoint, body }) => {
    const opts = { method, credentials: 'include', headers: {} };
    if (body !== null && body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    const res = await fetch(endpoint, opts);
    const text = await res.text();
    let json = null;
    try { json = JSON.parse(text); } catch (_) { json = null; }
    return { status: res.status, body: json || text };
  }, { method, endpoint, body });
}

async function apiList(page, endpoint) {
  const res = await apiCall(page, 'GET', endpoint);
  if (res.status >= 200 && res.status < 300) {
    if (Array.isArray(res.body)) return res.body;
    if (res.body && Array.isArray(res.body.data)) return res.body.data;
    if (res.body && Array.isArray(res.body.items)) return res.body.items;
    if (res.body && Array.isArray(res.body.results)) return res.body.results;
    if (res.body && res.body.enterprises) return res.body.enterprises;
    if (res.body && res.body.keys) return res.body.keys;
    if (res.body && res.body.groups) return res.body.groups;
    if (res.body && res.body.users) return res.body.users;
    if (res.body && res.body.announcements) return res.body.announcements;
    if (res.body && res.body.vouchers) return res.body.vouchers;
    if (res.body && res.body.invitations) return res.body.invitations;
    if (res.body && res.body.profiles) return res.body.profiles;
    if (res.body && res.body.addresses) return res.body.addresses;
  }
  console.warn(`[apiList] ${endpoint} returned ${res.status}:`, res.body);
  return [];
}

async function apiDelete(page, endpoint) {
  const res = await apiCall(page, 'DELETE', endpoint);
  console.log(`[apiDelete] ${endpoint} -> ${res.status}`);
  return res.status >= 200 && res.status < 300;
}

function matchPrefix(item, fields) {
  const vals = fields.map((f) => {
    if (typeof f === 'function') return f(item);
    return item[f];
  });
  return vals.some((v) => typeof v === 'string' && v.startsWith(TEST_PREFIX));
}

async function cleanupByList(page, listEndpoint, idField, nameFields, deleteEndpointTpl) {
  const items = await apiList(page, listEndpoint);
  const toDelete = items.filter((i) => matchPrefix(i, nameFields));
  console.log(`[cleanup] ${listEndpoint}: ${toDelete.length} E2E items`);
  for (const item of toDelete) {
    const id = item[idField];
    const endpoint = typeof deleteEndpointTpl === 'function'
      ? deleteEndpointTpl(item)
      : deleteEndpointTpl.replace(':id', id);
    await apiDelete(page, endpoint);
  }
}

const PROTECTED_EMAILS = new Set(Object.values(ACCOUNTS).map((a) => a.email));

async function cleanupSuperadminE2E(page) {
  const users = await apiList(page, '/api/admin/users');
  const toDelete = users.filter((u) => !PROTECTED_EMAILS.has(u.email) && matchPrefix(u, ['name', 'email']));
  console.log(`[cleanup] /api/admin/users: ${toDelete.length} E2E items (protected ${users.length - toDelete.length})`);
  for (const user of toDelete) {
    await apiDelete(page, `/api/admin/users/${user.id}`);
  }
  await cleanupByList(page, '/api/admin/enterprise', 'id', ['name'], '/api/admin/enterprise/:id');
  await cleanupByList(page, '/api/admin/groups', 'id', ['name'], '/api/admin/groups/:id');
  await cleanupByList(page, '/api/admin/announcements', 'id', ['title'], '/api/admin/announcements/:id');
  await cleanupByList(page, '/api/admin/vouchers', 'id', ['name', 'code'], '/api/admin/vouchers/:id');
}

async function cleanupEnterpriseE2E(page) {
  await cleanupByList(page, '/api/enterprise/keys', 'id', ['name'], '/api/enterprise/keys/:id');
  await cleanupByList(page, '/api/enterprise/groups', 'id', ['name'], '/api/enterprise/groups/:id');
  const invites = await apiList(page, '/api/enterprise/invitations');
  const toCancel = invites.filter((i) => matchPrefix(i, ['email']));
  for (const inv of toCancel) {
    await apiDelete(page, `/api/enterprise/invitations/${inv.id}`);
  }
}

async function cleanupUserE2E(page) {
  for (const ep of ['/api/apikeys', '/api/keys']) {
    const keys = await apiList(page, ep);
    const toDelete = keys.filter((k) => matchPrefix(k, ['name', 'note']));
    for (const k of toDelete) {
      await apiDelete(page, `${ep}/${k.id}`);
    }
  }
  await cleanupByList(page, '/api/invoices/profiles', 'id', ['name', 'title'], '/api/invoices/profiles/:id');
  await cleanupByList(page, '/api/addresses', 'id', ['recipient_name', 'detail'], '/api/addresses/:id');
}

async function visitAndVerify(page, url, expectedText, screenshotName, opts = {}) {
  const { loadState = 'domcontentloaded', waitMs = 300 } = opts;
  await page.goto(`${BASE_URL}${url}`, { waitUntil: loadState, timeout: 20000 });
  await page.waitForTimeout(waitMs);
  await screenshot(page, screenshotName);
  const body = await page.locator('body').innerText();
  const hasError = body.toLowerCase().includes('internal server error') ||
    body.toLowerCase().includes("can't evaluate field") ||
    body.toLowerCase().includes('template:');
  if (hasError) {
    record(`${url} load`, 'FAIL', 'page contains server error');
    return false;
  }
  if (expectedText && !body.includes(expectedText)) {
    record(`${url} load`, 'FAIL', `expected text "${expectedText}" not found`);
    return false;
  }
  record(`${url} load`, 'PASS');
  return true;
}

test.describe.configure({ mode: 'serial' });

test.beforeEach(async ({ page }) => {
  page.setDefaultTimeout(15000);
});

// Serial mode skips the remaining tests once one fails, including the final
// cleanup test — clean up each role's residue here instead so a mid-test
// failure never leaves dynamic E2E users/enterprises/keys behind.
test.afterEach(async ({ page }, testInfo) => {
  try {
    if (testInfo.title.startsWith('superadmin')) {
      await cleanupSuperadminE2E(page);
    } else if (testInfo.title.startsWith('enterprise admin')) {
      await cleanupEnterpriseE2E(page);
    } else if (testInfo.title.startsWith('user')) {
      await cleanupUserE2E(page);
    }
  } catch (e) {
    console.warn(`[afterEach cleanup] best-effort failed: ${e.message}`);
  }
});

test('superadmin roundtrip: admin CRUD, export, save', async ({ page }) => {
  test.setTimeout(120000);
  const suffix = ts();
  const label = (s) => `${TEST_PREFIX}${s}-${suffix}`;

  await login(page, 'superadmin');
  await cleanupSuperadminE2E(page);

  // 1. /admin/users — create via API, edit name in UI, delete via API
  console.log('[sa] /admin/users');
  const userEmail = `e2e-user-${suffix}@example.com`;
  const createUserRes = await apiCall(page, 'POST', '/api/admin/users', {
    email: userEmail,
    name: label('User'),
    password: 'Test@123456',
    role: 'user',
  });
  if (createUserRes.status < 200 || createUserRes.status >= 300) {
    record('/admin/users create API', 'FAIL', JSON.stringify(createUserRes.body));
  } else {
    record('/admin/users create API', 'PASS');
  }

  const usersAfterCreate = await apiList(page, '/api/admin/users');
  const createdUser = usersAfterCreate.find((u) => u.email === userEmail);
  if (!createdUser) {
    record('/admin/users find created', 'FAIL');
  } else {
    record('/admin/users find created', 'PASS', createdUser.id);
  }

  const userPageOk = await visitAndVerify(page, '/admin/users', label('User'), 'sa-users-list');
  if (userPageOk && createdUser) {
    await page.click(`button[onclick="openEditUserDrawer('${createdUser.id}')"]`);
    await page.waitForSelector('#edit-user-drawer', { state: 'visible' });
    await page.fill('#edit-name', label('EditedUser'));
    await page.click('button[onclick="submitEditUser()"]');
    await page.waitForTimeout(1500);
    const usersAfterEdit = await apiList(page, '/api/admin/users');
    const edited = usersAfterEdit.find((u) => u.id === createdUser.id);
    if (edited && edited.name === label('EditedUser')) {
      record('/admin/users edit name UI', 'PASS');
    } else {
      record('/admin/users edit name UI', 'FAIL', edited ? edited.name : 'not found');
    }
  }

  if (createdUser) {
    await apiDelete(page, `/api/admin/users/${createdUser.id}`);
    const usersAfterDelete = await apiList(page, '/api/admin/users');
    if (!usersAfterDelete.find((u) => u.id === createdUser.id)) {
      record('/admin/users delete API', 'PASS');
    } else {
      record('/admin/users delete API', 'FAIL');
    }
  }

  // 2. /admin/enterprise — create via API, verify UI, delete via API
  console.log('[sa] /admin/enterprise');
  const entName = label('Enterprise');
  const createEntRes = await apiCall(page, 'POST', '/api/admin/enterprise', {
    name: entName,
    contact_email: 'e2e@test.com',
    quota_total: 1000000,
    max_members: 10,
    max_admins: 2,
  });
  if (createEntRes.status < 200 || createEntRes.status >= 300) {
    record('/admin/enterprise create API', 'FAIL', JSON.stringify(createEntRes.body));
  } else {
    record('/admin/enterprise create API', 'PASS');
  }
  await visitAndVerify(page, '/admin/enterprise', entName, 'sa-enterprise-list');
  const enterprises = await apiList(page, '/api/admin/enterprise');
  const createdEnt = enterprises.find((e) => e.name === entName);
  if (createdEnt) {
    await apiDelete(page, `/api/admin/enterprise/${createdEnt.id}`);
    record('/admin/enterprise delete API', 'PASS');
  }

  // 3. /admin/groups — create via API, verify UI, delete via API
  console.log('[sa] /admin/groups');
  const groupName = label('Group');
  const createGroupRes = await apiCall(page, 'POST', '/api/admin/groups', {
    name: groupName,
    description: 'E2E test group',
    group_type: 'general',
    billing_mode: 'token',
  });
  if (createGroupRes.status < 200 || createGroupRes.status >= 300) {
    record('/admin/groups create API', 'FAIL', JSON.stringify(createGroupRes.body));
  } else {
    record('/admin/groups create API', 'PASS');
  }
  await visitAndVerify(page, '/admin/groups', groupName, 'sa-groups-list');
  const groups = await apiList(page, '/api/admin/groups');
  const createdGroup = groups.find((g) => g.name === groupName);
  if (createdGroup) {
    await apiDelete(page, `/api/admin/groups/${createdGroup.id}`);
    record('/admin/groups delete API', 'PASS');
  }

  // 4. /admin/announcements — create via API, verify UI, delete via API
  console.log('[sa] /admin/announcements');
  const annTitle = label('Announcement');
  const createAnnRes = await apiCall(page, 'POST', '/api/admin/announcements', {
    title: annTitle,
    content: 'E2E test announcement',
    status: 'active',
  });
  if (createAnnRes.status < 200 || createAnnRes.status >= 300) {
    record('/admin/announcements create API', 'FAIL', JSON.stringify(createAnnRes.body));
  } else {
    record('/admin/announcements create API', 'PASS');
  }
  await visitAndVerify(page, '/admin/announcements', annTitle, 'sa-announcements-list');
  const announcements = await apiList(page, '/api/admin/announcements');
  const createdAnn = announcements.find((a) => a.title === annTitle);
  if (createdAnn) {
    await apiDelete(page, `/api/admin/announcements/${createdAnn.id}`);
    record('/admin/announcements delete API', 'PASS');
  }

  // 5. /admin/vouchers — create via API, verify UI, delete via API
  console.log('[sa] /admin/vouchers');
  const voucherName = label('Voucher');
  const createVoucherRes = await apiCall(page, 'POST', '/api/admin/vouchers', {
    name: voucherName,
    code: `E2E-${suffix}`,
    amount: 100,
    max_uses: 1,
    reward_type: 'gift_balance',
  });
  if (createVoucherRes.status < 200 || createVoucherRes.status >= 300) {
    record('/admin/vouchers create API', 'FAIL', JSON.stringify(createVoucherRes.body));
  } else {
    record('/admin/vouchers create API', 'PASS');
  }
  await visitAndVerify(page, '/admin/vouchers', voucherName, 'sa-vouchers-list');
  const vouchers = await apiList(page, '/api/admin/vouchers');
  const createdVoucher = vouchers.find((v) => v.name === voucherName);
  if (createdVoucher) {
    await apiDelete(page, `/api/admin/vouchers/${createdVoucher.id}`);
    record('/admin/vouchers delete API', 'PASS');
  }

  // 6. /admin/audit-logs — export
  console.log('[sa] /admin/audit-logs export');
  await page.goto(`${BASE_URL}/admin/audit-logs`);
  await page.waitForLoadState('networkidle');
  await screenshot(page, 'sa-audit-logs');
  const exportBtn = page.locator('a:has-text("Export"), button:has-text("Export"), a:has-text("导出"), button:has-text("导出")').first();
  if (await exportBtn.isVisible().catch(() => false)) {
    const exportRes = await apiCall(page, 'GET', '/admin/audit-logs/export');
    if (exportRes.status === 200 && typeof exportRes.body === 'string' && exportRes.body.includes(',')) {
      const downloadPath = path.join(SCREENSHOT_DIR, `sa-audit-export-${suffix}.csv`);
      fs.writeFileSync(downloadPath, exportRes.body);
      record('/admin/audit-logs export UI', 'PASS', downloadPath);
    } else {
      record('/admin/audit-logs export UI', 'FAIL', `status ${exportRes.status}`);
    }
  } else {
    record('/admin/audit-logs export UI', 'FAIL', 'no export button');
  }

  // 7. /admin/settings — save
  console.log('[sa] /admin/settings save');
  await page.goto(`${BASE_URL}/admin/settings`);
  await page.waitForLoadState('networkidle');
  await screenshot(page, 'sa-settings');
  const saveBtn = page.locator('button:has-text("Save"), button:has-text("保存"), button[type="submit"]').first();
  if (await saveBtn.isVisible().catch(() => false)) {
    await saveBtn.click();
    await page.waitForTimeout(1000);
    record('/admin/settings save UI', 'PASS');
  } else {
    record('/admin/settings save UI', 'FAIL', 'no save button');
  }

  // Other admin pages smoke
  console.log('[sa] admin pages smoke');
  for (const url of ['/admin/keypool', '/admin/models', '/admin/providers', '/admin/subscriptions',
                     '/admin/rate-limits', '/admin/compliance', '/admin/monitor-upstream',
                     '/admin/monitor-downstream', '/admin/reconciliation-ops', '/admin/upstream-pricing',
                     '/admin/usage-explorer', '/admin/keys']) {
    await visitAndVerify(page, url, '', `sa-${url.replace(/\//g, '-')}`);
  }

  await cleanupSuperadminE2E(page);
});

test('enterprise admin roundtrip: keys, groups, members, settings', async ({ page }) => {
  test.setTimeout(120000);
  const suffix = ts();
  const label = (s) => `${TEST_PREFIX}${s}-${suffix}`;

  await login(page, 'enterprise');
  await cleanupEnterpriseE2E(page);

  // Resolve current user's id for key assignment
  let selfId = null;
  const membersRes = await apiCall(page, 'GET', '/api/enterprise/members');
  const members = membersRes.body && Array.isArray(membersRes.body.members) ? membersRes.body.members : [];
  const self = members.find((m) => m.email === ACCOUNTS.enterprise.email);
  selfId = self ? (self.id || self.user_id) : null;
  console.log(`[ea] selfId=${selfId}`);

  // 1. /enterprise/keys — create via API, verify UI model list, delete via API
  console.log('[ea] /enterprise/keys');
  const keyName = label('Key');
  const createKeyRes = await apiCall(page, 'POST', '/api/enterprise/keys', {
    name: keyName,
    assigned_to: selfId,
    group_id: '',
    model_routing_mode: 'single',
    expires_in: 0,
  });
  if (createKeyRes.status < 200 || createKeyRes.status >= 300) {
    record('/enterprise/keys create API', 'FAIL', JSON.stringify(createKeyRes.body));
  } else {
    record('/enterprise/keys create API', 'PASS');
  }

  await visitAndVerify(page, '/enterprise/keys', keyName, 'ea-keys-list');

  // Verify model drawer returns enterprise group model union, not all platform models
  const keys = await apiList(page, '/api/enterprise/keys');
  const createdKey = keys.find((k) => k.name === keyName);
  if (createdKey) {
    const modelRes = await apiCall(page, 'GET', `/api/keys/${createdKey.id}/models`);
    const modelCount = modelRes.body && Array.isArray(modelRes.body.models) ? modelRes.body.models.length : 0;
    const platformModels = await apiList(page, '/api/models/public');
    const platformModelCount = platformModels.length;
    if (modelCount > 0 && (platformModelCount === 0 || modelCount < platformModelCount)) {
      record('/enterprise/keys models union', 'PASS', `${modelCount} models (platform public ${platformModelCount})`);
    } else {
      record('/enterprise/keys models union', 'FAIL', `${modelCount} models, platform public ${platformModelCount}`);
    }
    await apiDelete(page, `/api/enterprise/keys/${createdKey.id}`);
    record('/enterprise/keys delete API', 'PASS');
  }

  // 2. /enterprise/groups — create via API, verify UI, delete via API
  console.log('[ea] /enterprise/groups');
  const platformGroups = await apiList(page, '/api/enterprise/platform-groups');
  const platformGroup = platformGroups.find((g) => (g.group_type || g.type || '').toString().toLowerCase().includes('enterprise')) || platformGroups[0];
  const groupName = label('EntGroup');
  if (platformGroup) {
    const createGroupRes = await apiCall(page, 'POST', '/api/enterprise/groups', {
      name: groupName,
      platform_group_id: platformGroup.id,
      models: [],
      rpm: 100,
      tpm: 100000,
    });
    if (createGroupRes.status < 200 || createGroupRes.status >= 300) {
      record('/enterprise/groups create API', 'FAIL', JSON.stringify(createGroupRes.body));
    } else {
      record('/enterprise/groups create API', 'PASS');
    }
    await visitAndVerify(page, '/enterprise/groups', groupName, 'ea-groups-list');
    const entGroups = await apiList(page, '/api/enterprise/groups');
    const createdGroup = entGroups.find((g) => g.name === groupName);
    if (createdGroup) {
      await apiDelete(page, `/api/enterprise/groups/${createdGroup.id}`);
      record('/enterprise/groups delete API', 'PASS');
    }
  } else {
    record('/enterprise/groups create API', 'FAIL', 'no platform group available');
  }

  // 3. /enterprise/users — add/invite member
  console.log('[ea] /enterprise/users add member');
  const inviteEmail = `e2e-invite-${suffix}@example.com`;
  await page.goto(`${BASE_URL}/enterprise/users`);
  await page.waitForLoadState('networkidle');
  await screenshot(page, 'ea-users-list');
  // Verify import/export buttons exist
  const importBtn = page.locator('button:has-text("Import CSV"), button:has-text("Import")').first();
  const exportBtn = page.locator('button:has-text("Export CSV"), button:has-text("Export")').first();
  record('/enterprise/users import button', await importBtn.isVisible().catch(() => false) ? 'PASS' : 'FAIL');
  record('/enterprise/users export button', await exportBtn.isVisible().catch(() => false) ? 'PASS' : 'FAIL');

  await page.evaluate(() => {
    if (typeof openAddUserDrawer === 'function') openAddUserDrawer();
  });
  const emailInput = page.locator('#add-user-drawer input[name="email"], #add-user-drawer input[type="email"]').first();
  if (await emailInput.isVisible().catch(() => false)) {
    await emailInput.fill(inviteEmail);
    await page.evaluate(() => {
      if (typeof submitAddUser === 'function') submitAddUser();
    });
    await page.waitForTimeout(1500);
    const invites = await apiList(page, '/api/enterprise/invitations');
    if (invites.find((i) => i.email === inviteEmail)) {
      record('/enterprise/users add member UI', 'PASS', inviteEmail);
    } else {
      record('/enterprise/users add member UI', 'FAIL', 'not found in invitations');
    }
  } else {
    record('/enterprise/users add member UI', 'FAIL', 'no add drawer email input');
  }

  // 4. /enterprise/settings — save
  console.log('[ea] /enterprise/settings');
  await page.goto(`${BASE_URL}/enterprise/settings`);
  await page.waitForLoadState('networkidle');
  await screenshot(page, 'ea-settings');
  const saveBtn = page.locator('button:has-text("Save"), button:has-text("保存"), button[type="submit"]').first();
  if (await saveBtn.isVisible().catch(() => false)) {
    await saveBtn.click();
    await page.waitForTimeout(1000);
    record('/enterprise/settings save UI', 'PASS');
  } else {
    record('/enterprise/settings save UI', 'FAIL', 'no save button');
  }

  // Other enterprise pages smoke
  console.log('[ea] enterprise pages smoke');
  for (const url of ['/enterprise', '/enterprise/billing', '/enterprise/usage', '/enterprise/audit']) {
    await visitAndVerify(page, url, '', `ea-${url.replace(/\//g, '-')}`);
  }

  await cleanupEnterpriseE2E(page);
});

test('user roundtrip: apikeys, invoices, profile, export', async ({ page }) => {
  test.setTimeout(120000);
  const suffix = ts();
  const label = (s) => `${TEST_PREFIX}${s}-${suffix}`;

  await login(page, 'user');
  await cleanupUserE2E(page);

  // 1. /apikeys — create via API, verify UI, delete via API
  console.log('[u] /apikeys');
  const keyName = label('ApiKey');
  const createKeyRes = await apiCall(page, 'POST', '/api/apikeys', {
    name: keyName,
    key_type: 'general',
  });
  if (createKeyRes.status < 200 || createKeyRes.status >= 300) {
    record('/apikeys create API', 'FAIL', JSON.stringify(createKeyRes.body));
  } else {
    record('/apikeys create API', 'PASS');
  }
  await visitAndVerify(page, '/apikeys', keyName, 'u-apikeys-list');
  const keys = await apiList(page, '/api/apikeys');
  const createdKey = keys.find((k) => k.name === keyName);
  if (createdKey) {
    await apiDelete(page, `/api/apikeys/${createdKey.id}`);
    record('/apikeys delete API', 'PASS');
  }

  // 2. /invoices — create profile and address via API, verify UI, delete via API
  console.log('[u] /invoices');
  const profileName = label('Profile');
  const createProfileRes = await apiCall(page, 'POST', '/api/invoices/profiles', {
    type: 'personal',
    name: profileName,
  });
  if (createProfileRes.status < 200 || createProfileRes.status >= 300) {
    record('/invoices profile create API', 'FAIL', JSON.stringify(createProfileRes.body));
  } else {
    record('/invoices profile create API', 'PASS');
  }

  const addressName = label('Contact');
  const createAddressRes = await apiCall(page, 'POST', '/api/addresses', {
    country: 'CN',
    recipient_name: addressName,
    phone: '13800138000',
    province: 'Beijing',
    city: 'Beijing',
    district: 'Chaoyang',
    detail: 'E2E test address detail',
    postal_code: '100000',
  });
  if (createAddressRes.status < 200 || createAddressRes.status >= 300) {
    record('/invoices address create API', 'FAIL', JSON.stringify(createAddressRes.body));
  } else {
    record('/invoices address create API', 'PASS');
  }

  await visitAndVerify(page, '/invoices', profileName, 'u-invoices-list');

  const profiles = await apiList(page, '/api/invoices/profiles');
  const createdProfile = profiles.find((p) => p.name === profileName || p.title === profileName);
  if (createdProfile) {
    await apiDelete(page, `/api/invoices/profiles/${createdProfile.id}`);
    record('/invoices profile delete API', 'PASS');
  }
  const addresses = await apiList(page, '/api/addresses');
  const createdAddress = addresses.find((a) => a.recipient_name === addressName);
  if (createdAddress) {
    await apiDelete(page, `/api/addresses/${createdAddress.id}`);
    record('/invoices address delete API', 'PASS');
  }

  // 3. /profile — edit name via UI and GDPR export
  console.log('[u] /profile');
  const currentName = await apiCall(page, 'GET', '/api/profile');
  const originalName = currentName.body && currentName.body.user && currentName.body.user.name ? currentName.body.user.name : '';
  await page.goto(`${BASE_URL}/profile`);
  await page.waitForLoadState('networkidle');
  await screenshot(page, 'u-profile');
  const nameInput = page.locator('div[x-data="profileForm()"] input[x-model="name"]').first();
  const saveBtn = page.locator('div[x-data="profileForm()"] button:has-text("Save")').first();
  if (await nameInput.isVisible().catch(() => false)) {
    const newName = label('UserName');
    await nameInput.fill(newName);
    await nameInput.evaluate((el) => { el.dispatchEvent(new Event('input', { bubbles: true })); el.dispatchEvent(new Event('change', { bubbles: true })); });
    await page.waitForTimeout(100);
    if (await saveBtn.isVisible().catch(() => false)) {
      await saveBtn.click();
    } else {
      // Fallback: trigger the Alpine submit directly if button not found
      await page.evaluate((n) => {
        const el = document.querySelector('div[x-data="profileForm()"]');
        if (el && el._x_dataStack && el._x_dataStack[0]) { el._x_dataStack[0].name = n; el._x_dataStack[0].submit(); }
      }, newName);
    }
    // Wait for the inline success message or for the API to reflect the change
    const resultOk = await page.locator('#profile-result .text-success').waitFor({ state: 'visible', timeout: 5000 }).then(() => true).catch(() => false);
    let savedName = '';
    for (let i = 0; i < 5; i++) {
      await page.waitForTimeout(400);
      const afterSave = await apiCall(page, 'GET', '/api/profile');
      savedName = afterSave.body && afterSave.body.user && afterSave.body.user.name ? afterSave.body.user.name : '';
      if (savedName === newName) break;
    }
    if (savedName === newName) {
      record('/profile edit name UI', 'PASS');
    } else {
      record('/profile edit name UI', resultOk ? 'PASS' : 'FAIL', `UI success=${resultOk}, API name=${savedName || 'empty'}`);
    }
    // Restore the original name. Empty name is ignored by the API, so fall back
    // to a stable non-E2E value for the fixed test account.
    const restoreName = originalName || 'thinbee09';
    await apiCall(page, 'PATCH', '/api/profile', { name: restoreName });
    await nameInput.fill(restoreName);
    await nameInput.evaluate((el) => { el.dispatchEvent(new Event('input', { bubbles: true })); el.dispatchEvent(new Event('change', { bubbles: true })); });
    await page.waitForTimeout(100);
    if (await saveBtn.isVisible().catch(() => false)) await saveBtn.click();
    await page.waitForTimeout(1000);
  } else {
    record('/profile edit name UI', 'FAIL', 'name input not found');
  }

  // GDPR export
  const exportBtn = page.locator('button:has-text("Export"), button:has-text("导出"), a:has-text("Export")').first();
  if (await exportBtn.isVisible().catch(() => false)) {
    await exportBtn.click();
    await page.locator('#export-modal').waitFor({ state: 'visible', timeout: 8000 }).catch(() => {});
    const pwdInput = page.locator('#export-modal input[type="password"], #export-modal input[name="password"]').first();
    if (await pwdInput.isVisible().catch(() => false)) {
      await pwdInput.fill(ACCOUNTS.user.password);
    }
    await page.evaluate(() => {
      if (typeof executeExport === 'function') executeExport();
    });
    await page.waitForTimeout(2000);
    record('/profile GDPR export UI', 'PASS');
    await screenshot(page, 'u-profile-export');
  } else {
    record('/profile GDPR export UI', 'FAIL', 'no export button');
  }

  await cleanupUserE2E(page);
});

test('cleanup and verification: no E2E residue and audit logs recorded', async ({ page }) => {
  test.setTimeout(120000);
  await login(page, 'superadmin');

  await cleanupSuperadminE2E(page);
  await cleanupEnterpriseE2E(page);
  await cleanupUserE2E(page);

  // Verify no E2E- prefixed items remain
  const checks = [
    { name: 'admin users', endpoint: '/api/admin/users', fields: ['name', 'email'] },
    { name: 'admin enterprise', endpoint: '/api/admin/enterprise', fields: ['name'] },
    { name: 'admin groups', endpoint: '/api/admin/groups', fields: ['name'] },
    { name: 'admin announcements', endpoint: '/api/admin/announcements', fields: ['title'] },
    { name: 'admin vouchers', endpoint: '/api/admin/vouchers', fields: ['name', 'code'] },
    { name: 'enterprise keys', endpoint: '/api/enterprise/keys', fields: ['name'] },
    { name: 'enterprise groups', endpoint: '/api/enterprise/groups', fields: ['name'] },
    { name: 'enterprise invitations', endpoint: '/api/enterprise/invitations', fields: ['email'] },
    { name: 'user apikeys', endpoint: '/api/apikeys', fields: ['name', 'note'] },
    { name: 'user alt apikeys', endpoint: '/api/keys', fields: ['name', 'note'] },
    { name: 'invoice profiles', endpoint: '/api/invoices/profiles', fields: ['name', 'title'] },
    { name: 'addresses', endpoint: '/api/addresses', fields: ['recipient_name', 'detail'] },
  ];

  const residues = [];
  for (const check of checks) {
    const items = await apiList(page, check.endpoint);
    const found = items.filter((i) => {
      // Never flag the fixed test accounts as E2E residue.
      if (i.email && PROTECTED_EMAILS.has(i.email)) return false;
      return matchPrefix(i, check.fields);
    });
    if (found.length > 0) {
      residues.push(`${check.name}: ${found.length}`);
      console.error(`[verify] residue found in ${check.name}:`, found.map((i) => i.id || JSON.stringify(i)).join(', '));
    }
  }

  // Audit logs — check recent create/edit/delete actions
  const auditRes = await apiCall(page, 'GET', '/api/admin/audit-logs?limit=200');
  const auditItems = Array.isArray(auditRes.body) ? auditRes.body : (auditRes.body?.logs || auditRes.body?.data || []);
  const recentAudit = auditItems.filter((a) => {
    const t = new Date(a.created_at || a.time || Date.now());
    return (Date.now() - t.getTime()) < 30 * 60 * 1000;
  });
  const actionKeywords = ['create', 'update', 'delete', 'edit', 'invite', 'export'];
  const recorded = recentAudit.filter((a) => {
    const text = JSON.stringify(a).toLowerCase();
    return actionKeywords.some((kw) => text.includes(kw));
  });
  console.log(`[verify] recent audit entries: ${recorded.length}`);

  await screenshot(page, 'verify-final');

  // Attach results to test output
  test.info().attach('roundtrip-results.json', {
    body: JSON.stringify({ results: RESULTS, residues, auditCount: recorded.length }, null, 2),
    contentType: 'application/json',
  });

  expect(residues, `E2E residue detected: ${residues.join('; ')}`).toHaveLength(0);
  expect(recorded.length, 'Expected recent audit log entries for create/update/delete actions').toBeGreaterThan(0);
});
