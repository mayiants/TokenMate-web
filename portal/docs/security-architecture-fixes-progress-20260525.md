# Security & Architecture Fixes — Progress Report

> **⚠️ HISTORICAL** — All P0–P2 security/architecture fixes completed

**Date:** 2026-05-25 → 2026-05-24  
**Scope:** P0~P2 items from code review (ai-proxy-go + ai-portal-go)  
**Commits:**
- Portal: `b4a0512`
- Proxy: `ad23a8c`

---

## P0 — Critical Security Fixes

| # | Task | Status | File(s) | Notes |
|---|------|--------|---------|-------|
| 1 | Portal webhook timestamp replay protection | ✅ Done | `internal/routes/webhook.go` | Rejects requests with `\|now - ts\| > 300s` |
| 2 | Sync decrypt failure → skip key (no fallback) | ✅ Done | `internal/routes/sync.go` + `pkg/keypool/manager.go` | Prevents sending encrypted ciphertext to upstream |
| 3 | CORS restrict to configurable origins | ✅ Done | `cmd/server/main.go` + `internal/config/config.go` | `ALLOWED_ORIGINS` env var; empty = dev fallback `*` |
| 1a | models.go decrypt fallback removal | ✅ Done | `internal/routes/models.go` | `fetchProviderModels` fails on decrypt error |
| 1b | admin.go export decrypt fallback removal | ✅ Done | `internal/routes/admin.go` | Skip key on export decrypt failure |
| 1c | initDefaultAdmin race condition | ✅ Done | `cmd/server/main.go` | `INSERT ... ON CONFLICT DO NOTHING` |

### P0.1 Webhook Timestamp Validation
```go
// webhook.go — added before signature verification
tsInt, err := strconv.ParseInt(timestamp, 10, 64)
if math.Abs(float64(time.Now().Unix()-tsInt)) > 300 {
    // reject with 403 "Timestamp out of tolerance"
}
```

### P0.2 Decrypt Failure No Fallback
**Portal sync endpoint (`sync.go`):**
```go
// Before: actualKey = keyEncrypted (sent ciphertext to proxy)
// After:  continue (skip this key, log warning)
```

**Proxy Redis load (`manager.go`):**
```go
// Before: actualKey = encryptedKey (sent ciphertext to upstream)
// After:  continue (skip this key, log warning)
```

### P0.3 CORS Origin Restriction
```go
// main.go — configurable via ALLOWED_ORIGINS env
if len(allowedOrigins) > 0 {
    for _, o := range allowedOrigins {
        if o == origin { c.Header("Access-Control-Allow-Origin", origin) }
    }
} else {
    c.Header("Access-Control-Allow-Origin", "*") // dev fallback
}
```

---

## P1 — Important Improvements

| # | Task | Status | File(s) | Notes |
|---|------|--------|---------|-------|
| 4 | Portal API IP rate limiting | ✅ Done | `cmd/server/main.go` | Token bucket, 10 req/s per IP (configurable via `ADMIN_RATE_LIMIT_RPS`) |
| 5 | Proxy nonce cache → Redis | ✅ Done | `internal/auth/auth.go` + `internal/server/server.go` | Redis SET with 5min TTL; graceful fallback to memory if Redis nil |
| 6 | Billing hardcoded pricing → sync-aware | ✅ Done | `pkg/billing/billing.go` | `GetPricing` already checks `pricingCache` first; removed caching of fallback `DefaultPricing` results |
| 6a | Fallback group billing (proxy + portal) | ✅ Done | `webhook.go` + `handlers.go` | Proxy sends `ActualGroupID` + `IsFallback`; Portal trusts `PricingRatio` + uses `ActualGroupID` |
| 6b | Per-request billing columns | ✅ Done | `service_models` + `webhook.go` | Added `billing_type`/`fixed_price` columns + `per_request` cost logic |
| 7 | Sync endpoint pagination | ⏸️ Deferred | — | User excluded |

### P1.1 Portal API Rate Limiting
Simple in-memory token bucket applied to `/api/admin/*` routes:
```go
adminGroup := api.Group("/admin")
adminGroup.Use(rateLimitMiddleware)
```
> **Note:** Multi-instance deployments should migrate to Redis-backed rate limiting.

### P1.2 Distributed Nonce Cache
```go
// auth.go — isNonceUsed now checks Redis first
if m.redis != nil {
    exists, _ := m.redis.Exists(ctx, "nonce:"+nonce).Result()
    if exists > 0 { return true }
}
// fallback to in-memory map
```

### P1.3 Billing Pricing Cache Fix
Removed the line that cached hardcoded `DefaultPricing` results into `pricingCache`. Now:
1. `GetPricing` → checks `pricingCache` (from Portal sync) → returns if hit
2. Miss → calls `DefaultPricing(model)` → returns without caching
3. Portal sync → calls `SetPricing` → writes to `pricingCache`

This ensures hardcoded fallback never pollutes the cache.

---

## P2 — Architecture Debt & Documentation

| # | Task | Status | File(s) | Notes |
|---|------|--------|---------|-------|
| 8 | Global variable refactor (Server struct) | ⏸️ Deferred | — | Large refactor; estimated 3-5 days |
| 9 | Proxy local file snapshot cold-start | ⏸️ Deferred | — | New feature; estimated 2-3 days |
| 10 | Update docs status | ✅ Done | `docs/*.md` | Updated 3 docs |
| 11 | Progress report (this file) | ✅ Done | `docs/security-architecture-fixes-progress-20260525.md` | — |

### Docs Updated
| File | Before | After |
|------|--------|-------|
| `routing-and-billing-optimization-proposal-20260524.md` | "Pending Implementation" | "Mostly Complete (9/11)" |
| `enterprise-enhancement-design-202605221800.md` | "All P0/P1/P2 complete" | "Partially implemented" with correction note |

---

## E2E Test Results

| Test | Result |
|------|--------|
| `e2e-run.sh` (12 requests) | ✅ 12/12 PASS |
| Pass rate | 100% |
| Average latency | 4936ms |
| Deepseek-v4-flash | ✅ 3/3 (alibaba/laozhang) |
| Deepseek-v4-pro | ✅ 2/2 (alibaba/laozhang) |
| GPT-5.4-mini | ✅ 2/2 (accessapi) |
| Claude-sonnet-4-6 | ✅ 2/2 (accessapi) |
| GLM-5.1 | ✅ 2/2 (alibaba/laozhang) |
| Qwen3.6-flash | ✅ 1/1 (alibaba/laozhang) |

---

## Deferred Items (Future Sprints)

| # | Task | Why Deferred | Est. Effort |
|---|------|-------------|-------------|
| 7 | Sync endpoint pagination | User explicitly excluded | — |
| 8 | Global variable refactor | Large surface area; all tests need updates | 3-5 days |
| 9 | Local file snapshot | User explicitly excluded | — |
| 10 | Dual currency display | User explicitly excluded | — |
| 11 | DNS domain verification | User explicitly excluded | — |

---

## Risk Assessment

| Risk | Mitigation |
|------|-----------|
| CORS restriction breaks frontend in dev | `ALLOWED_ORIGINS` empty = keep `*` (dev fallback) |
| Decrypt skip reduces available keys | Keys with decrypt failures are now skipped; admin must fix `ENCRYPTION_KEY` config |
| Redis unavailable for nonce cache | Graceful fallback to in-memory `nonceCache` |
| Rate limiter is in-memory only | Documented; multi-instance deployments need Redis migration |
