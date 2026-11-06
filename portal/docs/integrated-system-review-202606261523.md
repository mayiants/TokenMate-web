# AI Platform Integrated System Review

**Date:** 2026-06-26 15:23 UTC  
**Scope:** `ai-portal-go` (Portal) + `ai-proxy-go` (Proxy) as an integrated system  
**Portal branch/commit:** `main` @ `44d4c8daeed63d36e1e7286ac86f2968b95f9b66`  
**Proxy branch/commit:** `main` @ `c6f4aed0aa0965d177564e427e694fd820f3d954`  
**Review dimensions:** Architecture, Implementation, Security, Maintainability, Efficiency, Performance

---

## Executive Summary

The AI Platform is a Go-based Portal + Proxy pair that is **functionally very mature** (≈90–95% of the intended feature set is implemented and working). The Proxy has received focused security hardening recently (atomic nonce checks, sync-secret validation, crypto/rand fixes), and the Portal has a broad feature surface covering billing, enterprise management, subscriptions, coupons, invitations, announcements, and provider health monitoring.

However, **production readiness is held back by a cluster of high-impact security shortcuts, an un-run migration pipeline, and architectural monoliths/global state that make testing and operations harder than they should be.** Most issues are known, localized, and fixable; a few are legacy design choices that need conscious decisions.

### Maturity Scorecard (1–5)

| Dimension | Portal | Proxy | Notes |
|-----------|--------|-------|-------|
| Functional completeness | 4.5 | 4.5 | Core flows work; enterprise/dashboard polish remains |
| Security | 2.5 | 3.0 | Several HIGH findings still open (see tables below) |
| Implementation quality | 2.5 | 3.0 | Route-heavy monolith, globals, duplication |
| Maintainability | 2.5 | 3.0 | Migration gap, outdated deps, global state |
| Efficiency / Performance | 3.0 | 3.5 | Good basics; unbounded caches and streaming leaks |
| Test / CI coverage | 2.5 | 3.5 | Proxy race tests pass; Portal route coverage is low |

### Overall Achievement

- **Feature achievement:** ~92% — most planned modules are present and integrated.
- **Production-readiness:** ~75% — functional, but not yet safe to expose broadly without fixing P0/P1 items below.

---

## 1. Architecture

### 1.1 System Context

```text
Client ──▶ ai-proxy-go:8080 ──▶ Upstream AI Providers
                │
                │ webhook (usage / health / upstream-usage)
                ▼
        ai-portal-go:3001 ──▶ PostgreSQL + Redis
```

- **Portal** is the source of truth for users, API keys, billing, subscriptions, models, and provider keys.
- **Proxy** validates keys against Portal, enforces rate limits, routes to upstream providers, transforms requests/responses, and reports usage back via webhook.
- **Integration channels:** HTTP sync (Portal → Proxy), webhooks (Proxy → Portal), Redis Pub/Sub (balance updates, key-pool sync).

### 1.2 Portal Architecture

| Layer | Key Files | Observation |
|-------|-----------|-------------|
| Entry | `cmd/server/bootstrap.go`, `cmd/server/router.go`, `cmd/server/server.go` | Manual `AppDeps` wiring; middleware chain is clean |
| Routing | `internal/routes/*.go` (129 files, ~15k+ lines) | Business logic, SQL, auth, and JSON responses mixed together |
| Data access | `internal/db/schema.go`, `internal/db/migrate.go`, `internal/query/` | `InitSchema` runs; `Migrate` exists but is **never called** |
| Services | `internal/services/` | Only reward, exchange, pricing, billing — too thin |
| Web | `internal/web/` | HTMX/Alpine/Tailwind server-rendered UI |
| Shared packages | `pkg/jwt`, `pkg/crypto`, `pkg/audit`, `pkg/mail`, `pkg/payment`, `pkg/metrics` | Good primitives, but metrics are not wired to production code |

**Key concern:** The Portal is a **route-heavy monolith**. There is no repository/domain layer; authorization rules and SQL are duplicated across handlers. This makes unit testing difficult and explains the very low `internal/routes` coverage.

### 1.3 Proxy Architecture

| Layer | Key Files | Observation |
|-------|-----------|-------------|
| Entry | `cmd/server/main.go` | Good startup secret checks; wires globals via setter functions |
| Routing | `internal/server/server.go`, `internal/server/routes.go` | Clean middleware chain (request ID, logging, CORS, concurrency limit) |
| Core handlers | `internal/server/handlers_*.go` (split from ~3,517-line `handlers.go`) | Per-concern files: chat, embeddings, images, audio, responses, status, common helpers |
| Auth | `internal/auth/auth.go` | API-key validation + JWT path + admin auth + nonce replay prevention |
| Rate limit | `internal/ratelimit/ratelimit.go` | Local token buckets + Redis async sync |
| Provider layer | `pkg/relay/`, `pkg/convert/`, `pkg/keypool/` | Adaptor interface is the strongest part of the design |

**Key concern (resolved):** `internal/server` previously stored dependencies as package globals. They are now fields of `HandlerService` (`internal/server/handler_service.go`), constructed in `cmd/server/main.go` and injected into `server.New`. All handlers and shared helpers are methods on `*HandlerService`, improving testability and lifecycle control.

### 1.4 Integration Points

| Direction | Mechanism | Risk |
|-----------|-----------|------|
| Portal → Proxy key sync | `GET /api/admin/key-pool/sync` (signed with `X-Admin-Secret`) | Depends on shared admin secret; recent fixes ensure missing sync secret is rejected |
| Portal → Proxy model/pricing sync | `GET /api/admin/sync` | Same shared secret |
| Proxy → Portal usage | `POST /api/webhook/usage` / `usage-batch` | Batched via `UsageBuffer`; fallback logs written locally |
| Proxy ↔ Portal balance | Redis Pub/Sub `balance_update` | Good near-real-time rejection |

---

## 2. Implementation Quality

### 2.1 Portal

#### Strengths
- Middleware chain is well-structured: request ID → structured logging → CORS → security headers → CSRF.
- Password hashing uses `bcrypt.DefaultCost` (`internal/web/auth.go:198`, `internal/web/auth.go:318`, `internal/web/actions.go:132`) — the earlier `cost=10` issue has been fixed.
- API keys are SHA-256 hashed for lookup and upstream keys are AES-256-GCM encrypted (`internal/routes/apikeys_crud.go`, `pkg/crypto/encryption.go`).
- Webhook HMAC signatures use `crypto/subtle.ConstantTimeCompare` (`internal/routes/webhook_common.go`).
- Metrics endpoint uses constant-time comparison (`cmd/server/router.go:48`).

#### Weaknesses
- **`MustExec` anti-pattern** — previously widespread; now fixed in production code. DB write failures are returned/surfaced instead of being swallowed.
- **`_ =` ignored errors** are common, especially `rows.Scan()` and Redis calls.
- **SQL duplication:** `usage_records` was queried directly in dozens of route files. Billing/dashboard usage queries are now centralized in `internal/query/usage.go` (`GetUsageSummary`, `GetUsageRecords`, `GetUsageDistribution`, `GetDashboardStats`); other route files still contain direct SQL that can be migrated incrementally.
- **`http.DefaultClient` with no timeout** in `internal/services/exchange.go:143,173`.
- **`context.Background()` used for external SDK calls** (e.g., WeChat Pay `CreateSession`/`QueryOrder` in `pkg/payment/wechat.go:130-138`) — ignores cancellation and request deadlines.
- **Metrics are defined but not instrumented.** `pkg/metrics.Metrics` counters are declared but never incremented in production code; `/metrics` currently returns mostly zeros.
- **Error responses are inconsistent** — at least four different JSON shapes are returned across handlers.

### 2.2 Proxy

#### Strengths
- Provider adaptor pattern (`pkg/relay/interface.go`, `pkg/relay/providers.go`) is clean and extensible.
- Smart routing in `pkg/keypool/manager.go` considers health, tier, RPM, latency, quota, and affinity.
- Sharded maps/locks reduce lock contention (`pkg/keypool/manager.go` sharded usage counters).
- Concurrency semaphore caps in-flight requests (`internal/server/server.go:46-60`).
- Race tests pass (`go test -race ./internal/... ./pkg/...`).

#### Weaknesses
- **`internal/server/handlers.go` has been decomposed** into `handlers_common.go`, `handlers_chat.go`, `handlers_embeddings.go`, `handlers_images.go`, `handlers_audio.go`, `handlers_responses.go`, and `handlers_status.go`. Shared provider resolution, retry/fallback, usage recording, and streaming utilities live in `handlers_common.go`.
- **Duplicated per-endpoint logic** across chat/embeddings/images/audio handlers has been reduced by moving shared helpers into `handlers_common.go` as methods on `HandlerService`.
- **Magic constants** scattered through handlers: `120s`, `10m`, `15s`, `5m`, `3s`, `30s` timeouts and flush intervals are not config-driven.
- **Error taxonomy is informal** — retry/fallback decisions rely on HTTP status and string matching instead of typed errors.
- **Responses API passthrough** previously created a fresh `http.Client{Timeout: 120 * time.Second}`; it now uses the shared pooled client (`sharedHandlerClient`).

### 2.3 Test Coverage

| Project | Overall Coverage | Notable Gaps |
|---------|------------------|--------------|
| Portal | ~21% (DB-less run) | `internal/routes` ~4%, `internal/query` ~0%, streaming/reconciliation paths untested |
| Proxy | ~50–60% | `internal/server/handlers.go` ~20%, `internal/auth` ~12%, `pkg/keypool` ~19% |

Proxy race tests pass cleanly. Portal `go vet ./...` fails locally only because `uploads/` contains temporary test files with restricted permissions; this is expected and does not affect CI.

### 2.4 Coverage Potential Assessment

Current measured coverage (selected packages):

| Project | Package | Current | Achievable without major refactor | Notes |
|---------|---------|---------|-----------------------------------|-------|
| Portal | `pkg/*` | 17–100% | 80–100% | Mostly pure logic; easy to test |
| Portal | `internal/middleware` | 85.4% | 95%+ | Already well covered |
| Portal | `internal/services` | 68.4% | 85%+ | Reward/exchange logic testable with DB or mocks |
| Portal | `internal/config` | 93.1% | 95%+ | Config validation already tested |
| Portal | `internal/db` | 46.6% | 70% | Schema/migration helpers testable; connection code harder |
| Portal | `internal/routes` | 4.0% | 25–35% | Handlers mix SQL/business logic; need DB or heavy mocking |
| Portal | `internal/web` | 13.3% | 25–35% | Template rendering + DB-heavy handlers |
| Portal | `internal/query` | 0.0% | 60–80% | SQL wrappers can be tested against Postgres |
| Proxy | `internal/audit` | 92.7% | 95%+ | Already strong |
| Proxy | `internal/middleware` | 92.6% | 95%+ | Already strong |
| Proxy | `internal/config` | 88.5% | 95%+ | Already strong |
| Proxy | `pkg/cache` | 82.2% | 90%+ | Easy to test |
| Proxy | `pkg/circuitbreaker` | 96.7% | 98%+ | Already strong |
| Proxy | `pkg/convert` | 49.6% | 75% | Format conversion tests easy to add |
| Proxy | `pkg/relay/*` | 40–94% | 75–90% | Provider adapters testable with HTTP mocks |
| Proxy | `pkg/token` | 59.5% | 80% | Token counting testable |
| Proxy | `pkg/keypool` | 19.3% | 40–50% | Large manager; needs refactor to go higher |
| Proxy | `internal/auth` | 12.4% | 40–50% | Needs HTTP mocks for Portal + Redis |
| Proxy | `internal/server` | 20.4% | 35–45% | Monolithic handlers; refactor needed for >50% |
| Proxy | `pkg/billing` | 41.3% | 70% | Billing calculation tests easy to add |

Realistic short-term targets (without major architectural changes):
- **Portal:** raise overall coverage from ~21% to **40–45%** by testing `pkg/*`, `internal/services`, `internal/query`, and adding happy-path handler tests with a test database.
- **Proxy:** raise overall coverage from ~50–60% to **70–75%** by adding tests for `pkg/convert`, `pkg/billing`, provider adapters, and handler happy paths with mocked upstreams.

To exceed those numbers, the Portal route monolith and Proxy handler/global-state monolith must be decomposed into testable services.

---

## 3. Security Findings

### 3.1 Portal

| Severity | Finding | Location | Status |
|----------|---------|----------|--------|
| **HIGH** | Hardcoded fallback admin secret (`"admin-secret"`) when `PROXY_SYNC_SECRET` is unset | `internal/routes/admin_common.go:458-461` | **Fixed** — now returns an error if the env var is missing |
| **HIGH** | TLS certificate verification disabled for upstream key validation (`InsecureSkipVerify: true`) | `internal/routes/admin_common.go:515-522` | **Fixed** — TLS verification enabled by default; can be disabled only via `SKIP_UPSTREAM_TLS_VERIFY=true` |
| **HIGH** | TLS verification disabled when fetching provider model lists | `internal/routes/models_sync.go:340-348` | **Fixed** — same `SKIP_UPSTREAM_TLS_VERIFY` override |
| **HIGH** | WeChat Pay webhook signature verification is a no-op (returns `true`) | `pkg/payment/wechat.go:121-126` | **Fixed** — added RSA-SHA256 verification using `Wechatpay-*` headers and a configured platform public key |
| MEDIUM | JWT lacks `iss`/`aud` checks and has 30-day lifetime | `pkg/jwt/jwt.go:30-61` | **Open** |
| MEDIUM | Email HTML injection: `enterpriseName` and `reason` concatenated into HTML without escaping | `pkg/mail/mail.go:270-274` | **Open** |
| MEDIUM | Registration duplicate-email race: `SELECT COUNT(*)` is not atomic with `INSERT` | `internal/routes/auth.go:189-196` | **Open** |
| MEDIUM | Referral/welcome voucher `max_uses`/`used_count` and expiration checked **outside** the transaction | `internal/services/reward.go:30-82`, `96-111`, `201-205` | **Open** |
| LOW | Audit logger silently drops events on DB error | `pkg/audit/audit.go:37-47` | **Open** |
| LOW | `uploads/enterprise-apps/` permissions break `go vet ./...` / `go test ./...` | repository root | **Expected — temp user-uploaded test files; not a functional bug** |

**Note on fixed items:** The earlier `bcrypt cost=10` finding is resolved (now `bcrypt.DefaultCost`), and the metrics endpoint now uses `subtle.ConstantTimeCompare`. CORS no longer defaults to allow-all when `ALLOWED_ORIGINS` is empty; however, a `"*"` entry still permits any origin.

### 3.2 Proxy

| Severity | Finding | Location | Status |
|----------|---------|----------|--------|
| **HIGH** | Admin endpoints fall back to simple `X-Admin-Secret` validation; HMAC signature is optional | `internal/auth/auth.go:388-421` | **Fixed** — HMAC signature is now required; simple secret fallback removed |
| **HIGH** | CORS middleware defaults to `allowAll = true` when `allowedOrigins` is empty and sets `Access-Control-Allow-Credentials: true` with `Access-Control-Allow-Origin: *` | `internal/server/server.go:294-334` | **Fixed** — empty list denies cross-origin; wildcard no longer sends `Allow-Credentials: true` |
| MEDIUM | Default admin secret `"admin-secret"` is only rejected at startup; config default remains | `internal/config/config.go:384`; `cmd/server/main.go:43-45` | **Partially fixed** |
| MEDIUM | AES-decrypted upstream key is not validated for expected format; mismatch only logs/skips, does not mark key inactive | `pkg/keypool/manager.go:2161-2172` | **Partially fixed** |
| MEDIUM | Fallback logs (`usage`, `upstream-usage`, `provider-health`) write usage records to local NDJSON unencrypted | `internal/server/usage_buffer.go:36-40`, `pkg/fallbacklog/writer.go` | **Open** |
| MEDIUM | Model validator can place API keys in URL query parameters for `AuthType == "query"` | `pkg/models/validator.go:170-174` | **Open** |
| LOW | Dev-mode auth bypass branch has been removed; empty Portal URL now refuses authentication, and startup fatal-check exists | `internal/auth/auth.go:720-723`; `cmd/server/main.go:52-55` | **Fixed** |
| LOW | No explicit prompt-injection or output-filtering controls | proxy design assumes upstream trust | **Acknowledged risk** |

**Cross-check against the 2026-06-13 comprehensive review:**
- **Fixed:** Proxy dev-mode bypass, Portal bcrypt cost, Portal metrics constant-time compare.
- **Still open:** both projects' global-state / monolith issues (P1 #10–#12). All listed HIGH security findings are now fixed.

---

## 4. Maintainability

### 4.1 Database Schema Management (Portal)

The project intentionally moved away from timestamped SQL migrations to an idempotent `InitSchema()` approach in `internal/db/schema.go` because the schema was changing rapidly during early development. `cmd/server/bootstrap.go:123-128` calls `database.InitSchema()` and `database.SeedDefaultData()`, while `internal/db/migrate.go` is currently unused.

Current state:
- Old migration files still exist under `migrations/` but are **not executed** at startup.
- `internal/db/schema.go:467` only creates `usage_records_default` partition. Without monthly partitions (which were defined in the unused `migrations/002_usage_and_records.sql`), the default partition grows unbounded.
- `schema.go` itself is the source of truth; drift is possible if multiple engineers edit it without coordination.

Recommendation:
1. **Archive or delete the stale `migrations/` files** now to avoid confusion and accidental execution. Keep only a short `migrations/README.md` documenting the strategy.
2. Before mass-production release, create a clean baseline from `schema.go` and switch back to versioned migrations for all future schema changes.
3. Until then, add an automated `schema.go` diff check in CI (e.g., spin up Postgres, run `InitSchema`, dump schema, compare to a golden dump) to catch unintended drift.

### 4.2 Dependencies

**Portal `go.mod` highlights:**

| Dependency | Current | Latest / Fixed In | Risk |
|------------|---------|-------------------|------|
| `github.com/jackc/pgx/v5` | `v5.5.0` | `v5.9.2` | Known SQL-injection/panic CVEs |
| `github.com/golang-jwt/jwt/v5` | `v5.2.0` | `v5.2.2` | Memory allocation CVE |
| `github.com/redis/go-redis/v9` | `v9.3.0` | `v9.6.3` | Out-of-order response CVE |
| `github.com/gin-gonic/gin` | `v1.9.1` | `v1.12.0` | Older release with known DoS fixes |
| `github.com/go-playground/validator/v10` | `v10.14.0` | `v10.30.3` | Bug fixes |

**Proxy `go.mod` highlights:**

| Dependency | Current | Latest | Risk |
|------------|---------|--------|------|
| `github.com/gin-gonic/gin` | `v1.9.1` | `v1.12.0` | Same as above |
| `github.com/go-playground/validator/v10` | `v10.14.0` | `v10.30.3` | Same as above |
| `golang.org/x/net` | `v0.39.0` | newer | Should track security fixes |

**`govulncheck` results (installed and run on 2026-06-26):**

| Project | Reachable Vulnerabilities | Key Modules Affected |
|---------|---------------------------|----------------------|
| Portal | 31 | `pgx/v5@v5.5.0`, `jwt/v5@v5.2.0`, `redis/v9@v9.3.0`, Go stdlib `crypto/tls`, `crypto/x509` |
| Proxy | 29 | `jwt/v5@v5.2.1`, `redis/v9@v9.5.1`, Go stdlib `crypto/x509`, `net/http`, `os` |

Most reachable issues will be resolved by upgrading the three third-party modules and aligning both projects on the latest patch release of Go (currently ≥1.25.2 for Portal, ≥1.24.9 for Proxy). `govulncheck` should be added to CI.

### 4.3 CI / Tooling

- **Portal CI** (`ci-portal.yml`) runs build, vet, fmt, unit tests, i18n audit, and Playwright. `go vet ./...` fails locally only because `uploads/` contains temporary test files with restricted permissions; this is expected and does not affect CI.
- **Proxy CI** (`ci-proxy.yml`) runs build, vet, fmt, race tests, and Docker build. `golangci-lint` is commented out with a TODO — re-enable it.
- Both projects use Go toolchain versions that differ slightly (Portal `go 1.25.1` in `go.mod`, Proxy `go 1.24.0`). CI pins `1.24`. Align on one supported version and bump CI to that version.

### 4.4 Documentation

- READMEs and design docs exist and are reasonably current.
- The original Proxy design doc (`ai-proxy-go-design-202604291600.md`) planned packages such as `internal/portal/` and `internal/audit/retention.go`; the current structure has drifted (e.g. billing/audit live in `pkg/billing` and `internal/audit`). This is not a bug, but architectural drift should be documented.

### 4.5 Data Retention & Singleton Background Jobs

After the review, a number of background jobs were identified that could grow the database unbounded or run concurrently across multiple Portal instances:

- **Retention cleanup** (`internal/db/retention.go`) runs daily and removes old rows from high-volume tables: `audit_logs` (7 years), `verification_codes`, `email_notifications`, `channel_health_history`, `provider_health_stats*`, `model_group_health_stats`, `upstream_key_usage`, `usage_summary_*`, `checkin_records`, inactive `user_devices`, `sensitive_check_logs`, and accepted/expired `enterprise_invitations`.
- **Advisory locks** (`internal/db/lock.go`) are used in `cmd/server/background.go` so that scheduled jobs (`monthly-billing-reset`, `auto-renewals`, `enterprise-app-expiry`, `model-lifecycle`, `upstream-key-validation`, `retention-cleanup`) run on only one instance at a time.
- **Audit log lifecycle**: `audit_logs` are retained for 7 years for compliance. The web UI and API list/export endpoints only expose the last 12 months (the "hot" window). Older records can still be fetched by `super_admin` or via manual lookup.
- **Old partition dropping**: `USAGE_RECORDS_RETENTION_MONTHS` can be set to drop monthly `usage_records` partitions older than the retention window; by default no partitions are dropped.

---

## 5. Efficiency & Performance

### 5.1 Portal

| Concern | Location | Impact |
|---------|----------|--------|
| In-memory IP rate limiter | `internal/middleware/ratelimit.go` | Does not scale horizontally; per-instance state |
| `usage_records` default partition only | `internal/db/schema.go:467` | Unbounded table growth; slow queries |
| Dashboard aggregates large ranges | `internal/routes/dashboard.go` | No materialized pre-aggregation beyond hourly/daily summaries |
| Billing batch N+1 user lookup | `internal/routes/webhook_billing.go:45-54` | One `api_keys` lookup per record outside the per-user transaction |
| Exchange-rate fetch no timeout/circuit breaker | `internal/services/exchange.go:143,173` | Can block or retry indefinitely |
| Metrics not instrumented | `pkg/metrics/metrics.go` | No real latency histograms or production counters |

### 5.2 Proxy

| Concern | Location | Impact |
|---------|----------|--------|
| Handler monolith | `internal/server/handlers_*.go` (decomposed) | Individual paths are now in focused files; optimization and testing are easier |
| Streaming goroutine leak risk | `internal/server/handlers_chat.go` | Background reader may block on `ReadString` until the 10-minute timeout even if client disconnects |
| Fresh HTTP client for Responses API | `internal/server/handlers.go:2857,2953` | Bypasses connection pooling; extra TCP handshakes |
| Unbounded rate-limit maps | `internal/ratelimit/ratelimit.go:98-101` | `localCounters` / `concurrentCounters` `sync.Map` entries never evicted |
| Global background goroutines | `internal/server/handlers.go`, `pkg/keypool/manager.go` | No lifecycle hooks; shutdown/leak detection is harder |
| Aggressive Redis sync ticker | `internal/ratelimit/ratelimit.go` (100 ms) | Steady CPU/Redis load at low traffic |
| Affinity cache and key pool memory | `pkg/cache/affinity.go`, `pkg/keypool/manager.go` | No explicit memory ceiling or eviction policy |

### 5.3 Positive Performance Choices

- Shared `http.Client` with connection pooling for most upstream traffic.
- Atomic pointer for `ModelIndex` enables lock-free reads in `pkg/models/registry.go`.
- Lazy encoder cache in `pkg/token/counter.go`.
- Proxy concurrency semaphore prevents scheduler thrashing.

---

## 6. Prioritized Recommendations

### P0 — Blockers for Broad Production Use

| # | Recommendation | Owner / Area | Effort | Status |
|---|----------------|--------------|--------|--------|
| 1 | **Clean up stale migration files** and document the `InitSchema` baseline strategy; prepare to switch back to versioned migrations after mass-production release. | Portal / DB | 4 h | **Fixed** — stale SQL files removed; `migrations/README.md` documents the `InitSchema` baseline strategy. |
| 2 | **Remove hardcoded secrets:** replace `"admin-secret"` fallback in `validateViaProxyFilter` with a required, strong `PROXY_SYNC_SECRET`. | Portal / Security | 2 h | **Fixed** — missing `PROXY_SYNC_SECRET` now returns an error instead of falling back to a hardcoded secret. |
| 3 | **Re-enable TLS verification** in `validateUpstreamKey` and `models_sync.go`; add a config override only for explicitly known broken providers. | Portal / Security | 4 h | **Fixed** — TLS verification enabled by default; `SKIP_UPSTREAM_TLS_VERIFY=true` can disable it for known broken providers. |
| 4 | **Implement WeChat Pay webhook signature verification** or fail closed. | Portal / Payments | 8 h | **Fixed** — RSA-SHA256 verification using `Wechatpay-*` headers and a configured platform public key. |
| 5 | **Force HMAC signatures on Proxy admin endpoints** and remove the simple-secret fallback. | Proxy / Security | 6 h | **Fixed** — admin endpoints require HMAC signature; simple secret fallback removed. |
| 6 | **Fix Proxy CORS** so empty `allowedOrigins` does not default to allow-all, and never combine `Allow-Credentials: true` with wildcard origin. | Proxy / Security | 2 h | **Fixed** — empty origin list denies cross-origin; wildcard origin no longer sends `Allow-Credentials: true`. |

### P1 — High Priority (Fix Within 1–2 Weeks)

| # | Recommendation | Owner / Area | Effort | Status |
|---|----------------|--------------|--------|--------|
| 7 | Upgrade Portal deps: `pgx` → ≥5.9.2, `jwt` → ≥5.2.2, `redis` → ≥9.6.3, `gin` → ≥1.12.0. Add `govulncheck` to CI. | Both / Security | 8 h | **Fixed** — deps upgraded and toolchain bumped to `go1.25.11`; `govulncheck ./...` reports 0 reachable vulns. CI `govulncheck` step still to add. |
| 8 | Add `usage_records` monthly partition creation in `InitSchema`/startup or via a scheduled job so the default partition does not grow unbounded. | Portal / DB | 6 h | **Fixed** — `internal/db/partition.go` creates rolling monthly partitions; `InitSchema` calls it at startup and `StartUsagePartitionMaintenance` runs a daily background job. |
| 9 | Replace `MustExec` with error-returning `Exec` and surface DB failures to callers. | Portal / Quality | 16 h | **Fixed** — all production call sites in `internal/routes/*`, `internal/web/*`, and `internal/services/exchange.go` converted with AST-based refactor; HTTP handlers return 500, error-returning helpers return `error`, and background jobs log. `MustExec`/`MustExecTx` definitions remain only in `internal/db/db.go` for existing tests. Test setup files (`enterprise_test.go`, `reward_test.go`, `exchange_test.go`) also converted to fail on DB error. |
| 10 | Move billing/dashboard duplicated SQL into `internal/query/` or a repository layer. | Portal / Architecture | 16 h | **Fixed** — new `internal/query/usage.go` helpers (`GetUsageSummary`, `GetUsageRecords`, `GetUsageDistribution`, `GetDashboardStats`) now centralize `usage_records` queries; `billing.go` and `dashboard.go` route handlers format responses only. |
| 11 | Inject Proxy dependencies instead of package globals; pass a service facade into handlers. | Proxy / Architecture | 24 h | **Fixed** — `internal/server/handler_service.go` introduced `HandlerService`; all `server.Set*` globals removed and `cmd/server/main.go` injects one service into `server.New`. |
| 12 | Refactor `internal/server/handlers.go` into provider-resolution, fallback, streaming, and per-endpoint services. | Proxy / Architecture | 24 h | **Fixed** — monolithic `handlers.go` replaced by `handlers_common.go`, `handlers_chat.go`, `handlers_embeddings.go`, `handlers_images.go`, `handlers_audio.go`, `handlers_responses.go`, and `handlers_status.go`; shared helpers and per-concern handlers are methods on `HandlerService`. |
| 13 | Bound memory growth: evict stale rate-limit counters, model-index cache entries, and nonce maps. | Proxy / Performance | 8 h | **Fixed** — local token-bucket counters evicted after inactivity; `pkg/cache/affinity.go` local cache and stats maps trimmed to `maxEntries`; auth middleware now cleans stale balance-cache entries. Nonce and API-key caches already had cleanup loops. |
| 14 | Fix streaming lifecycle so client disconnect propagates cancellation to the reader goroutine. | Proxy / Performance | 4 h | **Fixed** — `handleStreamingResponse` closes `resp.Body` via `context.AfterFunc` when the stream context is done; `handleResponsesPassthroughStreaming` now builds the request with `streamCtx` and also closes the body on cancellation. |
| 15 | Use the shared pooled HTTP client for the Responses API passthrough. | Proxy / Performance | 2 h | **Fixed** — both Responses API handlers now use `sharedHandlerClient` instead of creating a fresh `http.Client`. |

### P2 — Medium Priority (Within a Month)

| # | Recommendation | Owner / Area | Effort |
|---|----------------|--------------|--------|
| 16 | Add JWT `iss`/`aud` validation and shorten default token lifetime; implement refresh/rotation. | Portal / Security | 8 h |
| 17 | Escape email HTML or use templates consistently in `pkg/mail/mail.go`. | Portal / Security | 2 h |
| 18 | Close registration/referral race windows by moving limit checks inside transactions or adding DB constraints. | Portal / Security | 6 h |
| 19 | Encrypt fallback log files or write them to a restricted, monitored location. | Proxy / Security | 4 h |
| 20 | Remove `AuthType == "query"` or rotate/log-warning when keys are exposed in URLs. | Proxy / Security | 4 h |
| 21 | Instrument Portal metrics counters or remove the unused `/metrics` endpoint. | Portal / Observability | 4 h |
| 22 | Add real E2E tests for Proxy chat completion, streaming, fallback, and cross-provider conversion. | Proxy / Testing | 16 h |
| 23 | Re-enable `golangci-lint` in Proxy CI and fix existing lint issues. | Proxy / CI | 8 h |

### P3 — Continuous Improvements

- Extract magic constants to config/consts.
- Standardize error response formats across both services.
- Add OpenAPI/Swagger specs.
- Implement distributed rate limiting for Portal.
- Add latency histograms (Prometheus) for both services.
- Document architectural drift from original design docs.

---

## 7. Static-Check Summary

| Check | Portal | Proxy |
|-------|--------|-------|
| `go vet` (selected packages) | ✅ clean | ✅ clean |
| `go vet ./...` | ✅ clean (temp `uploads/` files removed) | ✅ clean |
| `go test -race` (selected) | ✅ packages pass | ✅ all packages pass |
| Coverage (selected packages) | See §2.4; routes/query low | See §2.4; handlers/keypool low |
| `go list -m -u all` | Deps aligned to latest stable (pgx, jwt, redis, gin, validator) | Deps aligned (gin, validator, x/net) |
| `govulncheck` | ✅ 0 reachable vulns (toolchain `go1.25.11`) | ✅ 0 reachable vulns (toolchain `go1.25.11`) |

---

## 8. Conclusion

The AI Platform is **feature-rich and close to productization**, but it is not yet a safe, low-maintenance production system. The highest-leverage next steps are:

1. **Clean up stale Portal migrations** and add `usage_records` monthly partition creation to `InitSchema`/startup.
2. ~~Close the remaining HIGH security gaps~~ — all P0 HIGH findings are now fixed.
3. **Upgrade vulnerable dependencies** and add `govulncheck` to CI.
4. **Architectural decomposition** of the Portal billing/dashboard SQL and the Proxy handler/global-state monolith is complete; remaining route-level SQL duplication can be migrated incrementally.

After these P0/P1 items are resolved, the system will be in a much stronger position for public beta and production traffic.

---

## Appendix A — Reference Documents

- `ai-portal-go/docs/project-comprehensive-review-202606132130.md` — prior combined review
- `ai-portal-go/docs/stage-6-productization-roadmap-202605151800.md` — productization roadmap
- `ai-portal-go/docs/monolithic-files-refactor-plan-202606141050.md` — main.go/routes refactor plan
- `ai-proxy-go/docs/ai-proxy-go-design-202604291600.md` — original Proxy design
- `ai-proxy-go/docs/proxy-architecture-design-202605022300.md` — Proxy architecture/design notes
- `ai-proxy-go/docs/reports/key-sync-routing-audit-202606181345.md` — recent Proxy audit
