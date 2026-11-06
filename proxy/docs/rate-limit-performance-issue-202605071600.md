# Rate Limiter Performance Issue

> **ACTIVE** - Critical performance issue requiring fix

## Problem Summary

The rate limiter implementation causes significant latency overhead, making the system unusable in production scenarios.

## Performance Impact

### Current Measurements (Simulate Mode, 100ms latency)

| Test | Concurrency | Avg Latency | P99 Latency | RPS |
|------|-------------|-------------|-------------|-----|
| May 2 (old) | 300 | 174ms | 520ms | 1,277 |
| May 7 (new) | 300 | 364ms | 704ms | 782 |
| May 7 (new) | 25,000 burst | 11,940ms | 23,446ms | 801 |

### Projected Production Latency

| Component | Local Test | Production |
|-----------|------------|------------|
| Upstream AI | 0ms (simulate) | 300-500ms |
| Proxy overhead | 180-700ms | 180-700ms |
| **Total** | **180-700ms** | **480-1200ms** |

**Result: 0.5-1.2 second latency is unacceptable for production use.**

## Root Cause Analysis

### 1. Double Mutex Acquisition (May 7 regression)

Each request now acquires mutex **twice**:

```go
// Before request - RPM check
func (m *Middleware) AllowRPM(redisKey string, rpmLimit int) bool {
    // ...
    counter.Allow(redisKey + ":rpm")  // <-- mutex lock #1
}

// After request - TPM record (NEW in May 5)
func (m *Middleware) RecordTokens(redisKey string, tpmLimit int, tokens int64) bool {
    // ...
    counter.AllowN(redisKey+":tpm", int(tokens))  // <-- mutex lock #2
}
```

**May 2 version did NOT have TPM recording**, so only 1 mutex acquisition per request.

### 2. Mutex Contention Under Concurrency

```go
// internal/ratelimit/ratelimit.go:53
bucket.mu.Lock()
defer bucket.mu.Unlock()
```

All concurrent requests to the same key serialize on this mutex:
- 300 concurrent requests to same key = 300 requests queueing on mutex
- Each mutex hold: ~1μs, but queueing causes exponential latency growth

### 3. Per-Key Bucket Design

Current design: One token bucket per key
- Good: No global lock, different keys don't block each other
- Bad: All requests to same key serialize on same mutex

## Proposed Solutions

### Option 1: Lock-Free Token Bucket (Recommended)

Replace mutex with atomic operations:

```go
type tokenBucket struct {
    tokens     atomic.Float64
    lastUpdate atomic.Int64  // Unix nano timestamp
}

func (lc *localCounter) AllowN(key string, n int) bool {
    for {
        now := time.Now().UnixNano()
        bucket := lc.getBucket(key)
        
        last := bucket.lastUpdate.Load()
        tokens := bucket.tokens.Load()
        
        // Calculate new tokens
        elapsed := float64(now-last) / 1e9
        newTokens := min(lc.maxTokens, tokens + elapsed*lc.rate)
        
        if newTokens < float64(n) {
            return false
        }
        
        // CAS update - no mutex needed
        if bucket.lastUpdate.CompareAndSwap(last, now) {
            bucket.tokens.Store(newTokens - float64(n))
            return true
        }
        // CAS failed, retry
    }
}
```

**Pros:**
- No mutex contention
- O(1) operations
- Scales with concurrency

**Cons:**
- CAS loop may spin under extreme contention
- Slightly more complex logic

### Option 2: Sharded Counters

Divide each key's counter into N shards:

```go
type shardedCounter struct {
    shards [16]struct {
        mu     sync.Mutex
        tokens float64
    }
}

func (sc *shardedCounter) Allow(n int) bool {
    shard := &sc.shards[rand.Intn(16)]
    shard.mu.Lock()
    defer shard.mu.Unlock()
    // ... token bucket logic
}
```

**Pros:**
- Reduces contention by 16x
- Simpler than lock-free

**Cons:**
- Still has mutex
- Less accurate rate limiting (shards may be uneven)

### Option 3: Async TPM Recording

Move TPM recording to async goroutine:

```go
// Don't block request on TPM recording
go func() {
    rateLimiter.RecordTokens(keyHash, tpm, tokens)
}()
```

**Pros:**
- Minimal code change
- Removes one mutex acquisition from hot path

**Cons:**
- TPM may be slightly inaccurate
- Still has RPM mutex contention

### Option 4: Batch Rate Limit Checks

Group multiple requests and check rate limit once:

```go
// Check rate limit for batch of requests
func (m *Middleware) AllowBatch(key string, count int) (allowed int) {
    // Single mutex acquisition for multiple requests
}
```

**Pros:**
- Reduces mutex acquisitions
- Good for high-throughput scenarios

**Cons:**
- Changes API semantics
- May not work for all use cases

## Recommended Implementation

**Phase 1 (Quick Fix):** Option 3 - Async TPM recording
- Remove TPM mutex from hot path immediately
- Expected improvement: ~50% latency reduction

**Phase 2 (Proper Fix):** Option 1 - Lock-free token bucket
- Complete rewrite of rate limiter
- Expected improvement: ~90% latency reduction

## Performance Targets

| Metric | Current | Target | Gap |
|--------|---------|--------|-----|
| Proxy overhead | 180-700ms | <50ms | 4-14x |
| P99 latency | 704ms | <200ms | 3.5x |
| RPS (300 concurrent) | 782 | 10,000+ | 13x |
| RPM | 46,920 | 100,000 | 2x |

## Test Verification

After fix, run enhanced test:

```bash
cd /home/ubuntu/workspace/ai-portal-go/scripts/loadtest
./loadtest -users 5000 -rpm 100000 -concurrency 300 -duration 30 -warmup=true -mode=both
```

**Success criteria:**
- P99 latency < 200ms (with 100ms simulate)
- RPS > 1,500 (with 300 concurrent)
- No rate limiting when limits are high (100K RPM)
