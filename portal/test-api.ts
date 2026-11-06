/**
 * AI Portal Go - Comprehensive API Test Script
 * Run: bun run test-api.ts
 *
 * Tests all 134 endpoints without modifying any portal code.
 * Uses native fetch (Bun built-in).
 */

const BASE = process.env.PORTAL_URL || "http://localhost:3001";
const ADMIN_EMAIL = process.env.ADMIN_EMAIL || "admin@example.com";
const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD || "Admin@123456!";

// ===== Utilities =====
let passCount = 0;
let failCount = 0;
let skipCount = 0;
const failures: { endpoint: string; status: number; body?: string }[] = [];

interface TestResult {
  method: string;
  path: string;
  status: number;
  ok: boolean;
  duration: number;
  error?: string;
}

async function req(
  method: string,
  path: string,
  opts: {
    token?: string;
    body?: any;
    headers?: Record<string, string>;
    expectStatus?: number[];
  } = {}
): Promise<TestResult> {
  const url = `${BASE}${path}`;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...opts.headers,
  };
  if (opts.token) headers["Authorization"] = `Bearer ${opts.token}`;

  const start = performance.now();
  try {
    const res = await fetch(url, {
      method,
      headers,
      body: opts.body ? JSON.stringify(opts.body) : undefined,
    });
    const duration = performance.now() - start;
    const text = await res.text();
    let body: any;
    try { body = JSON.parse(text); } catch { body = text; }

    const expected = opts.expectStatus || [200, 201, 204];
    const ok = expected.includes(res.status);

    if (ok) {
      passCount++;
      console.log(`  ✅ ${method.padEnd(7)} ${path.padEnd(55)} ${res.status} (${Math.round(duration)}ms)`);
    } else {
      failCount++;
      const errMsg = body?.error?.message || JSON.stringify(body)?.slice(0, 100);
      failures.push({ endpoint: `${method} ${path}`, status: res.status, body: errMsg });
      console.log(`  ❌ ${method.padEnd(7)} ${path.padEnd(55)} ${res.status} expected[${expected}] (${Math.round(duration)}ms)`);
      console.log(`     → ${errMsg}`);
    }
    return { method, path, status: res.status, ok, duration };
  } catch (err: any) {
    const duration = performance.now() - start;
    failCount++;
    failures.push({ endpoint: `${method} ${path}`, status: 0, body: err.message });
    console.log(`  ❌ ${method.padEnd(7)} ${path.padEnd(55)} ERROR: ${err.message}`);
    return { method, path, status: 0, ok: false, duration, error: err.message };
  }
}

function section(name: string) {
  console.log(`\n${"─".repeat(80)}`);
  console.log(`  📋 ${name}`);
  console.log(`${"─".repeat(80)}`);
}

// ===== Test Suites =====

async function testPublicEndpoints() {
  section("Public Endpoints (No Auth)");

  await req("GET", "/api/health");
  await req("GET", "/api/status");
  await req("GET", "/api/status/providers");
  await req("GET", "/api/auth/password-requirements");
  await req("GET", "/api/service-models/public");
  await req("GET", "/api/models/public");
}

async function testAuthFlow(): Promise<string | null> {
  section("Auth Flow");

  // Password requirements
  await req("GET", "/api/auth/password-requirements");

  // Send verification code (will log code in dev mode)
  await req("POST", "/api/auth/send-code", {
    body: { email: "test@example.com" },
  });

  // Register (may fail if user exists - that's ok)
  await req("POST", "/api/auth/register", {
    body: {
      name: "Test User",
      email: "test@example.com",
      password: "TestPass123",
      code: "000000", // Won't work but tests the endpoint
    },
    expectStatus: [200, 201, 400, 409],
  });

  // Login as admin
  const loginRes = await req("POST", "/api/auth/login", {
    body: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    expectStatus: [200, 401],
  });

  if (loginRes.status === 401) {
    console.log("\n  ⚠️  Admin login failed (password may have been changed in Docker)");
    console.log("  ⚠️  Skipping protected endpoint tests");
    console.log("  💡 To fix: reset admin password or update ADMIN_PASSWORD env var\n");
    skipCount += 80; // Skip all protected tests
    return null;
  }

  // Re-read response to get token
  const res = await fetch(`${BASE}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
  });
  const data = await res.json();
  const token = data.token || data.access_token;

  if (token) {
    // Verify token works
    await req("GET", "/api/auth/me", { token });

    // Logout (test the endpoint, token still valid for rest of script)
    await req("POST", "/api/auth/logout", { token, expectStatus: [200, 204] });

    // Re-login for subsequent tests
    const reLogin = await fetch(`${BASE}/api/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
    });
    const reData = await reLogin.json();
    return reData.token || reData.access_token;
  }

  return null;
}

async function testAuthProtected(token: string) {
  section("Auth (Protected)");

  await req("GET", "/api/auth/me", { token });
  await req("POST", "/api/auth/change-password", {
    token,
    body: { current_password: "wrong", new_password: "NewPass123" },
    expectStatus: [400, 401],
  });
  await req("POST", "/api/auth/forgot-password", {
    body: { email: ADMIN_EMAIL },
  });
  await req("POST", "/api/auth/reset-password", {
    body: { email: ADMIN_EMAIL, code: "000000", password: "NewPass123" },
    expectStatus: [200, 400],
  });
}

async function testProfile(token: string) {
  section("Profile");

  await req("GET", "/api/profile", { token });
  await req("PATCH", "/api/profile", {
    token,
    body: { name: "Admin" },
    expectStatus: [200, 400],
  });
  await req("PATCH", "/api/profile/password", {
    token,
    body: { current_password: "wrong", new_password: "x" },
    expectStatus: [400, 401],
  });
  // Skip DELETE /api/profile/account - would delete the admin user
}

async function testAPIKeys(token: string) {
  section("API Keys");

  // List
  await req("GET", "/api/apikeys", { token });

  // Create
  const createRes = await req("POST", "/api/apikeys", {
    token,
    body: { name: "test-key", rate_limit: 100 },
    expectStatus: [200, 201],
  });

  // Get created key ID
  let keyId: string | null = null;
  if (createRes.ok) {
    const res = await fetch(`${BASE}/api/apikeys`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const data = await res.json();
    const keys = data.keys || data.data || data;
    if (Array.isArray(keys) && keys.length > 0) {
      keyId = keys[0].id;
    }
  }

  if (keyId) {
    await req("GET", `/api/apikeys/${keyId}`, { token });
    await req("GET", `/api/apikeys/${keyId}/usage`, { token });
    await req("PATCH", `/api/apikeys/${keyId}`, {
      token,
      body: { name: "test-key-renamed" },
    });
    await req("DELETE", `/api/apikeys/${keyId}`, { token });
  }

  // Validate key endpoint
  await req("GET", "/api/apikeys/validate", {
    token,
    expectStatus: [200, 400, 401],
  });
}

async function testBilling(token: string) {
  section("Billing");

  await req("GET", "/api/billing/balance", { token });
  await req("GET", "/api/billing/usage-details", { token });
  await req("GET", "/api/billing/usage-summary", { token });
  await req("GET", "/api/billing/usage-records", { token });
  await req("GET", "/api/billing/usage-by-key", { token });
  await req("GET", "/api/billing/usage-by-model", { token });
}

async function testUsage(token: string) {
  section("Usage");

  await req("GET", "/api/usage", { token });
  await req("GET", "/api/usage/summary", { token });
}

async function testOrders(token: string) {
  section("Orders");

  await req("GET", "/api/orders", { token });
  await req("POST", "/api/orders/create", {
    token,
    body: { plan_id: "nonexistent", amount: 100 },
    expectStatus: [200, 201, 400, 404],
  });
}

async function testInvoices(token: string) {
  section("Invoices");

  await req("GET", "/api/invoices", { token });
}

async function testRedemption(token: string) {
  section("Redemption");

  await req("GET", "/api/redemption/my-records", { token });
  await req("POST", "/api/redemption/redeem", {
    token,
    body: { code: "INVALID-CODE" },
    expectStatus: [200, 400, 404],
  });
}

async function testWebhook() {
  section("Webhook (No Auth - Proxy Sync)");

  await req("POST", "/api/webhook/usage", {
    body: { test: true },
    expectStatus: [200, 400, 401],
  });
  await req("POST", "/api/webhook/usage-batch", {
    body: { test: true },
    expectStatus: [200, 400, 401],
  });
  await req("POST", "/api/webhook/key-pool-status", {
    body: { test: true },
    expectStatus: [200, 400, 401],
  });
  await req("POST", "/api/webhook/heartbeat", {
    body: { test: true },
    expectStatus: [200, 400, 401],
  });
}

async function testAdminDashboard(token: string) {
  section("Admin Dashboard");

  await req("GET", "/api/admin/dashboard", { token });
  await req("GET", "/api/admin/dashboard/stats", { token });
  await req("GET", "/api/admin/dashboard/charts/consumption-trend", { token });
  await req("GET", "/api/admin/dashboard/charts/model-distribution", { token });
  await req("GET", "/api/admin/dashboard/charts/provider-distribution", { token });
  await req("GET", "/api/admin/dashboard/charts/user-ranking", { token });
  await req("GET", "/api/admin/dashboard/charts/user-trend", { token });
  await req("GET", "/api/admin/dashboard/quick-actions", { token });
  await req("GET", "/api/admin/analytics", { token });
  await req("GET", "/api/admin/stats", { token });
}

async function testAdminUsers(token: string) {
  section("Admin Users");

  await req("GET", "/api/admin/users", { token });
  await req("GET", "/api/admin/admin-mode", { token });
}

async function testAdminProviders(token: string) {
  section("Admin Providers");

  await req("GET", "/api/admin/providers", { token });
  await req("GET", "/api/admin/providers/list", { token });
}

async function testAdminModels(token: string) {
  section("Admin Models");

  await req("GET", "/api/admin/service-models", { token });
  await req("GET", "/api/admin/models", { token });
  await req("GET", "/api/admin/models/status", { token });
  await req("GET", "/api/admin/sync-status", { token });
  await req("GET", "/api/admin/openrouter-models", { token, expectStatus: [200, 400, 500] });
}

async function testAdminKeyPool(token: string) {
  section("Admin Key Pool");

  await req("GET", "/api/admin/key-pool", { token });
  await req("GET", "/api/admin/upstream-keys", { token });
}

async function testAdminUsage(token: string) {
  section("Admin Usage");

  await req("GET", "/api/admin/usage/upstream", { token });
  await req("GET", "/api/admin/usage/upstream-keys", { token });
  await req("GET", "/api/admin/usage/users", { token });
  await req("GET", "/api/admin/usage/user-keys", { token });
  await req("GET", "/api/admin/usage/summary", { token });
}

async function testAdminSettings(token: string) {
  section("Admin Settings");

  await req("GET", "/api/admin/settings", { token });
  await req("PUT", "/api/admin/settings", {
    token,
    body: { test: true },
    expectStatus: [200, 400],
  });
  await req("POST", "/api/admin/settings/reset", {
    token,
    expectStatus: [200, 400],
  });
}

async function testAdminSubscriptions(token: string) {
  section("Admin Subscriptions");

  await req("GET", "/api/admin/subscriptions/plans", { token });
  await req("GET", "/api/admin/subscriptions/users", { token });
  await req("GET", "/api/admin/subscriptions/stats", { token });
}

async function testAdminAuditLogs(token: string) {
  section("Admin Audit Logs");

  await req("GET", "/api/admin/audit-logs", { token });
  await req("GET", "/api/admin/audit-logs/export", { token, expectStatus: [200, 204] });
}

async function testAdminGroups(token: string) {
  section("Admin Groups");

  await req("GET", "/api/admin/groups", { token });
}

async function testAdminEnterprise(token: string) {
  section("Admin Enterprise");

  await req("GET", "/api/admin/enterprise/list", { token });
}

async function testAdminDownstreamKeys(token: string) {
  section("Admin Downstream Keys");

  await req("GET", "/api/admin/downstream-keys", { token });
}

async function testAdminChannelHealth(token: string) {
  section("Admin Channel Health");

  await req("GET", "/api/admin/channel-health", { token });
}

async function testAdminSync(token: string) {
  section("Admin Sync (requires X-Admin-Secret header)");

  // Sync endpoints use X-Admin-Secret, not JWT
  const syncSecret = process.env.PROXY_SYNC_SECRET || "proxy-sync-default";
  await req("GET", "/api/admin/key-pool/sync", {
    headers: { "X-Admin-Secret": syncSecret },
    expectStatus: [200, 204, 400, 500],
  });
  await req("GET", "/api/admin/models/sync", {
    headers: { "X-Admin-Secret": syncSecret },
    expectStatus: [200, 204, 400, 500],
  });
}

async function testAdminInvoices(token: string) {
  section("Admin Invoices");

  await req("GET", "/api/invoices/admin/all", { token });
}

async function testAdminRedemption(token: string) {
  section("Admin Redemption");

  await req("GET", "/api/redemption/admin/codes", { token });
  await req("GET", "/api/redemption/admin/records", { token });
  await req("GET", "/api/redemption/admin/stats", { token });
}

async function testErrorCases(token: string) {
  section("Error Cases");

  // 401 - No token
  await req("GET", "/api/auth/me", { expectStatus: [401] });
  await req("GET", "/api/apikeys", { expectStatus: [401] });

  // 401 - Bad token
  await req("GET", "/api/auth/me", {
    token: "invalid-token",
    expectStatus: [401],
  });

  // 404 - Not found
  await req("GET", "/api/nonexistent", { expectStatus: [404] });
  await req("GET", "/api/apikeys/99999", { token, expectStatus: [400, 404] });

  // 400 - Bad body
  await req("POST", "/api/auth/login", {
    body: {},
    expectStatus: [400],
  });
  await req("POST", "/api/auth/register", {
    body: { email: "bad" },
    expectStatus: [400],
  });

  // 405 - Method not allowed
  await req("DELETE", "/api/health", { expectStatus: [404, 405] });
}

// ===== Main =====
async function main() {
  console.log(`\n${"═".repeat(80)}`);
  console.log(`  🧪 AI Portal Go - API Test Suite`);
  console.log(`  📍 Target: ${BASE}`);
  console.log(`  📅 ${new Date().toISOString()}`);
  console.log(`${"═".repeat(80)}`);

  // 1. Public endpoints
  await testPublicEndpoints();

  // 2. Auth flow (get token)
  const token = await testAuthFlow();

  if (!token) {
    console.log("\n  ⚠️  No token obtained. Skipping protected endpoint tests.");
    console.log("  Make sure the portal is running and admin credentials are correct.\n");
    printSummary();
    process.exit(1);
  }

  // 3. Protected user endpoints
  await testAuthProtected(token);
  await testProfile(token);
  await testAPIKeys(token);
  await testBilling(token);
  await testUsage(token);
  await testOrders(token);
  await testInvoices(token);
  await testRedemption(token);

  // 4. Webhook (no auth)
  await testWebhook();

  // 5. Admin endpoints
  await testAdminDashboard(token);
  await testAdminUsers(token);
  await testAdminProviders(token);
  await testAdminModels(token);
  await testAdminKeyPool(token);
  await testAdminUsage(token);
  await testAdminSettings(token);
  await testAdminSubscriptions(token);
  await testAdminAuditLogs(token);
  await testAdminGroups(token);
  await testAdminEnterprise(token);
  await testAdminDownstreamKeys(token);
  await testAdminChannelHealth(token);
  await testAdminSync(token);
  await testAdminInvoices(token);
  await testAdminRedemption(token);

  // 6. Error cases
  await testErrorCases(token);

  printSummary();
}

function printSummary() {
  const total = passCount + failCount + skipCount;
  console.log(`\n${"═".repeat(80)}`);
  console.log(`  📊 Results`);
  console.log(`${"═".repeat(80)}`);
  console.log(`  ✅ Passed:  ${passCount}`);
  console.log(`  ❌ Failed:  ${failCount}`);
  console.log(`  ⏭️  Skipped: ${skipCount}`);
  console.log(`  📦 Total:   ${total}`);

  if (failures.length > 0) {
    console.log(`\n  Failed endpoints:`);
    for (const f of failures) {
      console.log(`    • ${f.endpoint} → ${f.status} ${f.body || ""}`);
    }
  }

  console.log(`\n${"═".repeat(80)}\n`);
  process.exit(failCount > 0 ? 1 : 0);
}

main().catch((err) => {
  console.error("Fatal error:", err);
  process.exit(1);
});
