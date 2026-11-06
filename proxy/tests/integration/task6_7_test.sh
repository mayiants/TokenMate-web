#!/bin/bash
# ============================================
# Task 6 & 7 Integration Test Script
# Tests: User Key Rate Limit + Redis Pub/Sub Sync
# ============================================

set -e

PORTAL_URL="http://127.0.0.1:3001"
PROXY_URL="http://127.0.0.1:8080"
ADMIN_EMAIL="admin@example.com"
ADMIN_PASSWORD="Admin@123456!"

RED="\033[0;31m"
GREEN="\033[0;32m"
YELLOW="\033[1;33m"
NC="\033[0m"

PASS=0
FAIL=0

log_pass() { echo -e "${GREEN}✓ PASS${NC}: $1"; ((PASS++)) || true; }
log_fail() { echo -e "${RED}✗ FAIL${NC}: $1"; ((FAIL++)) || true; }
log_info() { echo -e "${YELLOW}ℹ INFO${NC}: $1"; }

# --------------------------------------------
# Test 1: Portal Login
# --------------------------------------------
echo ""
echo "=== Test 1: Portal Login ==="
LOGIN_RESP=$(curl -s -X POST "$PORTAL_URL/api/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}")
TOKEN=$(echo "$LOGIN_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || echo "")
if [ -n "$TOKEN" ]; then
  log_pass "Login successful"
else
  log_fail "Login failed: $LOGIN_RESP"
  exit 1
fi

# --------------------------------------------
# Test 2: Create API Key with custom rate limits
# --------------------------------------------
echo ""
echo "=== Test 2: Create API Key (RPM=5, TPM=500) ==="
KEY_RESP=$(curl -s -X POST "$PORTAL_URL/api/apikeys" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"test-rl-key","rate_limit_rpm":5,"rate_limit_tpm":500}')
API_KEY=$(echo "$KEY_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('key',''))" 2>/dev/null || echo "")
RPM=$(echo "$KEY_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('rate_limit_rpm',0))" 2>/dev/null || echo "0")
if [ -n "$API_KEY" ] && [ "$RPM" = "5" ]; then
  log_pass "API key created with RPM=5"
else
  log_fail "API key creation failed: $KEY_RESP"
  exit 1
fi

# --------------------------------------------
# Test 3: Validate API Key returns rate limits
# --------------------------------------------
echo ""
echo "=== Test 3: Validate API Key Response ==="
VALIDATE=$(curl -s "$PORTAL_URL/api/apikeys/validate" \
  -H "Authorization: Bearer $API_KEY")
V_RPM=$(echo "$VALIDATE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('rate_limit_rpm',0))" 2>/dev/null || echo "0")
V_TPM=$(echo "$VALIDATE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('rate_limit_tpm',0))" 2>/dev/null || echo "0")
if [ "$V_RPM" = "5" ] && [ "$V_TPM" = "500" ]; then
  log_pass "Validate returns rate_limit_rpm=5, rate_limit_tpm=500"
else
  log_fail "Validate returned rpm=$V_RPM, tpm=$V_TPM (expected 5, 500)"
fi

# --------------------------------------------
# Test 4: RPM Rate Limit at Proxy
# --------------------------------------------
echo ""
echo "=== Test 4: RPM Rate Limit (5 req/min) ==="
# Wait for rate limit bucket to reset if previously used
sleep 65

RATE_LIMITED=0
for i in 1 2 3 4 5 6; do
  RESP=$(curl -s -w "\n%{http_code}" -X POST "$PROXY_URL/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d '{"model":"moonshot-v1-8k","messages":[{"role":"user","content":"Hello"}],"stream":false}')
  HTTP_CODE=$(echo "$RESP" | tail -1)
  if [ "$HTTP_CODE" = "429" ]; then
    RATE_LIMITED=1
    log_pass "Request $i correctly rate-limited (429)"
    break
  fi
done

if [ "$RATE_LIMITED" = "0" ]; then
  log_fail "Rate limit not triggered after 6 requests"
fi

# --------------------------------------------
# Test 5: Redis Pub/Sub Sync
# --------------------------------------------
echo ""
echo "=== Test 5: Redis Pub/Sub Real-time Sync ==="

# Get current key count
BEFORE=$(curl -s "$PROXY_URL/providers/stats" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(sum(p.get('totalKeys', 0) for p in d.values()))
" 2>/dev/null || echo "0")
log_info "Proxy keys before: $BEFORE"

# Publish a sync message directly to Redis
docker exec ai-portal-redis redis-cli \
  -a '106cfd8b90d6fe25de19adea58aec03a35de083bd16037fe364004628d1b40d7' \
  PUBLISH ai-proxy:sync '{"type":"sync_needed","source":"test","timestamp":"2026-05-05T22:40:00Z"}' \
  >/dev/null 2>&1

# Wait for sync
sleep 3

# Check Proxy logs for Pub/Sub reception
LOG_HIT=$(docker logs ai-proxy-go --since 5s 2>&1 | grep -c "Received sync notification from Pub/Sub" || echo "0")
if [ "$LOG_HIT" -ge 1 ]; then
  log_pass "Proxy received Pub/Sub sync notification"
else
  log_fail "Proxy did not receive Pub/Sub notification"
fi

# Check that sync was triggered
SYNC_HIT=$(docker logs ai-proxy-go --since 5s 2>&1 | grep -c "Synced keys from portal" || echo "0")
if [ "$SYNC_HIT" -ge 1 ]; then
  log_pass "Proxy triggered sync after Pub/Sub message"
else
  log_fail "Proxy did not trigger sync after Pub/Sub message"
fi

# --------------------------------------------
# Test 6: Portal mutation triggers Pub/Sub
# --------------------------------------------
echo ""
echo "=== Test 6: Portal mutation triggers Pub/Sub ==="
# Add a dummy upstream key via Portal API
MUTATION_RESP=$(curl -s -X POST "$PORTAL_URL/api/admin/upstream-keys" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"provider":"test-provider","key":"sk-test-dummy-key-12345","tier":"default","base_url":"https://example.com"}')

sleep 2

# Check that Portal published a message (check Proxy logs)
MUTATION_HIT=$(docker logs ai-proxy-go --since 5s 2>&1 | grep "Received sync notification from Pub/Sub" | wc -l || echo "0")
MUTATION_HIT=$(echo "$MUTATION_HIT" | tr -d ' ')
if [ "$MUTATION_HIT" -ge 1 ]; then
  log_pass "Portal mutation triggered Pub/Sub sync"
else
  log_fail "Portal mutation did not trigger Pub/Sub sync"
fi

# Cleanup dummy key
curl -s -X DELETE "$PORTAL_URL/api/admin/upstream-keys/test-provider/sk-test-dummy-key-12345" \
  -H "Authorization: Bearer $TOKEN" >/dev/null 2>&1 || true

# --------------------------------------------
# Summary
# --------------------------------------------
echo ""
echo "========================================"
echo "Test Results: $PASS passed, $FAIL failed"
echo "========================================"

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
