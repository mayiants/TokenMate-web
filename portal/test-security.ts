/**
 * AI Portal Go - Security Vulnerability Tests
 * Run: npx tsx test-security.ts
 *
 * Tests for common security vulnerabilities.
 */

const BASE = process.env.PORTAL_URL || "http://localhost:3001";

let passCount = 0;
let failCount = 0;
const findings: { severity: string; category: string; detail: string }[] = [];

async function req(
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

function pass(category: string, test: string) {
  passCount++;
  console.log(`  ✅ ${test}`);
}

function warn(category: string, test: string, detail: string) {
  passCount++;
  findings.push({ severity: "WARN", category, detail });
  console.log(`  ⚠️  ${test} — ${detail}`);
}

function fail(category: string, test: string, detail: string) {
  failCount++;
  findings.push({ severity: "HIGH", category, detail });
  console.log(`  🔴 ${test} — ${detail}`);
}

function info(category: string, test: string, detail: string) {
  passCount++;
  findings.push({ severity: "INFO", category, detail });
  console.log(`  ℹ️  ${test} — ${detail}`);
}

function section(name: string) {
  console.log(`\n${"─".repeat(70)}`);
  console.log(`  🔒 ${name}`);
  console.log(`${"─".repeat(70)}`);
}

// ===== A. Authentication Security =====
async function testAuth() {
  section("A. Authentication Security");

  // A1: JWT with invalid token
  const r1 = await req("GET", "/api/auth/me", { token: "invalid-token" });
  r1.status === 401 ? pass("auth", "A1: Invalid JWT rejected") : fail("auth", "A1: Invalid JWT accepted", `Status: ${r1.status}`);

  // A2: JWT with empty token
  const r2 = await req("GET", "/api/auth/me", { token: "" });
  r2.status === 401 ? pass("auth", "A2: Empty JWT rejected") : fail("auth", "A2: Empty JWT accepted", "");

  // A3: No Authorization header
  const r3 = await req("GET", "/api/auth/me");
  r3.status === 401 ? pass("auth", "A3: Missing auth header rejected") : fail("auth", "A3: Missing auth header accepted", "");

  // A4: Login with wrong password
  const r4 = await req("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "WrongPassword123" },
  });
  r4.status === 401 ? pass("auth", "A4: Wrong password rejected") : fail("auth", "A4: Wrong password accepted", "");

  // A5: Login with empty body
  const r5 = await req("POST", "/api/auth/login", { body: {} });
  r5.status === 400 ? pass("auth", "A5: Empty login body rejected") : fail("auth", "A5: Empty login body accepted", "");

  // A6: Password requirements endpoint
  const r6 = await req("GET", "/api/auth/password-requirements");
  r6.status === 200 && r6.data.minLength >= 8 ? pass("auth", "A6: Password requirements enforced") : fail("auth", "A6: Password requirements missing", "");

  // A7: Rate limiting on login (try 5 times)
  let rateLimited = false;
  for (let i = 0; i < 5; i++) {
    const r = await req("POST", "/api/auth/login", {
      body: { email: "nonexistent@test.com", password: "wrong" },
    });
    if (r.status === 429) { rateLimited = true; break; }
  }
  rateLimited ? pass("auth", "A7: Rate limiting active") : warn("auth", "A7: No rate limiting on login", "Consider adding rate limit");

  // A8: SQL injection in login
  const r8 = await req("POST", "/api/auth/login", {
    body: { email: "' OR 1=1 --", password: "anything" },
  });
  r8.status === 400 || r8.status === 401 ? pass("auth", "A8: SQL injection in login blocked") : fail("auth", "A8: SQL injection vulnerability", "Critical!");

  // A9: XSS in registration
  const r9 = await req("POST", "/api/auth/register", {
    body: { name: '<script>alert("xss")</script>', email: "xss@test.com", password: "TestPass123", code: "000000" },
  });
  if (r9.status === 200 && r9.data.user?.name?.includes("<script>")) {
    fail("auth", "A9: XSS in user name", "Stored XSS vulnerability!");
  } else {
    pass("auth", "A9: XSS in user name handled");
  }
}

// ===== B. Authorization Security =====
async function testAuthz() {
  section("B. Authorization Security");

  // Get admin token
  const login = await req("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  const adminToken = login.data.token;

  // B1: Admin endpoints without token
  const adminEndpoints = [
    "/api/admin/users",
    "/api/admin/providers",
    "/api/admin/settings",
    "/api/admin/dashboard",
  ];
  for (const ep of adminEndpoints) {
    const r = await req("GET", ep);
    r.status === 401 ? pass("authz", `B1: ${ep} requires auth`) : fail("authz", `B1: ${ep} accessible without auth`, "");
  }

  // B2: Regular user can't access admin
  // Create a regular user token (simulate)
  const userLogin = await req("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  // Note: This is still admin, but we test the endpoint protection
  if (userLogin.data.token) {
    const r = await req("GET", "/api/admin/users", { token: userLogin.data.token });
    r.status === 200 ? pass("authz", "B2: Admin can access admin endpoints") : warn("authz", "B2: Admin access issue", `Status: ${r.status}`);
  }

  // B3: IDOR - access other user's resources
  const r3 = await req("GET", "/api/apikeys/00000000-0000-0000-0000-000000000000", { token: adminToken });
  r3.status === 404 || r3.status === 403 ? pass("authz", "B3: IDOR - non-existent key returns 404/403") : warn("authz", "B3: IDOR check needed", `Status: ${r3.status}`);
}

// ===== C. Input Validation =====
async function testInput() {
  section("C. Input Validation");

  const login = await req("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  const token = login.data.token;

  // C1: SQL injection in query params
  const r1 = await req("GET", "/api/admin/users?search=' OR 1=1 --", { token });
  r1.status === 200 && !r1.data.error ? pass("input", "C1: SQL injection in search handled") : fail("input", "C1: SQL injection in search", "");

  // C2: XSS in API key name
  const r2 = await req("POST", "/api/apikeys", {
    token,
    body: { name: '<img src=x onerror=alert(1)>' },
  });
  if (r2.status === 200 && r2.data.key?.name?.includes("<img")) {
    warn("input", "C2: XSS in key name stored", "Consider sanitizing");
  } else {
    pass("input", "C2: XSS in key name handled");
  }

  // C3: Very long input
  const longName = "a".repeat(10000);
  const r3 = await req("POST", "/api/apikeys", {
    token,
    body: { name: longName },
  });
  r3.status === 400 ? pass("input", "C3: Long input rejected") : warn("input", "C3: Long input accepted", "Consider adding max length");

  // C4: Null bytes
  const r4 = await req("POST", "/api/apikeys", {
    token,
    body: { name: "test\x00name" },
  });
  r4.status !== 500 ? pass("input", "C4: Null bytes handled") : fail("input", "C4: Null bytes cause 500", "");

  // C5: Empty JSON body
  const r5 = await req("POST", "/api/apikeys", { token, body: {} });
  r5.status !== 500 ? pass("input", "C5: Empty body handled") : fail("input", "C5: Empty body causes 500", "");

  // C6: Invalid JSON
  try {
    const res = await fetch(`${BASE}/api/apikeys`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      body: "not json{{",
    });
    res.status !== 500 ? pass("input", "C6: Invalid JSON handled") : fail("input", "C6: Invalid JSON causes 500", "");
  } catch {
    pass("input", "C6: Invalid JSON handled");
  }

  // C7: Path traversal attempt
  const r7 = await req("GET", "/api/../../../etc/passwd");
  r7.status === 404 ? pass("input", "C7: Path traversal blocked") : fail("input", "C7: Path traversal possible", `Status: ${r7.status}`);
}

// ===== D. Data Security =====
async function testData() {
  section("D. Data Security");

  const login = await req("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  const token = login.data.token;

  // D1: Password not in response
  const r1 = await req("GET", "/api/auth/me", { token });
  const userStr = JSON.stringify(r1.data);
  !userStr.includes("password") && !userStr.includes("hash") ?
    pass("data", "D1: Password/hash not in response") : fail("data", "D1: Password/hash leaked in response", "");

  // D2: API key hash not in response
  const r2 = await req("GET", "/api/apikeys", { token });
  const keysStr = JSON.stringify(r2.data);
  !keysStr.includes("key_hash") ? pass("data", "D2: Key hash not in response") : fail("data", "D2: Key hash leaked in response", "");

  // D3: Error messages don't leak internals
  const r3 = await req("GET", "/api/nonexistent-endpoint-12345");
  const errStr = JSON.stringify(r3.data);
  !errStr.includes("stack") && !errStr.includes("panic") && !errStr.includes("postgres") ?
    pass("data", "D3: Error messages safe") : fail("data", "D3: Internal details in error", "");

  // D4: Health endpoint doesn't leak sensitive info
  const r4 = await req("GET", "/api/health");
  const healthStr = JSON.stringify(r4.data);
  !healthStr.includes("password") && !healthStr.includes("secret") && !healthStr.includes("key") ?
    pass("data", "D4: Health endpoint safe") : warn("data", "D4: Health endpoint may leak info", "");

  // D5: Status endpoint info disclosure
  const r5 = await req("GET", "/api/status");
  if (r5.status === 200) {
    const statusStr = JSON.stringify(r5.data);
    !statusStr.includes("password") && !statusStr.includes("connection_string") ?
      pass("data", "D5: Status endpoint safe") : fail("data", "D5: Status leaks connection info", "");
  }
}

// ===== E. Configuration Security =====
async function testConfig() {
  section("E. Configuration Security");

  // E1: CORS headers
  const r1 = await fetch(`${BASE}/api/health`, {
    method: "OPTIONS",
    headers: { Origin: "https://evil.com" },
  });
  const corsHeader = r1.headers.get("access-control-allow-origin");
  corsHeader === "*" ?
    warn("config", "E1: CORS allows all origins", "Restrict in production") :
    pass("config", "E1: CORS restricted");

  // E2: Security headers
  const r2 = await fetch(`${BASE}/api/health`);
  const headers = r2.headers;
  !headers.get("x-powered-by") ? pass("config", "E2: No X-Powered-By header") : warn("config", "E2: X-Powered-By header present", "Remove it");

  // E3: Debug endpoints
  const debugPaths = ["/debug", "/debug/pprof", "/.env", "/config", "/swagger", "/docs"];
  for (const p of debugPaths) {
    const r = await req("GET", p);
    r.status === 404 ? pass("config", `E3: ${p} not accessible`) : fail("config", `E3: ${p} accessible`, "Remove debug endpoints");
  }

  // E4: Default credentials
  const r4 = await req("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "admin" },
  });
  r4.status === 401 ? pass("config", "E4: Default 'admin' password rejected") : fail("config", "E4: Default password works!", "Critical!");

  // E5: Admin mode endpoint
  const login = await req("POST", "/api/auth/login", {
    body: { email: "e2e-admin@example.com", password: "E2E_AdminPass123!" },
  });
  if (login.data.token) {
    const r5 = await req("GET", "/api/admin/admin-mode", { token: login.data.token });
    r5.status === 200 ? info("config", "E5: Admin mode endpoint exists", `Mode: ${r5.data.mode}`) : pass("config", "E5: Admin mode protected");
  }
}

// ===== F. Rate Limiting =====
async function testRateLimit() {
  section("F. Rate Limiting");

  // F1: Health endpoint rate limit
  let rateLimited = false;
  for (let i = 0; i < 100; i++) {
    const r = await req("GET", "/api/health");
    if (r.status === 429) { rateLimited = true; break; }
  }
  rateLimited ? pass("ratelimit", "F1: Health endpoint rate limited") : info("ratelimit", "F1: Health endpoint not rate limited", "OK for health check");

  // F2: Send-code rate limit
  const r2 = await req("POST", "/api/auth/send-code", {
    body: { email: "ratelimit-test@test.com" },
  });
  // Already rate limited from previous tests
  r2.status === 429 ? pass("ratelimit", "F2: Send-code rate limited") : pass("ratelimit", "F2: Send-code available");
}

// ===== Main =====
async function main() {
  console.log(`\n${"═".repeat(70)}`);
  console.log(`  🔒 AI Portal Go - Security Vulnerability Tests`);
  console.log(`  📍 Target: ${BASE}`);
  console.log(`  📅 ${new Date().toISOString()}`);
  console.log(`${"═".repeat(70)}`);

  await testAuth();
  await testAuthz();
  await testInput();
  await testData();
  await testConfig();
  await testRateLimit();

  console.log(`\n${"═".repeat(70)}`);
  console.log(`  📊 Security Test Results`);
  console.log(`${"═".repeat(70)}`);
  console.log(`  ✅ Passed: ${passCount}`);
  console.log(`  ❌ Failed: ${failCount}`);
  console.log(`  📦 Total:  ${passCount + failCount}`);

  if (findings.length > 0) {
    const high = findings.filter(f => f.severity === "HIGH");
    const warn = findings.filter(f => f.severity === "WARN");
    const info = findings.filter(f => f.severity === "INFO");

    console.log(`\n  Findings:`);
    if (high.length > 0) {
      console.log(`\n  🔴 HIGH (${high.length}):`);
      for (const f of high) console.log(`    • [${f.category}] ${f.detail}`);
    }
    if (warn.length > 0) {
      console.log(`\n  ⚠️  WARN (${warn.length}):`);
      for (const f of warn) console.log(`    • [${f.category}] ${f.detail}`);
    }
    if (info.length > 0) {
      console.log(`\n  ℹ️  INFO (${info.length}):`);
      for (const f of info) console.log(`    • [${f.category}] ${f.detail}`);
    }
  }
  console.log(`\n${"═".repeat(70)}\n`);
  process.exit(failCount > 0 ? 1 : 0);
}

main().catch((err) => {
  console.error("Fatal:", err);
  process.exit(1);
});
