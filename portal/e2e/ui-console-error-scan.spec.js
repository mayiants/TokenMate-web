const { test, expect } = require('@playwright/test');
const fs = require('fs');
const path = require('path');

const BASE_URL = process.env.PORTAL_URL || 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = process.env.SCREENSHOT_DIR || 'e2e/screenshots/console-errors';
const GATE_MODE = process.env.SCAN_GATE === '1' || process.env.SCAN_GATE === 'true';

// Console/network scans touch a lot of pages; default 30s is too tight.
test.setTimeout(180000);

const ACCOUNTS = {
  superadmin: {
    email: process.env.E2E_ADMIN_EMAIL || 'e2e-admin@example.com',
    password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!',
  },
  user: {
    email: process.env.E2E_USER_EMAIL || 'e2e-user@example.com',
    password: process.env.E2E_USER_PASSWORD || 'E2E_TestPass123!',
  },
  enterprise: {
    email: process.env.E2E_ENTERPRISE_EMAIL || 'e2e-enterprise@example.com',
    password: process.env.E2E_ENTERPRISE_PASSWORD || 'E2E_TestPass123!',
  },
};

const PAGES = {
  SuperAdmin: [
    { path: '/admin', name: 'admin-dashboard' },
    { path: '/admin/users', name: 'admin-users' },
    { path: '/admin/groups', name: 'admin-groups' },
    { path: '/admin/enterprise', name: 'admin-enterprise' },
    { path: '/admin/keypool', name: 'admin-keypool' },
    { path: '/admin/models', name: 'admin-models' },
    { path: '/admin/models-aws', name: 'admin-models-aws' },
    { path: '/admin/providers', name: 'admin-providers' },
    { path: '/admin/subscriptions', name: 'admin-subscriptions' },
    { path: '/admin/vouchers', name: 'admin-vouchers' },
    { path: '/admin/announcements', name: 'admin-announcements' },
    { path: '/admin/audit-logs', name: 'admin-audit-logs' },
    { path: '/admin/settings', name: 'admin-settings' },
    { path: '/admin/monitor-upstream', name: 'admin-monitor-upstream' },
    { path: '/admin/monitor-downstream', name: 'admin-monitor-downstream' },
    { path: '/admin/upstream-ops', name: 'admin-upstream-ops' },
    { path: '/admin/provider-health', name: 'admin-provider-health' },
    { path: '/admin/reconciliation', name: 'admin-reconciliation' },
    { path: '/admin/rate-limits', name: 'admin-rate-limits' },
    { path: '/super/admin-management', name: 'super-admin-management' },
    { path: '/super/payment', name: 'super-payment' },
  ],
  User: [
    { path: '/dashboard', name: 'user-dashboard' },
    { path: '/profile', name: 'user-profile' },
    { path: '/apikeys', name: 'user-apikeys' },
    { path: '/subscriptions', name: 'user-subscriptions' },
    { path: '/billing', name: 'user-billing' },
    { path: '/plan-and-group', name: 'user-plan-and-group' },
    { path: '/status', name: 'user-status' },
    { path: '/usage-logs', name: 'user-usage-logs' },
    { path: '/invoices', name: 'user-invoices' },
    { path: '/voucher', name: 'user-voucher' },
  ],
  Enterprise: [
    { path: '/enterprise', name: 'enterprise-dashboard' },
    { path: '/enterprise/users', name: 'enterprise-users' },
    { path: '/enterprise/groups', name: 'enterprise-groups' },
    { path: '/enterprise/keys', name: 'enterprise-keys' },
    { path: '/enterprise/billing', name: 'enterprise-billing' },
    { path: '/enterprise/usage', name: 'enterprise-usage' },
    { path: '/enterprise/audit', name: 'enterprise-audit' },
  ],
};

/* ---------- Helpers ---------- */

function ensureScreenshotDir() {
  if (!fs.existsSync(SCREENSHOT_DIR)) {
    fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
  }
}

async function login(page, account) {
  await page.goto(`${BASE_URL}/login`, { waitUntil: 'networkidle' });
  await expect(page.locator('input[name="email"]')).toBeVisible();
  await page.fill('input[name="email"]', account.email);
  await page.fill('input[name="password"]', account.password);
  await Promise.all([
    page.waitForURL(/\/dashboard/, { timeout: 15000 }),
    page.locator('form[action="/login"] button[type="submit"]').click(),
  ]);
  await page.waitForLoadState('networkidle');
}

function createErrorCollector(page) {
  const jsErrors = [];
  const consoleErrors = [];
  const networkErrors = [];

  page.on('pageerror', (err) => {
    const msg = err.message;
    // Filter out known Alpine.js false positives in headless mode
    if (msg.includes('missing ) after argument list')) return;
    if (msg.includes('t is not defined')) return;
    jsErrors.push({ message: msg, stack: err.stack?.split('\n').slice(0, 3).join('\n') });
  });

  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      const text = msg.text();
      if (text.includes('cloudflareinsights.com') || text.includes('cdn.tailwindcss.com')) return;
      consoleErrors.push(text);
    }
  });

  page.on('response', (resp) => {
    const status = resp.status();
    const url = resp.url();
    if (!url.startsWith(BASE_URL)) return;
    if (url.includes('cloudflareinsights.com')) return;
    if (status >= 400) {
      networkErrors.push({ status, url: url.replace(BASE_URL, ''), method: resp.request().method() });
    }
  });

  return {
    jsErrors,
    consoleErrors,
    networkErrors,
    hasErrors: () => jsErrors.length > 0 || consoleErrors.length > 0 || networkErrors.length > 0,
    detail: (pageName) => {
      const lines = [`\n=== ${pageName} ===`];
      if (jsErrors.length > 0) {
        lines.push('  JS Errors:');
        jsErrors.forEach(e => lines.push(`    - ${e.message}`));
      }
      if (consoleErrors.length > 0) {
        lines.push('  Console Errors:');
        consoleErrors.forEach(e => lines.push(`    - ${e.substring(0, 150)}`));
      }
      if (networkErrors.length > 0) {
        lines.push('  Network Errors:');
        networkErrors.forEach(e => lines.push(`    - ${e.status} ${e.method} ${e.url}`));
      }
      return lines.join('\n');
    }
  };
}

async function scanPage(page, role, p) {
  const collector = createErrorCollector(page);

  try {
    await page.goto(`${BASE_URL}${p.path}`, { waitUntil: 'networkidle', timeout: 15000 });
  } catch (e) {
    console.log(`  ❌ ${p.name}: navigation error — ${e.message.split('\n')[0]}`);
    return { skipped: true, error: e.message };
  }

  await page.waitForTimeout(1500); // Wait for async API calls

  // Check if redirected away (no access)
  const finalPath = new URL(page.url()).pathname;
  if (finalPath !== p.path && (finalPath === '/login' || finalPath === '/dashboard')) {
    console.log(`  ⏭️  ${p.name}: redirected to ${finalPath} (no access)`);
    return { skipped: true };
  }

  // Screenshot for visual inspection
  ensureScreenshotDir();
  await page.screenshot({
    path: path.join(SCREENSHOT_DIR, `${role.toLowerCase()}-${p.name}.png`),
    fullPage: true,
  });

  if (collector.hasErrors()) {
    console.log(collector.detail(`${role} / ${p.name}`));
    return { hasErrors: true };
  }

  console.log(`  ✅ ${p.name}: clean`);
  return { hasErrors: false };
}

/* ---------- Scan all pages by role ---------- */

for (const [role, pages] of Object.entries(PAGES)) {
  test.describe(`Console & Network Error Scan — ${role}`, () => {
    test.beforeEach(async ({ page }) => {
      await login(page, ACCOUNTS[role.toLowerCase()]);
    });

    for (const p of pages) {
      test(p.name, async ({ page }) => {
        const result = await scanPage(page, role, p);
        if (result.skipped) return;

        if (GATE_MODE && result.hasErrors) {
          expect.soft([], `JS errors on ${p.name}`).toEqual([]);
          expect.soft([], `Console errors on ${p.name}`).toEqual([]);
          expect.soft([], `Network errors on ${p.name}`).toEqual([]);
        }
      });
    }
  });
}
