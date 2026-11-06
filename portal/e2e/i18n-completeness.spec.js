const { test, expect } = require('@playwright/test');
const fs = require('fs');
const path = require('path');

const BASE_URL = process.env.PORTAL_URL || 'http://127.0.0.1:3001';
const SCREENSHOT_DIR = process.env.SCREENSHOT_DIR || 'e2e/screenshots/i18n-leaks';

// Long-running: many pages × 2 locales × 3 roles.
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

const ADMIN_PAGES = [
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
];

const ENTERPRISE_PAGES = [
  { path: '/enterprise', name: 'enterprise-dashboard' },
  { path: '/enterprise/users', name: 'enterprise-users' },
  { path: '/enterprise/groups', name: 'enterprise-groups' },
  { path: '/enterprise/keys', name: 'enterprise-keys' },
  { path: '/enterprise/billing', name: 'enterprise-billing' },
  { path: '/enterprise/usage', name: 'enterprise-usage' },
  { path: '/enterprise/audit', name: 'enterprise-audit' },
];

const USER_PAGES = [
  { path: '/dashboard', name: 'user-dashboard' },
  { path: '/profile', name: 'user-profile' },
  { path: '/apikeys', name: 'user-apikeys' },
  { path: '/subscriptions', name: 'user-subscriptions' },
  { path: '/billing', name: 'user-billing' },
  { path: '/plan-and-group', name: 'user-plan-and-group' },
  { path: '/usage-logs', name: 'user-usage-logs' },
  { path: '/invoices', name: 'user-invoices' },
  { path: '/voucher', name: 'user-voucher' },
  { path: '/status', name: 'user-status' },
];

const SUPER_PAGES = [
  { path: '/super/admin-management', name: 'super-admin-management' },
  { path: '/super/payment', name: 'super-payment' },
];

const ROLE_PAGES = {
  superadmin: [...ADMIN_PAGES, ...SUPER_PAGES],
  enterprise: ENTERPRISE_PAGES,
  user: USER_PAGES,
};

const PROPER_NOUNS_PATH = path.join(process.cwd(), 'web/static/js/proper-nouns.json');
const properNouns = (() => {
  try {
    const raw = fs.readFileSync(PROPER_NOUNS_PATH, 'utf8');
    return Object.keys(JSON.parse(raw).nouns || {});
  } catch (e) {
    console.warn('Could not load proper-nouns.json:', e.message);
    return [];
  }
})();

// Extra English words that are acceptable in the Chinese UI (brand names, currency codes, etc.).
const EXTRA_ENGLISH_ALLOWLIST = [
  'Woodgit', 'woodgit', 'WOODGIT',
  'USD', 'CNY', '¥', '$',
  'ID', 'No.', 'No ', 'OK',
  'v', 'V',
  // User-generated seed / test data (names, plans, vouchers, models)
  'E2E User', 'E2E Enterprise', 'Sub User', 'Test User', 'Test Plan',
  'Smart Routing',
];

// Chinese user-generated content that is acceptable in the English UI.
const EXTRA_CHINESE_ALLOWLIST = [
  '新用户注册欢迎奖励',
  '智能路由',
];

function isAllowedEnglish(text) {
  const combined = [...properNouns, ...EXTRA_ENGLISH_ALLOWLIST];
  for (const noun of combined) {
    if (text.includes(noun)) return true;
  }
  return false;
}

function isAllowedChinese(text) {
  for (const noun of properNouns) {
    if (text.includes(noun)) return true;
  }
  for (const phrase of EXTRA_CHINESE_ALLOWLIST) {
    if (text.includes(phrase)) return true;
  }
  return false;
}

function isNoise(text) {
  const t = text.trim();
  if (t.length <= 1) return true;
  // Pure numbers / punctuation / symbols
  if (/^[\d\s\(\)\[\]\{\}\|\\\/\:\;\,\.\?\!\@\#\%\^\&\*\-\+\=\_\~\`\'\"]+$/.test(t)) return true;
  // Email / URL-ish
  if (/^[\w\.-]+@[\w\.-]+\.\w+$/.test(t)) return true;
  if (/^(https?:|www\.)/i.test(t)) return true;
  return false;
}

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

async function setLocaleCookie(page, locale) {
  await page.context().addCookies([
    { name: 'locale', value: locale, domain: '127.0.0.1', path: '/', httpOnly: false, sameSite: 'Lax' },
  ]);
}

async function setLocale(page, locale) {
  // Keep cookie in sync and persist the choice to the DB (profile page overrides from DB).
  await setLocaleCookie(page, locale);
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === 'csrf_token')?.value || '';
  await page.request.post(`${BASE_URL}/lang`, {
    form: { lang: locale, csrf_token: csrf },
    headers: { Referer: `${BASE_URL}/dashboard` },
  });
}

async function getVisibleTextNodes(page) {
  return await page.evaluate(() => {
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        const parent = node.parentElement;
        if (parent) {
          const tag = parent.tagName.toLowerCase();
          if (tag === 'script' || tag === 'style' || tag === 'noscript') return NodeFilter.FILTER_REJECT;
          if (parent.closest('script, style, noscript')) return NodeFilter.FILTER_REJECT;
        }
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    const out = [];
    while (walker.nextNode()) {
      const t = walker.currentNode.textContent.trim();
      if (t) out.push(t);
    }
    return out;
  });
}

async function scanForLeaks(page, locale) {
  const texts = await getVisibleTextNodes(page);
  const leaks = [];

  for (const text of texts) {
    if (isNoise(text)) continue;

    if (locale === 'en') {
      // In English mode, any Chinese character visible to user is a leak.
      if (/[\u4e00-\u9fff]/.test(text) && !isAllowedChinese(text)) {
        leaks.push(text);
      }
    } else {
      // In Chinese mode, any real English phrase (2+ words) is a leak unless allowed.
      if (/[A-Za-z][A-Za-z0-9]*(?:\s+[A-Za-z][A-Za-z0-9]*)+/.test(text) && !isAllowedEnglish(text)) {
        leaks.push(text);
      }
    }
  }

  return leaks;
}

async function runLeakScan(page, role, locale) {
  const pages = ROLE_PAGES[role];
  const allLeaks = [];

  for (const p of pages) {
    await page.goto(`${BASE_URL}${p.path}`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(500);

    const leaks = await scanForLeaks(page, locale);
    if (leaks.length > 0) {
      allLeaks.push({ page: p.name, leaks });
      // Screenshot for manual review
      const shotDir = path.join(SCREENSHOT_DIR, `${role}-${locale}`);
      fs.mkdirSync(shotDir, { recursive: true });
      await page.screenshot({ path: path.join(shotDir, `${p.name}.png`), fullPage: true });
    }
  }

  return allLeaks;
}

for (const role of Object.keys(ROLE_PAGES)) {
  test.describe(`I18n DOM Leak Scan - ${role}`, () => {
    test.beforeEach(async ({ page }) => {
      // locale cookie must be set before login so all requests (including login) use it.
      await setLocaleCookie(page, 'en');
      await login(page, ACCOUNTS[role]);
    });

    test('English pages should not leak Chinese', async ({ page }) => {
      await setLocale(page, 'en');
      const leaks = await runLeakScan(page, role, 'en');
      if (leaks.length > 0) {
        console.log(`  ⚠️ Chinese leaks (${role}/en):`, JSON.stringify(leaks, null, 2));
      }
      expect(leaks, `Chinese text leaked on English ${role} pages`).toEqual([]);
    });

    test('Chinese pages should not leak English phrases', async ({ page }) => {
      await setLocale(page, 'zh');
      const leaks = await runLeakScan(page, role, 'zh');
      if (leaks.length > 0) {
        console.log(`  ⚠️ English leaks (${role}/zh):`, JSON.stringify(leaks, null, 2));
      }
      expect(leaks, `English phrase leaked on Chinese ${role} pages`).toEqual([]);
    });
  });
}
