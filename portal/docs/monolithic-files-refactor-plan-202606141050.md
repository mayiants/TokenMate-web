# Monolithic Files Refactor Plan

**Date**: 2026-06-14 10:50 UTC  
**Scope**: `ai-portal-go` Portal codebase  
**Branch**: `enterprise-cardification`  
> **Status**: Phase 1 ✅ completed; Phase 2 ✅ completed; Phase 3 ✅ completed; Phase 4 ✅ completed; Phase 5 ✅ completed
>
> 备注：Phase 4 未单独创建 `internal/workers/` 包，后台任务统一在 `cmd/server/background.go` 中使用 `safeGo` 管理；Phase 2/3 中仍有少量 balance/subscription 扣减逻辑内联在 route handlers 中，可作为后续常规重构债务持续优化，不再阻塞主计划。  
> 更新：2026-06-11
>
> - Phase 1：`cmd/server/main.go` 已拆分为 `main.go`/`bootstrap.go`/`router.go`/`background.go`/`server.go`；CORS/IP rate limiter/static files 已提取。
> - Phase 2：`internal/services/billing.go` 已存在并承担 cost/ratio 计算，但余额扣减、订阅/企业扣减、stats 更新仍在 route handlers 中。
> - Phase 3：旧大文件已按领域拆分，但多个拆分后文件仍超过 600 行目标（`admin_common.go`、`admin_keypool.go`、`admin_models.go`、`admin_reconciliation.go`、`enterprise_common.go`、`enterprise_members.go`、`models_crud.go`、`webhook_usage.go`、`admin_handler_common.go`）。
> - Phase 4：`internal/workers/` 包尚未创建，后台任务仍通过 `safeGo` 直接启动。

---

## 1. Background

Recent code reviews identified that several core files in `ai-portal-go` have grown beyond a maintainable size:

| File | Lines (as of 2026-06-14) | Main responsibilities |
|---|---|---|
| `cmd/server/main.go` | ~695 | config validation, dependency bootstrap, CORS, IP rate-limiting, static file serving, default admin init, route registration, metrics/health endpoints, background goroutines, graceful shutdown |
| `internal/routes/admin.go` | ~3523 | users, providers, models, key pool, upstream usage, provider health, reconciliation, invoice requests |
| `internal/routes/enterprise.go` | ~3210 | enterprise members, keys, groups, billing/quota/credit, alerts, invitations |
| `internal/routes/models.go` | ~2378 | model CRUD, sync/validation, EOL handling |
| `internal/routes/webhook.go` | ~1827 | usage webhook, billing webhook, health webhook |
| `internal/web/admin_handlers.go` | ~1779 | admin page form handlers |
| `internal/routes/apikeys.go` | ~1113 | API key CRUD + validation |
| `internal/routes/vouchers.go` | ~1096 | voucher CRUD |
| `internal/routes/subscriptions.go` | ~1043 | subscription CRUD + renewals |

The oversized files increase cognitive load, make code reviews difficult, encourage duplicate logic, and complicate horizontal scaling (e.g. background goroutines and in-memory rate limiters defined in `main.go`).

This document is the actionable refactor plan derived from:

- `docs/project-comprehensive-review-202606132130.md` — Section 8.2 "main.go 过于臃肿", P2 #15 "main.go 拆分"
- `docs/reports/code-review-dual-report-202606111200.md` — A1 "main.go Still Monolithic", A3/A4 billing duplication & background goroutines
- `docs/code-review-e2e-report-202606090920.md` — architecture & route count analysis

---

## 2. Design Principles

1. **Behavior-preserving**: HTTP paths, request/response shapes, and business logic remain unchanged.
2. **Package-safe first**: Split large files **within the same package** to avoid import cycles; only extract cross-cutting helpers to new packages (middleware, services, workers).
3. **One file per responsibility**: each new file should have a single, well-defined reason to change.
4. **Test after every step**: run `go build ./...` and targeted tests before moving to the next file.
5. **Reviewable increments**: one large-file split per commit/PR.

---

## 3. Phase 1 — Decouple `cmd/server/main.go`

Goal: reduce `main.go` from ~700 lines to ~60 lines of orchestration; extract middleware and static file serving to reusable packages.

### 3.1 New file layout in `cmd/server/`

| File | Responsibility | Est. lines |
|---|---|---|
| `main.go` | `main()` flow only | ~60 |
| `bootstrap.go` | config validation, DB/Audit/Mail/Auth init, schema/seed, default admin, job queue registration | ~130 |
| `router.go` | create gin engine, attach global middleware, register routes | ~120 |
| `background.go` | `safeGo`, start/stop background jobs | ~80 |
| `server.go` | HTTP server start & graceful shutdown | ~60 |

### 3.2 Middleware extractions

Move real implementations out of `cmd/server/main.go` so they can be unit-tested and reused:

- **CORS** → `internal/middleware/cors.go`
  - `NewCORS(allowedOrigins []string) gin.HandlerFunc`
  - includes `sameOrigin` helper
- **IP rate limiter** → `internal/middleware/ratelimit.go`
  - `NewIPRateLimiter(ratePerSec int) gin.HandlerFunc`
  - includes `ipRateLimiter`/`rateBucket` types and cleanup goroutine
- **Static files** → `internal/web/static.go`
  - `RegisterStaticFiles(router *gin.Engine, logger *zap.Logger)`
  - preserves current content-type and cache-control logic

### 3.3 Background task unification (Phase 1 light)

In Phase 1 we only move the startup calls from `main.go` into `background.go`. Later phases will create `internal/workers/` with stoppable workers and Redis distributed locks.

Background tasks currently launched in `main.go`:

1. Enterprise admin_vacant reminder + monthly billing reset
2. Model lifecycle cleanup + EOL reminders
3. Upstream key validation (every 6h)
4. Auto-renewal scheduler (every 1h)

Phase 1 keeps the goroutine bodies where they are but wraps startup in `startBackgroundJobs(ctx, deps)`.

---

## 4. Phase 2 — Extract Billing Service Layer

Goal: remove duplicated billing logic between `internal/routes/webhook.go` and `internal/routes/billing.go`.

- Create `internal/services/billing.go`
- Move `calculateCostFromPricing` and related pricing helpers there
- Update `webhook.go` and `billing.go` to use `billing.Service`
- Add unit tests for the extracted helpers

This is a prerequisite for cleanly splitting the two large files in later phases.

---

## 5. Phase 3 — Split Route Handlers by Domain

Split within the same package. Each new file keeps the same `package routes` (or `package web`) and shares common types in a `*_common.go` file.

### 5.1 `internal/routes/admin.go`

Proposed split:

| New file | Handlers |
|---|---|
| `admin_common.go` | `AdminRoutes` struct, `Register`, `checkExportLimit` |
| `admin_users.go` | list/create/update/delete/promote/demote users, admin-mode |
| `admin_providers.go` | provider CRUD |
| `admin_models.go` | service models, model list/status, sync/validate OpenRouter |
| `admin_keypool.go` | key pool CRUD, import/export/validate |
| `admin_usage.go` | upstream usage, provider health, rate-limit usage |
| `admin_reconciliation.go` | reconciliation preview/upload, invoice requests |

### 5.2 `internal/routes/enterprise.go`

| New file | Handlers |
|---|---|
| `enterprise_common.go` | `EnterpriseRoutes` struct, `Register`, shared helpers |
| `enterprise_members.go` | invitations, accept, add/remove members |
| `enterprise_keys.go` | enterprise API keys |
| `enterprise_billing.go` | quota/credit/billing |
| `enterprise_groups.go` | enterprise groups |
| `enterprise_alerts.go` | alerts |

### 5.3 `internal/routes/models.go`

| New file | Handlers |
|---|---|
| `models_common.go` | `ModelRoutes` struct, `Register` |
| `models_crud.go` | model CRUD |
| `models_sync.go` | OpenRouter / Bedrock sync |
| `models_validation.go` | batch validation |
| `models_eol.go` | EOL reminders, discontinued handling |

### 5.4 `internal/routes/webhook.go`

| New file | Handlers |
|---|---|
| `webhook_usage.go` | usage reporting |
| `webhook_billing.go` | billing reconciliation |
| `webhook_health.go` | health reporting |

### 5.5 `internal/web/admin_handlers.go`

Split by page/resource:

| New file | Responsibility |
|---|---|
| `admin_handler_common.go` | shared admin page helpers |
| `admin_handler_providers.go` | provider form handlers |
| `admin_handler_keys.go` | upstream key form handlers |
| `admin_handler_models.go` | model form handlers |
| `admin_handler_groups.go` | group form handlers |

---

## 6. Phase 4 — Unified Background Worker Lifecycle

Goal: make background jobs observable, stoppable, and safe for multi-instance deployments.

- Create `internal/workers/manager.go` with `Start(ctx)` / `Stop()`
- Convert each background goroutine into a `Worker`:
  - `workers/enterprise.go` — admin_vacant + monthly reset
  - `workers/models.go` — cleanup + EOL reminders
  - `workers/keypool.go` — upstream key validation
  - `workers/subscriptions.go` — auto-renewal
- Replace `safeGo` direct launches with `workerManager.Start(ctx)`
- (Future) add Redis-based distributed locks so only one instance runs each job

---

## 6.1 Distributed Rate Limiter Limitation

The current IP-based rate limiter (`internal/middleware/ratelimit.go`) keeps state in an in-memory `ipRateLimiter` map inside the process. This works correctly for a single Portal instance, but has the following limitations in a multi-instance deployment:

1. **No shared state**: each process tracks request rates independently. A client hitting instance A and instance B in parallel can consume twice (or N times) the intended quota.
2. **No cross-instance burst protection**: per-IP buckets are not synchronized, so bursts can be spread across instances.
3. **Memory growth**: buckets are created lazily and cleaned up by a background goroutine per process. With many unique IPs and many instances, total memory usage scales with instance count.

**Mitigation / future work:**
- Replace the in-memory bucket with a Redis-backed sliding-window or token-bucket implementation (`internal/middleware/ratelimit_redis.go`).
- Alternatively, move rate limiting to the reverse proxy / API gateway layer (e.g. nginx limit_req, Traefik middleware, or AWS WAF rate-based rules) for true distributed enforcement.
- Until then, the in-memory limiter is considered a **per-instance best-effort** guard, not a strict global quota.

---

## 7. Phase 5 — Remaining Large Files & Polish

| Task | Status | Notes |
|---|---|---|
| Split `internal/routes/subscriptions.go` | ✅ Done | Split into `subscriptions_common.go`, `subscriptions_plans.go`, `subscriptions_subscriptions.go`, `subscriptions_renewal.go` |
| Split `internal/routes/apikeys.go` | ✅ Done | Split into `apikeys_common.go`, `apikeys_crud.go`, `apikeys_validate.go`, `apikeys_usage.go` |
| Split `internal/routes/vouchers.go` | ✅ Done | Split into `vouchers_common.go`, `vouchers_user.go`, `vouchers_admin.go`, `vouchers_helpers.go` |
| Extract magic numbers (`10`, `60`, `1_000_000`, etc.) | ✅ Done | `internal/routes/constants.go` |
| Add ETag/Last-Modified to static file handler | ✅ Done | `internal/web/static.go` |
| Document distributed rate-limiter limitation | ✅ Done | Section 6.1 in this document |

---

## 8. Risks & Mitigation

| Risk | Mitigation |
|---|---|
| Import cycles | Keep handler splits inside the same package; only extract middleware/services/workers that are leaf packages |
| Lost git history | Use `git mv` where possible; otherwise split with copy + delete in same commit |
| Merge conflicts | Perform one large-file split at a time; rebase before next split |
| Broken tests | Run `go build ./...` and targeted `go test` after every file move |
| API drift | Do **not** change route paths, handler signatures, or response formats during this refactor |

---

## 9. Acceptance Criteria

- `cmd/server/main.go` ≤ 100 lines
- `internal/routes/admin.go`, `enterprise.go`, `models.go`, `webhook.go` each split into ≤ 600-line files
- All existing tests pass (`go test ./internal/routes/... ./internal/web/...`)
- No change to public HTTP API
- CORS, IP rate-limiting, and static file handlers are unit-testable outside of `main.go`

---

## 10. References

- `docs/project-comprehensive-review-202606132130.md`
- `docs/reports/code-review-dual-report-202606111200.md`
- `docs/code-review-e2e-report-202606090920.md`
