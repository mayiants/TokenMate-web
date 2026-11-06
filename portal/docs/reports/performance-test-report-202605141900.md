# Performance Test Report

> **Date:** 2026-05-14
> **Status:** ACTIVE
> **Scope:** ai-portal-go + ai-proxy-go
> **Environment:** Docker Compose (simulate mode)

---

## 1. Test Objectives

1. Measure maximum throughput (RPS) of the **real** proxy middleware chain (not mock handlers)
2. Measure request latency distribution (P50, P90, P99) including all middleware
3. Verify rate limiter accuracy under load
4. Verify circuit breaker state transitions
5. Identify performance bottlenecks and optimize
6. Compare Go client vs Python urllib for accurate measurement

---

## 2. Test Environment

| Component | Configuration |
|-----------|--------------|
| OS | Linux 6.17.0-1013-aws |
| CPU | 2 cores (EC2) |
| Go | 1.24 |
| PostgreSQL | Docker (ai-proxy-postgres-dev) |
| Redis | Docker (ai-proxy-redis-dev) |
| Portal | Port 3001, Gin framework |
| Proxy | Port 8080, Gin framework, simulate mode |

---

## 3. Real Middleware Chain Tested

Unlike previous mock-based tests, these tests exercise the **complete request processing chain**:

```
Client → Proxy (8080)
  → Concurrency limiter (semaphore, CPUs × 128)
  → Auth middleware (API key validation, 5-min local cache)
  → Rate limit middleware (local lock-free token bucket, async Redis sync)
  → Affinity cache lookup (Redis)
  → Key pool selection (weighted round-robin)
  → Circuit breaker check (per-provider)
  → Simulate mode response (configurable latency)
  → Token usage recording
  → Response
```

Each request involves:
- Auth: local cache hit (~50µs) or Portal DB query (~5-8ms on miss)
- Rate limit: local atomic CAS (~1µs), async Redis sync (non-blocking)
- In-memory circuit breaker check (~0.1µs)
- In-memory key pool selection (~0.5µs)
- Simulate response: configurable latency (0ms for throughput test, 100ms for realistic test)

---

## 4. Test Results — Proxy Throughput (Simulate Mode, latency=0ms)

### Go Client with Connection Pooling (accurate results)

Pre-conditions: 500 API keys with RPM=6000, auth cache pre-warmed, Go `http.Transport` with `MaxIdleConns=1000`.

#### Before Optimization (no concurrency limiter, default http.Server)

| Concurrency | RPS | P50 | P90 | P99 | OK | 429 | Err |
|-------------|-----|-----|-----|-----|----|----|-----|
| 10 | ~1,200 | 2.7ms | 5ms | 146ms | 200 | 0 | 0 |
| 50 | ~2,400 | 10ms | 22ms | 205ms | 1000 | 0 | 0 |
| 100 | ~5,150 | 15ms | 40ms | 58ms | 2000 | 0 | 0 |
| 200 | ~4,600 | 9ms | 30ms | 366ms | 4000 | 0 | 0 |
| 500 | ~3,400 | 75ms | 200ms | 592ms | 10000 | 0 | 0 |

#### After Optimization (concurrency limiter + http.Server tuning)

| Concurrency | RPS | P50 | P90 | P99 | OK | 429 | Err |
|-------------|-----|-----|-----|-----|----|----|-----|
| 1 | 2,638 | 0.33ms | 0.5ms | 1.6ms | 20 | 0 | 0 |
| 10 | 6,343 | 1.4ms | 2.4ms | 6.8ms | 200 | 0 | 0 |
| 50 | 6,317 | 5.5ms | 11ms | 25ms | 1000 | 0 | 0 |
| 100 | 5,079 | 14ms | 31ms | 100ms | 2000 | 0 | 0 |
| 200 | 5,546 | 18ms | 49ms | 272ms | 4000 | 0 | 0 |
| 500 | 5,699 | 57ms | 155ms | 225ms | 10000 | 0 | 0 |

**Key improvements:**
- C=10: RPS 1,200 → **6,343** (5.3x), P99 146ms → **6.8ms** (21x)
- C=50: RPS 2,400 → **6,317** (2.6x), P99 205ms → **25ms** (8x)
- C=500: RPS 3,400 → **5,699** (1.7x), P99 592ms → **225ms** (2.6x)

### Python urllib (inaccurate, client-bottlenecked)

| Concurrency | RPS | P50 | Notes |
|-------------|-----|-----|-------|
| 50 | ~1,500 | ~20ms | No connection pooling, GIL overhead |
| 100 | ~1,600 | ~40ms | Each request creates new TCP connection |

Python urllib results are ~3x lower due to client-side bottlenecks, not server performance.

---

## 5. Rate Limit Accuracy Test

### TC-06: RPM Enforcement (RPM=60, single key)
- **Setup:** Create API key with RPM=60
- **Test:** Send 70 rapid requests with same key
- **Expected:** ~60 success, ~10 rate limited (429)
- **Actual:** 60 success, 10 rate limited (429)
- **Result:** PASS — Rate limit is precisely enforced

---

## 6. Circuit Breaker Tests

### TC-07: Circuit Breaker State Transitions
- **Config:** threshold=3, resetTimeout=100ms
- **Steps:**
  1. Start in Closed state PASS
  2. Record 3 failures -> Open PASS
  3. Allow() returns false when Open PASS
  4. Wait 150ms -> HalfOpen, Allow() returns true PASS
  5. Record 3 successes -> Closed PASS
- **Result:** PASS

### TC-08: Per-Provider Circuit Breakers
- **Steps:**
  1. Open circuit for "openai" (2 failures) PASS
  2. Allow("openai") returns false PASS
  3. Allow("anthropic") returns true (independent) PASS
- **Result:** PASS

---

## 7. Bottleneck Analysis

### Proxy Internal Latency

From structured logs, proxy internal processing time:
- Auth cache hit: **~50µs**
- Rate limit check (local): **~1µs**
- Circuit breaker check: **~0.1µs**
- Key pool selection: **~0.5µs**
- Total proxy internal: **~50-120µs**

The ~6-100ms client-measured latency is dominated by:
1. **Network RTT** (client ↔ proxy via Docker bridge): ~1-2ms
2. **Go runtime scheduler contention** (2 CPU cores): increases with concurrency
3. **TCP accept queue backlog** under burst traffic

### Why Go Client vs Python urllib: 3x Difference

| Factor | Go `http.Transport` | Python `urllib.request` |
|--------|---------------------|-------------------------|
| Connection pooling | MaxIdleConns=1000, reuse TCP | New connection per request |
| Concurrency | Native goroutines, M:N scheduling | OS threads, GIL-limited |
| Per-request overhead | ~1µs (compiled, zero-alloc) | ~5-10µs (interpreted, object creation) |
| Compression | DisableCompression=true | Default header processing |

### Optimal Concurrency

On 2-core EC2:
- **Max RPS** at C=10-50: ~6,300 RPS
- **Stable RPS** at C=100-500: ~5,000-5,700 RPS
- **Best P99** at C=10: 6.8ms
- **Acceptable P99** at C=100: 100ms

The sweet spot is **C=50**: RPS=6,317, P50=5.5ms, P99=25ms.

### Production Throughput Estimate

With real upstream API latency (500ms-5s):
- **Bottleneck shifts to upstream response time**
- Proxy can handle ~5,000+ concurrent connections
- Estimated RPS = concurrent_connections / avg_upstream_latency
- Example: 5,000 / 1s = ~5,000 RPS with 1s average upstream latency

---

## 8. Optimizations Applied

### Concurrency Limiter Middleware

Added semaphore-based concurrency limiter in `server.go`:
```go
maxConcurrent := runtime.GOMAXPROCS(0) * 128
concSem := make(chan struct{}, maxConcurrent)
router.Use(func(c *gin.Context) {
    select {
    case concSem <- struct{}{}:
        defer func() { <-concSem }()
        c.Next()
    default:
        c.JSON(503, gin.H{"error": "server busy"})
    }
})
```

This prevents goroutine scheduler thrashing on low-CPU instances. Returns 503 when at capacity instead of letting all requests compete for CPU.

### http.Server Tuning

```go
&http.Server{
    Handler:      router,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 30 * time.Second,
    IdleTimeout:  120 * time.Second,
}
```

---

## 9. Recommended Production Settings

| Setting | Default | Recommended | Reason |
|---------|---------|-------------|--------|
| DB pool size | 25 | 50-100 | Prevent connection saturation |
| Redis pool size | 10 | 25-50 | Rate limit async sync needs connections |
| Concurrency limit | CPUs × 128 | CPUs × 128 | Prevent scheduler thrashing |
| Upstream timeout | 30s | 30s (chat), 60s (streaming) | Standard |
| Circuit breaker threshold | 5 | 5 | Appropriate |
| Circuit breaker reset | 30s | 30s | Appropriate |
| Rate limit (RPM) | 60 | Per-plan | Already configurable |
| Horizontal scaling | Single instance | 2-3 instances behind LB | Linear RPS scaling |

---

## 10. Performance Test Execution

### Run Tests

```bash
# Ensure services are running with simulate mode
cd /mnt/workspace/workspace/ai-portal-go
bash start.sh simulate

# Run Go-based latency benchmark (recommended)
go run test/proxy-latency-bench-202605142240.go

# Run Python v2 test (with Go load generator)
python3 test/perf-test-202605142200.py --keys 500

# Run Python v1 test (less accurate, Python urllib)
python3 test/perf-test-202605141900.py --keys 100
```

### Check Metrics

```bash
# Portal metrics
curl http://127.0.0.1:3001/metrics

# Proxy metrics
curl http://127.0.0.1:8080/metrics

# Health checks
curl http://127.0.0.1:3001/api/health
curl http://127.0.0.1:8080/health
```

---

## 11. Summary

| Metric | Before Optimization | After Optimization | Improvement |
|--------|--------------------|--------------------|-------------|
| Max RPS (C=10) | ~1,200 | **~6,343** | 5.3x |
| Max RPS (C=100) | ~5,150 | **~5,079** | ~same |
| P99 at C=10 | 146ms | **6.8ms** | 21x |
| P99 at C=50 | 205ms | **25ms** | 8x |
| P99 at C=500 | 592ms | **225ms** | 2.6x |
| Rate limit accuracy | 100% | 100% | — |
| Circuit breaker | PASS | PASS | — |

The proxy can handle **~6,300 RPS** at low concurrency and **~5,000-5,700 RPS** at high concurrency on a 2-core EC2 instance. For production, run 2-3 instances behind a load balancer for ~10,000-17,000 RPS.
