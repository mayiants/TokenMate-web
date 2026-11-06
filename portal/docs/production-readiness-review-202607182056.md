# Production Readiness Review — 2026-07-18

## Executive Summary

Both `ai-portal-go` and `ai-proxy-go` are architecturally sound with strong security fundamentals (parameterized SQL, HMAC webhooks, bcrypt passwords, transaction-based billing). However, there are several issues that should be addressed before production launch.

- **Portal**: 4 P0, 6 P1, 7 P2
- **Proxy**: 3 P0, 6 P1, 11 P2

---

## ai-portal-go

### P0 — Must Fix Before Launch

| # | Issue | File | Impact |
|---|-------|------|--------|
| 1 | **Payment double-processing race** — `completeOrder()` has no transaction; crash between status UPDATE and balance credit leaves money lost | `internal/routes/payment.go:738` | Financial loss |
| 2 | **Reset password lacks brute-force protection** — 6-digit code (1M combos) with no rate limit on `/api/auth/reset-password` | `internal/routes/auth.go:49` | Account takeover |
| 3 | **CORS trusts X-Forwarded-Host unconditionally** — direct exposure allows CORS bypass via header spoofing; no startup warning when `ALLOWED_ORIGINS` unset | `internal/middleware/cors.go` | Cross-origin attacks |
| 4 | **Verification code logged at INFO** — when SMTP misconfigured, actual codes appear in production logs | `internal/routes/auth.go:149` | Code exposure |

#### Fix Suggestions

**#1**: Wrap `completeOrder` in `db.WithTx()`, check `RowsAffected()` on status UPDATE (if 0 → already processed, return early).

**#2**: Add `r.bruteForceProtection()` middleware to the reset-password route.

**#3**: Only trust `X-Forwarded-Host` behind a known proxy; log warning at startup if `ALLOWED_ORIGINS` is empty.

**#4**: Change log level to DEBUG, or remove code value from log message entirely.

---

### P1 — Fix Soon After Launch

| # | Issue | File | Impact |
|---|-------|------|--------|
| 5 | Profile update placeholder overflow — `rune('0'+argIdx)` breaks at argIdx≥10 | `internal/routes/profile.go:105` | Latent bug |
| 6 | Enterprise credit no FOR UPDATE in batch path — relies on 110% buffer | `internal/routes/webhook_billing.go:156` | Overcharge possible |
| 7 | Queue processor goroutines never stop on shutdown — no context cancellation | `internal/routes/webhook_common.go:198` | Partial state on exit |
| 8 | N+1 Redis in `getActiveFreezeTotal` — individual GET per freeze key | `internal/routes/webhook_preconsume.go:290` | Redis latency spike |
| 9 | Billing callback endpoint has no auth — can cancel other users' orders via `status=cancel` | `internal/routes/payment.go:66` | Order manipulation |
| 10 | sendCode rate limit TOCTOU race — GET then INCR allows burst | `internal/routes/auth.go:112` | Rate limit bypass |

---

### P2 — Backlog

| # | Issue | File |
|---|-------|------|
| 11 | In-memory rate limiter not suitable for multi-instance | `internal/middleware/ratelimit.go` |
| 12 | Background jobs don't respect shutdown context | `cmd/server/background.go:84` |
| 13 | Unbounded Redis queue growth under backpressure | `webhook_usage.go`, `webhook_billing.go` |
| 14 | Health check doesn't verify Redis write capability | `cmd/server/router.go:76` |
| 15 | Verification code reuse between register and reset (same Redis key) | `internal/routes/auth.go` |
| 16 | Missing LIMIT on public service-models endpoint | `cmd/server/router.go:119` |
| 17 | JWT not invalidated on password change | `internal/routes/auth.go` (changePassword) |

---

### Positive Findings ✅

- SQL injection: All queries use parameterized placeholders
- Webhook auth: HMAC-SHA256 with 5-min timestamp window
- Password: bcrypt + server-side strength validation
- Billing transactions: FOR UPDATE row locking in processRecord/processBillingBatch
- Graceful shutdown: Signal handling, 10s timeout, DB close
- Config validation: JWT secret length, admin password blocklist, encryption key
- Panic recovery: gin.Recovery() + safeGo() wrapper

---

## ai-proxy-go

### P0 — Must Fix Before Launch

| # | Issue | File | Impact |
|---|-------|------|--------|
| 1 | **Unbounded response body read (OOM)** — `io.ReadAll(resp.Body)` in non-streaming dispatch without size limit; single upstream response can kill process | `internal/server/handlers_common.go:1126` | Process crash |
| 2 | **Streaming memory accumulation** — no total response size cap in chunk read loop; extended thinking output can exhaust memory | `internal/server/handlers_chat.go:830` | OOM under load |
| 3 | **Unauthenticated endpoints expose infrastructure** — `/api/status`, `/providers/stats`, `/models/list` expose key counts, provider topology, capacity info | `internal/server/server.go:222` | Information leak |

#### Fix Suggestions

**#1**: Add `io.LimitReader(resp.Body, 50<<20)` (50 MiB) before `io.ReadAll` in the inline dispatch path.

**#2**: Add a running byte counter; abort if cumulative stream bytes exceed threshold (configurable, default 100 MiB).

**#3**: Move stats/status endpoints behind admin auth or API key middleware. Keep only a minimal `/health` public.

---

### P1 — High Severity

| # | Issue | File | Impact |
|---|-------|------|--------|
| 4 | **Circuit breaker never recovers** — requires `threshold=5` successes in half-open but only allows `halfOpenMax=1` request per attempt | `pkg/circuitbreaker/breaker.go:89` | Provider permanently dead |
| 5 | **GeoIP cannot hot-reload** — `initDone=true` permanently; stale data after MaxMind weekly update | `pkg/geoip/geoip.go:38` | Geo accuracy degrades |
| 6 | **Usage buffer lost on shutdown** — `cancel()` before `srv.Shutdown()`; requests completing during drain call `Add()` after flush loop exits | `cmd/server/main.go:236` | Silent usage data loss |
| 7 | **Pricing cache falls back to defaults** — 60s TTL can expire between syncs, causing CalculateQuota to use inaccurate DefaultPricing | `pkg/billing/billing.go:155` | Billing jumps |
| 8 | **ReadTimeout 10s cuts off large uploads** — audio/embedding multipart bodies may take >10s to transmit | `internal/server/server.go:96` | Silent connection reset |
| 9 | **Streaming token accumulation** — `totalUsage.CompletionTokens += streamResp.Usage.CompletionTokens` per chunk; if provider sends cumulative usage in multiple chunks, tokens double-count | `internal/server/handlers_chat.go:937` | Overcharging |

---

### P2 — Medium Severity

| # | Issue | File |
|---|-------|------|
| 10 | No retry for non-streaming upstream transport errors | `internal/server/handlers_common.go:1101` |
| 11 | `SyncFromPortal` creates new HTTP client per call (no pooling) | `pkg/keypool/sync.go:203` |
| 12 | Auth cache (sync.Map) grows without bound under key scanning | `internal/auth/auth.go:276` |
| 13 | `handleWebhookSync` uses `context.Background()` not server context | `internal/server/admin_handlers.go:431` |
| 14 | Budget controller tracks spend in-memory only (resets on restart) | `pkg/routing/budget_controller.go` |
| 15 | `CalculateCost` vs `CalculateCostWithCache` duplication — inconsistent Claude billing | `pkg/billing/billing.go:35` |
| 16 | Non-streaming error body read without size limit | `internal/server/handlers_chat.go:755` |
| 17 | `/providers/stats` returns key-level detail without auth | `internal/server/handlers_status.go:134` |
| 18 | Health check reports Redis healthy without actually pinging | `internal/server/server.go:147` |
| 19 | Concurrent PreConsumeLocal + SettleLocal race on in-memory balance | `internal/auth/auth.go:414` |
| 20 | `SyncVersionFromPortal` also creates new client per call | `pkg/keypool/sync.go:127` |

---

### Positive Findings ✅

- Request-level context propagation with trace IDs
- Provider circuit breaker with auto-detection (though recovery logic needs fix)
- Key pool sync with version detection (avoids unnecessary full reloads)
- Proper streaming SSE protocol implementation
- Usage buffer with flush-on-shutdown (though timing needs fix)
- GeoIP lookup graceful degradation (empty string on failure)
- Audit logger fire-and-forget pattern (doesn't block hot path)

---

## Prioritized Action Plan

### Before Launch (P0)

1. Portal: Transaction-wrap `completeOrder` + check `RowsAffected`
2. Portal: Add brute-force protection to `/reset-password`
3. Portal: Remove verification code from INFO logs
4. Proxy: Add `LimitReader` to non-streaming response read
5. Proxy: Protect `/api/status` and `/providers/stats` endpoints
6. Proxy: Fix circuit breaker recovery (halfOpenMax ≥ threshold or threshold=1 in half-open)

### Week 1 Post-Launch (P1)

7. Portal: Fix CORS X-Forwarded-Host trust
8. Portal: Transaction for billing callback cancel
9. Proxy: Fix usage buffer shutdown ordering
10. Proxy: Increase ReadTimeout or use ReadHeaderTimeout
11. Proxy: Fix streaming token accumulation (only use last chunk's usage)
12. Proxy: Increase pricing cache TTL or use stale-while-revalidate

### Backlog (P2)

- Redis-based rate limiting for multi-instance portal
- Redis MGET for freeze totals
- Queue backpressure / length limits
- GeoIP hot-reload
- JWT invalidation on password change
- Budget controller persistence
- HTTP client pooling in key sync

---

## Review Metadata

- **Date**: 2026-07-18T20:56 UTC
- **Portal commit**: `2b5accb`
- **Proxy commit**: `67b3d20`
- **Reviewer**: Kiro CLI (automated code review)
- **Scope**: Security, data integrity, reliability, performance, operations
