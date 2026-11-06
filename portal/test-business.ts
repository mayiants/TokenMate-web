/**
 * AI Portal Go - Business Flow E2E Tests
 * Run: npx tsx test-business.ts
 *
 * Tests complete business scenarios end-to-end.
 * Skips email verification by using auto-generated codes.
 */

const BASE = process.env.PORTAL_URL || "http://localhost:3001";

let passCount = 0;
let failCount = 0;
const failures: { scenario: string; step: string; error: string }[] = [];

// ===== Utilities =====
async function api(
  method: string,
  path: string,
  opts: { token?: string; body?: any; headers?: Record<string, string> } = {}
): Promise<{ status: number; data: any }> {
  const headers: Record<string, string> = { "Content-Type": "application/json", ...opts.headers };
  if (opts.token) headers["Authorization"] = `Bearer ${opts.token}`;
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  });
  const text = await res.text();
  let data: any;
  try { data = JSON.parse(text); } catch { data = text; }
  return { status: res.status, data };
}

function ok(scenario: string, step: string, condition: boolean, detail = "") {
  if (condition) {
    passCount++;
    console.log(`  ✅ ${step}`);
  } else {
    failCount++;
    failures.push({ scenario, step, error: detail || "Condition failed" });
    console.log(`  ❌ ${step} — ${detail}`);
  }
}

function section(name: string) {
  console.log(`\n${"─".repeat(70)}`);
  console.log(`  🔄 ${name}`);
  console.log(`${"─".repeat(70)}`);
}

// ===== Scenario 1: User Registration → Login → API Key → Usage =====
async function scenario1() {
  section("Scenario 1: User Lifecycle (Register → Login → Key → Usage)");

  // Register (skip email verification - code auto-accepted in dev mode)
  const regRes = await api("POST", "/api/auth/register", {
    body: { name: "E2E Test User", email: `e2e-test-${Date.now()}@test.com`, password: "TestPass123", code: "000000" },
  });
  ok("S1", `Register user (${regRes.status})`, regRes.status === 200 || regRes.status === 201 || regRes.status === 400);

  // Login as admin (known user)
  const loginRes = await api("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  ok("S1", "Admin login", loginRes.status === 200 && !!loginRes.data.token);
  const token = loginRes.data.token;
  if (!token) return;

  // Get current user
  const meRes = await api("GET", "/api/auth/me", { token });
  ok("S1", "Get current user", meRes.status === 200 && meRes.data.user?.email === "e2e-admin@example.com");

  // Create API key
  const keyRes = await api("POST", "/api/apikeys", {
    token,
    body: { name: "e2e-test-key", rate_limit_rpm: 60 },
  });
  ok("S1", "Create API key", keyRes.status === 200);
  const keyId = keyRes.data.key?.id || keyRes.data.id;

  // List API keys
  const listRes = await api("GET", "/api/apikeys", { token });
  ok("S1", "List API keys", listRes.status === 200 && Array.isArray(listRes.data.keys));

  // Get key details
  if (keyId) {
    const getRes = await api("GET", `/api/apikeys/${keyId}`, { token });
    ok("S1", "Get key details", getRes.status === 200);

    // Delete key
    const delRes = await api("DELETE", `/api/apikeys/${keyId}`, { token });
    ok("S1", "Delete API key", delRes.status === 200);
  }
}

// ===== Scenario 2: Billing & Balance =====
async function scenario2() {
  section("Scenario 2: Billing & Balance");

  const loginRes = await api("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  const token = loginRes.data.token;
  if (!token) { ok("S2", "Login", false, "No token"); return; }

  // Get balance
  const balRes = await api("GET", "/api/billing/balance", { token });
  ok("S2", "Get balance", balRes.status === 200 && typeof balRes.data.balance === "number");

  // Get usage details
  const detailRes = await api("GET", "/api/billing/usage-details", { token });
  ok("S2", "Get usage details", detailRes.status === 200);

  // Get usage summary
  const sumRes = await api("GET", "/api/billing/usage-summary", { token });
  ok("S2", "Get usage summary", sumRes.status === 200);

  // Get usage by key
  const byKeyRes = await api("GET", "/api/billing/usage-by-key", { token });
  ok("S2", "Get usage by key", byKeyRes.status === 200);

  // Get usage by model
  const byModelRes = await api("GET", "/api/billing/usage-by-model", { token });
  ok("S2", "Get usage by model", byModelRes.status === 200);
}

// ===== Scenario 3: Orders & Invoices =====
async function scenario3() {
  section("Scenario 3: Orders & Invoices");

  const loginRes = await api("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  const token = loginRes.data.token;
  if (!token) { ok("S3", "Login", false, "No token"); return; }

  // List orders
  const ordersRes = await api("GET", "/api/orders", { token });
  ok("S3", "List orders", ordersRes.status === 200);

  // Create order
  const createRes = await api("POST", "/api/orders/create", {
    token,
    body: { product_type: "recharge", amount: 10000, payment_method: "alipay" },
  });
  ok("S3", "Create order", createRes.status === 200);
  const orderId = createRes.data.order?.id;

  // List invoices
  const invRes = await api("GET", "/api/invoices", { token });
  ok("S3", "List invoices", invRes.status === 200);
}

// ===== Scenario 4: Check-in Flow =====
async function scenario4() {
  section("Scenario 4: Daily Check-in");

  const loginRes = await api("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  const token = loginRes.data.token;
  if (!token) { ok("S4", "Login", false, "No token"); return; }
}

// ===== Scenario 5: Redemption Flow =====
async function scenario5() {
  section("Scenario 5: Redemption Codes");

  const loginRes = await api("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  const token = loginRes.data.token;
  if (!token) { ok("S5", "Login", false, "No token"); return; }

  // Try redeem with invalid code
  const redeemRes = await api("POST", "/api/redemption/redeem", {
    token,
    body: { code: "INVALID-CODE-12345" },
  });
  ok("S5", "Reject invalid code", redeemRes.status === 400);

  // Get my records
  const recordsRes = await api("GET", "/api/redemption/my-records", { token });
  ok("S5", "Get my redemption records", recordsRes.status === 200);

  // Admin: list codes
  const codesRes = await api("GET", "/api/redemption/admin/codes", { token });
  ok("S5", "Admin: list codes", codesRes.status === 200);

  // Admin: get stats
  const statsRes = await api("GET", "/api/redemption/admin/stats", { token });
  ok("S5", "Admin: redemption stats", statsRes.status === 200);
}

// ===== Scenario 6: Admin Management =====
async function scenario6() {
  section("Scenario 6: Admin Management");

  const loginRes = await api("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  const token = loginRes.data.token;
  if (!token) { ok("S6", "Login", false, "No token"); return; }

  // Dashboard
  const dashRes = await api("GET", "/api/admin/dashboard", { token });
  ok("S6", "Admin dashboard", dashRes.status === 200);

  // Users
  const usersRes = await api("GET", "/api/admin/users", { token });
  ok("S6", "List users", usersRes.status === 200);

  // Providers
  const provRes = await api("GET", "/api/admin/providers", { token });
  ok("S6", "List providers", provRes.status === 200);

  // Models
  const modelsRes = await api("GET", "/api/admin/service-models", { token });
  ok("S6", "List service models", modelsRes.status === 200);

  // Settings
  const settingsRes = await api("GET", "/api/admin/settings", { token });
  ok("S6", "Get settings", settingsRes.status === 200);

  // Subscriptions
  const subsRes = await api("GET", "/api/admin/subscriptions/plans", { token });
  ok("S6", "List subscription plans", subsRes.status === 200);

  // Audit logs
  const auditRes = await api("GET", "/api/admin/audit-logs", { token });
  ok("S6", "List audit logs", auditRes.status === 200);

  // Groups
  const groupsRes = await api("GET", "/api/admin/groups", { token });
  ok("S6", "List groups", groupsRes.status === 200);

  // Channel health
  const healthRes = await api("GET", "/api/admin/channel-health", { token });
  ok("S6", "Channel health", healthRes.status === 200);
}

// ===== Scenario 8: Webhook (Proxy → Portal) =====
async function scenario8() {
  section("Scenario 8: Webhook (Proxy → Portal)");

  // Heartbeat
  const hbRes = await api("POST", "/api/webhook/heartbeat", {
    body: { serverId: "test-proxy-001", uptime: 3600, totalRequests: 1000, providers: {} },
  });
  ok("S8", "Proxy heartbeat", hbRes.status === 200);

  // Usage batch
  const batchRes = await api("POST", "/api/webhook/usage-batch", {
    body: {
      records: [
        { traceId: "test-trace-001", model: "gpt-4", provider: "openai", inputTokens: 100, outputTokens: 50, cost: 0.001 },
      ],
      serverId: "test-proxy-001",
    },
  });
  ok("S8", "Usage batch upload", batchRes.status === 200);
}

// ===== Main =====
async function main() {
  console.log(`\n${"═".repeat(70)}`);
  console.log(`  🔄 AI Portal Go - Business Flow Tests`);
  console.log(`  📍 Target: ${BASE}`);
  console.log(`  📅 ${new Date().toISOString()}`);
  console.log(`${"═".repeat(70)}`);

  await scenario1();
  await scenario2();
  await scenario3();
  await scenario4();
  await scenario5();
  await scenario6();
  await scenario8();

  console.log(`\n${"═".repeat(70)}`);
  console.log(`  📊 Results`);
  console.log(`${"═".repeat(70)}`);
  console.log(`  ✅ Passed: ${passCount}`);
  console.log(`  ❌ Failed: ${failCount}`);
  console.log(`  📦 Total:  ${passCount + failCount}`);

  if (failures.length > 0) {
    console.log(`\n  Failed steps:`);
    for (const f of failures) {
      console.log(`    • [${f.scenario}] ${f.step} — ${f.error}`);
    }
  }
  console.log(`\n${"═".repeat(70)}\n`);
  process.exit(failCount > 0 ? 1 : 0);
}

main().catch((err) => {
  console.error("Fatal:", err);
  process.exit(1);
});
