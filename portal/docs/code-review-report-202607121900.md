# Code Review Report

**Date:** 2026-07-12 19:00 UTC
**Updated:** 2026-07-13 03:15 UTC (fix status added)
**Scope:** ai-portal-go / ai-proxy-go (main branch)
**Commit portal:** 542ecd4 — fix(render): Partial() also needs renderMu lock
**Commit proxy:** f275fcd — refactor(lifecycle): add context.Context to all background goroutines

---

## ai-portal-go — 健康分 6.5/10 → 7.5/10 (after fixes)

### Summary

Well-structured codebase with good separation of concerns, comprehensive test coverage, and thoughtful architecture (advisory locks for singleton jobs, HMAC webhook signing, partial unique indexes). However, there are critical defects in payment webhook signature verification (Alipay/PayPal), SQL injection via string concatenation in dynamic query building, a race condition in the web handler's shared `currentLocale` field, and CSRF protection issues.

### Critical Issues

| # | File:Line | Issue | Status |
|---|-----------|-------|--------|
| 1 | `pkg/payment/alipay.go:117-121` | **Alipay webhook signature verification is a no-op** — `VerifySignature` returns `true` if `signature != ""`. Since the route handler passes `signature` as `""` (line 409 of `payment.go`), the check is `false` and all Alipay webhooks are rejected. | ⏳ TODO |
| 2 | `pkg/payment/paypal.go:93-98` | **PayPal webhook signature verification is a no-op** — `VerifySignature` returns `signature != ""`. Any payload with a non-empty signature header passes. | ⏳ TODO |
| 3 | `internal/routes/billing.go:393-406,518-538,668-690` | **SQL injection via string concatenation** — Dynamic WHERE clauses built with `fmt.Sprintf` and incrementing `argIndex`. While args are parameterized, the pattern is fragile. | ⏳ TODO |
| 4 | `internal/routes/payment.go:606-612` | **Double response write in handleBillingCallback** — The `cancel` branch writes two HTTP responses. This is a copy-paste error from the `fail` case. | ✅ FIXED (8972d85) |
| 5 | `internal/routes/enterprise_keys.go:131` | **Encryption failure falls back to plaintext** — When `crypto.Encrypt` fails, `keyEncrypted = key` stores the raw API key in the database. | ✅ FIXED (8972d85) |
| 6 | `internal/web/handler.go:164` | **Race condition on `h.currentLocale`** — Shared struct field set during `Render`/`RenderPartial`/`Partial`, read by template functions in other goroutines. | ✅ FIXED (b568427, 542ecd4) |
| 7 | `internal/routes/auth.go:381` | **Ignored bcrypt error** — `newHash, _ := bcrypt.GenerateFromPassword(...)`. On bcrypt failure, user gets 200 success with a blank password hash. | ✅ FIXED (8972d85) |
| 8 | `pkg/payment/wechat.go:128` | **WeChat gateway VerifySignature always returns false** — relies on `VerifyWebhookSignature` being called separately. | ⏳ TODO |
| 9 | `.env` | **Hardcoded secrets** — Contains DB_PASSWORD, JWT_SECRET, ENCRYPTION_KEY, ADMIN_SECRET, etc. (Note: `.env` is gitignored, this is a deployment hygiene concern.) | ℹ️ Won't fix (gitignored) |

### Medium Issues

| # | File:Line | Issue | Status |
|---|-----------|-------|--------|
| 10 | `internal/routes/enterprise_application.go:130` | Error classification via `strings.Contains(err.Error(), "duplicate")` — fragile and locale-dependent. Use PostgreSQL error code `23505`. | ⏳ TODO |
| 11 | `internal/db/db.go:607` | Goroutine captures loop variable `base` by reference (classic Go <1.22 bug). | ⏳ TODO |
| 12 | `internal/web/middleware.go:52` | CSRF token cookie is `HttpOnly: false` — defeats CSRF protection if XSS exists. | ⏳ TODO |
| 13 | `internal/routes/auth.go:165,191-192,241-242` | DB errors silently discarded with `_ = ...Scan()`. | ⏳ TODO |
| 14 | `internal/middleware/auth.go:222-229` | JWT cookie lacks Secure/HttpOnly flags. | ⏳ TODO |
| 15 | `internal/routes/billing.go:175` | User-provided date concatenated without format validation. | ⏳ TODO |
| 16 | `internal/routes/webhook_preconsume.go:110-119` | TOCTOU race in pre-consume freeze check. | ⏳ TODO |
| 17 | `internal/db/db.go:484-485` | MD5 used for config versioning — prefer SHA-256. | ⏳ TODO |
| 18 | `internal/routes/admin_users.go:25-37` | ILIKE search with `%` wildcard can cause heavy table scans. | ⏳ TODO |
| 19 | `internal/routes/webhook_billing.go:47-49` | User ID lookup by key_hash on hot billing path — consider Redis caching. | ⏳ TODO |
| 20 | `internal/config/config.go:153` | App BaseURL has `https://portal.example.com` as placeholder default. | ⏳ TODO |
| 21 | `internal/web/helpers.go:334` | `toJSON` returns `template.JS` bypassing auto-escaping. | ⏳ TODO |

### Low Issues

| # | File:Line | Issue | Status |
|---|-----------|-------|--------|
| 22 | `cmd/server/bootstrap.go:109` | `zap.NewProduction()` error ignored — nil logger would panic. | ⏳ TODO |
| 23 | `internal/routes/payment.go:61-66` | `/billing/callback` is not under auth middleware. | ⏳ TODO |
| 24 | `internal/web/middleware.go:81` | CSRF comparison uses `!=` not `subtle.ConstantTimeCompare`. | ✅ FIXED (b568427) |
| 25 | `internal/routes/webhook_preconsume.go:130-135` | Fail-open on Redis error: pre-consume allows request without freezing balance. | ⏳ TODO |
| 26 | `internal/routes/sensitive.go:77` | Uses `zap.L()` global logger instead of injected logger. | ⏳ TODO |

### Test Coverage Gaps

- Payment webhook handlers (Stripe/PayPal/Alipay/WeChat) lack direct unit tests
- Pre-consume/settle billing flow (`webhook_preconsume.go`) has no test file
- CSRF/XSS middleware has no dedicated tests
- Enterprise application approval flow lacks integration tests

### Dockerfile Issues

- `npm install` + `npx tailwindcss` in Dockerfile adds ~30-60s build time
- `-ldflags="-s -w"` strips line numbers from panics
- No HEALTHCHECK instruction

---

## ai-proxy-go — 健康分 6.5/10 → 7.5/10 (after fixes)

### Summary

A large, well-structured Go AI proxy with good concurrency patterns and comprehensive error classification. However, there are several swallowed errors (particularly Redis ops), missing graceful shutdown for background goroutines, a race condition in the audit logger's `Flush` method, and critical billing fail-open behavior. Most concerning: `errcheck`, `ineffassign`, `staticcheck`, and `unused` linters are explicitly disabled in `.golangci.yml`, hiding ~220 legacy issues.

### Critical Issues

| # | File:Line | Issue | Status |
|---|-----------|-------|--------|
| 1 | `internal/audit/audit.go:174,196` | **Race condition in `Flush`** — `Log`/`Record` spawns `go l.Flush()` when batch is full. `Flush()` swaps `l.batch` under lock, but concurrent goroutines read `batchLen` before lock acquisition. | ⏳ TODO |
| 2 | `internal/audit/audit.go:233` | **Batch dedup logic broken on flush failure** — `l.batch = append(batch, l.batch...)` prepends failed records but new records may have already been appended in `Log()` (no lock held during HTTP call). | ⏳ TODO |
| 3 | `internal/auth/auth.go:606,624,698,720` | **Swallowed Redis errors** — `_ = m.redis.Set(...).Err()`. Redis write failures silently bypass error logging. | ⏳ TODO |
| 4 | `pkg/billing/preconsume.go:77-127` | **Fail-open billing bypass** — `PreConsume` returns `Success: true` on ANY error (Marshal, CreateRequest, Do, Decode). If Portal is unreachable, all requests are allowed. | ⏳ TODO (intentional design) |
| 5 | `pkg/billing/billing.go:269` | **Initial sync error silently swallowed** — `_ = b.SyncPricingFromPortal(ctx)`. If Portal is down at startup, proxy starts with stale/default pricing for entire `syncInterval`. | ⏳ TODO |
| 6 | `.golangci.yml` | **4 major linters disabled** — `errcheck`, `ineffassign`, `staticcheck`, `unused` are all disabled. | ⏳ TODO (incremental) |
| 7 | `config.yaml` | **Hardcoded default `your-db-password`** — (Note: config.yaml is gitignored, this is a deployment hygiene concern.) | ℹ️ Won't fix (gitignored) |

### Medium Issues

| # | File:Line | Issue | Status |
|---|-----------|-------|--------|
| 8 | `cmd/server/main.go:97-110` | Background goroutines (PubSub, health check, sync loops) have no stop channel — leak on shutdown. | ✅ FIXED (f275fcd) |
| 9 | `pkg/keypool/manager.go:591-634` | PubSub reconnection retries with `time.Sleep(5s)` but no exponential backoff. | ⏳ TODO |
| 10 | `internal/auth/auth.go:727-775` | Balance listener reconnection leaks `time.After` channels until GC on each retry. | ⏳ TODO |
| 11 | `internal/audit/audit.go:173-175` | `go l.Flush()` on every batch-full event can spawn unlimited goroutines. | ⏳ TODO |
| 12 | `pkg/keypool/selection.go:168-301` | `GetKeyByPriority` acquires `lock.RLock()` then calls `m.loadFromRedis(ctx)` which acquires `m.poolsMu.Lock()` — potential deadlock. | ❌ Not confirmed |
| 13 | `pkg/billing/billing.go:696-701` | `os.IsTimeout(transportErr)` is dead code — doesn't work with `context.DeadlineExceeded`. First `errors.Is` branch already handles it. | ⏳ TODO |
| 14 | `pkg/relay/providers.go:302-305` | `SupportedProviders()` iterates `DefaultProviderConfigs` which is modified at runtime by `init()` AND `RegisterProvider()` — no mutex. | ⏳ TODO |
| 15 | `pkg/circuitbreaker/breaker.go:98` | Half-open → closed transition `successes` counter is never reset across multiple half-open windows. | ⏳ TODO |

### Low Issues

| # | File:Line | Issue |
|---|-----------|-------|
| 16 | `pkg/telemetry/telemetry.go:21` | Inconsistent atomic pattern: `atomic.Value` for tracer vs `atomic.Pointer` for tracerProvider. |
| 17 | `pkg/billing/billing.go:644-647` | `stringsContains` is a trivial wrapper — inline it. |
| 18 | `internal/auth/auth.go:1080-1088` | `hashCacheKey` and `hashKeySHA256` are identical functions (both SHA-256 hex). |
| 19 | `pkg/relay/base/adaptor.go:17-19` | `SharedHTTPClient()` returns a mutable `*http.Client` — callers could mutate Transport/Timeout. |
| 20 | `config.yaml:26` | `dev_mode: true` committed — production deployments must remember to override. |
| 21 | `pkg/billing/billing.go:37` | Duplicated `CalculateCost` logic between `CalculateCost` and `CalculateCostWithCache`. |

### Test Coverage Gaps

- `keypool/persist.go` — AES-256-GCM encryption/decryption round-trip untested
- `keypool/health.go` — `checkUpstreamTPMOverflow` timing edge cases untested
- `billing/preconsume.go` — `estimateInputCost` with margin/fixedPrice untested
- `auth/auth.go` — JWT path with stale cache and portal unhealthy untested
- `audit/audit.go` — batch full + flush race untested
- Response streaming: `convert_test.go` lacks streaming format tests for `/v1/responses` path

---

## Latest Commits (2026-07-12)

### ai-portal-go — `584f982`

```
fix(test): skip snapshot integrity check in CI (no docker exec available)
fix(enterprise-keys): listEnterpriseKeys fields, reassign validation, revoke/reactivate guards
fix(ci): skip docker-based DB snapshot in CI environments
fix(enterprise): 组卡片启停按钮文案 Disable/Enable；编辑抽屉平台组预选锁定+上限提示
```

### ai-proxy-go — `dca2bb9`

```
fix(handlers): complete error-path instrumentation for audio/responses
audio/embeddings/images 上游 4xx/5xx 不再误记成功：熔断器/key池/usage 按失败记录
docs(env): mark JWT_SECRET unused after sk-jwt removal
security: remove sk-jwt local validation bypass
```

---

## Cross-Project Observations

| Concern | Portal | Proxy |
|---------|--------|-------|
| Billing fail-open | Pre-consume Redis error allows unbounded usage | Pre-consume errors always return success |
| Swallowed errors | SQL query errors, bcrypt errors | Redis errors, startup sync errors |
| Goroutine safety | Loop variable capture, locale race | Unlimited Flush goroutines, no shutdown |
| Webhook security | Alipay/PayPal sig verification broken | N/A (proxy has no webhooks) |
| Linter coverage | Not assessed | 4 major linters disabled |
