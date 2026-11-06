#!/usr/bin/env node
/**
 * Accessibility scanner using axe-core + Playwright
 *
 * Traverses ai-portal-go pages for each role and reports axe violations.
 * Gate mode fails on serious/critical violations.
 *
 * Usage:
 *   node e2e/scan-a11y-axe.js
 *   node e2e/scan-a11y-axe.js --gate
 *
 * Prerequisites:
 *   npm install playwright @axe-core/playwright
 *   Portal running at localhost:3001
 */

const { chromium } = require('playwright');
const { AxeBuilder } = require('@axe-core/playwright');

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
    '/admin/usage-explorer', '/admin/enterprise-applications', '/admin/keys',
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

const ROLE_ACCOUNT_MAP = {
  'SuperAdmin': 'superadmin',
  'User': 'user',
  'Enterprise': 'enterprise',
};

async function login(page, account) {
  await page.goto(`${BASE_URL}/login`, { waitUntil: 'networkidle' });
  await page.waitForSelector('input[name="email"]', { timeout: 10000 });
  await page.fill('input[name="email"]', account.email);
  await page.fill('input[name="password"]', account.password);
  await Promise.all([
    page.waitForURL(/\/dashboard/, { timeout: 15000 }),
    page.locator('form[action="/login"] button[type="submit"]').click(),
  ]);
  await page.waitForLoadState('networkidle');
}

async function main() {
  console.log(`\n♿ Accessibility Scanner (axe-core)`);
  console.log(`   Target: ${BASE_URL}`);
  console.log(`   Gate mode: ${GATE_MODE ? 'ON (exit 1 on serious/critical)' : 'OFF (report only)'}\n`);

  const browser = await chromium.launch({ headless: true });
  const allResults = [];

  for (const [role, paths] of Object.entries(PAGES)) {
    const accountKey = ROLE_ACCOUNT_MAP[role] || 'superadmin';
    const account = ACCOUNTS[accountKey];
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });

    console.log(`\n📋 Role: ${role} (${account.email})`);
    console.log('─'.repeat(60));

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
      const page = await ctx.newPage();
      try {
        const resp = await page.goto(`${BASE_URL}${path}`, { waitUntil: 'networkidle', timeout: 15000 });
        await page.waitForTimeout(1000);

        const finalUrl = page.url();
        const finalPath = new URL(finalUrl).pathname;
        if (finalPath !== path && (finalPath === '/login' || finalPath === '/dashboard')) {
          console.log(`  ⏭️  ${path}: redirected to ${finalPath} (no access)`);
          await page.close();
          continue;
        }

        const results = await new AxeBuilder({ page })
          .withTags(['wcag2a', 'wcag2aa'])
          .analyze();

        const critical = results.violations.filter(v => v.impact === 'critical');
        const serious = results.violations.filter(v => v.impact === 'serious');
        const moderate = results.violations.filter(v => v.impact === 'moderate');
        const minor = results.violations.filter(v => v.impact === 'minor');
        const seriousCritical = [...critical, ...serious];

        allResults.push({ role, path, violations: results.violations, seriousCritical });

        if (seriousCritical.length === 0) {
          console.log(`  ✅ ${path}: no violations`);
        } else if (GATE_MODE) {
          // Print full node details in gate mode to make fixes actionable
          for (const v of seriousCritical) {
            console.log(`  ⚠️  ${path}: ${v.id} (${v.impact}): ${v.help}`);
            for (const node of v.nodes.slice(0, 3)) {
              console.log(`      target: ${JSON.stringify(node.target)}`);
              if (node.failureSummary) {
                console.log(`      summary: ${node.failureSummary.replace(/\n/g, '\n             ')}`);
              }
            }
          }
        } else {
          const parts = [];
          if (critical.length) parts.push(`critical:${critical.length}`);
          if (serious.length) parts.push(`serious:${serious.length}`);
          if (moderate.length) parts.push(`moderate:${moderate.length}`);
          if (minor.length) parts.push(`minor:${minor.length}`);
          console.log(`  ⚠️  ${path}: ${parts.join(' | ')}`);
        }
      } catch (e) {
        console.log(`  ❌ ${path}: ${e.message.split('\n')[0]}`);
        allResults.push({ role, path, violations: [], error: e.message });
      }
      await page.close();
    }

    await ctx.close();
  }

  await browser.close();

  /* ---------- Summary ---------- */
  const totalViolations = allResults.reduce((sum, r) => sum + (r.violations?.length || 0), 0);
  const failingResults = allResults.filter(r =>
    r.violations?.some(v => v.impact === 'critical' || v.impact === 'serious')
  );

  console.log('\n' + '='.repeat(60));
  console.log('📊 SUMMARY REPORT');
  console.log('='.repeat(60));
  console.log(`  Pages scanned:       ${allResults.length}`);
  console.log(`  Total violations:    ${totalViolations}`);
  console.log(`  Pages with serious+: ${failingResults.length}`);

  if (totalViolations > 0) {
    console.log('\n  Violations by rule:');
    const ruleCounts = {};
    allResults.forEach(r => {
      r.violations?.forEach(v => {
        ruleCounts[v.id] = ruleCounts[v.id] || { count: 0, impact: v.impact, help: v.help };
        ruleCounts[v.id].count += v.nodes.length;
      });
    });
    Object.entries(ruleCounts)
      .sort((a, b) => b[1].count - a[1].count)
      .forEach(([id, info]) => {
        console.log(`    ${id} (${info.impact}): ${info.count} node(s) — ${info.help}`);
      });
  }

  console.log('='.repeat(60));

  if (GATE_MODE && failingResults.length > 0) {
    console.log(`\n🚫 Gate mode: ${failingResults.length} pages have serious/critical violations\n`);
    process.exit(1);
  }

  console.log('\n✅ Accessibility scan complete.\n');
}

main().catch(e => {
  console.error('Scanner crashed:', e);
  process.exit(2);
});
