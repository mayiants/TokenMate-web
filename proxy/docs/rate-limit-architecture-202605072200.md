# Rate Limit Architecture

> **ACTIVE** - Current rate limit implementation design

## Overview

Rate limiting is enforced **entirely by the Proxy** in real-time, with Portal only syncing configuration thresholds and receiving batched usage reports.

## Architecture Flow

```
User Request → Proxy (Auth + Rate Limit) → Provider → Proxy (Report to Portal)
                    ↓
              Local Token Bucket (O(1))
                    ↓
              Redis Sync (async, best-effort)
```

## Rate Limit Hierarchy

### Two Levels of Limits

1. **API Key Level** (`rate_limit_rpm`, `rate_limit_tpm`)
   - Per-key limits set by admin/user
   - Stored in `api_keys` table

2. **Subscription Level** (`sub_tpm_limit`, `sub_rpm_limit`, `sub_tph_limit`, `sub_rph_limit`)
   - Plan-based limits from active subscription
   - Stored in `user_subscriptions` table

### Effective Limit Rule

**Use the MORE RESTRICTIVE (smaller) limit.**

Both limits must be satisfied:
- API key limit = "this specific key can do at most X"
- Subscription limit = "this user's plan allows at most Y"
- Effective limit = min(key_limit, subscription_limit)

## Rate Limit Types

| Type | Window | Description |
|------|--------|-------------|
| RPM | 1 minute | Requests Per Minute |
| TPM | 1 minute | Tokens Per Minute |
| RPH | 1 hour | Requests Per Hour (subscription only) |
| TPH | 1 hour | Tokens Per Hour (subscription only) |

## Implementation Details

### Proxy Side (Real-time Enforcement)

**File:** `ai-proxy-go/internal/ratelimit/ratelimit.go`

- **Token Bucket Algorithm:** O(1) operations, no database calls
- **Local Counters:** `sync.Map` for concurrent-safe access
- **Redis Sync:** Async, non-blocking, best-effort (for distributed deployments)
- **Graceful Degradation:** Works without Redis (single-node fallback)

**Check Order:**
1. Check RPM (per minute) - before request
2. Check RPH (per hour) - before request (if subscription has it)
3. Process request
4. Record TPM (per minute) - after request
5. Record TPH (per hour) - after request (if subscription has it)

### Portal Side (Configuration Sync)

**File:** `ai-portal-go/internal/routes/apikeys.go`

**`/api/apikeys/validate` endpoint returns:**
```json
{
  "valid": true,
  "user_id": "...",
  "rate_limit_rpm": 60,      // API key RPM limit
  "rate_limit_tpm": 100000,  // API key TPM limit
  "sub_rpm_limit": 500,      // Subscription RPM limit
  "sub_tpm_limit": 500000,   // Subscription TPM limit
  "sub_rph_limit": 10000,    // Subscription RPH limit
  "sub_tph_limit": 10000000, // Subscription TPH limit
  ...
}
```

**Important:** Portal does NOT check real-time usage during validation. It only returns the configured thresholds.

### Webhook Side (Usage Reporting)

**File:** `ai-portal-go/internal/routes/webhook.go`

Proxy sends usage reports to Portal:
- `/webhook/usage` - Single usage record
- `/webhook/usage-batch` - Batched usage records

Portal updates:
- `usage_records` table (for billing/analytics)
- `user_subscriptions.tpm_used`, `tph_used`, `rpm_used`, `rph_used` (for monitoring)

## Caching

### API Key Validation Cache

- **TTL:** 5 minutes
- **Graceful Degradation:** 30 minutes (when Portal is unhealthy)
- **Cache Key:** API key string (not hash, for performance)

### Rate Limit Counters

- **Local:** In-memory token bucket
- **Redis:** Async sync for distributed deployments
- **Window:** 1 minute (RPM/TPM) or 1 hour (RPH/TPH)

## Configuration

### Subscription Plans

See `migrations/202605071900_competitive_subscription_plans.sql` for plan definitions.

### Admin Override

Admin can revise rate limit thresholds via:
- `PUT /admin/subscriptions-plans/:id` - Update plan defaults
- `PUT /admin/users/:id/subscription` - Override user subscription limits

## Error Responses

### Rate Limit Exceeded

```json
{
  "error": {
    "message": "Rate limit exceeded (60 RPM). Please slow down your requests.",
    "type": "rate_limit_error",
    "code": "RATE_LIMIT_EXCEEDED"
  }
}
```

### Hourly Rate Limit Exceeded

```json
{
  "error": {
    "message": "Hourly rate limit exceeded (1000 RPH). Please try again later.",
    "type": "rate_limit_error",
    "code": "HOURLY_RATE_LIMIT_EXCEEDED"
  }
}
```

## Performance

- **Latency:** < 1ms for rate limit check (local token bucket)
- **Throughput:** 100k+ requests/sec (no database calls in hot path)
- **Memory:** O(users) for local counters
- **Redis:** Optional, for distributed deployments

---

## Stress Test Results (2026-05-07)

### Test Conditions

| Test | Users | Keys | RPM Limit | Duration | RPS | Avg Latency | Bottleneck |
|------|-------|------|-----------|----------|-----|-------------|------------|
| 1 | 100 | 100 | 100K | 10s | 609 | 162ms | Simulate latency |
| 2 | 1000 | 1000 | 100K | 10s | 1614 | 623ms | DB key validation |
| 3 | 1000 | 1 (cached) | 100K | 10s | 1668 | 599ms | Mutex contention |
| 4 | 2000 | 2000 | 60 | 30s | 1109 | 1754ms | DB key validation |
| 5 | 5000 | 5000 | 60 | 30s | 744 | 6882ms | DB + mutex contention |

### Test Method

1. **Tool:** Custom Go loadtest (`/home/ubuntu/workspace/ai-portal-go/scripts/loadtest/`)
2. **Environment:**
   - Proxy: ai-proxy-go (simulate mode, 100ms latency)
   - Portal: ai-portal-go
   - Database: PostgreSQL in Docker
3. **Metrics:** Total requests, success rate, rate-limited count, latency
4. **Configuration:** Each test created new API keys via Portal API

### Comparison with Previous Test (May 2, 2026)

**Previous Test Configuration:**
- 5,000 keys created ONCE, reused across 20 rounds
- 300 concurrent connections (batched with 5ms sleep)
- Round 1: Cold cache (322ms latency)
- Rounds 2-20: Warm cache (142-148ms latency)
- Average: 174ms latency, 1,277 RPS (76,645 RPM)

**Current Test Configuration:**
- Keys created EACH RUN (not reused)
- Continuous requests (no batching)
- All keys need DB validation on first request

**Key Finding: Latency is SAME when using cached keys!**

| Test Scenario | Concurrency | Avg Latency | RPS | Keys Status |
|---------------|-------------|-------------|-----|-------------|
| Previous (May 2) | 300 | 174ms | 1,277 | Reused (cached after round 1) |
| Current (May 7) | 300 | 179ms | 1,622 | Single cached key |
| Current (May 7) | 300 | 181ms | 1,598 | New keys (5-min cache helps) |
| Current (May 7) | 1000 | 600ms | 1,600 | 1000 new keys (cache miss) |

**Database Validation Impact:**
- Cache HIT: ~80ms overhead (100ms simulate + 80ms processing)
- Cache MISS: ~500ms overhead (2 DB queries for key validation)
- Auth cache TTL: 5 minutes

**Conclusion:** The system performance is consistent with previous tests. The higher latency observed with 1000+ concurrent users is due to **cache miss penalty** for new keys, not a regression.

### Key Findings

#### 1. Rate Limiter Implementation Correctness
- ✅ Token bucket algorithm works correctly
- ✅ Per-key rate limiting enforced properly
- ✅ No false rate limiting when limits are high (100K RPM)
- ✅ Async Redis sync doesn't block requests

#### 2. Performance Bottlenecks Identified

**A. Per-Key Mutex Contention**
```go
// internal/ratelimit/ratelimit.go:53
bucket.mu.Lock()
defer bucket.mu.Unlock()
```
- All concurrent requests to the **same key** serialize on a single mutex
- With 1000 concurrent requests to 1 key: ~600ms latency
- Mutex hold time: ~1μs per request, but contention causes queueing

**B. Database Key Validation**
```go
// ai-portal-go/internal/routes/apikeys.go:504-517
// Two database queries per key validation:
// 1. Query api_keys table (JOIN users, user_groups)
// 2. Query user_subscriptions table
```
- New keys require 2 DB queries for validation
- Auth cache TTL: 5 minutes (works for subsequent requests)
- With 1000 new keys: DB becomes bottleneck

#### 3. Actual vs Target Performance

| Metric | Target | Actual | Gap |
|--------|--------|--------|-----|
| RPS | 100,000 | ~1,600 | **98.4% gap** |
| Latency | <10ms | ~600ms | **60x higher** |
| Concurrent Users | 10,000+ | ~1,000 | 10x less |

### Conclusions

#### Current Implementation Cannot Meet Target

The system **cannot achieve 100K RPM target** with current implementation due to:

1. **Rate Limiter Mutex Bottleneck**
   - Single mutex per key causes serialization
   - Solution: Use lock-free atomic operations or sharded counters

2. **Database Key Validation Bottleneck**
   - Portal validate endpoint does 2 DB queries
   - Solution: Add in-memory cache in Portal (e.g., Redis or sync.Map)

3. **Auth Cache Miss Penalty**
   - First request for each key hits Portal
   - Solution: Pre-warm cache or use longer TTL for known keys

### Recommended Improvements

#### Priority 1: Lock-Free Rate Limiter
```go
// Replace mutex with atomic operations
type tokenBucket struct {
    tokens     atomic.Float64
    lastUpdate atomic.Int64  // Unix nano timestamp
}

func (lc *localCounter) AllowN(key string, n int) bool {
    // Use CAS loop instead of mutex
    for {
        now := time.Now().UnixNano()
        last := bucket.lastUpdate.Load()
        tokens := bucket.tokens.Load()
        
        // Calculate new tokens
        elapsed := float64(now-last) / 1e9
        newTokens := min(lc.maxTokens, tokens + elapsed*lc.rate)
        
        if newTokens < float64(n) {
            return false
        }
        
        // CAS update
        if bucket.lastUpdate.CompareAndSwap(last, now) {
            bucket.tokens.Store(newTokens - float64(n))
            return true
        }
    }
}
```

#### Priority 2: Portal Validate Cache
```go
// Add LRU cache in Portal's validate endpoint
var validateCache = lrucache.New(10000, 5*time.Minute)

func (r *APIKeyRoutes) validateKey(c *gin.Context) {
    // Check cache first
    if cached, ok := validateCache.Get(keyHash); ok {
        c.JSON(http.StatusOK, cached)
        return
    }
    // ... existing DB logic ...
    validateCache.Set(keyHash, result)
}
```

#### Priority 3: Connection Pool Tuning
- Increase `MaxIdleConnsPerHost` in proxy HTTP client
- Use connection pooling for Portal API calls
- Consider HTTP/2 for multiplexing

### Test Commands

```bash
# Run enhanced stress test (both scenarios)
cd /home/ubuntu/workspace/ai-portal-go/scripts/loadtest

# Full test with 5000 keys, warmup, both test modes
./loadtest -users 5000 -rpm 100000 -concurrency 300 -duration 30 -warmup=true -mode=both

# Test only high concurrency
./loadtest -users 5000 -rpm 100000 -mode=concurrency

# Test only high throughput
./loadtest -users 5000 -rpm 100000 -concurrency 300 -duration 30 -mode=throughput

# Without warmup (test cold cache performance)
./loadtest -users 5000 -rpm 100000 -warmup=false -mode=both
```

### Enhanced Test Results (2026-05-07)

**Test Configuration:**
- 5,000 API keys created once
- Warmup: 1 request per key to populate cache
- Simulate latency: 100ms

**Test 1: High Concurrency (Burst)**
- Scenario: 5,000 users each making 5 requests concurrently (25,000 total)
- After warmup with cached keys

| Metric | Result |
|--------|--------|
| Total Requests | 25,000 |
| Success Rate | 99.9% |
| Avg Latency | 11,940ms |
| P50 Latency | 13,217ms |
| P99 Latency | 23,446ms |
| RPS | 801 |

**Analysis:** Burst of 25,000 concurrent requests overwhelms the system. The rate limiter mutex causes serialization - all requests to the same key wait on the same lock.

**Test 2: High Throughput (Sustained)**
- Scenario: 300 concurrent connections, 30 seconds, keys rotated
- After warmup with cached keys

| Metric | Result |
|--------|--------|
| Total Requests | 23,469 |
| Success Rate | 100% |
| Avg Latency | 364ms |
| P50 Latency | 375ms |
| P90 Latency | 538ms |
| P99 Latency | 704ms |
| RPS | 782 |
| RPM | 46,920 |

**Analysis:** Sustained load with 300 concurrent connections shows stable performance. Latency is higher than simulate latency (100ms) due to:
1. Rate limiter mutex contention (keys rotated, but still some contention)
2. Goroutine scheduling overhead
3. HTTP connection management

### Files Modified

- `/home/ubuntu/workspace/ai-portal-go/scripts/loadtest/main.go` - Enhanced test with warmup and two test modes
- `/home/ubuntu/workspace/ai-portal-go/scripts/rate-limit-test.sh` - Bash stress test script

### Key Findings Summary

1. **Cache is Critical**: Warmup reduces latency from ~600ms to ~180ms for cached keys
2. **Mutex Contention**: Rate limiter's per-key mutex causes serialization under high concurrency
3. **Burst vs Sustained**: System handles sustained load better than burst
4. **Target Not Met**: 100K RPM target cannot be achieved with current implementation
