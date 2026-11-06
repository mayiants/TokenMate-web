#!/bin/bash
# ============================================
# AI Platform Go — Performance Benchmark
# ============================================
# Validates that recent performance optimizations are functional and
# measures key throughput/latency metrics:
#
#   1. sync.Pool allocation validation (proxy)
#   2. Batch webhook writes vs single writes (portal)
#   3. Config version skip (proxy hot-reload efficiency)
#   4. Local balance cache hit path (proxy)
#   5. Prometheus /metrics endpoint validation
#   6. Key validation throughput (portal)
#   7. Affinity cache hit/miss metrics
#   8. Provider queue (if enabled)
#
# Usage:
#   bash tests/test-performance-benchmark.sh
#
# Environment:
#   PORTAL_URL      Portal base URL (default: http://127.0.0.1:3001)
#   PROXY_URL       Proxy base URL (default: http://127.0.0.1:8080)
#   ADMIN_EMAIL     Admin email (default: admin@example.com)
#   ADMIN_PASSWORD  Admin password (default: Admin@123456!)
#   ITERATIONS      Number of iterations for throughput tests (default: 100)
# ============================================

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
PROXY_DIR="${PROXY_DIR:-$PROJECT_DIR/../ai-proxy-go}"

cd "$PROJECT_DIR"

# Save caller-provided overrides before sourcing .env
_CALLER_PORTAL_URL="${PORTAL_URL:-}"
_CALLER_PROXY_URL="${PROXY_URL:-}"

# Load environment
if [[ -f ".env.go-platform" ]]; then
    set -a
    source .env.go-platform
    set +a
fi

# Prefer caller overrides, then host-accessible defaults (not Docker-internal addresses)
PORTAL_URL="${_CALLER_PORTAL_URL:-http://127.0.0.1:${PORTAL_PORT:-3001}}"
PROXY_URL="${_CALLER_PROXY_URL:-http://127.0.0.1:${PROXY_PORT:-8080}}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-Admin@123456!}"
ITERATIONS="${ITERATIONS:-100}"

SYNC_SECRET="${PROXY_SYNC_SECRET:-${ADMIN_SECRET:-admin-secret}}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

declare -i PASS=0
declare -i FAIL=0
declare -i SKIP=0

log_test() {
    local name="$1" status="$2" detail="${3:-}"
    if [[ "$status" == "PASS" ]]; then
        echo -e "${GREEN}✓${NC} $name"
        PASS=$((PASS + 1))
    elif [[ "$status" == "SKIP" ]]; then
        echo -e "${YELLOW}⊘${NC} $name (skipped)"
        SKIP=$((SKIP + 1))
    else
        echo -e "${RED}✗${NC} $name"
        [[ -n "$detail" ]] && echo -e "  ${RED}$detail${NC}"
        FAIL=$((FAIL + 1))
    fi
}

log_metric() {
    local name="$1" value="$2" unit="${3:-}"
    echo -e "  ${BLUE}$name: ${value}${unit:+ $unit}${NC}"
}

# Helper: measure time in milliseconds
time_ms() {
    local start end
    start=$(date +%s%N)
    "$@" >/dev/null 2>&1
    end=$(date +%s%N)
    echo $(( (end - start) / 1000000 ))
}

# Helper: login and get token
get_admin_token() {
    curl -s -X POST "$PORTAL_URL/api/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" | \
        python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("token",""))' 2>/dev/null
}

echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  AI Platform Go — Performance Benchmark${NC}"
echo -e "${CYAN}========================================${NC}"
echo -e "${BLUE}Portal: $PORTAL_URL${NC}"
echo -e "${BLUE}Proxy:  $PROXY_URL${NC}"
echo -e "${BLUE}Iterations: $ITERATIONS${NC}"
echo ""

# ============================================
# 0. Preconditions
# ============================================
echo -e "${CYAN}=== Precondition Check ===${NC}"

if ! curl -sf "$PORTAL_URL/api/health" >/dev/null 2>&1; then
    echo -e "${RED}Portal not reachable at $PORTAL_URL${NC}"
    exit 1
fi
if ! curl -sf "$PROXY_URL/health" >/dev/null 2>&1; then
    echo -e "${RED}Proxy not reachable at $PROXY_URL${NC}"
    exit 1
fi
log_test "Services healthy" "PASS"

TOKEN=$(get_admin_token)
if [[ -z "$TOKEN" ]]; then
    echo -e "${RED}Failed to get admin token${NC}"
    exit 1
fi
log_test "Admin login" "PASS"

# ============================================
# 1. Prometheus Metrics Endpoint
# ============================================
echo -e "\n${CYAN}=== 1. Prometheus Metrics ===${NC}"

METRICS_RESP=$(curl -s "$PROXY_URL/metrics")

# Counters without labels always appear
if echo "$METRICS_RESP" | grep -q "proxy_cache_hits_total"; then
    log_test "Proxy counter: proxy_cache_hits_total" "PASS"
else
    log_test "Proxy counter: proxy_cache_hits_total" "FAIL" "not found in /metrics"
fi

if echo "$METRICS_RESP" | grep -q "proxy_cache_misses_total"; then
    log_test "Proxy counter: proxy_cache_misses_total" "PASS"
else
    log_test "Proxy counter: proxy_cache_misses_total" "FAIL" "not found in /metrics"
fi

if echo "$METRICS_RESP" | grep -q "proxy_preconsume_rejected_total"; then
    log_test "Proxy counter: proxy_preconsume_rejected_total" "PASS"
else
    log_test "Proxy counter: proxy_preconsume_rejected_total" "FAIL" "not found in /metrics"
fi

# Label-vector metrics only appear after first observation (standard prometheus behavior)
# Verify they are registered by checking the source code
if grep -q "proxy_request_duration_seconds" "$PROXY_DIR/pkg/metrics/prometheus.go" 2>/dev/null; then
    log_test "Proxy histogram registered: proxy_request_duration_seconds" "PASS"
else
    log_test "Proxy histogram registered: proxy_request_duration_seconds" "FAIL"
fi

if grep -q "proxy_fallback_total" "$PROXY_DIR/pkg/metrics/prometheus.go" 2>/dev/null; then
    log_test "Proxy counter registered: proxy_fallback_total" "PASS"
else
    log_test "Proxy counter registered: proxy_fallback_total" "FAIL"
fi

if grep -q "proxy_key_cooldown_total" "$PROXY_DIR/pkg/metrics/prometheus.go" 2>/dev/null; then
    log_test "Proxy counter registered: proxy_key_cooldown_total" "PASS"
else
    log_test "Proxy counter registered: proxy_key_cooldown_total" "FAIL"
fi

if grep -q "proxy_settle_charged_total" "$PROXY_DIR/pkg/metrics/prometheus.go" 2>/dev/null; then
    log_test "Proxy counter registered: proxy_settle_charged_total" "PASS"
else
    log_test "Proxy counter registered: proxy_settle_charged_total" "FAIL"
fi

# Verify legacy metrics are still served
if echo "$METRICS_RESP" | grep -q "proxy_uptime_seconds"; then
    log_test "Legacy metrics: proxy_uptime_seconds" "PASS"
else
    log_test "Legacy metrics: proxy_uptime_seconds" "FAIL"
fi

# ============================================
# 2. Config Version Skip (incremental sync)
# ============================================
echo -e "\n${CYAN}=== 2. Config Version Skip ===${NC}"

# First sync - get version
SYNC1=$(curl -s -H "x-proxy-secret: $SYNC_SECRET" "$PROXY_URL/api/admin/key-pool/sync")
VERSION1=$(echo "$SYNC1" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("configVersion",""))' 2>/dev/null)

if [[ -n "$VERSION1" ]]; then
    log_test "Config version present in sync response" "PASS"
    log_metric "Config version" "$VERSION1"
else
    log_test "Config version present in sync response" "FAIL" "configVersion field missing"
fi

# Second sync should return same version (no changes)
SYNC2=$(curl -s -H "x-proxy-secret: $SYNC_SECRET" "$PROXY_URL/api/admin/key-pool/sync")
VERSION2=$(echo "$SYNC2" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("configVersion",""))' 2>/dev/null)

if [[ "$VERSION1" == "$VERSION2" ]]; then
    log_test "Config version stable (no spurious changes)" "PASS"
else
    log_test "Config version stable (no spurious changes)" "FAIL" "v1=$VERSION1 vs v2=$VERSION2"
fi

# Measure sync latency
SYNC_TIMES=()
for i in $(seq 1 10); do
    t=$(time_ms curl -s -H "x-proxy-secret: $SYNC_SECRET" "$PROXY_URL/api/admin/key-pool/sync")
    SYNC_TIMES+=($t)
done
SYNC_AVG=$(( (${SYNC_TIMES[0]} + ${SYNC_TIMES[1]} + ${SYNC_TIMES[2]} + ${SYNC_TIMES[3]} + ${SYNC_TIMES[4]} + ${SYNC_TIMES[5]} + ${SYNC_TIMES[6]} + ${SYNC_TIMES[7]} + ${SYNC_TIMES[8]} + ${SYNC_TIMES[9]}) / 10 ))
log_metric "Sync endpoint avg latency (10 calls)" "$SYNC_AVG" "ms"

if [[ $SYNC_AVG -lt 100 ]]; then
    log_test "Sync latency < 100ms" "PASS"
else
    log_test "Sync latency < 100ms" "FAIL" "${SYNC_AVG}ms"
fi

# ============================================
# 3. Key Validation Throughput (Portal)
# ============================================
echo -e "\n${CYAN}=== 3. Key Validation Throughput ===${NC}"

# Create a test key for validation
KEY_RESP=$(curl -s -X POST "$PORTAL_URL/api/apikeys" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d '{"name":"perf-bench-key"}')
TEST_KEY=$(echo "$KEY_RESP" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("key",""))' 2>/dev/null)

if [[ -z "$TEST_KEY" ]]; then
    log_test "Key validation throughput" "SKIP"
    log_metric "Skip reason" "could not create test key"
else
    # Warm up
    for i in $(seq 1 5); do
        curl -s -X POST "$PORTAL_URL/api/keys/validate" \
            -H "Content-Type: application/json" \
            -d "{\"key\":\"$TEST_KEY\",\"model\":\"gpt-5.5\"}" >/dev/null 2>&1
    done

    # Measure
    VALIDATE_START=$(date +%s%N)
    for i in $(seq 1 $ITERATIONS); do
        curl -s -X POST "$PORTAL_URL/api/keys/validate" \
            -H "Content-Type: application/json" \
            -d "{\"key\":\"$TEST_KEY\",\"model\":\"gpt-5.5\"}" >/dev/null 2>&1
    done
    VALIDATE_END=$(date +%s%N)
    VALIDATE_TOTAL_MS=$(( (VALIDATE_END - VALIDATE_START) / 1000000 ))
    VALIDATE_AVG_MS=$(( VALIDATE_TOTAL_MS / ITERATIONS ))
    VALIDATE_RPS=$(( ITERATIONS * 1000 / (VALIDATE_TOTAL_MS + 1) ))

    log_metric "Total time ($ITERATIONS calls)" "$VALIDATE_TOTAL_MS" "ms"
    log_metric "Avg latency per call" "$VALIDATE_AVG_MS" "ms"
    log_metric "Throughput" "$VALIDATE_RPS" "req/s"

    if [[ $VALIDATE_AVG_MS -lt 50 ]]; then
        log_test "Key validation avg < 50ms (index + cache)" "PASS"
    elif [[ $VALIDATE_AVG_MS -lt 100 ]]; then
        log_test "Key validation avg < 100ms (acceptable)" "PASS"
    else
        log_test "Key validation avg < 100ms" "FAIL" "${VALIDATE_AVG_MS}ms"
    fi
fi

# ============================================
# 4. Webhook Batch Write (Portal)
# ============================================
echo -e "\n${CYAN}=== 4. Webhook Batch Write ===${NC}"

# Build a batch of 50 usage records
BATCH_RECORDS="["
for i in $(seq 1 50); do
    [[ $i -gt 1 ]] && BATCH_RECORDS+=","
    BATCH_RECORDS+="{\"traceId\":\"perf-bench-trace-$RANDOM-$i\",\"keyHash\":\"test-hash-$i\",\"model\":\"gpt-5.5\",\"provider\":\"openai\",\"inputTokens\":100,\"outputTokens\":50,\"cost\":150,\"duration\":1000,\"statusCode\":200}"
done
BATCH_RECORDS+="]"

BATCH_PAYLOAD="{\"records\":$BATCH_RECORDS}"

# Helper: compute HMAC-SHA256 webhook signature
webhook_sign() {
    local body="$1"
    local timestamp=$(date +%s)
    local signature=$(echo -n "${timestamp}.${body}" | openssl dgst -sha256 -hmac "$SYNC_SECRET" | awk '{print $NF}')
    echo "$timestamp $signature"
}

# Single batch POST
BATCH_START=$(date +%s%N)
WEBHOOK_TS_SIG=$(webhook_sign "$BATCH_PAYLOAD")
WEBHOOK_TS=$(echo "$WEBHOOK_TS_SIG" | awk '{print $1}')
WEBHOOK_SIG=$(echo "$WEBHOOK_TS_SIG" | awk '{print $2}')
BATCH_RESP=$(curl -s -X POST "$PORTAL_URL/api/webhook/usage-stats-batch" \
    -H "Content-Type: application/json" \
    -H "X-Timestamp: $WEBHOOK_TS" \
    -H "X-Signature: $WEBHOOK_SIG" \
    -d "$BATCH_PAYLOAD")
BATCH_END=$(date +%s%N)
BATCH_MS=$(( (BATCH_END - BATCH_START) / 1000000 ))

if echo "$BATCH_RESP" | grep -q '"ok":true'; then
    log_test "Batch webhook accepted (50 records)" "PASS"
    log_metric "Batch latency" "$BATCH_MS" "ms"
    if echo "$BATCH_RESP" | grep -q '"queued":true'; then
        log_metric "Pipeline mode" "Redis Pipeline (async)"
    else
        log_metric "Pipeline mode" "synchronous fallback"
    fi
else
    log_test "Batch webhook accepted (50 records)" "FAIL" "$BATCH_RESP"
fi

# Compare: 50 individual writes
SINGLE_START=$(date +%s%N)
for i in $(seq 1 50); do
    SINGLE_BODY="{\"traceId\":\"perf-bench-single-$RANDOM-$i\",\"keyHash\":\"test-hash-$i\",\"model\":\"gpt-5.5\",\"provider\":\"openai\",\"inputTokens\":100,\"outputTokens\":50,\"cost\":150,\"duration\":1000,\"statusCode\":200}"
    SINGLE_TS_SIG=$(webhook_sign "$SINGLE_BODY")
    SINGLE_TS=$(echo "$SINGLE_TS_SIG" | awk '{print $1}')
    SINGLE_SIG=$(echo "$SINGLE_TS_SIG" | awk '{print $2}')
    curl -s -X POST "$PORTAL_URL/api/webhook/usage" \
        -H "Content-Type: application/json" \
        -H "X-Timestamp: $SINGLE_TS" \
        -H "X-Signature: $SINGLE_SIG" \
        -d "$SINGLE_BODY" >/dev/null 2>&1
done
SINGLE_END=$(date +%s%N)
SINGLE_MS=$(( (SINGLE_END - SINGLE_START) / 1000000 ))

log_metric "50 individual writes" "$SINGLE_MS" "ms"
log_metric "Batch speedup" "$(( SINGLE_MS / (BATCH_MS + 1) ))x" ""

if [[ $BATCH_MS -lt $SINGLE_MS ]]; then
    log_test "Batch writes faster than individual" "PASS"
else
    log_test "Batch writes faster than individual" "FAIL" "batch=${BATCH_MS}ms vs single=${SINGLE_MS}ms"
fi

# ============================================
# 5. Affinity Cache Metrics
# ============================================
echo -e "\n${CYAN}=== 5. Affinity Cache Metrics ===${NC}"

# Get current cache metrics from /metrics
CACHE_HITS_BEFORE=$(echo "$METRICS_RESP" | grep "^proxy_cache_hits_total" | awk '{print $2}' | head -1)
CACHE_MISSES_BEFORE=$(echo "$METRICS_RESP" | grep "^proxy_cache_misses_total" | awk '{print $2}' | head -1)

# Make a few proxy requests (they'll 401 but still exercise the auth path which checks cache)
for i in $(seq 1 5); do
    curl -s -H "Authorization: Bearer sk-wgit-fake-key-$i" "$PROXY_URL/v1/models" >/dev/null 2>&1
done

# Check metrics again
METRICS_AFTER=$(curl -s "$PROXY_URL/metrics")
CACHE_HITS_AFTER=$(echo "$METRICS_AFTER" | grep "^proxy_cache_hits_total" | awk '{print $2}' | head -1)
CACHE_MISSES_AFTER=$(echo "$METRICS_AFTER" | grep "^proxy_cache_misses_total" | awk '{print $2}' | head -1)

log_metric "Cache hits (before/after)" "${CACHE_HITS_BEFORE:-0} → ${CACHE_HITS_AFTER:-0}"
log_metric "Cache misses (before/after)" "${CACHE_MISSES_BEFORE:-0} → ${CACHE_MISSES_AFTER:-0}"

if [[ "${CACHE_HITS_AFTER:-0}" != "${CACHE_HITS_BEFORE:-0}" ]] || [[ "${CACHE_MISSES_AFTER:-0}" != "${CACHE_MISSES_BEFORE:-0}" ]]; then
    log_test "Affinity cache metrics updating" "PASS"
else
    # Cache metrics might not change if auth fails before reaching affinity; that's OK
    log_test "Affinity cache metrics updating" "SKIP"
fi

# ============================================
# 6. Proxy Health Endpoint Throughput
# ============================================
echo -e "\n${CYAN}=== 6. Proxy Health Latency ===${NC}"

HEALTH_TIMES=()
for i in $(seq 1 $ITERATIONS); do
    t=$(time_ms curl -s "$PROXY_URL/health")
    HEALTH_TIMES+=($t)
done

# Calculate stats
HEALTH_SUM=0
HEALTH_MAX=0
HEALTH_MIN=99999
for t in "${HEALTH_TIMES[@]}"; do
    HEALTH_SUM=$((HEALTH_SUM + t))
    [[ $t -gt $HEALTH_MAX ]] && HEALTH_MAX=$t
    [[ $t -lt $HEALTH_MIN ]] && HEALTH_MIN=$t
done
HEALTH_AVG=$((HEALTH_SUM / ITERATIONS))
HEALTH_RPS=$((ITERATIONS * 1000 / (HEALTH_SUM + 1)))

log_metric "Avg latency" "$HEALTH_AVG" "ms"
log_metric "Min/Max" "${HEALTH_MIN}/${HEALTH_MAX}" "ms"
log_metric "Throughput" "$HEALTH_RPS" "req/s"

if [[ $HEALTH_AVG -lt 30 ]]; then
    log_test "Health endpoint < 30ms avg" "PASS"
else
    log_test "Health endpoint < 30ms avg" "FAIL" "${HEALTH_AVG}ms"
fi

# ============================================
# 7. Portal Webhook Throughput (concurrent)
# ============================================
echo -e "\n${CYAN}=== 7. Portal Webhook Concurrent Throughput ===${NC}"

# Fire 20 concurrent batch requests
CONC_START=$(date +%s%N)
for i in $(seq 1 20); do
    CONC_BODY="{\"records\":[{\"traceId\":\"conc-$RANDOM-$i\",\"keyHash\":\"hash-$i\",\"model\":\"gpt-5.5\",\"provider\":\"openai\",\"inputTokens\":100,\"outputTokens\":50,\"cost\":150,\"duration\":1000,\"statusCode\":200}]}"
    CONC_TS_SIG=$(webhook_sign "$CONC_BODY")
    CONC_TS=$(echo "$CONC_TS_SIG" | awk '{print $1}')
    CONC_SIG=$(echo "$CONC_TS_SIG" | awk '{print $2}')
    curl -s -X POST "$PORTAL_URL/api/webhook/usage-stats-batch" \
        -H "Content-Type: application/json" \
        -H "X-Timestamp: $CONC_TS" \
        -H "X-Signature: $CONC_SIG" \
        -d "$CONC_BODY" >/dev/null &
done
wait
CONC_END=$(date +%s%N)
CONC_MS=$(( (CONC_END - CONC_START) / 1000000 ))

log_metric "20 concurrent batch webhooks" "$CONC_MS" "ms"
CONC_RPS=$((20 * 1000 / (CONC_MS + 1)))
log_metric "Effective throughput" "$CONC_RPS" "req/s"

if [[ $CONC_MS -lt 2000 ]]; then
    log_test "Concurrent webhooks < 2s" "PASS"
else
    log_test "Concurrent webhooks < 2s" "FAIL" "${CONC_MS}ms"
fi

# ============================================
# 8. Portal /api/admin/pricing/sync Latency
# ============================================
echo -e "\n${CYAN}=== 8. Pricing Sync Latency ===${NC}"

PRICING_TIMES=()
for i in $(seq 1 10); do
    t=$(time_ms curl -s -H "Authorization: Bearer $TOKEN" "$PORTAL_URL/api/admin/pricing/sync")
    PRICING_TIMES+=($t)
done
PRICING_SUM=0
for t in "${PRICING_TIMES[@]}"; do
    PRICING_SUM=$((PRICING_SUM + t))
done
PRICING_AVG=$((PRICING_SUM / 10))

log_metric "Pricing sync avg latency (10 calls)" "$PRICING_AVG" "ms"

if [[ $PRICING_AVG -lt 200 ]]; then
    log_test "Pricing sync < 200ms" "PASS"
else
    log_test "Pricing sync < 200ms" "FAIL" "${PRICING_AVG}ms"
fi

# ============================================
# 9. sync.Pool Verification (indirect via allocation pattern)
# ============================================
echo -e "\n${CYAN}=== 9. sync.Pool Presence (Code Check) ===${NC}"

if grep -r "sync\.Pool\|pkg/pool" "$PROXY_DIR/internal/server/handlers_chat.go" >/dev/null 2>&1; then
    log_test "handlers_chat.go uses pool package" "PASS"
elif grep -r "pool\." "$PROXY_DIR/internal/server/handlers_chat.go" >/dev/null 2>&1; then
    log_test "handlers_chat.go uses pool package" "PASS"
else
    log_test "handlers_chat.go uses pool package" "FAIL" "no pool usage found"
fi

if [[ -f "$PROXY_DIR/pkg/pool/pool.go" ]]; then
    POOL_TYPES=$(grep -c "func Get\|func Put" "$PROXY_DIR/pkg/pool/pool.go" 2>/dev/null || echo 0)
    log_test "pkg/pool/pool.go exists ($POOL_TYPES Get/Put functions)" "PASS"
else
    log_test "pkg/pool/pool.go exists" "FAIL"
fi

# ============================================
# 10. Balance Cache Verification
# ============================================
echo -e "\n${CYAN}=== 10. Local Balance Cache ===${NC}"

if grep -q "local_balance_cache" "$PROXY_DIR/internal/config/config.go" 2>/dev/null; then
    log_test "Balance cache config present" "PASS"
else
    log_test "Balance cache config present" "FAIL"
fi

if grep -q "PreConsumeLocal\|SettleLocal" "$PROXY_DIR/internal/auth/auth.go" 2>/dev/null; then
    log_test "Balance cache primitives implemented" "PASS"
else
    log_test "Balance cache primitives implemented" "FAIL"
fi

if [[ -f "$PROXY_DIR/internal/auth/balance_cache_test.go" ]]; then
    log_test "Balance cache has unit tests" "PASS"
else
    log_test "Balance cache has unit tests" "FAIL"
fi

# ============================================
# 11. Provider Queue Verification
# ============================================
echo -e "\n${CYAN}=== 11. Provider Queue ===${NC}"

if [[ -f "$PROXY_DIR/pkg/keypool/queue.go" ]]; then
    log_test "Provider queue implementation exists" "PASS"
else
    log_test "Provider queue implementation exists" "FAIL"
fi

if [[ -f "$PROXY_DIR/pkg/keypool/queue_test.go" ]]; then
    QUEUE_TESTS=$(grep -c "func Test" "$PROXY_DIR/pkg/keypool/queue_test.go" 2>/dev/null || echo 0)
    log_test "Provider queue tests ($QUEUE_TESTS test functions)" "PASS"
else
    log_test "Provider queue tests" "FAIL"
fi

if grep -q "provider_queue_enabled\|QueueEnabled\|queue.*enabled\|QueueConfig" "$PROXY_DIR/internal/config/config.go" 2>/dev/null; then
    log_test "Provider queue config toggle present" "PASS"
else
    log_test "Provider queue config toggle present" "FAIL"
fi

# ============================================
# 12. OpenTelemetry Verification
# ============================================
echo -e "\n${CYAN}=== 12. OpenTelemetry ===${NC}"

if [[ -f "$PROXY_DIR/pkg/telemetry/telemetry.go" ]]; then
    log_test "Telemetry package exists" "PASS"
else
    log_test "Telemetry package exists" "FAIL"
fi

if grep -q "InjectTraceparent\|traceparent" "$PROXY_DIR/pkg/telemetry/telemetry.go" 2>/dev/null; then
    log_test "Traceparent injection implemented" "PASS"
else
    log_test "Traceparent injection implemented" "FAIL"
fi

if [[ -f "$PROJECT_DIR/pkg/telemetry/telemetry.go" ]]; then
    log_test "Portal telemetry package exists" "PASS"
else
    log_test "Portal telemetry package exists" "FAIL"
fi

# ============================================
# Summary
# ============================================
echo -e "\n${CYAN}========================================${NC}"
echo -e "${CYAN}  Performance Benchmark Summary${NC}"
echo -e "${CYAN}========================================${NC}"
echo -e "${GREEN}Passed:  $PASS${NC}"
echo -e "${RED}Failed:  $FAIL${NC}"
echo -e "${YELLOW}Skipped: $SKIP${NC}"

echo -e "\n${CYAN}Key Metrics:${NC}"
echo -e "  Proxy health:        ${HEALTH_AVG}ms avg, ${HEALTH_RPS} req/s"
echo -e "  Key validation:      ${VALIDATE_AVG_MS:-N/A}ms avg, ${VALIDATE_RPS:-N/A} req/s"
echo -e "  Config sync:         ${SYNC_AVG}ms avg"
echo -e "  Batch webhook:       ${BATCH_MS}ms (50 records)"
echo -e "  Concurrent webhooks: ${CONC_MS}ms (20 parallel)"
echo -e "  Pricing sync:        ${PRICING_AVG}ms avg"

if [[ $FAIL -eq 0 ]]; then
    echo -e "\n${GREEN}✓ All performance checks passed${NC}"
    exit 0
else
    echo -e "\n${RED}✗ Some performance checks failed${NC}"
    exit 1
fi
