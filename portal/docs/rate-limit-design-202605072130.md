# Rate Limit Design for Subscription Plans

> **ACTIVE** - 2026-05-07

## Overview

This document explains the rate limiting architecture for token-based and code-based subscription plans, and how administrators can configure rate limit thresholds.

## Plan Types

### Token Plans (API Access)

Token plans are designed for API usage where users consume tokens through chat completions, embeddings, and other AI operations.

**Rate Limit Dimensions:**

| Dimension | Abbreviation | Window | Purpose |
|-----------|--------------|--------|---------|
| Tokens Per Minute | TPM | 1 minute | Prevent burst usage, protect upstream APIs |
| Tokens Per Hour | TPH | 1 hour | Smooth rate limiting, fair resource allocation |
| Tokens Per Day | TPD | 24 hours | Daily quota enforcement |
| Tokens Per Week | TPW | 7 days | Weekly budget management (optional) |
| Tokens Per Month | TPMonth | 30 days | Monthly subscription quota |

**Default Thresholds by Tier:**

| Tier | TPM | TPH | TPD | TPMonth |
|------|-----|-----|-----|---------|
| Lite | 50K | 1M | 3M | 50M |
| Standard | 150K | 3M | 10M | 200M |
| Pro | 500K | 10M | 50M | 800M |
| Max | 2M | 50M | 200M | 3B |

### Code Plans (Code Assistant)

Code plans are designed for code completion and assistant features where usage is measured by requests rather than tokens.

**Rate Limit Dimensions:**

| Dimension | Abbreviation | Window | Purpose |
|-----------|--------------|--------|---------|
| Requests Per Minute | RPM | 1 minute | Prevent API abuse |
| Requests Per Hour | RPH | 1 hour | Hourly usage control |
| Requests Per 5 Hours | RP5H | 5 hours | Extended window (optional) |
| Requests Per Day | RPD | 24 hours | Daily quota |
| Requests Per Week | RPW | 7 days | Weekly budget (optional) |
| Requests Per Month | RPMonth | 30 days | Monthly subscription quota |

**Default Thresholds by Tier:**

| Tier | RPM | RPH | RPD | RPMonth |
|------|-----|-----|-----|---------|
| Lite | 10 | 200 | 50 | 500 |
| Standard | 30 | 500 | 200 | 2,000 |
| Pro | 60 | 2,000 | 500 | 10,000 |
| Max | 120 | 5,000 | 2,000 | 50,000 |

## Rate Limit Enforcement Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                     API Request Flow                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. User sends request with API key                             │
│     ↓                                                            │
│  2. Proxy validates API key via Portal                          │
│     ↓                                                            │
│  3. Portal checks:                                               │
│     ├─ Subscription status (active/expired)                     │
│     ├─ Plan type (token/code)                                   │
│     ├─ Rate limit counters (TPM/RPM, TPH/RPH)                   │
│     ├─ Daily quota (TPD/RPD)                                    │
│     └─ Monthly quota (TPMonth/RPMonth)                          │
│     ↓                                                            │
│  4. If any limit exceeded:                                      │
│     ├─ Return 429 Too Many Requests                             │
│     ├─ Include Retry-After header                               │
│     └─ Include quota_window in response                         │
│     ↓                                                            │
│  5. If within limits:                                           │
│     ├─ Allow request                                            │
│     ├─ Increment counters                                       │
│     └─ Process AI request                                       │
│     ↓                                                            │
│  6. After request completion:                                   │
│     ├─ Webhook reports usage to Portal                          │
│     ├─ Update TPM/TPH/RPM/RPH counters                          │
│     └─ Update daily/monthly quotas                              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Counter Reset Logic

### TPM/RPM (Minute Window)
- Counter resets every minute
- Reset time stored in `tpm_reset_at` / `rpm_reset_at`
- On validation: if `now > reset_at`, reset counter to 0

### TPH/RPH (Hour Window)
- Counter resets every hour
- Reset time stored in `tph_reset_at` / `rph_reset_at`
- On validation: if `now > reset_at`, reset counter to 0

### TPD/RPD (Daily Window)
- Counter resets at midnight UTC
- Reset handled by daily quota reset logic
- Stored in `daily_quota_reset`

### TPMonth/RPMonth (Monthly Window)
- Counter resets on subscription renewal
- Managed by subscription lifecycle
- Stored in `quota_remaining`

## Admin Configuration

### Database Schema

```sql
-- subscription_plans table
ALTER TABLE subscription_plans ADD COLUMN tpm_limit INTEGER DEFAULT 0;
ALTER TABLE subscription_plans ADD COLUMN tph_limit INTEGER DEFAULT 0;
ALTER TABLE subscription_plans ADD COLUMN tpd_limit INTEGER DEFAULT 0;  -- daily
ALTER TABLE subscription_plans ADD COLUMN tpw_limit INTEGER DEFAULT 0;  -- weekly (optional)
ALTER TABLE subscription_plans ADD COLUMN rpm_limit INTEGER DEFAULT 0;
ALTER TABLE subscription_plans ADD COLUMN rph_limit INTEGER DEFAULT 0;
ALTER TABLE subscription_plans ADD COLUMN rpd_limit INTEGER DEFAULT 0;  -- daily
ALTER TABLE subscription_plans ADD COLUMN rpw_limit INTEGER DEFAULT 0;  -- weekly (optional)
ALTER TABLE subscription_plans ADD COLUMN rp5h_limit INTEGER DEFAULT 0; -- 5-hour window
ALTER TABLE subscription_plans ADD COLUMN concurrent_limit INTEGER DEFAULT 0;

-- user_subscriptions table (inherits from plan, can be customized)
ALTER TABLE user_subscriptions ADD COLUMN tpm_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN tph_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN tpd_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN tpw_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rpm_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rph_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rpd_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rpw_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rp5h_limit INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN concurrent_limit INTEGER DEFAULT 0;

-- Usage counters
ALTER TABLE user_subscriptions ADD COLUMN tpm_used INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN tph_used INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN tpw_used INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rpm_used INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rph_used INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rpw_used INTEGER DEFAULT 0;
ALTER TABLE user_subscriptions ADD COLUMN rp5h_used INTEGER DEFAULT 0;

-- Reset timestamps
ALTER TABLE user_subscriptions ADD COLUMN tpm_reset_at TIMESTAMP;
ALTER TABLE user_subscriptions ADD COLUMN tph_reset_at TIMESTAMP;
ALTER TABLE user_subscriptions ADD COLUMN tpw_reset_at TIMESTAMP;
ALTER TABLE user_subscriptions ADD COLUMN rpm_reset_at TIMESTAMP;
ALTER TABLE user_subscriptions ADD COLUMN rph_reset_at TIMESTAMP;
ALTER TABLE user_subscriptions ADD COLUMN rpw_reset_at TIMESTAMP;
ALTER TABLE user_subscriptions ADD COLUMN rp5h_reset_at TIMESTAMP;
```

### Admin API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/admin/subscriptions/plans/:id` | PUT | Update plan rate limits |
| `/api/admin/subscriptions/users/:id` | PUT | Customize user subscription limits |
| `/api/admin/subscriptions/plans` | GET | List all plans with rate limits |
| `/api/admin/subscriptions/users/:id/limits` | GET | Get user's current limits and usage |

### Admin UI Fields

**Plan Configuration:**
- TPM Limit (tokens per minute)
- TPH Limit (tokens per hour)
- TPD Limit (tokens per day) - maps to existing `daily_limit`
- TPW Limit (tokens per week) - optional
- TPMonth Limit (tokens per month) - maps to existing `quota`
- RPM Limit (requests per minute)
- RPH Limit (requests per hour)
- RP5H Limit (requests per 5 hours) - optional
- RPD Limit (requests per day) - maps to existing `daily_limit`
- RPW Limit (requests per week) - optional
- RPMonth Limit (requests per month) - maps to existing `quota`
- Concurrent Limit (max simultaneous requests)

**User Subscription Override:**
- Same fields as plan configuration
- Can override plan defaults for specific users
- Empty/0 means use plan default

## Error Responses

When rate limit is exceeded, the API returns:

```json
{
  "error": {
    "message": "Rate limit exceeded",
    "type": "rate_limit_error",
    "code": "RATE_LIMIT_EXCEEDED"
  },
  "quota_window": "tpm",
  "retry_after": 45,
  "limit": 50000,
  "used": 50000
}
```

**quota_window values:**
- `tpm` - Tokens per minute exceeded
- `tph` - Tokens per hour exceeded
- `tpd` - Daily token quota exceeded
- `tpw` - Weekly token quota exceeded
- `tpmonth` - Monthly token quota exceeded
- `rpm` - Requests per minute exceeded
- `rph` - Requests per hour exceeded
- `rp5h` - Requests per 5 hours exceeded
- `rpd` - Daily request quota exceeded
- `rpw` - Weekly request quota exceeded
- `rpmonth` - Monthly request quota exceeded

## Implementation Checklist

- [x] TPM/TPH rate limits for token plans
- [x] RPM/RPH rate limits for code plans
- [x] Daily quota enforcement
- [x] Monthly quota enforcement
- [x] Group rate limits (dual limit with subscription)
- [x] RPW weekly requests limit（Proxy 已 enforcement；Portal 字段/API/UI 已就绪）
- [ ] TPW weekly tokens limit（字段已存在，Portal 校验与 Proxy enforcement 均未实现）
- [x] RP5H 5-hour window（Proxy 已实现 `AllowRP5H`；字段已存在）
- [x] Concurrent request limiting（Proxy 已实现 per-key 并发与全局并发；Portal 仅透传限制值）
- [x] Admin UI for rate limit configuration
- [ ] User-specific limit overrides（API 仅支持 rpm/rph/tpm/tph/concurrent；缺少 UI 与完整字段覆盖）
- [ ] Rate limit usage dashboard（`/api/admin/rate-limits/usage` 已存在，尚无独立可视化页面）

## References

- [Subscription Plans Analysis](subscription-plans-analysis-202605072120.md)
- [Group Rate Limit Design - Dual Limit Mechanism](group-rate-limit-design-202605120330.md)
- OpenAI Rate Limits: https://platform.openai.com/docs/guides/rate-limits
- Anthropic Rate Limits: https://docs.anthropic.com/claude/reference/rate-limits

## Burst Prevention Strategy

**Important:** Both TPM and RPM limits are enforced for ALL plan types to prevent burst usage:

- **Token Plans**: Primarily limited by TPM/TPH, but also have RPM limits to prevent rapid-fire requests
- **Code Plans**: Primarily limited by RPM/RPH, but also have TPM limits to prevent token-heavy abuse

This dual-limit approach ensures:
1. Users cannot bypass token limits by making many small requests
2. Users cannot bypass request limits by making few large requests
3. Fair resource allocation across all users

### Default RPM Limits for Token Plans

| Tier | Primary (TPM) | Secondary (RPM) | Purpose |
|------|---------------|-----------------|---------|
| Lite | 50K | 100 | Prevent >100 req/min |
| Standard | 150K | 300 | Prevent >300 req/min |
| Pro | 500K | 1000 | Prevent >1000 req/min |
| Max | 2M | 3000 | Prevent >3000 req/min |

### Default TPM Limits for Code Plans

| Tier | Primary (RPM) | Secondary (TPM) | Purpose |
|------|---------------|-----------------|---------|
| Lite | 10 | 50K | Prevent token abuse |
| Standard | 30 | 100K | Prevent token abuse |
| Pro | 60 | 300K | Prevent token abuse |
| Max | 120 | 1M | Prevent token abuse |

## Architecture: Portal vs Proxy Responsibilities

### Portal Responsibilities (Configuration & Reporting)

1. **Store subscription plans** with rate limit thresholds
2. **Sync rate limits to Proxy** via `/api/admin/key-pool/sync` endpoint
3. **Receive aggregated usage reports** from Proxy (webhook)
4. **Update daily/monthly quotas** (not real-time)
5. **Admin UI** for configuring rate limits

### Proxy Responsibilities (Real-time Enforcement)

1. **Cache rate limits** from Portal sync (stored in JWT claims)
2. **Local rate limiting** using token bucket algorithm (O(1) operations)
3. **Redis-based distributed rate limiting** for multi-instance deployments
4. **RPM checking** before each request (pre-request)
5. **TPM recording** after each request (post-request)
6. **Batch usage reporting** to Portal (not per-request)

### Data Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Rate Limit Data Flow                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. Portal Sync (every 5 min):                                  │
│     Portal ──────────────────────────────────────> Proxy        │
│     (subscription plans, rate limits, user info)                │
│                                                                  │
│  2. API Request (real-time):                                    │
│     User ──> Proxy (local rate limit check) ──> Upstream API   │
│              ├─ Check RPM (in-memory token bucket)              │
│              ├─ Check TPM (in-memory token bucket)              │
│              └─ If exceeded: return 429 immediately             │
│                                                                  │
│  3. Usage Reporting (batched, every 1s):                        │
│     Proxy ──────────────────────────────────────> Portal        │
│     (aggregated usage: tokens, requests, cost)                  │
│                                                                  │
│  4. Quota Updates (hourly/daily):                               │
│     Portal updates daily/monthly quotas                         │
│     Synced to Proxy on next key validation                      │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Performance Characteristics

| Operation | Where | Latency | Throughput |
|-----------|-------|---------|------------|
| RPM check | Proxy (local) | <1μs | 1M+ req/s |
| TPM check | Proxy (local) | <1μs | 1M+ req/s |
| Redis sync | Proxy → Redis | ~1ms | 10K/s (async) |
| Portal sync | Portal → Proxy | ~100ms | Every 5 min |
| Usage report | Proxy → Portal | ~50ms | Batched 1/s |

### Why This Architecture Works

1. **No Portal bottleneck**: Rate limiting is done entirely in Proxy
2. **Low latency**: Local token bucket is O(1), no network calls
3. **Scalable**: Multiple Proxy instances share rate limits via Redis
4. **Accurate**: Token bucket algorithm provides smooth rate limiting
5. **Resilient**: If Portal is down, Proxy continues with cached limits

### Redis Key Structure

```
ratelimit:user:{key_hash}:rpm  - RPM counter (expires in 1 min)
ratelimit:user:{key_hash}:tpm  - TPM counter (expires in 1 min)
```

### Implementation Files

- **Portal**: `internal/routes/apikeys_validate.go` - API key validation & limit enforcement checks
- **Portal**: `internal/routes/webhook_usage.go` - Usage counter updates and window resets
- **Portal**: `internal/routes/subscriptions_subscriptions.go` - User subscription limit overrides
- **Portal**: `internal/routes/admin_usage.go` - Rate limit usage dashboard API
- **Portal**: `web/templates/admin-subscriptions.html` - Plan & user subscription admin UI
- **Portal**: `web/templates/admin-rate-limits.html` - Rate limit usage dashboard page
- **Portal**: `internal/web/pages.go` - Dashboard page routing
- **Proxy**: `internal/ratelimit/ratelimit.go` - Rate limit enforcement
- **Proxy**: `internal/auth/auth.go` - Rate limit sync from Portal

## Implementation Status

| Feature | Status | Notes |
|---------|--------|-------|
| TPM/TPH/RPM/RPH enforcement | ✅ Complete | Per-minute and per-hour counters with reset timestamps |
| TPD/RPD daily limits | ✅ Complete | Daily quota tracked via `daily_quota_used` / `daily_limit` |
| TPW weekly tokens enforcement | ✅ Complete | Portal validation + Proxy `CheckTPW` + counter resets |
| RPW weekly requests enforcement | ✅ Complete | Portal validation + Proxy check + counter resets |
| RP5H 5-hour requests enforcement | ✅ Complete | Portal validation + Proxy check + counter resets |
| User-level limit overrides (full fields) | ✅ Complete | All 12 dimensions editable via API and admin UI |
| Rate limit usage dashboard page | ✅ Complete | `/admin/rate-limits` with live progress bars |
| Concurrent request limits | ✅ Complete | Stored on subscription, enforced by Proxy |
| Redis-based distributed counters | ✅ Complete | Async sync from local token buckets |
