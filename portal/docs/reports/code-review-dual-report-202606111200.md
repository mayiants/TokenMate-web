# Code Review Report — ai-portal-go & ai-proxy-go

**Date**: 2026-06-11  
**Scope**: ai-portal-go (Portal) + ai-proxy-go (Proxy)  
**Branch**: ai-portal-go `enterprise-cardification` | ai-proxy-go `main`  
**Git Status**: Both clean (no uncommitted changes)  
**Files**: 104 Go files (portal) + 67 Go files (proxy) + docs in both repos

---

## Executive Summary

This report consolidates findings from a full-codebase review of both services. The Portal (ai-portal-go) has improved significantly from prior rounds but retains architectural debt and some security gaps. The Proxy (ai-proxy-go) has a solid provider-adapter architecture but contains **critical bugs** (self-assignments, resource leaks) and a **dev-mode security bypass** that need immediate attention.

| Service | Critical | High | Medium | Low |
|---------|----------|------|--------|-----|
| ai-portal-go | 0 | 1 | 14 | 12 |
| ai-proxy-go | 3 | 6 | 5 | 3 |
| **Total** | **3** | **7** | **19** | **15** |

---

## Part 1: ai-portal-go (Portal)

### 1.1 Security Findings

#### S1. CORS Allow-All When ALLOWED_ORIGINS Empty [HIGH]
- **File**: `cmd/server/main.go:514-560`
- **Issue**: When `ALLOWED_ORIGINS` is empty, `allowAll = true` and the middleware reflects the request's `Origin` header with `Access-Control-Allow-Origin`. Combined with cookie-based JWT auth, this allows arbitrary origins to make authenticated cross-origin requests.
- **Recommendation**: Change default to deny-all. Require explicit `ALLOWED_ORIGINS`. Fail startup if not configured in production.

#### S2. Metrics Endpoint Secret Comparison Not Timing-Safe [MEDIUM]
- **File**: `cmd/server/main.go:195-206`
- **Issue**: Plain string `!=` comparison for `X-Admin-Secret` header.
- **Recommendation**: Use `crypto/subtle.ConstantTimeCompare`.

#### S3. Error Message Exposure [MEDIUM]
- **Files**: `internal/routes/auth.go:179,345,406`, `internal/routes/models.go:1599,1618`, `internal/routes/vouchers.go:643`
- **Issue**: Several endpoints return `err.Error()` directly, exposing internal details (SQL errors, file paths).
- **Recommendation**: Return generic messages for 5xx. Log detailed errors server-side only.

#### S4. API Key Returned in Plaintext on Creation [MEDIUM]
- **File**: `internal/routes/apikeys.go:413-440`
- **Issue**: Raw API key returned in JSON response on creation. Single exposure point.
- **Recommendation**: Ensure this is the only exposure. Add audit logging. Consider UI warning.

#### S5. Bcrypt Cost Hardcoded to 10 [MEDIUM]
- **Files**: `internal/routes/auth.go`, `internal/routes/profile.go`, `internal/routes/enterprise.go`, `internal/web/auth.go`, `internal/web/actions.go`
- **Issue**: `bcrypt.GenerateFromPassword(..., 10)` used everywhere. Cost 10 is low for 2026.
- **Recommendation**: Use `bcrypt.DefaultCost` consistently. Consider 12-13 for production.

#### S6. Redis Error Fail-Open in Brute Force Protection [LOW]
- **File**: `internal/routes/auth.go:48-84`
- **Issue**: Runtime Redis errors block all login attempts (503). No fallback.
- **Recommendation**: Add circuit breaker or in-memory fallback for temporary Redis unavailability.

#### S7. Sensitive Content Check Logs Content Preview [LOW]
- **File**: `internal/routes/sensitive.go:68-78`
- **Issue**: 100-char preview of checked content logged to DB. Could capture sensitive user data.
- **Recommendation**: Truncate to 20 chars or hash the content for audit.

### 1.2 Code Quality Findings

#### Q1. MustExec Used Without Caller Error Handling [MEDIUM]
- **Files**: `internal/routes/apikeys.go`, `internal/routes/groups.go`, `internal/routes/webhook.go`, `internal/routes/sensitive.go`, `internal/routes/enterprise.go`
- **Issue**: `MustExec` logs errors internally but returns nothing. Handlers return `{"success": true}` even if DB operation failed.
- **Recommendation**: Replace `MustExec` with `Exec` in handlers. Check `RowsAffected()` for updates.

#### Q2. Inconsistent Error Response Formats [MEDIUM]
- **Files**: Across all `internal/routes/*.go`
- **Issue**: Multiple formats: `{"error": "msg"}`, `{"error": {"message": "msg"}}`, `{"error": {"message": "...", "code": "CODE"}}`, plain strings.
- **Recommendation**: Define a standard error response struct and helper in a shared package.

#### Q3. Dynamic SQL via fmt.Sprintf Still Present [MEDIUM]
- **Files**: `internal/routes/billing.go`, `internal/routes/enterprise.go`, `internal/routes/admin_reconciliation.go`, `internal/routes/admin.go`, `internal/query/*.go`
- **Issue**: SQL structure dynamically assembled with `fmt.Sprintf`. Example: `billing.go:143` interpolates `days` into SQL.
- **Recommendation**: Use parameterized queries: `INTERVAL '$2 days'`.

#### Q4. Missing Context Cancellation Checks [LOW]
- **Files**: `internal/routes/webhook.go`, `internal/routes/billing.go`
- **Issue**: Long-running operations (webhook batch, CSV export) don't check `ctx.Done()` in loops.
- **Recommendation**: Add `select { case <-ctx.Done(): return ctx.Err() }` in long loops.

#### Q5. Unused Error Returns Silently Ignored [LOW]
- **Files**: Widespread
- **Issue**: Many `rows.Scan()` errors ignored with `_ = rows.Scan(...)`. Masks data corruption.
- **Recommendation**: Log scan errors. Return 500 on critical paths.

#### Q6. Magic Numbers and Strings [LOW]
- **Files**: Widespread
- **Issue**: Hardcoded `10` (max personal keys), `60` (default RPM), `100000` (default TPM), `1_000_000` (balance threshold), `5` (brute force limit), `15*time.Minute`.
- **Recommendation**: Extract to named constants or configuration values.

### 1.3 Architecture Findings

#### A1. main.go Still Monolithic [MEDIUM] — Resolved (Phase 1~3 completed)
- **File**: `cmd/server/main.go` (now ~19 lines)
- **Issue**: CORS middleware, rate limiter, static file serving, default admin init, route registration all in one file.
- **Resolution**: CORS → `internal/middleware/cors.go`, IP rate limiter → `internal/middleware/ratelimit.go`, static files → `internal/web/static.go`; `cmd/server/main.go` reduced to orchestration only.
- **Plan**: `docs/monolithic-files-refactor-plan-202606141050.md` — Phase 5 continues splitting remaining >600-line route files.

#### A2. Duplicate Route Registration [MEDIUM]
- **Files**: `cmd/server/main.go:261-262`, `internal/routes/admin.go:117-118`
- **Issue**: `/api/apikeys` and `/api/keys` both registered. `/keypool` and `/key-pool` both exist.
- **Recommendation**: Consolidate to single canonical paths. Use redirects for backward compatibility.

#### A3. Billing Logic Split Between Webhook and Billing Routes [MEDIUM]
- **Files**: `internal/routes/webhook.go`, `internal/routes/billing.go`
- **Issue**: `calculateCostFromPricing` duplicated. Conceptually belongs in service layer.
- **Recommendation**: Extract to `internal/services/billing.go`.

#### A4. Background Goroutines Started in Routes [LOW]
- **Files**: `internal/routes/webhook.go`, `internal/routes/models.go`, `internal/routes/enterprise.go`, `internal/routes/admin.go`
- **Issue**: Email sending, cache cleanup, queue processing spawned from handlers. No centralized lifecycle.
- **Recommendation**: Move to `internal/workers/` with explicit start/stop hooks.

#### A5. Service Layer Dependencies Inconsistent [LOW]
- **Files**: `internal/services/reward.go`, `internal/services/exchange.go`
- **Issue**: No unified DI pattern. `RewardService` takes `*db.DB` and `*audit.Logger` directly.
- **Recommendation**: Consider a `Services` struct in `main.go` or simple DI container.

### 1.4 Performance Findings

#### P1. N+1 Query Patterns in API Key Validation [MEDIUM]
- **File**: `internal/routes/apikeys.go:623-1007`
- **Issue**: Main JOIN query followed by additional queries for enterprise pricing ratio, credit check, active model filtering.
- **Recommendation**: Add enterprise fields to main JOIN or use materialized view.

#### P2. In-Memory Rate Limiter Not Shared Across Instances [MEDIUM]
- **File**: `cmd/server/main.go:608-668`
- **Issue**: IP rate limiter uses in-memory `map[string]*rateBucket`. In multi-instance deployment, each instance has its own state.
- **Recommendation**: Document limitation. Implement Redis-backed rate limiting for production.

#### P3. Missing Connection Pool Configuration Validation [LOW]
- **File**: `internal/db/db.go:45-83`
- **Issue**: `MaxConns: 20` hardcoded. No runtime validation.
- **Recommendation**: Make pool size configurable. Add health check metrics for pool saturation.

#### P4. Static File Serving Without ETag/Last-Modified [LOW]
- **File**: `cmd/server/main.go:150-188`
- **Issue**: No cache validation headers.
- **Recommendation**: Add ETag generation based on file content hash.

### 1.5 Testing Findings

#### T1. E2E Tests Are UI-Level Only [MEDIUM]
- **File**: `tests/e2e/e2e_test.go`
- **Issue**: Playwright browser automation only. No API-level E2E for auth, billing, webhook, enterprise flows.
- **Recommendation**: Add API-level E2E tests per project instructions.

#### T2. Limited Unit Test Coverage for Critical Paths [MEDIUM]
- **Files**: `internal/routes/*_test.go`
- **Issue**: Tests focus on happy paths. Edge cases (Redis failure, DB timeout, invalid JSON, race conditions) under-tested.
- **Recommendation**: Add tests for Redis nil, malformed webhooks, concurrent key creation, credit exhaustion.

#### T3. No Load/Stress Tests for Billing Path [LOW]
- **File**: `tests/performance/stress_test.go`
- **Issue**: Stress test exists but may have division-by-zero issue.
- **Recommendation**: Verify and fix. Add load tests for webhook batch processing.

### 1.6 Go Idioms and Best Practices

#### G1. Mixed Use of `interface{}` for Nullable Fields [LOW]
- **Files**: `internal/routes/apikeys.go`, `internal/routes/enterprise.go`
- **Issue**: `interface{}` used for JSONB fields (`allowed_models`, `selected_models`). Bypasses type safety.
- **Recommendation**: Define proper struct types or use `json.RawMessage`.

#### G2. Context Not Propagated to Background Jobs [LOW]
- **Files**: `internal/routes/enterprise.go:1781`, `internal/routes/admin.go:1345`
- **Issue**: Background goroutines capture `c.Request.Context()` which may cancel before completion.
- **Recommendation**: Use `context.Background()` with timeout for background ops.

#### G3. `itoa` Helper Duplicated [LOW]
- **File**: `internal/query/apikeys.go:177-198`
- **Issue**: Custom `itoa` to avoid importing `strconv`.
- **Recommendation**: Replace with `strconv.Itoa`.

---

## Part 2: ai-proxy-go (Proxy)

### 2.1 Critical Issues

#### C1. Self-Assignment Bug in Anthropic Passthrough [CRITICAL]
- **File**: `internal/server/handlers.go:2032`
- **Issue**: `bodyBytes = bodyBytes` is a no-op. When `json.Marshal` fails, the fallback is ineffective because `bodyBytes` was already overwritten.
- **Recommendation**: Preserve original body before marshaling, or return proper error response.

#### C2. Self-Assignment Bug in Mantle Key Validation [CRITICAL]
- **File**: `internal/server/key_validation_handler.go:98`
- **Issue**: `status = status` is a no-op. Likely intended to be `status = "valid"` or similar.
- **Recommendation**: Remove or set correct status value.

#### C3. Dev Mode Security Bypass [CRITICAL]
- **File**: `internal/auth/auth.go:700-722`
- **Issue**: When `dev_mode` is enabled with empty Portal URL, auth middleware returns placeholder claims with `manual_balance` access mode, bypassing all rate limits and billing checks.
- **Recommendation**: Require explicit dev_mode flag. Log prominent warning. Use restrictive defaults.

### 2.2 High Severity Issues

#### H1. Missing Error Handling in HTTP Request Creation [HIGH]
- **File**: `internal/server/handlers.go:1545, 1690, 1778, 1864`
- **Issue**: `http.NewRequest` errors ignored with `_`.
- **Recommendation**: Check and handle errors properly.

#### H2. Context.Background in Async Goroutines [HIGH]
- **File**: `internal/server/handlers.go:361`
- **Issue**: Async billing goroutine uses `context.Background()` with no timeout. Could hang indefinitely.
- **Recommendation**: Use context with timeout (e.g., 10s).

#### H3. Race Condition in Rate Limit Token Bucket [HIGH]
- **File**: `internal/ratelimit/ratelimit.go:44-82`
- **Issue**: CAS loop updates `lastUpdate` atomically but `tokens` non-atomically. Another goroutine may modify `tokens` between CAS and store.
- **Recommendation**: Use single atomic variable or proper mutex for both fields.

#### H4. Missing Input Validation on Request Body Size [HIGH]
- **File**: `internal/server/handlers.go:453`
- **Issue**: `io.ReadAll(io.LimitReader(..., maxBodySize))` silently truncates at 10MB without error.
- **Recommendation**: Check if actual read size equals limit and return error if truncated.

#### H5. Key Pool Encryption Key Padding is Weak [HIGH]
- **File**: `pkg/keypool/manager.go:1908-1916`
- **Issue**: Short encryption keys zero-padded to 32 bytes. Easily brute-forced.
- **Recommendation**: Require exactly 32-byte keys or derive from PBKDF2/Argon2.

#### H6. Admin Secret Comparison Timing Attack [HIGH]
- **File**: `internal/server/server.go:228-229`
- **Issue**: `subtle.ConstantTimeCompare` used but preceded by `secret == ""` short-circuit.
- **Recommendation**: Always use `subtle.ConstantTimeCompare` without short-circuiting.

### 2.3 Medium Severity Issues

#### M1. goroutine Leak in cleanupLoop [MEDIUM]
- **File**: `internal/auth/auth.go:483-511`
- **Issue**: `cleanupLoop` goroutine runs indefinitely. No guarantee `Stop()` is called on shutdown.
- **Recommendation**: Ensure `Stop()` called during server shutdown.

#### M2. Missing Error Handling in JSON Unmarshal [MEDIUM]
- **Files**: `internal/server/handlers.go`, `pkg/convert/claude.go`, `pkg/convert/responses.go`
- **Issue**: Multiple `json.Unmarshal` errors ignored.
- **Recommendation**: Handle or log all unmarshal errors.

#### M3. Hardcoded Default Admin Secret [MEDIUM]
- **File**: `internal/config/config.go:350`
- **Issue**: Default admin secret is `"admin-secret"`.
- **Recommendation**: Remove default. Require explicit configuration.

#### M4. CORS Middleware Allows All Origins by Default [MEDIUM]
- **File**: `internal/server/server.go:243-282`
- **Issue**: Empty `allowedOrigins` allows all origins.
- **Recommendation**: Default to denying all. Require explicit configuration.

#### M5. Missing Content-Type Validation [MEDIUM]
- **File**: `internal/server/handlers.go`
- **Issue**: No `Content-Type` validation for POST endpoints.
- **Recommendation**: Add `Content-Type: application/json` validation.

### 2.4 Security Concerns

#### SC1. API Key Exposure in Logs [HIGH]
- **File**: `internal/auth/auth.go`
- **Issue**: `maskKey` exposes first 4 and last 2 characters.
- **Recommendation**: Mask more aggressively or use hash for logging.

#### SC2. Request Body Stored in Context [MEDIUM]
- **File**: `internal/server/handlers.go:465`
- **Issue**: Full request body stored in gin context. Could be logged by middleware.
- **Recommendation**: Store only hash/fingerprint for affinity cache.

#### SC3. SSRF Protection Incomplete [MEDIUM]
- **File**: `internal/server/admin_handlers.go:30-57`
- **Issue**: `validateBaseURL` checks localhost and private IPs but misses DNS rebinding, IPv6 loopback, URL-encoded bypasses.
- **Recommendation**: Add comprehensive SSRF protection.

### 2.5 Architecture and Design

**Positive Aspects**:
- Clean Provider Adapter Pattern with `BaseAdaptor`
- Proper Circuit Breaker with Closed/Open/HalfOpen states
- Sophisticated Key Pool Management with health checks, cooldown, LRU
- Lock-free token bucket with Redis sync
- Proper SSE handling with format conversion

**Areas for Improvement**:
- Inconsistent error wrapping (`%w` not always used)
- `context.Background()` in async paths
- Some goroutines lack proper shutdown
- Many packages have no test files

### 2.6 Testing Coverage

| Package | Test Files | Coverage |
|---------|-----------|----------|
| internal/auth | auth_test.go | Basic |
| internal/ratelimit | ratelimit_test.go | Basic |
| internal/server | handlers_test.go | Basic |
| pkg/billing | billing_test.go, billing_v3_test.go | Good |
| pkg/convert | convert_test.go, responses_test.go | Good |
| pkg/keypool | manager_test.go, keypool_v3_test.go | Good |
| pkg/metrics | metrics_test.go | Basic |
| pkg/models | registry_test.go, validator_test.go | Basic |
| pkg/relay | interface_test.go | Basic |
| pkg/token | counter_test.go | Basic |
| pkg/cache | **None** | Missing |
| pkg/circuitbreaker | **None** | Missing |
| internal/middleware | **None** | Missing |
| internal/audit | **None** | Missing |

**Critical gaps**: No tests for cache, circuit breaker, middleware, audit. Integration tests are mostly stubs.

**Update (2026-06-11)**: Tests have been added for all previously untested modules:
- `pkg/cache/affinity_test.go` - 11 tests covering cache operations
- `pkg/circuitbreaker/breaker_test.go` - 8 tests covering state machine and concurrency
- `internal/middleware/logging_test.go` - 4 tests covering structured logging
- `internal/middleware/requestid_test.go` - 5 tests covering request ID generation
- `internal/audit/audit_test.go` - 13 tests covering audit logger functionality
- `tests/e2e/e2e_test.go` - E2E tests covering full business flows

### 2.7 Performance Concerns

#### PF1. Memory Allocation in Hot Paths
- **File**: `internal/server/handlers.go`
- **Issue**: `recordUsageAsync` allocates new goroutine per request. Could cause goroutine explosion under load.
- **Recommendation**: Use worker pool.

#### PF2. sync.Map Performance
- **Issue**: Multiple `sync.Map` instances for counters and caches. Overhead vs `RWMutex` + map for read-heavy workloads.
- **Recommendation**: Benchmark and consider `RWMutex` + map for read-heavy paths.

#### PF3. JSON Marshal/Unmarshal in Hot Paths
- **Issue**: Multiple marshal/unmarshal in request/response conversion.
- **Recommendation**: Optimize with reusable buffers.

---

## Part 3: Git Status Summary

### ai-portal-go
- **Branch**: `enterprise-cardification`
- **Status**: Clean (no uncommitted changes)
- **Last 3 commits**:
  - `3f0abd5` refactor(billing-details): remove Request Details tab
  - `231a63d` style(ui): unify page titles to 20px, fix plan-and-group and invoices layout
  - `3ac5a2f` feat(ui): merge Subscriptions + My Group into Plan & Group; add Usage & Logs page

### ai-proxy-go
- **Branch**: `main`
- **Status**: Clean (no uncommitted changes)
- **Last 3 commits**:
  - `e991752` chore: remove docker-compose.proxy.yml from git, ignore it
  - `d154e6e` docs: add OpenAI-Claude conversion comparison analysis
  - `e58b684` fix(aws): correct SigV4 signing for Bedrock Runtime AK/SK

---

## Part 4: Recommendations Summary

### Immediate (This Sprint)

| Priority | Action | Service |
|----------|--------|---------|
| **Critical** | Fix self-assignment bugs in `handlers.go` and `key_validation_handler.go` | Proxy |
| **Critical** | Secure dev_mode fallback in auth middleware | Proxy |
| **Critical** | Fix BaseAdaptor.ReadResponse double-read issue | Proxy |
| **High** | Fix CORS default-deny in Portal | Portal |
| **High** | Add timing-safe secret comparison for metrics endpoint | Portal |
| **High** | Fix rate limit token bucket race condition | Proxy |
| **High** | Improve encryption key handling (reject short keys) | Proxy |
| **High** | Add proper error handling for `http.NewRequest` | Proxy |

### Short Term (Next 2 Weeks)

| Priority | Action | Service |
|----------|--------|---------|
| **Medium** | Replace `MustExec` with `Exec` + error handling | Portal |
| **Medium** | Unify error response format across all routes | Portal |
| **Medium** | Extract `calculateCostFromPricing` to `internal/services/billing.go` | Portal |
| **Medium** | Add Redis-backed rate limiter for multi-instance | Portal |
| **Medium** | Increase bcrypt cost to `DefaultCost` (or 12) | Portal |
| **Medium** | Add context cancellation checks to long-running ops | Portal |
| **Medium** | Add goroutine shutdown mechanisms | Proxy |
| **Medium** | Improve SSRF protection in admin handlers | Proxy |

### Medium Term (Next Month)

| Priority | Action | Service |
|----------|--------|---------|
| **Low** | Split `main.go` / routes per `docs/monolithic-files-refactor-plan-202606141050.md` | Portal | Phase 1~3 ✅, Phase 5 进行中 |
| **Low** | Consolidate duplicate route paths | Portal |
| **Low** | Add API-level E2E tests | Portal |
| **Low** | Add comprehensive load tests for webhook batch | Portal |
| **Low** | ~~Add tests for cache, circuitbreaker, middleware~~ ✅ Done | Proxy |
| **Low** | Add E2E tests for proxy | Proxy |
| **Low** | Extract magic numbers to constants | Both |
| **Low** | Standardize error response format | Both |
| **Low** | Replace `interface{}` JSONB fields with typed structs | Portal |

---

## Part 5: Prior Review Cross-Reference (Portal)

| Prior Finding | Status |
|---------------|--------|
| S2: Default admin password hardcoded | Fixed |
| S3: Key export no 2FA | Fixed |
| S4: Webhook signature bypass | Fixed |
| S5: Mail HTML injection | Fixed |
| S6: math/rand for codes | Fixed |
| S7: Metrics no auth | Fixed |
| S8: CORS allow-all | **Still open** (HIGH) |
| B1-B5: Various bugs | Fixed |
| D1: main.go / routes bloated | Phase 1~3 ✅ — Phase 5 continues splitting remaining large route files (`subscriptions.go`, `apikeys.go`, `vouchers.go`, etc.) |
| D3: Dual billing path | Partially addressed |
| D4: Goroutine recovery | Fixed in webhook.go, still open in models.go/admin.go |
| N1: math/rand remaining | Fixed |

---

*Report generated: 2026-06-11*  
*Reviewer: Claude Code*  
*Files analyzed: 171 Go files, docs in both repos*
