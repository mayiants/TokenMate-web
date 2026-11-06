#!/usr/bin/env node
/**
 * Console & Network Error Scanner
 *
 * 遍历 ai-portal-go 所有页面，收集 JS 错误、Console 错误、网络 4xx/5xx，
 * 输出汇总报告。不需要 Playwright test runner，直接 node 运行。
 *
 * 用法:
 *   node e2e/scan-console-errors.js
 *   node e2e/scan-console-errors.js --gate   # 有错误时退出码 1
 *
 * 前置:
 *   npm install playwright   (首次)
 *   Portal 运行在 localhost:3001
 */

const { chromium } = require('playwright');

const BASE_URL = process.env.PORTAL_URL || 'http://127.0.0.1:3001';
const GATE_MODE = process.argv.includes('--gate');

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
  'SuperAdmin': [
    '/admin', '/admin/users', '/admin/groups', '/admin/enterprise',
    '/admin/keypool', '/admin/models', '/admin/models-aws', '/admin/providers',
    '/admin/subscriptions', '/admin/vouchers', '/admin/announcements',
    '/admin/audit-logs', '/admin/settings', '/admin/monitor-upstream',
    '/admin/monitor-downstream', '/admin/upstream-ops', '/admin/provider-health',
    '/admin/reconciliation', '/super/admin-management', '/super/payment',
    '/admin/pricing', '/admin/rate-limits', '/admin/compliance',
    '/admin/reconciliation-ops', '/admin/upstream-pricing',
    '/admin/usage-explorer', '/admin/enterprise-applications',
  ],
  'User': [
    '/dashboard', '/profile', '/apikeys', '/subscriptions',
    '/billing', '/plan-and-group', '/status', '/usage-logs', '/invoices', '/voucher',
    '/billing-details', '/my-group', '/upstream-ops', '/subscriptions/my',
  ],
  'Enterprise': [
    '/enterprise', '/enterprise/users', '/enterprise/groups', '/enterprise/keys',
    '/enterprise/billing', '/enterprise/usage', '/enterprise/audit',
    '/enterprise/alerts', '/enterprise/settings',
  ],
};

/* ---------- Login helpers per role ---------- */

const ROLE_ACCOUNT_MAP = {
  'SuperAdmin': 'superadmin',
  'User': 'user',
  'Enterprise': 'enterprise',
};

/* ---------- Helpers ---------- */

async function login(page, account) {
  await page.goto(`${BASE_URL}/login`, { waitUntil: 'networkidle' });
  // Wait for the login form to be fully rendered
  await page.waitForSelector('input[name="email"]', { timeout: 10000 });
  await page.fill('input[name="email"]', account.email);
  await page.fill('input[name="password"]', account.password);
  await Promise.all([
    page.waitForURL(/\/dashboard/, { timeout: 15000 }),
    page.locator('form[action="/login"] button[type="submit"]').click(),
  ]);
  // Extra wait for page to fully load
  await page.waitForLoadState('networkidle');
}

function createErrorCollector(page) {
  const errors = { js: [], console: [], network: [] };

  page.on('pageerror', (err) => {
    errors.js.push({ message: err.message, stack: err.stack?.split('\n').slice(0, 3).join('\n') });
  });

  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      errors.console.push(msg.text());
    }
  });

  page.on('response', (resp) => {
    const status = resp.status();
    const url = resp.url();
    if (!url.startsWith(BASE_URL)) return;
    if (url.includes('cloudflareinsights.com')) return;
    if (status >= 400) {
      errors.network.push({ status, url: url.replace(BASE_URL, ''), method: resp.request().method() });
    }
  });

  return errors;
}

/* ---------- Main ---------- */

async function main() {
  console.log(`\n🔍 Console & Network Error Scanner`);
  console.log(`   Target: ${BASE_URL}`);
  console.log(`   Gate mode: ${GATE_MODE ? 'ON (exit 1 on errors)' : 'OFF (report only)'}\n`);

  const browser = await chromium.launch({ headless: true });

  const allResults = []; // { role, path, errors }

  for (const [role, paths] of Object.entries(PAGES)) {
    const accountKey = ROLE_ACCOUNT_MAP[role] || 'superadmin';
    const account = ACCOUNTS[accountKey];

    // Use a fresh context per role to avoid cookie conflicts
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });

    console.log(`\n📋 Role: ${role} (${account.email})`);
    console.log('─'.repeat(60));

    // Login once; cookies live in the context
    const loginPage = await ctx.newPage();
    try {
      await login(loginPage, account);
    } catch (e) {
      console.log(`  ❌ Login failed for ${role}: ${e.message}`);
      await loginPage.close();
      await ctx.close();
      continue;
    }
    await loginPage.close();

    for (const path of paths) {
      // Fresh page per path so listeners/errors are never carried over
      const page = await ctx.newPage();
      const errors = createErrorCollector(page);
      let httpStatus = 0;

      try {
        const resp = await page.goto(`${BASE_URL}${path}`, { waitUntil: 'networkidle', timeout: 15000 });
        httpStatus = resp?.status() || 0;
        await page.waitForTimeout(1500); // Wait for async API calls
      } catch (e) {
        console.log(`  ❌ ${path}: navigation error — ${e.message.split('\n')[0]}`);
        allResults.push({ role, path, errors: { js: [], console: [], network: [], nav: e.message } });
        await page.close();
        continue;
      }

      // Check if redirected away (no access). Playwright returns the final
      // response, so httpStatus is usually 200 even after a redirect.
      const finalUrl = page.url();
      const finalPath = new URL(finalUrl).pathname;
      if (finalPath !== path && (finalPath === '/login' || finalPath === '/dashboard')) {
        console.log(`  ⏭️  ${path}: redirected to ${finalPath} (no access)`);
        await page.close();
        continue;
      }

      const hasErrors = errors.js.length > 0 || errors.console.length > 0 || errors.network.length > 0;
      allResults.push({ role, path, errors });

      if (hasErrors) {
        const parts = [];
        if (errors.js.length) parts.push(`JS:${errors.js.length}`);
        if (errors.console.length) parts.push(`Console:${errors.console.length}`);
        if (errors.network.length) parts.push(`Network:${errors.network.length}`);
        console.log(`  ⚠️  ${path}: ${parts.join(' | ')}`);

        errors.js.forEach(e => console.log(`      JS: ${e.message.substring(0, 120)}`));
        errors.console.forEach(e => console.log(`      Console: ${e.substring(0, 150)}`));
        errors.network.forEach(e => console.log(`      Network: ${e.status} ${e.method} ${e.url}`));
      } else {
        console.log(`  ✅ ${path}`);
      }

      await page.close();
    }

    await ctx.close();
  }

  await browser.close();

  /* ---------- Summary Report ---------- */

  const errorPages = allResults.filter(r =>
    r.errors.js?.length > 0 || r.errors.console?.length > 0 || r.errors.network?.length > 0 || r.errors.nav
  );
  const cleanPages = allResults.filter(r => !errorPages.includes(r));

  console.log('\n' + '='.repeat(60));
  console.log('📊 SUMMARY REPORT');
  console.log('='.repeat(60));
  console.log(`  Total pages scanned: ${allResults.length}`);
  console.log(`  Clean pages:         ${cleanPages.length}`);
  console.log(`  Pages with errors:   ${errorPages.length}`);

  if (errorPages.length > 0) {
    console.log('\n❌ Pages with errors:\n');

    // Group by error type for a cleaner report
    const jsErrorPages = errorPages.filter(r => r.errors.js?.length > 0);
    const consoleErrorPages = errorPages.filter(r => r.errors.console?.length > 0);
    const networkErrorPages = errorPages.filter(r => r.errors.network?.length > 0);

    if (jsErrorPages.length > 0) {
      console.log('  ── JS Errors ──');
      jsErrorPages.forEach(r => {
        r.errors.js.forEach(e => {
          console.log(`  [${r.role}] ${r.path}: ${e.message.substring(0, 150)}`);
        });
      });
    }

    if (consoleErrorPages.length > 0) {
      console.log('\n  ── Console Errors ──');
      // Deduplicate console errors
      const seen = new Set();
      consoleErrorPages.forEach(r => {
        r.errors.console.forEach(e => {
          const key = e.substring(0, 80);
          if (seen.has(key)) return;
          seen.add(key);
          console.log(`  [${r.role}] ${r.path}: ${e.substring(0, 150)}`);
        });
      });
    }

    if (networkErrorPages.length > 0) {
      console.log('\n  ── Network Errors (4xx/5xx) ──');
      const seen = new Set();
      networkErrorPages.forEach(r => {
        r.errors.network.forEach(e => {
          const key = `${e.status} ${e.url}`;
          if (seen.has(key)) return;
          seen.add(key);
          console.log(`  [${r.role}] ${r.path}: ${e.status} ${e.method} ${e.url}`);
        });
      });
    }
  }

  console.log('\n' + '='.repeat(60));

  if (GATE_MODE && errorPages.length > 0) {
    console.log(`\n🚫 Gate mode: exiting with code 1 (${errorPages.length} pages have errors)\n`);
    process.exit(1);
  } else if (errorPages.length > 0) {
    console.log(`\n⚠️  Found errors on ${errorPages.length} pages. Run with --gate to fail CI.\n`);
  } else {
    console.log('\n✅ All pages clean — no errors found.\n');
  }
}

main().catch(e => {
  console.error('Scanner crashed:', e);
  process.exit(2);
});
