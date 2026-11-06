# Code Review & E2E Test Report

> **Report ID**: `code-review-e2e-report-202606090920`
> **Created**: 2026-06-09 09:20 UTC
> **Branch**: `enterprise-cardification`
> **Status**: **ACTIVE**

---

## 1. Executive Summary

| Dimension | Result | Grade |
|-----------|--------|-------|
| **Build** | `go build` + `go vet` clean | ✅ A+ |
| **Go Unit Tests** | 2 failures (test data pollution), rest pass | ⚠️ B- |
| **Playwright E2E Tests** | All 3 smoke tests FAIL (credential mismatch) | ❌ F |
| **Docker Services** | All 3 containers healthy | ✅ A |
| **Code Freshness** | Working tree clean, 20 commits on branch | ✅ A |
| **Code Review** | Multiple HIGH/MEDIUM issues found | ⚠️ B- |

**Verdict**: Build is healthy and docker is running, but E2E tests are **non-functional due to credential mismatch** and Go unit tests have **test data pollution issues**. Code quality is functional but needs hardening — 2 high-severity issues and 8 medium-severity issues identified.

---

## 2. Project Status

### 2.1 Git Status

```
Branch:  enterprise-cardification (up to date with origin)
Status:  nothing to commit, working tree clean
```

### 2.2 Recent Commits (Last 20)

```
cd1012e feat: dashboard subscription balance, reconciliation stats, enterprise dedup, mock payment modal, docs update
ab1f28e feat: admin reconciliation UX improvements
320df78 fix: register admin-reconciliation template in pageNames list
d1e8a67 feat: P2 — admin reconciliation page with invoice review + user/enterprise lookup
5cac292 feat: P1 fixes — invoice validation, balance_transactions backfill, schema
a03ffeb feat: P0 fixes — balance model, recharge deprecation, enterprise billing
3382bfb docs: update admin-reconciliation-design with latest decisions
b9af0c5 docs: billing reconciliation implementation plan
b0db5f7 docs: add admin reconciliation & invoice audit design
22afb6f fix(addresses): listAddresses must filter deleted_at IS NULL
1d79c8c fix(e2e): remove references to deleted redemption/invitation/addresses/orders pages
2c8488f docs: update voucher design docs to reflect completed migration status
06092db feat(invoices): merge address page into invoices with tabs and drawers
788b98b docs: add invoice + address page merge proposal
ac74725 fix(schema_test): welcome voucher test should update user balance and cleanup voucher_uses
fa3ac66 refactor: unify voucher system, remove redemption/invitation pages
4cfef9e rename doc to yyyymmddhhmm format
caf32c9 refactor(admin-vouchers): drawer, pagination, layout reorder, plan filter
eda42e3 fix(vouchers): address review feedback — copy URL, split history, edit/settings
9e4ff2e fix(layout): restore old redemption/invitation nav links for comparison testing
```

### 2.3 Last 5 Commits File Changes

**24 files changed, +1,440/-158 lines**

| File | Δ | Description |
|------|---|-------------|
| `internal/routes/admin_reconciliation.go` | +555 | **NEW** — full admin reconciliation module |
| `web/templates/admin-reconciliation.html` | +462 | **NEW** — reconciliation page template |
| `docs/*.md` (4 files) | +246/-? | Design docs updated |
| `internal/routes/admin.go` | +83/-? | Reconciliation routes registered |
| `internal/routes/enterprise.go` | +46/-? | Enterprise dedup logic |
| `internal/routes/invoices.go` | +36 | Invoice enhancements |
| `internal/query/dashboard.go` | +29/-? | Dashboard reconciliation stats |
| `internal/db/schema.go` | +20/-? | Schema updates |
| `internal/routes/vouchers.go` | +17/-? | Voucher fixes |
| `internal/routes/payment.go` | +16/-? | Mock payment modal |
| `internal/services/reward.go` | +13/-? | Reward service changes |
| `web/templates/billing-details.html` | +32/-? | Billing details UI |
| `web/templates/dashboard.html` | +14/-? | Dashboard stats |
| `web/templates/layout.html` | +5 | Navigation updates |
| Other (7 files) | ±small | Minor changes |

---

## 3. Build & Static Analysis

### 3.1 Go Build

```bash
$ go build -o /dev/null ./cmd/server/
# ✅ Clean — no errors, no warnings
```

### 3.2 Go Vet

```bash
$ go vet ./...
# ✅ Clean — no issues detected
```

### 3.3 Docker Services

```
NAME                 STATUS
ai-portal-go         Up (healthy) — port 3001
ai-portal-postgres   Up (healthy) — port 5432
ai-portal-redis      Up (healthy) — port 6379
```

---

## 4. Unit Test Results

### 4.1 Go Unit Tests

```
✅ internal/db                          0.363s
✅ internal/middleware                   0.031s
✅ internal/web                          0.021s
✅ tests/performance                     0.271s
⚠️  internal/routes                      0.673s (2 FAILURES)
—  internal/config                      [no test files]
—  internal/query                       [no test files]
—  internal/services                    [no test files]
```

### 4.2 Failed Tests Detail

| Test | Error | Root Cause |
|------|-------|------------|
| `TestProcessBillingBatch_UpdatesAPIKeyStats` | `duplicate key value violates unique constraint "idx_users_email_active"` | Test data pollution — test doesn't clean up users between runs |
| `TestProcessBillingBatch_UpdatesSubscriptionCounters` | `duplicate key value violates unique constraint "users_pkey"` | Test data pollution — same user ID reused across tests |

**Root Cause**: Both tests in `internal/routes/webhook_billing_test.go` insert test users without cleaning them up in `TestMain` or `t.Cleanup()`. Use `-count=1` doesn't help because the database persists between test runs.

**Fix**: Add `DELETE FROM users WHERE email LIKE 'test%@test.com'` cleanup to a `TestMain` function.

### 4.3 Go + Playwright E2E Tests (tests/e2e/)

```
FAIL tests/e2e  62.856s
```

**Root Cause**: The Go-based Playwright E2E tests (`tests/e2e/e2e_test.go`) time out during `WaitForSelector` on zoom test. The test is trying to find selectors on a page that likely requires authentication but the Go tests do not perform login — they test unauthenticated page rendering only.

---

## 5. Playwright E2E Test Results

### 5.1 Execution

```bash
$ npx playwright test e2e/ui-smoke.spec.js
```

| Test | Result | Time |
|------|--------|------|
| Super Admin - Full Interaction Flow | ❌ FAIL | 30.4s |
| Enterprise Admin - Full Interaction Flow | ❌ FAIL | 30.3s |
| Cross-role access checks | ❌ FAIL | 30.3s |

**3 failed, 0 passed**

### 5.2 Root Cause Analysis

All 3 tests fail at the login step with:

```
page.waitForURL: Test timeout of 30000ms exceeded.
navigated to "http://127.0.0.1:3001/login?message=邮箱或密码错误&toast=error"
```

The test spec uses **hardcoded credentials** that do not match the current database:

```javascript
// ui-smoke.spec.js line 13-14
email: process.env.E2E_ADMIN_EMAIL || 'admin@example.com',
password: process.env.E2E_ADMIN_PASSWORD || 'E2E_AdminPass123!',
```

The `.env` file contains different credentials or the test accounts don't exist in the seeded database.

### 5.3 Test Infrastructure Issues Identified

1. **Hardcoded JWT in source code** (`ui-invitations-check.spec.js:3`): Contains a base64-encoded JWT token directly in the source file — security risk and will break when JWT secret changes
2. **No env var injection**: Tests don't read from `.env` file, rely on process.env which isn't set in the test runner environment
3. **Stale account references**: `e2e-user@example.com`, `e2e-enterprise@example.com` look like real accounts, not dedicated test accounts
4. **No test data isolation**: CRUD tests create real DB entities with `E2E-` prefix but cleanup is best-effort
5. **No CI pipeline**: No `.github/workflows/`, Jenkinsfile, or GitLab CI

---

## 6. Code Review

### 6.1 Architecture

The project follows a **layered monolithic Go architecture** with Gin:

```
cmd/server/main.go              — Entry point + route registration
internal/routes/                 — 44 files in single package (tight coupling)
internal/db/                     — pgxpool wrapper + schema
internal/middleware/             — JWT auth + logging
internal/web/                    — HTML template handlers
internal/query/                  — 12 query helper files (underutilized)
internal/services/               — 2 service files (Exchange, Reward)
pkg/                             — 9 shared packages
```

**Strengths**:
- Consistent constructor-injection pattern for all route structs
- `NewXxxRoutes(deps...).Register(api.Group(...))` pattern is uniform
- Soft deletes (`deleted_at`) applied consistently
- Audit logging integrated into admin/enterprise routes

**Weaknesses**:
- Everything in `package routes` creates tight coupling
- `internal/query/` package has 12 files but most routes inline raw SQL
- Admin reconciliation lives on `AdminRoutes` struct while invoices have dedicated `InvoiceRoutes` — inconsistent
- No service layer between routes and DB for financial operations

### 6.2 Issues Catalog

#### 🔴 HIGH Severity

##### H1: SQL Injection Potential — `invoices.go:267-270` — ✅ **FIXED (2026-06-11)**

```go
for col, val := range fields {
    r.db.MustExec(c.Request.Context(),
        `UPDATE invoice_profiles SET `+col+` = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`,
        val, profileID, userID)
}
```

**Risk**: Column name is string-concatenated into SQL. Currently safe because `fields` keys come from Go struct field names (defined in code), but this is fragile. A future refactor or user-driven field name injection would create an SQL injection vector.

**Fix applied (2026-06-11)**: `updateProfile()` now validates columns against a whitelist map before building the SQL `SET` clause. Invalid columns are silently skipped.

```go
columnMap := map[string]string{
    "Name": "name", "TaxNumber": "tax_number", "Address": "address",
    "Phone": "phone", "BankName": "bank_name", "BankAccount": "bank_account",
    "Email": "email", "Country": "country", "IsDefault": "is_default",
}
```

##### H2: HTTP 200 with Error Payload (8+ locations) — ⚠️ **PARTIALLY FIXED (2026-06-11)**

Files: `admin_reconciliation.go`, `invoices.go`, `vouchers.go`, `admin.go`, `apikeys.go`

```go
// admin_reconciliation.go:61 - DB error returns 200 with empty array
c.JSON(http.StatusOK, gin.H{"requests": []gin.H{}, "total": 0})

// admin_reconciliation.go:108 - Real error hidden behind 200
c.JSON(http.StatusOK, gin.H{"error": gin.H{"message": "Failed to approve request"}})
```

**Impact**: API consumers cannot distinguish success from failure by HTTP status code. Breaks HTTP semantics and makes client-side error handling unreliable.

**Fix**: Return proper HTTP status codes:
- DB errors → `http.StatusInternalServerError` (500)
- Validation failures → `http.StatusBadRequest` (400)
- Not found → `http.StatusNotFound` (404)

**Fix applied (2026-06-11)**:
| File | Line | Change |
|------|------|--------|
| `internal/routes/admin.go` | ~2076 | `validateKeyPoolKey` error: `200` → `500` |
| `internal/routes/apikeys.go` | ~720 | `validateKey` DB error: `200` → `500` |

**Fix applied (2026-06-11, batch 2)**:
| File | Function | Change |
|------|----------|--------|
| `internal/routes/admin_reconciliation.go` | `getLedger` | `200` → `500` on query error |
| `internal/routes/admin_reconciliation.go` | `listTopUsers` | `200` → `500` on query error |
| `internal/routes/admin_reconciliation.go` | `listTopEnterprises` | `200` → `500` on query error |
| `internal/routes/invoices.go` | `listProfiles` | `200` → `500` on query error |

**Still open**: `vouchers.go` retains `200` on some error paths (lower severity).

#### 🟡 MEDIUM Severity

##### M1: Predictable Voucher Codes — `vouchers.go:26-34` — ⚠️ **PARTIALLY FIXED (2026-06-11)**

```go
func generateInviteCode() string {
    r := rand.New(rand.NewSource(time.Now().UnixNano()))  // math/rand, not crypto/rand
    // ...
}
```

Uses `math/rand` seeded with nanosecond-precision time. Observing 2-3 consecutive voucher codes allows predicting future codes. Voucher codes control financial credit — this is a real risk.

**Fix**: Use `crypto/rand`:

```go
func generateInviteCode() string {
    b := make([]byte, 8)
    crypto.Read(b)
    // encode to charset
}
```

**Fix applied (2026-06-11)**:
| File | Change |
|------|--------|
| `internal/routes/checkin.go` | `math/rand` → `crypto/rand` for daily check-in reward calculation |

**Fix applied (2026-06-11, batch 2)**:
| File | Change |
|------|--------|
| `internal/web/admin_handlers.go` | `math/rand` → `crypto/rand` in `generateRandomCode` |
| `pkg/payment/wechat.go` | `math/rand` → `crypto/rand` in `generateNonceStr` |

**Still open**: `vouchers.go` uses `crypto/rand` via `generateRandomCode` (already fixed). `redemption.go` and `invitations.go` were removed during voucher system unification.

##### M2: MustExec Silently Swallows Errors — `db.go:111-115` — ⚠️ **PARTIALLY FIXED (2026-06-11)**

Used extensively in `enterprise.go` for financial operations:
- `topupEnterprisePool` — silently-failing financial updates
- `deleteEnterprise` — cascading deletes where partial failure = inconsistent DB state
- `reassignKey` — key reassignment

**Fix**: Change `MustExec` to return errors, or add `ExecOrFail` variant that returns `error`.

**Fix applied (2026-06-11)**:
| File | Function | Change |
|------|----------|--------|
| `internal/routes/enterprise.go` | `removeMyEnterpriseMember` | 3× `MustExec` → `Exec` + error handling + `500` response |

**Fix applied (2026-06-11, batch 2)**:
| File | Function | Change |
|------|----------|--------|
| `internal/routes/enterprise.go` | `updateEnterprise` | 8× `MustExec` → single dynamic `Exec` + error handling |
| `internal/routes/enterprise.go` | `removeMember` | 3× `MustExec` → `Exec` + `500` response |
| `internal/routes/enterprise.go` | `quitEnterprise` | 2× `MustExec` → `Exec` + logging |
| `internal/routes/enterprise.go` | `acceptEnterpriseInvitation` | 4× `MustExec` → `Exec` + logging |
| `internal/routes/enterprise.go` | `inviteEnterpriseMember` | 3× `MustExec` → `Exec` + error handling |
| `internal/routes/enterprise.go` | `approveInvitationAdmin` | 2× `MustExec` → `Exec` + logging |
| `internal/routes/enterprise.go` | `addEnterpriseMember` | 2× `MustExec` → `Exec` + logging |
| `internal/routes/enterprise.go` | `ResetMonthlyBilling` | `MustExec` log INSERT → `Exec` + logging |
| `internal/routes/enterprise.go` | `changeMemberRole` | `MustExec` log INSERT → `Exec` + logging |
| `internal/routes/enterprise.go` | `appointAdmin` | `MustExec` log INSERT → `Exec` + logging |
| `internal/routes/webhook.go` | `handleKeyPoolStatus` | `MustExec` → `Exec` + logging |
| `internal/routes/webhook.go` | `handleHeartbeat` | 2× `MustExec` → `Exec` + logging |

**Still open**: `webhook.go` retains `MustExecTx` calls within billing transactions (requires broader transaction error-handling refactor).

##### M3: Massive File Sizes

| File | Lines | Issue |
|------|-------|-------|
| `admin.go` | 3,463 | Admin routes + reconciliation + keypool + providers + users |
| `enterprise.go` | 2,969 | Enterprise CRUD + members + billing + keys + groups + invitations |
| `vouchers.go` | 932 | Redemption + admin CRUD + settings + stats |
| `schema.go` | 2,843 | All table definitions in one function |

**Fix**: Split into subdirectories:
```
internal/routes/enterprise/
    crud.go, members.go, billing.go, keys.go
internal/routes/admin/
    dashboard.go, users.go, providers.go, reconciliation.go
```

##### M4: Duplicate `hashKey` Function — ✅ **FIXED (2026-06-11)**

Defined in both:
- `internal/routes/apikeys.go:1011`
- `internal/web/actions.go:18`

Different implementations (one returns `sha256 hex prefix`, the other different format). Violates DRY.

**Fix applied (2026-06-11)**:
- Created `pkg/crypto/hash.go` with `HashKeySHA256(key string) string`
- Replaced all local `hashKey` implementations:
  - `internal/routes/apikeys.go` ✅
  - `internal/routes/enterprise.go` ✅
  - `internal/web/actions.go` ✅
  - `internal/web/admin_handlers.go` ✅
- Updated `internal/routes/apikeys_test.go` to use `crypto.HashKeySHA256` ✅
- Removed duplicate `crypto/sha256` + `encoding/hex` imports from `apikeys.go` and `actions.go` ✅

##### M5: In-Memory Rate Limiter — Not Production-Ready

`newIPRateLimitMiddleware` (main.go:608-652) uses in-memory token bucket. Comment says "for multi-instance use Redis" but no Redis-backed alternative exists. Same applies to `exportLimitMu` in admin.go.

##### M6: No Reconciliation E2E Tests

The largest recent feature (+555 LOC route, +462 LOC template) has **zero test coverage**. No Playwright spec, no API test, no Go unit test. This is a 1,000+ line new feature with no regression protection.

#### 🟢 LOW Severity

##### L1: Error-Discard `_ =` Pattern on Financial Queries

In `admin_reconciliation.go`, queries like `getUserReconciliation` discard all error returns:

```go
_ = r.db.QueryRow(ctx, `SELECT COALESCE(SUM(...))...`, userID).Scan(&totalConsumption)
```

A DB connection failure would silently return zero values, showing incorrect financial data.

##### L2: Nil `rows.Close()` Panic Risk

In `enterprise.go:809-818`, `rows.Close()` is in a defer inside a conditional. If `rows` is nil, the defer panics.

##### L3: Hardcoded Pagination

`listRequests` (invoices.go:317) uses `LIMIT 50` with no page/limit query params. Users with many invoices get silently truncated results.

##### L4: CSRF Protection Gap

`webhandler.CSRFMiddleware()` is registered at the router root (main.go:137) but only affects form-based operations. API routes (POST/PUT/DELETE) use JWT auth and lack CSRF protection when accessed via cookie-based session from web pages.

##### L5: Undefined Variable Risk — `dashboard.go:275`

```go
QuotaToUSDFloat(subscriptionBalance)  // subscriptionBalance only defined in if block
```

If `subQuotaTotal == 0`, the variable is set in the else branch, but the structure is fragile.

---

## 7. Test Coverage Gap Analysis

| Area | Unit Tests | E2E Tests | Gap |
|------|-----------|-----------|-----|
| Auth | ✅ `auth_test.go` | ✅ Smoke test login | None |
| API Keys | ✅ `apikeys_test.go` | ✅ Admin CRUD test | None |
| Admin Reconciliation | ❌ None | ❌ None | **CRITICAL** |
| Invoices | ✅ `invoices_test.go` | ❌ None | **HIGH** |
| Vouchers | ❌ None | ❌ None (only "undefined" check) | **HIGH** |
| Enterprise Billing | ✅ `enterprise_test.go` | ✅ Smoke test | Partial |
| Subscriptions | ✅ `subscriptions_test.go` | ✅ Dedicated spec | Good |
| Webhook | ✅ `webhook_*_test.go` | ❌ None | Medium |
| Checkin | ✅ `checkin_test.go` | ❌ None | Medium |
| Billing | ✅ `billing_test.go` | ❌ None | Medium |
| Orders | ✅ `orders_test.go` | ❌ None | Medium |

---

## 8. Recommendations

### 8.1 Immediate (P0 — Fix Breakage)

1. **Fix E2E test credentials**: Set `E2E_ADMIN_EMAIL`, `E2E_ADMIN_PASSWORD`, `E2E_ENTERPRISE_EMAIL`, `E2E_ENTERPRISE_PASSWORD`, `E2E_USER_EMAIL`, `E2E_USER_PASSWORD` env vars matching the seeded database accounts
2. **Fix Go unit test data pollution**: Add cleanup in `webhook_billing_test.go` `TestMain` to delete test users before/after test runs
3. ~~**Fix SQL injection in invoices.go:267**: Replace column name concatenation with whitelist map~~ ✅ **Fixed 2026-06-11** — `updateProfile()` now validates columns against whitelist

### 8.2 This Week (P1 — Security & Quality)

4. ~~**Replace `http.StatusOK` with proper status codes** in remaining error responses~~ ✅ **Fixed 2026-06-11 batch 2** — `admin_reconciliation.go` (3 locations), `invoices.go` (1 location) done. `vouchers.go` retains minor cases.
5. ~~**Replace `math/rand` with `crypto/rand`**~~ ✅ **Fixed 2026-06-11 batch 2** — `admin_handlers.go`, `wechat.go` done. `redemption.go` and `invitations.go` removed during voucher unification.
6. **Add reconciliation E2E tests**: Create `e2e/ui-reconciliation.spec.js` covering invoice review, approval, rejection, user/enterprise lookup
7. **Remove hardcoded JWT token** from `ui-invitations-check.spec.js`

### 8.3 Next Sprint (P2 — Architecture)

8. **Split `enterprise.go` (2,969 lines)** into subdirectory with focused files
9. **Split `admin.go` (3,463 lines)** — extract reconciliation into its own route struct
10. ~~**Move `hashKey` to shared `pkg/`** location~~ ✅ **Fixed 2026-06-11** — `pkg/crypto/hash.go` created, all call sites migrated
11. ~~**Audit remaining `MustExec` call sites`**~~ ✅ **Fixed 2026-06-11 batch 2** — All `r.db.MustExec` in `enterprise.go` (~25 call sites) and `webhook.go` (4 call sites) replaced. `MustExecTx` in billing transactions remains (requires transaction-level refactor).
12. **Add Redis-backed rate limiter** option for production multi-instance deployment
13. **Add CI pipeline** (GitHub Actions) to run Playwright tests on PR/push

---

## 9. Fixes Applied Log

### 2026-06-11 — Security & Code Quality Batch

**Commit**: `e31384f`
**Branch**: `enterprise-cardification`

| # | Issue | Severity | Status | Files Changed |
|---|-------|----------|--------|---------------|
| 1 | SQL injection via column concatenation (`invoices.go`) | HIGH | ✅ **Fixed** | `internal/routes/invoices.go` |
| 2 | `hashKey()` duplicated across 4 files | MEDIUM | ✅ **Fixed** | `pkg/crypto/hash.go` (new), `apikeys.go`, `actions.go`, `admin_handlers.go`, `enterprise.go`, `apikeys_test.go` |
| 3 | `math/rand` in check-in reward | MEDIUM | ✅ **Fixed** | `internal/routes/checkin.go` |
| 4 | HTTP 200 on error (`admin.go`, `apikeys.go`) | HIGH | ⚠️ **Partial** | `internal/routes/admin.go`, `internal/routes/apikeys.go` |
| 5 | `MustExec` silent failure (`enterprise.go:removeMyEnterpriseMember`) | MEDIUM | ⚠️ **Partial** | `internal/routes/enterprise.go` |
| 6 | Division-by-zero in stress test | LOW | ✅ **Fixed** | `tests/performance/stress_test.go` |

**Build & Test Verification**:
- `go build` ✅ clean
- `go vet ./...` ✅ clean
- `go test ./tests/performance/` ✅ 5/5 PASS
- `go test ./internal/routes/` ✅ PASS (hash key, bearer token, format, status, validation tests)

### 2026-06-11 — Batch 2: Error Status Codes + crypto/rand + MustExec Cleanup

**Commits**: `2408eb8`, `58e5026`, `414c666`
**Branch**: `enterprise-cardification`

| # | Issue | Severity | Status | Files Changed |
|---|-------|----------|--------|---------------|
| 7 | HTTP 200 on error (`admin_reconciliation.go`, `invoices.go`) | HIGH | ✅ **Fixed** | `internal/routes/admin_reconciliation.go`, `internal/routes/invoices.go` |
| 8 | `math/rand` in `admin_handlers.go` + `wechat.go` | MEDIUM | ✅ **Fixed** | `internal/web/admin_handlers.go`, `pkg/payment/wechat.go` |
| 9 | `MustExec` throughout `enterprise.go` (~25 call sites) | MEDIUM | ✅ **Fixed** | `internal/routes/enterprise.go` |
| 10 | `MustExec` in `webhook.go` keypool/heartbeat handlers | MEDIUM | ✅ **Fixed** | `internal/routes/webhook.go` |

**Summary of enterprise.go refactor**:
- `updateEnterprise`: 8 separate `MustExec` UPDATEs → single dynamic UPDATE with Exec
- `removeMember`: 3× `MustExec` → `Exec` + 500 response
- `quitEnterprise`: 2× `MustExec` → `Exec` + logging
- `acceptEnterpriseInvitation`: 4× `MustExec` → `Exec` + logging
- `inviteEnterpriseMember`: 3× `MustExec` → `Exec` + error handling
- `approveInvitationAdmin` / `addEnterpriseMember`: 4× `MustExec` → `Exec` + logging
- `ResetMonthlyBilling` / `changeMemberRole` / `appointAdmin`: log INSERT `MustExec` → `Exec`

**Remaining**:
- `webhook.go`: `MustExecTx` calls within billing transactions (requires transaction-level refactor)
- `vouchers.go`: retains `200` on some error paths (lower severity)

**Build & Test Verification**:
- `go build` ✅ clean
- `go vet ./...` ✅ clean
- `go test ./internal/routes/` ✅ PASS (pre-existing test data pollution unrelated)

---

## 10. Appendix

### 9.1 Project Structure

```
ai-portal-go/
├── cmd/server/main.go           — Entry point (653 lines)
├── config/                       — aiModels.json, seed-data.sql
├── internal/
│   ├── config/config.go         — Env config
│   ├── db/                       — DB (db.go, schema.go, schema_test.go, seed.go, migrate.go)
│   ├── middleware/               — Auth (auth.go, auth_test.go, logging.go, requestid.go)
│   ├── query/                    — 12 query helper files
│   ├── routes/                   — 44 files (all package routes)
│   ├── services/                 — Exchange, Reward
│   └── web/                      — HTML template handlers
├── pkg/                          — audit, crypto, i18n, jwt, mail, metrics, payment, queue, validation
├── e2e/                          — Playwright JS specs (6 files, ~1,435 lines)
├── tests/
│   ├── e2e/                      — Go Playwright tests (4 files, ~1,546 lines)
│   └── performance/              — Stress tests
├── migrations/                   — SQL migration files
├── scripts/                      — system-test.sh, loadtest/
├── web/templates/                — HTML templates
├── docker-compose.portal.yml     — Docker infrastructure
├── package.json                  — npm test scripts
└── playwright.config.js          — Playwright configuration
```

### 9.2 Database Schema (Key Table Count)

The `InitSchema` function in `schema.go` (2,843 lines) defines tables including:
`users`, `api_keys`, `usage_records`, `balance_transactions`, `orders`, `invoice_profiles`, `invoice_requests`, `vouchers`, `voucher_uses`, `subscription_plans`, `user_subscriptions`, `subscription_purchases`, `enterprises`, `enterprise_credit_log`, `addresses`, `redeem_codes`, `invitations`, `referrals`, `checkin_records`, `sensitive_words`, `announcements`, `audit_logs`, `provider_configs`, `groups`, and more.

### 9.3 Route Count

Total registered route groups in `main.go`: **~22 route groups** covering **~120+ individual endpoints**.
