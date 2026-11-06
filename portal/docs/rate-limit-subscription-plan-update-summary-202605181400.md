# Subscription Plan Rate Limit Update Summary

> **ACTIVE** - 2026-05-18

## Changes Made

### 1. Admin UI - Subscription Plans

**Removed:**
- "从模板创建" (Create from template) button from `/admin/subscriptions`

**Added to Create/Edit Plan Forms:**
- **Code Plans**: RPH, RP5H, RPW, Concurrent Limit
- **Token Plans**: TPH, TPD, TPW, Concurrent Limit
- Fields are conditionally displayed based on `plan_type` selection
- All rate limit fields default to `0` (unlimited)

**Files Modified:**
- `web/templates/admin-subscriptions.html`
- `web/templates/partials/plan-detail.html`

### 2. Backend - Plan CRUD

**Portal (`ai-portal-go`):**
- `internal/web/admin_handlers.go`: `HandleCreatePlan`, `HandleUpdatePlan`, `HandleGetPlanDetail`
- `internal/routes/subscriptions.go`: `createPlan`, `updatePlan`
- Added fields: `rp5h_limit`, `rpd_limit`, `rpw_limit`, `tpd_limit`, `tpw_limit`
- Retained fields: `rph_limit`, `tph_limit`, `concurrent_limit`, `rpm_limit`, `tpm_limit` (API-level)

**Bug Fixed:**
- Empty string form values caused `invalid input syntax for type integer: ""` SQL error
- Added `defaultIfEmpty(s, fallback)` helper to default all integer fields to `"0"` when missing

### 3. Purchase Flow - Rate Limit Propagation

**Portal (`ai-portal-go`):**
- `internal/routes/subscriptions.go`: `purchasePlan` now reads all rate limits from `subscription_plans` and stores them in order metadata
- `internal/routes/payment.go`: `activateSubscription` copies rate limits from metadata into `user_subscriptions` on payment completion

### 4. Portal → Proxy Rate Limit Passthrough

**Portal (`ai-portal-go`):**
- `internal/routes/apikeys.go`: Key validation query now selects:
  - `s.concurrent_limit`, `s.rp5h_limit`, `s.rpd_limit`, `s.rpw_limit`, `s.tpd_limit`, `s.tpw_limit`
- `validateKeyResponse` struct extended with new fields
- Returns new rate limits in JSON response to Proxy

**Proxy (`ai-proxy-go`):**
- `internal/auth/auth.go`:
  - `portalValidationResult` struct extended with new fields
  - `Claims` struct extended with new fields
  - Auth middleware sets new context values:
    - `sub_concurrent_limit`
    - `sub_rp5h_limit`
    - `sub_rpd_limit`
    - `sub_rpw_limit`
    - `sub_tpd_limit`
    - `sub_tpw_limit`

### 5. Database Migration

Existing migration `migrations/202605071910_extended_rate_limits.sql` already adds:
- `subscription_plans`: `tpd_limit`, `tpw_limit`, `rpd_limit`, `rpw_limit`, `rp5h_limit`
- `user_subscriptions`: `tpd_limit`, `tpw_limit`, `rpd_limit`, `rpw_limit`, `rp5h_limit`, plus usage counters and reset timestamps

## Field Mapping by Plan Type

| Field | Code Plan | Token Plan | Purpose |
|-------|-----------|------------|---------|
| `rph_limit` | ✅ Primary | ❌ | Requests per hour |
| `rp5h_limit` | ✅ Primary | ❌ | Requests per 5 hours |
| `rpw_limit` | ✅ Primary | ❌ | Requests per week |
| `tph_limit` | ❌ | ✅ Primary | Tokens per hour |
| `tpd_limit` | ❌ | ✅ Primary | Tokens per day |
| `tpw_limit` | ❌ | ✅ Primary | Tokens per week |
| `concurrent_limit` | ✅ Shared | ✅ Shared | Max concurrent requests |
| `rpm_limit` | ❌ Stored | ❌ Stored | Not shown in UI (legacy) |
| `tpm_limit` | ❌ Stored | ❌ Stored | Not shown in UI (legacy) |

## Effective Limit Logic (Proxy)

Proxy enforces the **stricter (smaller)** non-zero limit between subscription and group:

```
effectiveRPH  = min(sub_rph_limit,  group_rph)
effectiveRP5H = min(sub_rp5h_limit, group_rp5h)
effectiveRPW  = min(sub_rpw_limit,  group_rpw)
effectiveTPH  = min(sub_tph_limit,  group_tph)
effectiveTPD  = min(sub_tpd_limit,  group_tpd)
effectiveTPW  = min(sub_tpw_limit,  group_tpw)
```

If both are `0`, no limit is enforced.

## Testing Results

- ✅ Build passes: `go build ./...`
- ✅ Tests pass: `go test ./internal/routes/... ./internal/web/...`
- ✅ Plan creation (Token) with partial form data succeeds
- ✅ Plan creation (Code) with partial form data succeeds
- ✅ Plan edit loads and saves all rate limit fields
- ✅ Database stores correct values

## Git Commits

**Portal:**
- `73f7258` - feat(subscriptions): add rate limit fields to plan forms and proxy passthrough
- `16704e6` - fix(admin): default empty rate limit fields to 0 in plan create/update handlers
- `e83cbf4` - fix(admin): default all integer plan fields to avoid empty-string SQL errors

**Proxy:**
- `c289b74` - feat(auth): add new subscription rate limit fields to proxy claims and context
