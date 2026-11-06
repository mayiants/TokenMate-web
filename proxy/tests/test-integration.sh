#!/usr/bin/env bash
# AI Proxy Go - Integration Test Script
# Run: bash tests/test-integration.sh
# Prerequisites: Proxy running on localhost:8080
#
# Environment:
#   PROXY_URL          Proxy base URL (default: http://localhost:8080)
#   ADMIN_SECRET       Proxy admin secret (default: admin-secret)
#   PROXY_SYNC_SECRET  Portal->Proxy sync secret (default: ADMIN_SECRET)
#   API_KEY            Optional valid API key for /v1/* smoke tests

set -euo pipefail

BASE_URL="${PROXY_URL:-http://localhost:8080}"
PORTAL_URL="${PORTAL_URL:-http://localhost:3001}"
ADMIN_SECRET="${ADMIN_SECRET:-${PROXY_SYNC_SECRET:-admin-secret}}"
SYNC_SECRET="${PROXY_SYNC_SECRET:-$ADMIN_SECRET}"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0
FAIL=0
SKIP=0

# Helper functions
pass() { PASS=$((PASS + 1)); echo -e "${GREEN}✓ $1${NC}"; }
fail() { FAIL=$((FAIL + 1)); echo -e "${RED}✗ $1${NC}"; }
skip() { SKIP=$((SKIP + 1)); echo -e "${YELLOW}⊘ $1${NC}"; }

check_status() {
    local desc="$1"
    local url="$2"
    local expected="$3"
    local method="${4:-GET}"
    local data="${5:-}"
    local headers="${6:-}"

    local cmd="curl -s -m 10 -o /dev/null -w '%{http_code}' -X $method"

    if [ -n "$data" ]; then
        cmd="$cmd -d '$data' -H 'Content-Type: application/json'"
    fi

    if [ -n "$headers" ]; then
        cmd="$cmd -H '$headers'"
    fi

    cmd="$cmd '$url'"

    local status
    status=$(eval "$cmd" 2>/dev/null || true)

    if [ "$status" = "$expected" ]; then
        pass "$desc (HTTP $status)"
    else
        fail "$desc (HTTP ${status:-none}, expected $expected)"
    fi
}

check_json() {
    local desc="$1"
    local url="$2"
    local method="${3:-GET}"
    local data="${4:-}"
    local headers="${5:-}"

    local cmd="curl -s -m 10 -X $method"

    if [ -n "$data" ]; then
        cmd="$cmd -d '$data' -H 'Content-Type: application/json'"
    fi

    if [ -n "$headers" ]; then
        cmd="$cmd -H '$headers'"
    fi

    cmd="$cmd '$url'"

    local response
    response=$(eval "$cmd" 2>/dev/null || true)

    if echo "$response" | jq . > /dev/null 2>&1; then
        pass "$desc (valid JSON)"
        echo "$response" | jq .
    else
        fail "$desc (invalid JSON)"
        echo "$response"
    fi
}

echo "=========================================="
echo "  AI Proxy Go - Integration Tests"
echo "  Target: $BASE_URL"
echo "=========================================="
echo ""

# ============================================
# 1. Health & Status (No Auth)
# ============================================
echo "--- Health & Status ---"

check_status "Health check" "$BASE_URL/health" "200"
check_json "Health response" "$BASE_URL/health"

check_status "Metrics" "$BASE_URL/metrics" "200"
check_json "Status" "$BASE_URL/api/status"

check_status "Status providers" "$BASE_URL/api/status/providers" "200"
check_json "Provider stats" "$BASE_URL/providers/stats"
check_json "Model stats" "$BASE_URL/models/stats"
check_json "Model list" "$BASE_URL/models/list"

echo ""

# ============================================
# 2. Authentication (No Auth)
# ============================================
echo "--- Authentication ---"

check_status "No API key - should 401" "$BASE_URL/v1/chat/completions" "401" "POST" '{"model":"gpt-4"}'
check_status "Invalid format - should 401" "$BASE_URL/v1/chat/completions" "401" "POST" '{"model":"gpt-4"}' "Authorization: Bearer invalid-key"
check_status "Wrong prefix - should 401" "$BASE_URL/v1/chat/completions" "401" "POST" '{"model":"gpt-4"}' "Authorization: Bearer sk-other-key"

echo ""

# ============================================
# 3. Webhook Endpoints (require X-Sync-Secret)
# ============================================
echo "--- Webhook Endpoints ---"

WEBHOOK_HEADERS="X-Sync-Secret: $SYNC_SECRET"

check_status "Heartbeat" "$BASE_URL/api/webhook/heartbeat" "200" "POST" '{"serverId":"test","uptime":100}' "$WEBHOOK_HEADERS"
check_status "Usage report" "$BASE_URL/api/webhook/usage" "200" "POST" '{"traceId":"test-trace"}' "$WEBHOOK_HEADERS"
check_status "Batch usage" "$BASE_URL/api/webhook/usage-batch" "200" "POST" '{"records":[]}' "$WEBHOOK_HEADERS"
check_status "Key pool status" "$BASE_URL/api/webhook/key-pool-status" "200" "POST" '{"statuses":[]}' "$WEBHOOK_HEADERS"

check_status "Webhook no secret - should 401" "$BASE_URL/api/webhook/heartbeat" "401" "POST" '{"serverId":"test"}'
check_status "Webhook wrong secret - should 401" "$BASE_URL/api/webhook/heartbeat" "401" "POST" '{"serverId":"test"}' "X-Sync-Secret: wrong-secret"

echo ""

# ============================================
# 4. Admin Endpoints (require timestamp/nonce/signature)
# ============================================
echo "--- Admin Endpoints ---"

# Without timestamp/signature we should get 403 (not 401)
check_status "Admin stats missing auth headers" "$BASE_URL/admin/stats" "403"
check_status "Admin stats secret only (no timestamp)" "$BASE_URL/admin/stats" "403" "GET" "" "X-Admin-Secret: $ADMIN_SECRET"
check_status "Admin stats wrong secret" "$BASE_URL/admin/stats" "403" "GET" "" "X-Admin-Secret: wrong-secret"

echo ""

# ============================================
# 5. Internal Endpoints (require X-Admin-Secret)
# ============================================
echo "--- Internal Endpoints ---"

INTERNAL_HEADERS="X-Admin-Secret: $ADMIN_SECRET"

# Functional validation requires the model validator to be initialized.
# In deployments where it is disabled, these endpoints return 500; auth is still enforced.
if curl -s -m 10 -X POST "$BASE_URL/api/internal/validate-model" \
    -H 'Content-Type: application/json' \
    -H "$INTERNAL_HEADERS" \
    -d '{"provider":"amazon-us-east-1","product_id":"amazon.nova-2-lite-v1:0"}' 2>/dev/null | grep -q '"valid"'; then
    check_status "Validate model" "$BASE_URL/api/internal/validate-model" "200" "POST" '{"provider":"amazon-us-east-1","product_id":"amazon.nova-2-lite-v1:0"}' "$INTERNAL_HEADERS"
    check_status "Batch validate" "$BASE_URL/api/internal/batch-validate" "200" "POST" '{"models":[{"provider":"amazon-us-east-1","product_id":"amazon.nova-2-lite-v1:0"}]}' "$INTERNAL_HEADERS"
else
    skip "Model validator functional checks (validator not initialized)"
fi

check_status "Internal no secret - should 401" "$BASE_URL/api/internal/validate-model" "401" "POST" '{"provider":"amazon-us-east-1","product_id":"amazon.nova-2-lite-v1:0"}'
check_status "Internal wrong secret - should 401" "$BASE_URL/api/internal/validate-model" "401" "POST" '{"provider":"amazon-us-east-1","product_id":"amazon.nova-2-lite-v1:0"}' "X-Admin-Secret: wrong-secret"

echo ""

# ============================================
# 6. Key Pool Sync
# ============================================
echo "--- Key Pool Sync ---"

check_status "Key pool sync" "$BASE_URL/api/admin/key-pool/sync" "200" "GET" "" "x-proxy-secret: $SYNC_SECRET"
check_status "Key pool sync missing secret" "$BASE_URL/api/admin/key-pool/sync" "401"
check_status "Key pool sync wrong secret" "$BASE_URL/api/admin/key-pool/sync" "403" "GET" "" "x-proxy-secret: wrong-secret"

echo ""

# ============================================
# 7. Proxy Endpoints (with API key if available)
# ============================================
echo "--- Proxy Endpoints ---"

if [ -n "${API_KEY:-}" ]; then
    API_HEADERS="Authorization: Bearer $API_KEY"

    check_status "Chat completions" "$BASE_URL/v1/chat/completions" "200" "POST" \
        '{"model":"gpt-4o","messages":[{"role":"user","content":"Say hello"}]}' \
        "$API_HEADERS"

    check_status "Embeddings" "$BASE_URL/v1/embeddings" "200" "POST" \
        '{"model":"text-embedding-3-small","input":"Hello world"}' \
        "$API_HEADERS"
else
    skip "Proxy endpoints (no API_KEY set)"
fi

echo ""

# ============================================
# 8. Error Handling
# ============================================
echo "--- Error Handling ---"

check_status "404 endpoint" "$BASE_URL/nonexistent" "404"
# Gin returns 404 for unregistered methods on existing paths by default
check_status "Method not allowed on health" "$BASE_URL/health" "404" "POST"

echo ""

# ============================================
# Summary
# ============================================
echo "=========================================="
echo "  Test Results"
echo "=========================================="
echo -e "  ${GREEN}Passed: $PASS${NC}"
echo -e "  ${RED}Failed: $FAIL${NC}"
echo -e "  ${YELLOW}Skipped: $SKIP${NC}"
echo "  Total: $((PASS + FAIL + SKIP))"
echo "=========================================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
